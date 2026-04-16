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
			Sources:        []ragSourceChunk{{Score: 0.31}},
		})
	}))
	defer srv.Close()

	client := NewAdapterClient(srv.URL)
	result, err := client.AskBanner(context.Background(), "random question", AskOptions{})

	require.NoError(t, err)
	assert.True(t, result.Escalate)
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

func TestAdapterClient_WithModuleFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req bannerAskRequest
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "Student", req.ModuleFilter)
		json.NewEncoder(w).Encode(ragAskResponse{
			Answer:         "Registration opens on...",
			RetrievalCount: 1,
			Sources:        []ragSourceChunk{{Score: 0.78}},
		})
	}))
	defer srv.Close()

	client := NewAdapterClient(srv.URL)
	_, err := client.AskBanner(context.Background(), "When does registration open?",
		AskOptions{ModuleFilter: "Student"})

	require.NoError(t, err)
}
