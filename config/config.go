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

	// RAG tuning
	ChunkSize    int
	ChunkOverlap int
	TopKDefault  int

	// API
	APIPort  string
	LogLevel string
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

		// RAG tuning
		ChunkSize:    getEnvInt("CHUNK_SIZE", 1000),
		ChunkOverlap: getEnvInt("CHUNK_OVERLAP", 150),
		TopKDefault:  getEnvInt("TOP_K_DEFAULT", 5),

		// API
		APIPort:  getEnv("API_PORT", "8000"),
		LogLevel: getEnv("LOG_LEVEL", "info"),
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
