package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// redisConfig holds the Redis connection settings read from the environment.
type redisConfig struct {
	addr     string
	password string
	db       int
}

// newRedisClient opens a Redis client and verifies the connection with PING.
// Redis is required infrastructure, so a failed ping is returned as an error
// for the caller to treat as a startup failure.
func newRedisClient(ctx context.Context, cfg redisConfig) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.addr,
		Password: cfg.password,
		DB:       cfg.db,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		if closeErr := client.Close(); closeErr != nil {
			log.Printf("redis: close after failed ping: %v", closeErr)
		}
		return nil, fmt.Errorf("ping redis at %s: %w", cfg.addr, err)
	}
	return client, nil
}

// redisHealthHandler reports whether Redis is reachable. It is registered on
// /healthz so the connection is exercised after startup as well.
func redisHealthHandler(client *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := client.Ping(ctx).Err(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "redis": "down"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "redis": "up"})
	}
}
