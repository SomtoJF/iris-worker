package redis

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"
)

var redisClient *redis.Client

func GetRedis() (*redis.Client, error) {
	if redisClient == nil {
		var err error
		redisClient, err = InitRedis(context.Background())
		if err != nil {
			return nil, fmt.Errorf("failed to initialize Redis client: %v", err)
		}
	}
	return redisClient, nil
}

func InitRedis(ctx context.Context) (*redis.Client, error) {
	if redisClient != nil {
		return redisClient, nil
	}

	addr := os.Getenv("REDIS_HOST")
	if addr == "" {
		addr = "localhost:6379"
	}
	pass := os.Getenv("REDIS_PASSWORD")

	if strings.HasPrefix(addr, "redis://") {
		if u, err := url.Parse(addr); err == nil {
			addr = u.Host
			if pass == "" && u.User != nil {
				if p, ok := u.User.Password(); ok {
					pass = p
				}
			}
		}
	}

	redisClient = redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: pass,
		DB:       0,
	})

	_, err := redisClient.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("failed to connect to Redis: %v", err)
		return nil, fmt.Errorf("failed to connect to Redis: %v", err)
	}

	log.Printf("Successfully connected to Redis at %s", addr)

	return redisClient, nil
}
