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
	askBannerFn      func(ctx context.Context, q string, opts AskOptions) (AdapterResponse, error)
	askSopFn         func(ctx context.Context, q string) (AdapterResponse, error)
	askBannerGuideFn func(ctx context.Context, q string, module string) (AdapterResponse, error)
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

func (m *mockAdapterClient) AskBannerGuide(ctx context.Context, question string, module string) (AdapterResponse, error) {
	if m.askBannerGuideFn != nil {
		return m.askBannerGuideFn(ctx, question, module)
	}
	return AdapterResponse{}, nil
}

func TestChatAskHandler_BannerAdminIntent_UsesGeneralFilter(t *testing.T) {
	mockClient := &mockAdapterClient{
		askBannerFn: func(ctx context.Context, q string, opts AskOptions) (AdapterResponse, error) {
			assert.Equal(t, "General", opts.ModuleFilter)
			return AdapterResponse{
				Answer:     "Configure FGAC via Banner Security.",
				Confidence: 0.83,
				Sources:    []AdapterSource{{Title: "Banner Admin 9.3.37", Page: 5}},
				Escalate:   false,
			}, nil
		},
	}

	body := `{"message":"How do I configure FGAC?","session_id":"s1","intent":"BannerAdmin"}`
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

	body := `{"message":"What are the steps to approve a requisition?","session_id":"s2","intent":"SopQuery"}`
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

	body := `{"message":"What changed in 9.3.37?","session_id":"s4","intent":"BannerRelease"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(mockClient).ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var errBody map[string]string
	json.Unmarshal(w.Body.Bytes(), &errBody)
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

func TestIntentHandler_BannerRelease(t *testing.T) {
	body := `{"message":"What changed in Banner version 9.3.37?"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/intent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(&mockAdapterClient{}).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "BannerRelease", resp["intent"])
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

func TestChatAskHandler_GeneralIntent_UsesGeneralModuleFilter(t *testing.T) {
	mockClient := &mockAdapterClient{
		askBannerFn: func(_ context.Context, _ string, opts AskOptions) (AdapterResponse, error) {
			assert.Equal(t, "General", opts.ModuleFilter)
			return AdapterResponse{Answer: "Banner General info.", Confidence: 0.72}, nil
		},
	}

	body := `{"message":"What changed in Banner General 9.3.37?","session_id":"s5","intent":"General"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(mockClient).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestChatAskHandler_BannerReleaseIntent_UsesGeneralModuleFilter(t *testing.T) {
	mockClient := &mockAdapterClient{
		askBannerFn: func(_ context.Context, _ string, opts AskOptions) (AdapterResponse, error) {
			assert.Equal(t, "General", opts.ModuleFilter)
			return AdapterResponse{Answer: "Release summary.", Confidence: 0.65}, nil
		},
	}

	body := `{"message":"Show breaking changes in 9.3.37","session_id":"s6","intent":"BannerRelease"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(mockClient).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestChatAskHandler_SourceFieldOverridesIntent(t *testing.T) {
	mockClient := &mockAdapterClient{
		askBannerFn: func(_ context.Context, _ string, opts AskOptions) (AdapterResponse, error) {
			assert.Equal(t, "Finance", opts.ModuleFilter)
			return AdapterResponse{Answer: "Finance answer.", Confidence: 0.80}, nil
		},
	}

	body := `{"message":"How do I close a fiscal year?","session_id":"s7","intent":"General","source":"finance"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(mockClient).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestChatAskHandler_SourceSop_CallsAskSop(t *testing.T) {
	called := false
	mockClient := &mockAdapterClient{
		askSopFn: func(_ context.Context, _ string) (AdapterResponse, error) {
			called = true
			return AdapterResponse{Answer: "SOP answer.", Confidence: 0.88}, nil
		},
	}

	body := `{"message":"How do I restart Axiom?","session_id":"s8","intent":"General","source":"sop"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(mockClient).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, called, "expected AskSop to be called")
}

func TestChatAskHandler_InvalidSource_Student_Returns400(t *testing.T) {
	body := `{"message":"Register me for COMP101","session_id":"s9","intent":"General","source":"student"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(&mockAdapterClient{}).ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errBody map[string]string
	json.Unmarshal(w.Body.Bytes(), &errBody)
	assert.Equal(t, "invalid source", errBody["error"])
}

func TestChatAskHandler_InvalidSource_General_Returns400(t *testing.T) {
	body := `{"message":"Tell me something","session_id":"s10","intent":"General","source":"general"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(&mockAdapterClient{}).ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errBody map[string]string
	json.Unmarshal(w.Body.Bytes(), &errBody)
	assert.Equal(t, "invalid source", errBody["error"])
}

func TestChatAskHandler_SourceBanner_UsesGeneralFilter(t *testing.T) {
	mockClient := &mockAdapterClient{
		askBannerFn: func(_ context.Context, _ string, opts AskOptions) (AdapterResponse, error) {
			assert.Equal(t, "General", opts.ModuleFilter)
			return AdapterResponse{Answer: "Banner answer.", Confidence: 0.75}, nil
		},
	}

	body := `{"message":"What is the Banner FOAPAL structure?","session_id":"s11","intent":"General","source":"banner"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(mockClient).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestChatAsk_UserGuide_RoutesToGeneral(t *testing.T) {
	var gotModule string
	mockClient := &mockAdapterClient{
		askBannerGuideFn: func(_ context.Context, _ string, module string) (AdapterResponse, error) {
			gotModule = module
			return AdapterResponse{Answer: "Use the main menu.", Confidence: 0.04}, nil
		},
	}

	body := `{"message":"How do I navigate the Banner main menu?","session_id":"s12","source":"user_guide"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(mockClient).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "general", gotModule)
}

func TestChatAsk_UserGuide_Student(t *testing.T) {
	var gotModule string
	mockClient := &mockAdapterClient{
		askBannerGuideFn: func(_ context.Context, _ string, module string) (AdapterResponse, error) {
			gotModule = module
			return AdapterResponse{Answer: "Student guide answer.", Confidence: 0.03}, nil
		},
	}

	body := `{"message":"How do I look up a student record?","session_id":"s13","source":"user_guide_student"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(mockClient).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "student", gotModule)
}

func TestChatAsk_UserGuide_Finance(t *testing.T) {
	var gotModule string
	mockClient := &mockAdapterClient{
		askBannerGuideFn: func(_ context.Context, _ string, module string) (AdapterResponse, error) {
			gotModule = module
			return AdapterResponse{Answer: "Finance guide answer.", Confidence: 0.03}, nil
		},
	}

	body := `{"message":"Where do I find the journal voucher form?","session_id":"s14","source":"user_guide_finance"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(mockClient).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "finance", gotModule)
}

func TestChatAsk_BannerUsageIntent_RoutesToUserGuide(t *testing.T) {
	var gotModule string
	mockClient := &mockAdapterClient{
		askBannerGuideFn: func(_ context.Context, _ string, module string) (AdapterResponse, error) {
			gotModule = module
			return AdapterResponse{Answer: "Navigate via the main menu.", Confidence: 0.035}, nil
		},
	}

	body := `{"message":"How do I navigate the Banner main menu?","session_id":"s15","intent":"BannerUsage"}`
	req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	NewChatHandler(mockClient).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "general", gotModule)
}
