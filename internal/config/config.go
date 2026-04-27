package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	// Server
	ServerHost string // env: SERVER_HOST, default: "0.0.0.0"
	ServerPort int    // env: SERVER_PORT, default: 8080
	AppVersion string // env: APP_VERSION, default: "dev"

	// Database
	DatabaseURL        string        // env: DATABASE_URL, required
	DBMaxConns         int           // env: DB_MAX_CONNS, default: 25
	DBMinConns         int           // env: DB_MIN_CONNS, default: 5
	DBMaxConnIdleTime  time.Duration // env: DB_MAX_CONN_IDLE_TIME, default: 5m

	// Redis
	RedisURL string // env: REDIS_URL, required

	// JWT
	JWTSecret          string        // env: JWT_SECRET, required
	JWTAccessTokenTTL  time.Duration // env: JWT_ACCESS_TOKEN_TTL, default: 15m
	JWTRefreshTokenTTL time.Duration // env: JWT_REFRESH_TOKEN_TTL, default: 168h

	// Encryption
	EncryptionKey string // env: ENCRYPTION_KEY, required

	// WB API
	WBAPIBaseURL   string // env: WB_API_BASE_URL, default: "https://advert-api.wildberries.ru"
	WBAPIRateLimit int    // env: WB_API_RATE_LIMIT, default: 10

	// Ads Read service per-query data caps. Previously hardcoded as constants in
	// internal/service/ads_read_loader.go and tuned during the OOM incident; lifted
	// to config so ops can adjust per-deployment without a code change. A future
	// per-workspace override is tracked in the v1.0 roadmap.
	AdsReadEntityLimit int // env: ADS_READ_ENTITY_LIMIT, default: 5000
	AdsReadStatsLimit  int // env: ADS_READ_STATS_LIMIT, default: 20000

	// WB Catalog Parser
	WBParserMinDelay time.Duration // env: WB_PARSER_MIN_DELAY, default: 2s
	WBParserProxies  []string      // env: WB_PARSER_PROXIES, comma-separated

	// Export
	ExportStoragePath string // env: EXPORT_STORAGE_PATH, default: "./exports"

	// Sellico API
	SellicoAPIBaseURL string        // env: SELLICO_API_BASE_URL, default: "https://sellico.ru/api"
	SellicoAPITimeout time.Duration // env: SELLICO_API_TIMEOUT, default: 5s

	// CORS
	CORSAllowOrigins []string // env: CORS_ALLOW_ORIGINS, comma-separated, default: "*"

	// Rate Limiting
	RateLimitRPS   float64 // env: RATE_LIMIT_RPS, default: 20
	RateLimitBurst int     // env: RATE_LIMIT_BURST, default: 40

	// Worker Schedules
	SyncInterval            string // env: SYNC_INTERVAL, default: "@every 1h"
	RecommendationInterval  string // env: RECOMMENDATION_INTERVAL, default: "@every 2h"
	BidAutomationInterval   string // env: BID_AUTOMATION_INTERVAL, default: "@every 15m"

	// Logging
	LogLevel string // env: LOG_LEVEL, default: "info"
}

// Load reads configuration from environment variables, validates required fields,
// and terminates the process with a descriptive message if any required variable is missing.
func Load() *Config {
	cfg := &Config{
		ServerHost:         getEnvOrDefault("SERVER_HOST", "0.0.0.0"),
		ServerPort:         getEnvAsInt("SERVER_PORT", 8080),
		AppVersion:         getEnvOrDefault("APP_VERSION", "dev"),
		DatabaseURL:        requireEnv("DATABASE_URL"),
		DBMaxConns:         getEnvAsInt("DB_MAX_CONNS", 25),
		DBMinConns:         getEnvAsInt("DB_MIN_CONNS", 5),
		DBMaxConnIdleTime:  getEnvAsDuration("DB_MAX_CONN_IDLE_TIME", 5*time.Minute),
		RedisURL:           requireEnv("REDIS_URL"),
		JWTSecret:          requireEnv("JWT_SECRET"),
		JWTAccessTokenTTL:  getEnvAsDuration("JWT_ACCESS_TOKEN_TTL", 15*time.Minute),
		JWTRefreshTokenTTL: getEnvAsDuration("JWT_REFRESH_TOKEN_TTL", 168*time.Hour),
		EncryptionKey:      requireEnv("ENCRYPTION_KEY"),
		WBAPIBaseURL:       getEnvOrDefault("WB_API_BASE_URL", "https://advert-api.wildberries.ru"),
		WBAPIRateLimit:     getEnvAsInt("WB_API_RATE_LIMIT", 10),
		AdsReadEntityLimit: getEnvAsInt("ADS_READ_ENTITY_LIMIT", 5000),
		AdsReadStatsLimit:  getEnvAsInt("ADS_READ_STATS_LIMIT", 20000),
		WBParserMinDelay:   getEnvAsDuration("WB_PARSER_MIN_DELAY", 2*time.Second),
		WBParserProxies:    getEnvAsSlice("WB_PARSER_PROXIES", ","),
		ExportStoragePath:  getEnvOrDefault("EXPORT_STORAGE_PATH", "./exports"),
		SellicoAPIBaseURL:  getEnvOrDefault("SELLICO_API_BASE_URL", "https://sellico.ru/api"),
		SellicoAPITimeout:  getEnvAsDuration("SELLICO_API_TIMEOUT", 5*time.Second),
		SyncInterval:          getEnvOrDefault("SYNC_INTERVAL", "@every 1h"),
		RecommendationInterval: getEnvOrDefault("RECOMMENDATION_INTERVAL", "@every 2h"),
		BidAutomationInterval:  getEnvOrDefault("BID_AUTOMATION_INTERVAL", "@every 15m"),
		CORSAllowOrigins:   getEnvAsSlice("CORS_ALLOW_ORIGINS", ","),
		RateLimitRPS:       getEnvAsFloat("RATE_LIMIT_RPS", 20),
		RateLimitBurst:     getEnvAsInt("RATE_LIMIT_BURST", 40),
		LogLevel:           getEnvOrDefault("LOG_LEVEL", "info"),
	}

	return cfg
}

// requireEnv returns the value of the environment variable or terminates the process
// with a descriptive error message if the variable is missing or empty.
func requireEnv(key string) string {
	val, ok := os.LookupEnv(key)
	if !ok || val == "" {
		log.Fatalf("required environment variable %s is not set or empty", key)
	}
	return val
}

// getEnvOrDefault returns the value of the environment variable or the default value.
func getEnvOrDefault(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

// getEnvAsInt returns the environment variable parsed as int, or the default value.
// Terminates the process if the value is set but cannot be parsed.
func getEnvAsInt(key string, defaultVal int) int {
	val, ok := os.LookupEnv(key)
	if !ok || val == "" {
		return defaultVal
	}
	parsed, err := strconv.Atoi(val)
	if err != nil {
		log.Fatalf("environment variable %s must be a valid integer, got %q: %v", key, val, err)
	}
	return parsed
}

// getEnvAsDuration returns the environment variable parsed as time.Duration, or the default value.
// Terminates the process if the value is set but cannot be parsed.
func getEnvAsDuration(key string, defaultVal time.Duration) time.Duration {
	val, ok := os.LookupEnv(key)
	if !ok || val == "" {
		return defaultVal
	}
	parsed, err := time.ParseDuration(val)
	if err != nil {
		log.Fatalf("environment variable %s must be a valid duration, got %q: %v", key, val, err)
	}
	return parsed
}

// getEnvAsFloat returns the environment variable parsed as float64, or the default value.
// Terminates the process if the value is set but cannot be parsed.
func getEnvAsFloat(key string, defaultVal float64) float64 {
	val, ok := os.LookupEnv(key)
	if !ok || val == "" {
		return defaultVal
	}
	parsed, err := strconv.ParseFloat(val, 64)
	if err != nil {
		log.Fatalf("environment variable %s must be a valid float, got %q: %v", key, val, err)
	}
	return parsed
}

// getEnvAsSlice returns the environment variable split by the separator.
// Returns nil if the variable is not set or empty.
func getEnvAsSlice(key, sep string) []string {
	val, ok := os.LookupEnv(key)
	if !ok || val == "" {
		return nil
	}
	parts := strings.Split(val, sep)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
