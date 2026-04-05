package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port            string
	DBDir           string // empty = in-memory
	Seed            bool
	LogLevel        string // "debug", "info", "warn", "error"
	RateLimitRPS    int    // requests per second per IP
	ShutdownTimeout time.Duration
	CORSOrigins     []string // comma-separated in env
}

func Load() Config {
	return Config{
		Port:            getEnv("PORT", "8080"),
		DBDir:           getEnv("DB_DIR", ""),
		Seed:            getEnvBool("SEED", false),
		LogLevel:        getEnv("LOG_LEVEL", "info"),
		RateLimitRPS:    getEnvInt("RATE_LIMIT_RPS", 100),
		ShutdownTimeout: getEnvDuration("SHUTDOWN_TIMEOUT", 15*time.Second),
		CORSOrigins:     getEnvSlice("CORS_ORIGINS", []string{"*"}),
	}
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	s := getEnv(key, "")
	if s == "" {
		return defaultValue
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return defaultValue
	}
	return v
}

func getEnvInt(key string, defaultValue int) int {
	s := getEnv(key, "")
	if s == "" {
		return defaultValue
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultValue
	}
	return v
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	s := getEnv(key, "")
	if s == "" {
		return defaultValue
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return defaultValue
	}
	return v
}

func getEnvSlice(key string, defaultValue []string) []string {
	s := getEnv(key, "")
	if s == "" {
		return defaultValue
	}
	return strings.Split(s, ",")
}
