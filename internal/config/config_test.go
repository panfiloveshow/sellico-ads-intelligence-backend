package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setRequiredEnvVars sets all required environment variables to valid values.
// Returns a cleanup function that unsets them.
func setRequiredEnvVars(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("JWT_SECRET", "test-jwt-secret-min-32-chars-long!!")
	t.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")
}

func TestLoad_Defaults(t *testing.T) {
	setRequiredEnvVars(t)

	// Unset optional vars to ensure defaults are used
	os.Unsetenv("SERVER_HOST")
	os.Unsetenv("SERVER_PORT")
	os.Unsetenv("APP_VERSION")
	os.Unsetenv("JWT_ACCESS_TOKEN_TTL")
	os.Unsetenv("JWT_REFRESH_TOKEN_TTL")
	os.Unsetenv("WB_API_BASE_URL")
	os.Unsetenv("WB_API_RATE_LIMIT")
	os.Unsetenv("WB_PARSER_MIN_DELAY")
	os.Unsetenv("WB_PARSER_PROXIES")
	os.Unsetenv("EXPORT_STORAGE_PATH")
	os.Unsetenv("SMTP_HOST")
	os.Unsetenv("SMTP_PORT")
	os.Unsetenv("SMTP_USERNAME")
	os.Unsetenv("SMTP_PASSWORD")
	os.Unsetenv("SMTP_FROM_EMAIL")
	os.Unsetenv("SMTP_FROM_NAME")
	os.Unsetenv("SMTP_TIMEOUT")
	os.Unsetenv("LOG_LEVEL")

	cfg := Load()

	require.NotNil(t, cfg)
	assert.Equal(t, "0.0.0.0", cfg.ServerHost)
	assert.Equal(t, 8080, cfg.ServerPort)
	assert.Equal(t, "dev", cfg.AppVersion)
	assert.Equal(t, "postgres://user:pass@localhost:5432/db", cfg.DatabaseURL)
	assert.Equal(t, "redis://localhost:6379/0", cfg.RedisURL)
	assert.Equal(t, "test-jwt-secret-min-32-chars-long!!", cfg.JWTSecret)
	assert.Equal(t, 15*time.Minute, cfg.JWTAccessTokenTTL)
	assert.Equal(t, 168*time.Hour, cfg.JWTRefreshTokenTTL)
	assert.Equal(t, "0123456789abcdef0123456789abcdef", cfg.EncryptionKey)
	assert.Equal(t, "https://advert-api.wildberries.ru", cfg.WBAPIBaseURL)
	assert.Equal(t, 10, cfg.WBAPIRateLimit)
	assert.Equal(t, 2*time.Second, cfg.WBParserMinDelay)
	assert.Nil(t, cfg.WBParserProxies)
	assert.Equal(t, "./exports", cfg.ExportStoragePath)
	assert.Equal(t, "", cfg.SMTPHost)
	assert.Equal(t, 587, cfg.SMTPPort)
	assert.Equal(t, "", cfg.SMTPFromEmail)
	assert.Equal(t, "Sellico Ads Intelligence", cfg.SMTPFromName)
	assert.Equal(t, 10*time.Second, cfg.SMTPTimeout)
	assert.Equal(t, "info", cfg.LogLevel)
}

func TestLoad_CustomValues(t *testing.T) {
	setRequiredEnvVars(t)

	t.Setenv("SERVER_HOST", "127.0.0.1")
	t.Setenv("SERVER_PORT", "9090")
	t.Setenv("APP_VERSION", "1.2.3")
	t.Setenv("JWT_ACCESS_TOKEN_TTL", "30m")
	t.Setenv("JWT_REFRESH_TOKEN_TTL", "720h")
	t.Setenv("WB_API_BASE_URL", "https://custom-api.example.com")
	t.Setenv("WB_API_RATE_LIMIT", "20")
	t.Setenv("WB_PARSER_MIN_DELAY", "5s")
	t.Setenv("WB_PARSER_PROXIES", "http://proxy1:8080, http://proxy2:8080, http://proxy3:8080")
	t.Setenv("EXPORT_STORAGE_PATH", "/tmp/exports")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "2525")
	t.Setenv("SMTP_USERNAME", "mailer")
	t.Setenv("SMTP_PASSWORD", "secret")
	t.Setenv("SMTP_FROM_EMAIL", "reports@example.com")
	t.Setenv("SMTP_FROM_NAME", "Sellico Reports")
	t.Setenv("SMTP_TIMEOUT", "7s")
	t.Setenv("LOG_LEVEL", "debug")

	cfg := Load()

	require.NotNil(t, cfg)
	assert.Equal(t, "127.0.0.1", cfg.ServerHost)
	assert.Equal(t, 9090, cfg.ServerPort)
	assert.Equal(t, "1.2.3", cfg.AppVersion)
	assert.Equal(t, 30*time.Minute, cfg.JWTAccessTokenTTL)
	assert.Equal(t, 720*time.Hour, cfg.JWTRefreshTokenTTL)
	assert.Equal(t, "https://custom-api.example.com", cfg.WBAPIBaseURL)
	assert.Equal(t, 20, cfg.WBAPIRateLimit)
	assert.Equal(t, 5*time.Second, cfg.WBParserMinDelay)
	assert.Equal(t, []string{"http://proxy1:8080", "http://proxy2:8080", "http://proxy3:8080"}, cfg.WBParserProxies)
	assert.Equal(t, "/tmp/exports", cfg.ExportStoragePath)
	assert.Equal(t, "smtp.example.com", cfg.SMTPHost)
	assert.Equal(t, 2525, cfg.SMTPPort)
	assert.Equal(t, "mailer", cfg.SMTPUsername)
	assert.Equal(t, "secret", cfg.SMTPPassword)
	assert.Equal(t, "reports@example.com", cfg.SMTPFromEmail)
	assert.Equal(t, "Sellico Reports", cfg.SMTPFromName)
	assert.Equal(t, 7*time.Second, cfg.SMTPTimeout)
	assert.Equal(t, "debug", cfg.LogLevel)
}

func TestLoad_ProxiesSingleValue(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("WB_PARSER_PROXIES", "http://single-proxy:8080")

	cfg := Load()
	assert.Equal(t, []string{"http://single-proxy:8080"}, cfg.WBParserProxies)
}

func TestLoad_ProxiesEmptyString(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("WB_PARSER_PROXIES", "")

	cfg := Load()
	assert.Nil(t, cfg.WBParserProxies)
}

func TestValidateConfig_RejectsWeakSecrets(t *testing.T) {
	err := validateConfig(&Config{
		JWTSecret:     "short",
		EncryptionKey: "0123456789abcdef0123456789abcdef",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "JWT_SECRET")
}

func TestValidateConfig_RejectsInvalidEncryptionKeyLength(t *testing.T) {
	err := validateConfig(&Config{
		JWTSecret:     "test-jwt-secret-min-32-chars-long!!",
		EncryptionKey: "too-short",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ENCRYPTION_KEY")
}

func TestValidateConfig_RejectsNegativeRateLimit(t *testing.T) {
	err := validateConfig(&Config{
		JWTSecret:      "test-jwt-secret-min-32-chars-long!!",
		EncryptionKey:  "0123456789abcdef0123456789abcdef",
		RateLimitRPS:   -1,
		RateLimitBurst: 40,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "RATE_LIMIT_RPS")
}

func TestValidateConfig_RejectsSMTPHostWithoutFromEmail(t *testing.T) {
	err := validateConfig(&Config{
		JWTSecret:      "test-jwt-secret-min-32-chars-long!!",
		EncryptionKey:  "0123456789abcdef0123456789abcdef",
		RateLimitRPS:   20,
		RateLimitBurst: 40,
		SMTPHost:       "smtp.example.com",
		SMTPPort:       587,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SMTP_FROM_EMAIL")
}

func TestGetEnvOrDefault_Set(t *testing.T) {
	t.Setenv("TEST_VAR", "custom")
	assert.Equal(t, "custom", getEnvOrDefault("TEST_VAR", "default"))
}

func TestGetEnvOrDefault_Unset(t *testing.T) {
	os.Unsetenv("TEST_VAR_UNSET")
	assert.Equal(t, "default", getEnvOrDefault("TEST_VAR_UNSET", "default"))
}

func TestGetEnvOrDefault_Empty(t *testing.T) {
	t.Setenv("TEST_VAR_EMPTY", "")
	assert.Equal(t, "default", getEnvOrDefault("TEST_VAR_EMPTY", "default"))
}

func TestGetEnvAsInt_Valid(t *testing.T) {
	t.Setenv("TEST_INT", "42")
	assert.Equal(t, 42, getEnvAsInt("TEST_INT", 0))
}

func TestGetEnvAsInt_Unset(t *testing.T) {
	os.Unsetenv("TEST_INT_UNSET")
	assert.Equal(t, 99, getEnvAsInt("TEST_INT_UNSET", 99))
}

func TestGetEnvAsDuration_Valid(t *testing.T) {
	t.Setenv("TEST_DUR", "5m")
	assert.Equal(t, 5*time.Minute, getEnvAsDuration("TEST_DUR", time.Second))
}

func TestGetEnvAsDuration_Unset(t *testing.T) {
	os.Unsetenv("TEST_DUR_UNSET")
	assert.Equal(t, 10*time.Second, getEnvAsDuration("TEST_DUR_UNSET", 10*time.Second))
}

func TestGetEnvAsSlice_CommaSeparated(t *testing.T) {
	t.Setenv("TEST_SLICE", "a, b, c")
	result := getEnvAsSlice("TEST_SLICE", ",")
	assert.Equal(t, []string{"a", "b", "c"}, result)
}

func TestGetEnvAsSlice_Unset(t *testing.T) {
	os.Unsetenv("TEST_SLICE_UNSET")
	assert.Nil(t, getEnvAsSlice("TEST_SLICE_UNSET", ","))
}

func TestGetEnvAsSlice_OnlyWhitespace(t *testing.T) {
	t.Setenv("TEST_SLICE_WS", " , , ")
	assert.Nil(t, getEnvAsSlice("TEST_SLICE_WS", ","))
}
