package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdapterClient_BannerAsk_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/banner/ask", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)

		var req bannerAskRequest
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "When is the add/drop deadline?", req.Question)

		resp := ragAskResponse{
			Answer:         "The add/drop deadline is January 17th.",
			Question:       req.Question,
			RetrievalCount: 2,
			Sources: []ragSourceChunk{
				{Score: 0.87, DocumentTitle: "Banner Student 9.3.37", Page: 12, SourceType: "banner"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewAdapterClient(srv.URL)
	result, err := client.AskBanner(context.Background(), "When is the add/drop deadline?", AskOptions{})

	require.NoError(t, err)
	assert.Equal(t, "The add/drop deadline is January 17th.", result.Answer)
	assert.InDelta(t, 0.87, result.Confidence, 0.01)
	assert.False(t, result.Escalate)
	assert.Len(t, result.Sources, 1)
}

func TestAdapterClient_BannerAsk_LowConfidence_SetsEscalate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ragAskResponse{
			Answer:         "I'm not sure.",
			RetrievalCount: 1,
			Sources:        []ragSourceChunk{{Score: 0.005}}, // below calibrated floor 0.01
		})
	}))
	defer srv.Close()

	client := NewAdapterClient(srv.URL)
	result, err := client.AskBanner(context.Background(), "random question", AskOptions{})

	require.NoError(t, err)
	assert.True(t, result.Escalate)
}

func TestAdapterClient_ScoreAboveFloor_DoesNotEscalate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ragAskResponse{
			Answer:         "Banner General 9.3.37 changed X.",
			RetrievalCount: 3,
			Sources:        []ragSourceChunk{{Score: 0.033}}, // observed real-world score
		})
	}))
	defer srv.Close()

	client := NewAdapterClient(srv.URL)
	result, err := client.AskBanner(context.Background(), "What changed in Banner?", AskOptions{})

	require.NoError(t, err)
	assert.False(t, result.Escalate, "score 0.033 with 3 sources should not escalate")
}

func TestAdapterClient_BannerAsk_NoResults_Escalates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ragAskResponse{RetrievalCount: 0, Sources: []ragSourceChunk{}})
	}))
	defer srv.Close()

	client := NewAdapterClient(srv.URL)
	result, err := client.AskBanner(context.Background(), "anything", AskOptions{})

	require.NoError(t, err)
	assert.True(t, result.Escalate)
	assert.Zero(t, result.Confidence)
}

func TestAdapterClient_SopAsk_RoutesCorrectly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/sop/ask", r.URL.Path)
		json.NewEncoder(w).Encode(ragAskResponse{
			Answer:  "See SOP-42 for the procedure.",
			Sources: []ragSourceChunk{{Score: 0.91, SopNumber: "SOP-42", SourceType: "sop"}},
		})
	}))
	defer srv.Close()

	client := NewAdapterClient(srv.URL)
	result, err := client.AskSop(context.Background(), "How do I process a transcript request?")

	require.NoError(t, err)
	assert.Contains(t, result.Answer, "SOP-42")
	assert.Equal(t, "sop", result.Sources[0].SourceType)
}

func TestAdapterClient_AskBannerGuide_Success(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/banner/general/ask", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		json.NewDecoder(r.Body).Decode(&capturedBody)
		json.NewEncoder(w).Encode(ragAskResponse{
			Answer:         "Navigate to the Banner main menu via...",
			RetrievalCount: 2,
			Sources:        []ragSourceChunk{{Score: 0.042, DocumentTitle: "Banner General Use Guide", SourceType: "banner_user_guide"}},
		})
	}))
	defer srv.Close()

	client := NewAdapterClient(srv.URL)
	result, err := client.AskBannerGuide(context.Background(), "How do I navigate the Banner main menu?", "general")

	require.NoError(t, err)
	assert.False(t, result.Escalate)
	assert.Greater(t, result.Confidence, 0.0)
	assert.Equal(t, "banner_user_guide", capturedBody["source_type"])
	assert.Nil(t, capturedBody["version_filter"], "version_filter must not be set for user guide queries")
	assert.Nil(t, capturedBody["year_filter"], "year_filter must not be set for user guide queries")
}

func TestAdapterClient_AskBannerGuide_NoResults_Escalates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ragAskResponse{RetrievalCount: 0, Sources: []ragSourceChunk{}})
	}))
	defer srv.Close()

	client := NewAdapterClient(srv.URL)
	result, err := client.AskBannerGuide(context.Background(), "something obscure", "general")

	require.NoError(t, err)
	assert.True(t, result.Escalate)
}

func TestAdapterClient_AskBannerGuide_Module(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/banner/student/ask", r.URL.Path)
		json.NewEncoder(w).Encode(ragAskResponse{
			Answer:         "Student form navigation...",
			RetrievalCount: 1,
			Sources:        []ragSourceChunk{{Score: 0.035}},
		})
	}))
	defer srv.Close()

	client := NewAdapterClient(srv.URL)
	_, err := client.AskBannerGuide(context.Background(), "How do I look up a student?", "student")

	require.NoError(t, err)
}

func TestAdapterClient_WithModuleFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req bannerAskRequest
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "Finance", req.ModuleFilter)
		json.NewEncoder(w).Encode(ragAskResponse{
			Answer:         "The fiscal year closes on...",
			RetrievalCount: 1,
			Sources:        []ragSourceChunk{{Score: 0.78}},
		})
	}))
	defer srv.Close()

	client := NewAdapterClient(srv.URL)
	_, err := client.AskBanner(context.Background(), "When does the fiscal year close?",
		AskOptions{ModuleFilter: "Finance"})

	require.NoError(t, err)
}
