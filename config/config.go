// config/config.go
// Loads all settings from the .env file.
// Every other package imports this — one source of truth.
package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	// Azure OpenAI
	AzureOpenAIEndpoint            string
	AzureOpenAIAPIKey              string
	AzureOpenAIAPIVersion          string
	AzureOpenAIChatDeployment      string
	AzureOpenAIEmbeddingDeployment string

	// Azure AI Search
	AzureSearchEndpoint  string
	AzureSearchAPIKey    string
	AzureSearchIndexName string

	// Azure Blob Storage
	AzureStorageConnectionString string
	AzureStorageContainerName    string
	AzureStorageBlobPrefix       string

	// Web Search (Tavily)
	TavilyAPIKey  string
	WebSearchTopK int

	// Confidence-based routing thresholds (auto mode)
	// Azure AI Search RRF hybrid scores typically range from ~0.005 to 0.05+.
	ConfidenceHighThreshold float64 // score >= HIGH  → local only
	ConfidenceLowThreshold  float64 // score <  LOW   → web only; between → hybrid

	// RAG tuning
	ChunkSize    int
	ChunkOverlap int
	TopKDefault  int

	// API
	APIPort  string
	GRPCPort string
	LogLevel string
	APIKey   string // optional; when set, all non-health endpoints require Authorization: Bearer <key>
}

func Load() *Config {
	// Load .env file if present (ignored in production where env vars are set directly)
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, reading from environment variables")
	}

	return &Config{
		// Azure OpenAI
		AzureOpenAIEndpoint:            requireEnv("AZURE_OPENAI_ENDPOINT"),
		AzureOpenAIAPIKey:              requireEnv("AZURE_OPENAI_API_KEY"),
		AzureOpenAIAPIVersion:          getEnv("AZURE_OPENAI_API_VERSION", "2024-02-01"),
		AzureOpenAIChatDeployment:      getEnv("AZURE_OPENAI_CHAT_DEPLOYMENT", "gpt-4o-mini"),
		AzureOpenAIEmbeddingDeployment: getEnv("AZURE_OPENAI_EMBEDDING_DEPLOYMENT", "text-embedding-ada-002"),

		// Azure AI Search
		AzureSearchEndpoint:  requireEnv("AZURE_SEARCH_ENDPOINT"),
		AzureSearchAPIKey:    requireEnv("AZURE_SEARCH_API_KEY"),
		AzureSearchIndexName: getEnv("AZURE_SEARCH_INDEX_NAME", "omnivore-knowledge"),

		// Azure Blob Storage
		AzureStorageConnectionString: getEnv("AZURE_STORAGE_CONNECTION_STRING", ""),
		AzureStorageContainerName:    getEnv("AZURE_STORAGE_CONTAINER_NAME", "banner-release-notes"),
		AzureStorageBlobPrefix:       getEnv("AZURE_STORAGE_BLOB_PREFIX", ""),

		// Web Search (Tavily)
		TavilyAPIKey:  getEnv("TAVILY_API_KEY", ""),
		WebSearchTopK: getEnvInt("WEB_SEARCH_TOP_K", 5),

		// Confidence-based routing
		ConfidenceHighThreshold: getEnvFloat("CONFIDENCE_HIGH_THRESHOLD", 0.030),
		ConfidenceLowThreshold:  getEnvFloat("CONFIDENCE_LOW_THRESHOLD", 0.010),

		// RAG tuning
		ChunkSize:    getEnvInt("CHUNK_SIZE", 1000),
		ChunkOverlap: getEnvInt("CHUNK_OVERLAP", 150),
		TopKDefault:  getEnvInt("TOP_K_DEFAULT", 5),

		// API
		APIPort:  getEnv("API_PORT", "8000"),
		GRPCPort: getEnv("GRPC_PORT", "9000"),
		LogLevel: getEnv("LOG_LEVEL", "info"),
		APIKey:   getEnv("API_KEY", ""),
	}
}

// requireEnv panics on startup if a required variable is missing.
func requireEnv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		log.Fatalf("Required environment variable %q is not set", key)
	}
	return val
}

// getEnv returns the env var value or a default.
func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// getEnvInt returns the env var as int or a default.
func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

// getEnvFloat returns the env var as float64 or a default.
func getEnvFloat(key string, defaultVal float64) float64 {
	if val := os.Getenv(key); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	}
	return defaultVal
}
