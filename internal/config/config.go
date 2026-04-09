package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all configuration for the CDN storage server
type Config struct {
	// Required
	StorageAPIKey string

	// Server settings
	Port       string
	StorageDir string

	// Optional settings
	PublicBaseURL      string
	MaxUploadMB        int64
	AllowOverwrite     bool
	AllowDelete        bool
	CORSAllowedOrigins []string
	CacheMaxAge        int // in seconds
}

// Load reads configuration from environment variables
func Load() (*Config, error) {
	cfg := &Config{}

	// Required: API Key
	cfg.StorageAPIKey = os.Getenv("STORAGE_API_KEY")
	if cfg.StorageAPIKey == "" {
		return nil, fmt.Errorf("STORAGE_API_KEY is required")
	}

	// Port (default: 8080)
	cfg.Port = getEnvOrDefault("PORT", "8080")

	// Storage directory (default: /data for container)
	cfg.StorageDir = getEnvOrDefault("STORAGE_DIR", "/data")

	// Public base URL (optional)
	cfg.PublicBaseURL = strings.TrimSuffix(os.Getenv("PUBLIC_BASE_URL"), "/")

	// Max upload size in MB (default: 50)
	maxUploadStr := getEnvOrDefault("MAX_UPLOAD_MB", "50")
	maxUpload, err := strconv.ParseInt(maxUploadStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid MAX_UPLOAD_MB: %w", err)
	}
	cfg.MaxUploadMB = maxUpload

	// Allow overwrite (default: false for safety)
	allowOverwriteStr := getEnvOrDefault("ALLOW_OVERWRITE", "false")
	cfg.AllowOverwrite = strings.ToLower(allowOverwriteStr) == "true"

	// Allow delete (default: true)
	allowDeleteStr := getEnvOrDefault("ALLOW_DELETE", "true")
	cfg.AllowDelete = strings.ToLower(allowDeleteStr) != "false"

	// CORS allowed origins (default: *)
	corsOrigins := getEnvOrDefault("CORS_ALLOWED_ORIGINS", "*")
	if corsOrigins == "*" {
		cfg.CORSAllowedOrigins = []string{"*"}
	} else {
		cfg.CORSAllowedOrigins = strings.Split(corsOrigins, ",")
		for i, origin := range cfg.CORSAllowedOrigins {
			cfg.CORSAllowedOrigins[i] = strings.TrimSpace(origin)
		}
	}

	// Cache max age in seconds (default: 1 year)
	cacheMaxAgeStr := getEnvOrDefault("CACHE_MAX_AGE", "31536000")
	cacheMaxAge, err := strconv.Atoi(cacheMaxAgeStr)
	if err != nil {
		cacheMaxAge = 31536000
	}
	cfg.CacheMaxAge = cacheMaxAge

	return cfg, nil
}

// MaxUploadBytes returns the max upload size in bytes
func (c *Config) MaxUploadBytes() int64 {
	return c.MaxUploadMB * 1024 * 1024
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
