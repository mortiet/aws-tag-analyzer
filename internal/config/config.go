package config

import (
	"os"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog/log" // Import zerolog's global logger
)

type Config struct {
	AWSRegion          string
	AWSAccessKeyID     string
	AWSSecretAccessKey string
	AIAPIURL           string // New: AI API Endpoint URL
	AIAPIToken         string // New: AI API Bearer Token
}

// GetEnv retrieves an environment variable or returns a fallback value.
func GetEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// LoadConfig loads configuration from a .env file and environment variables.
// It prioritizes environment variables over the .env file.
func LoadConfig() (*Config, error) {
	// Load .env file, but don't treat error as fatal
	err := godotenv.Load()
	if err != nil && !os.IsNotExist(err) {
		// Log warning if .env exists but couldn't be loaded using zerolog
		log.Warn().Err(err).Msg("Error loading .env file")
		// Return the error to indicate a potential problem with the .env file itself
		return nil, err
	}

	// Read from environment variables, using GetEnv for potential fallbacks if needed
	return &Config{
		AWSRegion:          GetEnv("AWS_REGION", ""), // Keep checking this later in main
		AWSAccessKeyID:     GetEnv("AWS_ACCESS_KEY_ID", ""),
		AWSSecretAccessKey: GetEnv("AWS_SECRET_ACCESS_KEY", ""),
		AIAPIURL:           GetEnv("AI_API_URL", ""),   // Load AI API URL from env
		AIAPIToken:         GetEnv("AI_API_TOKEN", ""), // Load AI API Token from env
	}, nil
}
