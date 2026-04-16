package api

import (
	"context"
	"encoding/json"
	"net/http"

	"go-omnivore-rag/internal/adapter"
	"go-omnivore-rag/internal/intent"
	"go-omnivore-rag/internal/sentiment"
)

// Type aliases so callers (and test files) can reference them without the adapter prefix.
type AskOptions = adapter.AskOptions
type AdapterResponse = adapter.AdapterResponse
type AdapterSource = adapter.AdapterSource

// AdapterClient is the interface ChatHandler depends on.
// The concrete *adapter.AdapterClient satisfies this interface.
type AdapterClient interface {
	AskBanner(ctx context.Context, question string, opts AskOptions) (AdapterResponse, error)
	AskSop(ctx context.Context, question string) (AdapterResponse, error)
	AskBannerGuide(ctx context.Context, question string, module string) (AdapterResponse, error)
}

// chatAskRequest is the JSON body expected by POST /chat/ask.
type chatAskRequest struct {
	Message   string `json:"message"`
	SessionID string `json:"session_id"`
	Intent    string `json:"intent"`
	Source    string `json:"source"` // optional: "banner"|"finance"|"sop"|"auto"
}

// chatAskResponse is the JSON body returned by POST /chat/ask.
// It is identical in shape to AdapterResponse.
type chatAskResponse = AdapterResponse

// ChatHandler handles /chat/* and /health routes.
type ChatHandler struct {
	mux *http.ServeMux
}

// NewChatHandler wires routes onto a fresh ServeMux and returns a ready handler.
func NewChatHandler(client AdapterClient) *ChatHandler {
	h := &ChatHandler{mux: http.NewServeMux()}
	h.mux.HandleFunc("/chat/ask", askHandler(client))
	h.mux.HandleFunc("/chat/sentiment", sentimentHandler(sentiment.NewAnalyzer(sentiment.DefaultConfig())))
	h.mux.HandleFunc("/chat/intent", intentHandler(intent.NewClassifier(intent.DefaultIntentConfig())))
	h.mux.HandleFunc("/health", healthHandler)
	return h
}

func (h *ChatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// askHandler returns the http.HandlerFunc for POST /chat/ask.
func askHandler(client AdapterClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		var req chatAskRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Message == "" {
			writeJSONError(w, http.StatusBadRequest, "message is required")
			return
		}
		if req.SessionID == "" {
			writeJSONError(w, http.StatusBadRequest, "session_id is required")
			return
		}

		// Resolve effective source: explicit field wins; otherwise derive from intent.
		source := req.Source
		if source == "" || source == "auto" {
			source = sourceFromIntent(req.Intent)
		}

		var (
			resp AdapterResponse
			err  error
		)
		switch source {
		case "banner":
			resp, err = client.AskBanner(r.Context(), req.Message, AskOptions{ModuleFilter: "General"})
		case "finance":
			resp, err = client.AskBanner(r.Context(), req.Message, AskOptions{ModuleFilter: "Finance"})
		case "sop":
			resp, err = client.AskSop(r.Context(), req.Message)
		case "user_guide":
			resp, err = client.AskBannerGuide(r.Context(), req.Message, "general")
		case "user_guide_student":
			resp, err = client.AskBannerGuide(r.Context(), req.Message, "student")
		case "user_guide_finance":
			resp, err = client.AskBannerGuide(r.Context(), req.Message, "finance")
		default:
			writeJSONError(w, http.StatusBadRequest, "invalid source")
			return
		}

		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal server error")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// healthHandler handles GET /health.
func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// sentimentHandler returns the http.HandlerFunc for POST /chat/sentiment.
func sentimentHandler(analyzer *sentiment.Analyzer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		var req struct {
			Message string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Message == "" {
			writeJSONError(w, http.StatusBadRequest, "message is required")
			return
		}

		result := analyzer.Analyze(req.Message)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sentiment": string(result.Sentiment),
			"score":     result.Score,
		})
	}
}

// intentHandler returns the http.HandlerFunc for POST /chat/intent.
func intentHandler(classifier *intent.Classifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		var req struct {
			Message string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Message == "" {
			writeJSONError(w, http.StatusBadRequest, "message is required")
			return
		}

		result := classifier.Classify(req.Message)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"intent":     string(result.Intent),
			"confidence": result.Confidence,
		})
	}
}

// sourceFromIntent maps an intent name to a routing source string.
// Returns "banner" for unknown or catch-all intents so every query always
// carries a module context — prevents 0-result searches against the backend.
func sourceFromIntent(i string) string {
	switch i {
	case "BannerFinance":
		return "finance"
	case "SopQuery":
		return "sop"
	case "BannerUsage":
		return "user_guide"
	default: // BannerRelease, BannerAdmin, General, unknown
		return "banner"
	}
}

// writeJSONError writes a structured JSON error — never leaks upstream details.
func writeJSONError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
