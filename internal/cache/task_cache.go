package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/CarambaG/taskflow/internal/domain"
	"github.com/redis/go-redis/v9"
)

var ErrMiss = errors.New("cache miss")

type TaskCache interface {
	Get(ctx context.Context, filter domain.TaskFilter) (domain.TaskList, int64, error)
	Set(ctx context.Context, filter domain.TaskFilter, list domain.TaskList, generation int64) error
	InvalidateTeam(ctx context.Context, teamID int64) error
}

type RedisTaskCache struct {
	client *redis.Client
	ttl    time.Duration
}

func NewRedis(ctx context.Context, addr, password string, db int, ttl time.Duration) (*RedisTaskCache, *redis.Client, error) {
	client := redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("ping redis: %w", err)
	}
	return &RedisTaskCache{client: client, ttl: ttl}, client, nil
}

func (c *RedisTaskCache) Get(ctx context.Context, filter domain.TaskFilter) (domain.TaskList, int64, error) {
	generation, err := c.generation(ctx, filter.TeamID)
	if err != nil {
		return domain.TaskList{}, 0, err
	}
	payload, err := c.client.Get(ctx, cacheKey(filter, generation)).Bytes()
	if errors.Is(err, redis.Nil) {
		return domain.TaskList{}, generation, ErrMiss
	}
	if err != nil {
		return domain.TaskList{}, generation, fmt.Errorf("get task cache: %w", err)
	}
	currentGeneration, err := c.generation(ctx, filter.TeamID)
	if err != nil {
		return domain.TaskList{}, generation, err
	}
	if currentGeneration != generation {
		return domain.TaskList{}, currentGeneration, ErrMiss
	}
	var list domain.TaskList
	if err := json.Unmarshal(payload, &list); err != nil {
		return domain.TaskList{}, generation, fmt.Errorf("decode task cache: %w", err)
	}
	return list, generation, nil
}

func (c *RedisTaskCache) Set(ctx context.Context, filter domain.TaskFilter, list domain.TaskList, generation int64) error {
	payload, err := json.Marshal(list)
	if err != nil {
		return fmt.Errorf("encode task cache: %w", err)
	}
	if err := c.client.Set(ctx, cacheKey(filter, generation), payload, c.ttl).Err(); err != nil {
		return fmt.Errorf("set task cache: %w", err)
	}
	return nil
}

func (c *RedisTaskCache) InvalidateTeam(ctx context.Context, teamID int64) error {
	if err := c.client.Incr(ctx, generationKey(teamID)).Err(); err != nil {
		return fmt.Errorf("increment task cache generation: %w", err)
	}
	return nil
}

func (c *RedisTaskCache) Ping(ctx context.Context) error { return c.client.Ping(ctx).Err() }

func (c *RedisTaskCache) generation(ctx context.Context, teamID int64) (int64, error) {
	generation, err := c.client.Get(ctx, generationKey(teamID)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get task cache generation: %w", err)
	}
	return generation, nil
}

func cacheKey(filter domain.TaskFilter, generation int64) string {
	status, assignee := "", ""
	if filter.Status != nil {
		status = string(*filter.Status)
	}
	if filter.AssigneeID != nil {
		assignee = strconv.FormatInt(*filter.AssigneeID, 10)
	}
	raw := fmt.Sprintf("%d|%s|%s|%d|%d", filter.TeamID, status, assignee, filter.Limit, filter.Offset)
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("taskflow:tasks:%d:%d:%s", filter.TeamID, generation, hex.EncodeToString(sum[:]))
}

func generationKey(teamID int64) string {
	return "taskflow:tasks:generation:" + strconv.FormatInt(teamID, 10)
}
