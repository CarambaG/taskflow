package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Environment      string
	HTTPAddr         string
	HTTPReadTimeout  time.Duration
	HTTPWriteTimeout time.Duration
	HTTPIdleTimeout  time.Duration
	ShutdownTimeout  time.Duration
	MySQLDSN         string
	RedisAddr        string
	RedisPassword    string
	RedisDB          int
	JWTSecret        string
	JWTTTL           time.Duration
	TaskCacheTTL     time.Duration
	RateLimitRPS     float64
	RateLimitBurst   int
	LogLevel         string
}

func Load() (Config, error) {
	cfg := Config{
		Environment:   env("APP_ENV", "development"),
		HTTPAddr:      env("HTTP_ADDR", ":8080"),
		MySQLDSN:      os.Getenv("MYSQL_DSN"),
		RedisAddr:     env("REDIS_ADDR", "localhost:6379"),
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
		JWTSecret:     os.Getenv("JWT_SECRET"),
		LogLevel:      env("LOG_LEVEL", "info"),
	}

	var err error
	if cfg.HTTPReadTimeout, err = duration("HTTP_READ_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.HTTPWriteTimeout, err = duration("HTTP_WRITE_TIMEOUT", 15*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.HTTPIdleTimeout, err = duration("HTTP_IDLE_TIMEOUT", 60*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = duration("SHUTDOWN_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.JWTTTL, err = duration("JWT_TTL", 24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.TaskCacheTTL, err = duration("TASK_CACHE_TTL", 5*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.RedisDB, err = integer("REDIS_DB", 0); err != nil {
		return Config{}, err
	}
	if cfg.RateLimitBurst, err = integer("RATE_LIMIT_BURST", 40); err != nil {
		return Config{}, err
	}
	if cfg.RateLimitRPS, err = decimal("RATE_LIMIT_RPS", 20); err != nil {
		return Config{}, err
	}

	if cfg.MySQLDSN == "" {
		return Config{}, errors.New("MYSQL_DSN is required")
	}
	if len(cfg.JWTSecret) < 32 {
		return Config{}, errors.New("JWT_SECRET must contain at least 32 characters")
	}
	if cfg.RateLimitRPS <= 0 || cfg.RateLimitBurst <= 0 {
		return Config{}, errors.New("rate limit values must be positive")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func duration(key string, fallback time.Duration) (time.Duration, error) {
	value := env(key, fallback.String())
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func integer(key string, fallback int) (int, error) {
	value := env(key, strconv.Itoa(fallback))
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func decimal(key string, fallback float64) (float64, error) {
	value := env(key, strconv.FormatFloat(fallback, 'f', -1, 64))
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}
