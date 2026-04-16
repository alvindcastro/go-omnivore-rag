package api

import (
	"context"
	"encoding/json"
	"net/http"

	"go-omnivore-rag/internal/adapter"
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
}

// chatAskRequest is the JSON body expected by POST /chat/ask.
type chatAskRequest struct {
	Message   string `json:"message"`
	SessionID string `json:"session_id"`
	Intent    string `json:"intent"`
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

		var (
			resp AdapterResponse
			err  error
		)
		switch req.Intent {
		case "RegistrationBanner":
			resp, err = client.AskBanner(r.Context(), req.Message, AskOptions{ModuleFilter: "Student"})
		case "FinanceBanner":
			resp, err = client.AskBanner(r.Context(), req.Message, AskOptions{ModuleFilter: "Finance"})
		case "TranscriptSop", "HoldsSop":
			resp, err = client.AskSop(r.Context(), req.Message)
		default:
			resp, err = client.AskBanner(r.Context(), req.Message, AskOptions{})
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

// writeJSONError writes a structured JSON error — never leaks upstream details.
func writeJSONError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
