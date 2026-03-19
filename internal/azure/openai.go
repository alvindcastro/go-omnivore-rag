// internal/azure/openai.go
// Direct REST calls to Azure OpenAI.
// No third-party SDK — explicit, easy to follow, easy to debug.
package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"go-banner-rag/config"
)

// OpenAIClient wraps Azure OpenAI REST calls.
type OpenAIClient struct {
	cfg        *config.Config
	httpClient *http.Client
}

// NewOpenAIClient returns a new client with a sensible timeout.
func NewOpenAIClient(cfg *config.Config) *OpenAIClient {
	return &OpenAIClient{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ─── Embeddings ──────────────────────────────────────────────────────────────

type embeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *openAIError `json:"error,omitempty"`
}

// EmbedText sends a string to ada-002 and returns a 1536-dim vector.
func (c *OpenAIClient) EmbedText(text string) ([]float32, error) {
	url := fmt.Sprintf(
		"%s/openai/deployments/%s/embeddings?api-version=%s",
		strings.TrimRight(c.cfg.AzureOpenAIEndpoint, "/"),
		c.cfg.AzureOpenAIEmbeddingDeployment,
		c.cfg.AzureOpenAIAPIVersion,
	)

	body := embeddingRequest{
		Input: []string{text},
		Model: c.cfg.AzureOpenAIEmbeddingDeployment,
	}

	var result embeddingResponse
	if err := c.post(url, body, &result); err != nil {
		return nil, fmt.Errorf("embed request failed: %w", err)
	}
	if result.Error != nil {
		return nil, fmt.Errorf("azure openai error: %s", result.Error.Message)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	return result.Data[0].Embedding, nil
}

// ─── Chat Completions ─────────────────────────────────────────────────────────

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
}

type chatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Error *openAIError `json:"error,omitempty"`
}

type openAIError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// ChatComplete sends messages to GPT-4o-mini and returns the reply text.
func (c *OpenAIClient) ChatComplete(messages []ChatMessage) (string, error) {
	url := fmt.Sprintf(
		"%s/openai/deployments/%s/chat/completions?api-version=%s",
		strings.TrimRight(c.cfg.AzureOpenAIEndpoint, "/"),
		c.cfg.AzureOpenAIChatDeployment,
		c.cfg.AzureOpenAIAPIVersion,
	)

	body := chatRequest{
		Messages:    messages,
		MaxTokens:   800,
		Temperature: 0.1, // Low temperature = factual, grounded answers
	}

	var result chatResponse
	if err := c.post(url, body, &result); err != nil {
		return "", fmt.Errorf("chat request failed: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("azure openai error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no completion returned")
	}

	return result.Choices[0].Message.Content, nil
}

// ─── HTTP Helper ──────────────────────────────────────────────────────────────

// post marshals body to JSON, POSTs to url, and unmarshals the response into result.
func (c *OpenAIClient) post(url string, body, result any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := c.doRequest(url, payload, result, attempt)
		if err == nil {
			return nil
		}

		log.Printf("    Attempt %d failed: %v", attempt, err)
		if attempt < maxRetries {
			waitSecs := attempt * 5
			log.Printf("    Waiting %d seconds before retry...", waitSecs)
			time.Sleep(time.Duration(waitSecs) * time.Second)
		}
	}
	return fmt.Errorf("all %d attempts failed", maxRetries)
}

func (c *OpenAIClient) doRequest(url string, payload []byte, result any, attempt int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", c.cfg.AzureOpenAIAPIKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == 429 {
		log.Printf("    Rate limited (429) — waiting 15 seconds...")
		time.Sleep(15 * time.Second)
		return fmt.Errorf("rate limited")
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("azure openai HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	return json.Unmarshal(respBytes, result)
}
