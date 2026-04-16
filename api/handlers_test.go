package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// mockAdapterClient is a test double for AdapterClient.
type mockAdapterClient struct {
	askBannerFn func(ctx context.Context, q string, opts AskOptions) (AdapterResponse, error)
	askSopFn    func(ctx context.Context, q string) (AdapterResponse, error)
}

func (m *mockAdapterClient) AskBanner(ctx context.Context, question string, opts AskOptions) (AdapterResponse, error) {
	if m.askBannerFn != nil {
		return m.askBannerFn(ctx, question, opts)
	}
	return AdapterResponse{}, nil
}

func (m *mockAdapterClient) AskSop(ctx context.Context, question string) (AdapterResponse, error) {
	if m.askSopFn != nil {
		return m.askSopFn(ctx, question)
	}
	return AdapterResponse{}, nil
}

func TestChatAskHandler_ValidBannerIntent(t *testing.T) {
	mockClient := &mockAdapterClient{
		askBannerFn: func(ctx context.Context, q string, opts AskOptions) (AdapterResponse, error) {
			assert.Equal(t, "Student", opts.ModuleFilter)
			return AdapterResponse{
				Answer:     "Registration opens March 1st.",
				Confidence: 0.83,
				Sources:    []AdapterSource{{Title: "Banner Student 9.3.37", Page: 5}},
				Escalate:   false,
			}, nil
		},
	}

	body := `{"message":"When does registration open?","session_id":"s1","intent":"RegistrationBanner"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(mockClient).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp chatAskResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.False(t, resp.Escalate)
	assert.InDelta(t, 0.83, resp.Confidence, 0.01)
}

func TestChatAskHandler_SopIntent_CallsAskSop(t *testing.T) {
	mockClient := &mockAdapterClient{
		askSopFn: func(ctx context.Context, q string) (AdapterResponse, error) {
			return AdapterResponse{Answer: "See SOP-42.", Confidence: 0.91}, nil
		},
	}

	body := `{"message":"How do I request a transcript?","session_id":"s2","intent":"TranscriptSop"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(mockClient).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestChatAskHandler_MissingMessage_Returns400(t *testing.T) {
	body := `{"session_id":"s3","intent":"General"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(&mockAdapterClient{}).ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestChatAskHandler_BackendError_Returns500_WithSafeMessage(t *testing.T) {
	mockClient := &mockAdapterClient{
		askBannerFn: func(_ context.Context, _ string, _ AskOptions) (AdapterResponse, error) {
			return AdapterResponse{}, errors.New("upstream timeout: connection refused")
		},
	}

	body := `{"message":"When does registration open?","session_id":"s4","intent":"RegistrationBanner"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(mockClient).ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var errBody map[string]string
	json.Unmarshal(w.Body.Bytes(), &errBody)
	// Must not leak upstream error details
	assert.Equal(t, "internal server error", errBody["error"])
	assert.NotContains(t, w.Body.String(), "connection refused")
}

func TestSentimentHandler_Frustrated(t *testing.T) {
	body := `{"message":"WHY IS THIS NOT WORKING???"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/sentiment", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(&mockAdapterClient{}).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "Frustrated", resp["sentiment"])
	assert.Greater(t, resp["score"].(float64), 0.6)
}

func TestSentimentHandler_Neutral(t *testing.T) {
	body := `{"message":"When is the add/drop deadline?"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/sentiment", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(&mockAdapterClient{}).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "Neutral", resp["sentiment"])
}

func TestSentimentHandler_MissingMessage_Returns400(t *testing.T) {
	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/chat/sentiment", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(&mockAdapterClient{}).ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestIntentHandler_RegistrationBanner(t *testing.T) {
	body := `{"message":"When is the add/drop deadline?"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/intent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(&mockAdapterClient{}).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "RegistrationBanner", resp["intent"])
	assert.Greater(t, resp["confidence"].(float64), 0.0)
}

func TestIntentHandler_General_LowConfidence(t *testing.T) {
	body := `{"message":"I have a question"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/intent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(&mockAdapterClient{}).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "General", resp["intent"])
}

func TestIntentHandler_MissingMessage_Returns400(t *testing.T) {
	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/chat/intent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(&mockAdapterClient{}).ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
