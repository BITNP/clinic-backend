package app

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func envString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Fatalf("invalid %s: %v", key, err)
	}
	return d
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("invalid %s: %v", key, err)
	}
	return i
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		log.Fatalf("invalid %s: %v", key, err)
	}
	return b
}

func envSameSite(key string, fallback http.SameSite) http.SameSite {
	switch strings.ToLower(os.Getenv(key)) {
	case "none":
		return http.SameSiteNoneMode
	case "lax":
		return http.SameSiteLaxMode
	case "strict":
		return http.SameSiteStrictMode
	case "":
		return fallback
	default:
		log.Fatalf("invalid %s: must be none, lax, or strict", key)
		return fallback
	}
}

func envLocation(key string, fallback *time.Location) *time.Location {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	loc, err := time.LoadLocation(v)
	if err != nil {
		log.Fatalf("invalid %s: %v", key, err)
	}
	return loc
}
