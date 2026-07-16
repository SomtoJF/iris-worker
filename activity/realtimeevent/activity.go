package realtimeevent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	redisInit "github.com/SomtoJF/iris-worker/initializers/redis"
	"github.com/redis/go-redis/v9"
)

type EventType string

const (
	EventApplicationSuccessful EventType = "APPLICATION_SUCCESSFUL"
	EventApplicationFailed     EventType = "APPLICATION_FAILED"
	EventUserNotification      EventType = "USER_NOTIFICATION"
	EventUserActionRequired    EventType = "USER_ACTION_REQUIRED"
	EventApplicationCancelled  EventType = "APPLICATION_CANCELLED"
	EventApplicationHalted     EventType = "APPLICATION_HALTED"
)

type Activities struct {
	redisClient *redis.Client
}

func NewActivities() *Activities {
	rdb, err := redisInit.GetRedis()
	if err != nil {
		log.Fatalf("Failed to get Redis client: %v", err)
	}

	return &Activities{
		redisClient: rdb,
	}
}

func getUserChannel(userID uint) string {
	return fmt.Sprintf("user:%d:events", userID)
}

func (a *Activities) PublishRedisEvent(ctx context.Context, userID uint, eventType string, data interface{}) error {
	eventData := map[string]interface{}{
		"action": eventType,
		"data":   data,
	}

	message, err := json.Marshal(eventData)
	if err != nil {
		return fmt.Errorf("failed to marshal event data: %w", err)
	}

	channelName := getUserChannel(userID)

	_, err = a.redisClient.Publish(ctx, channelName, message).Result()
	if err != nil {
		return fmt.Errorf("failed to publish to Redis: %w", err)
	}

	return nil
}
