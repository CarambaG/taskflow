package httpapi

import (
	"context"

	"github.com/CarambaG/taskflow/internal/domain"
)

type contextKey string

const (
	userIDKey    contextKey = "user_id"
	requestIDKey contextKey = "request_id"
)

func withUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

func userID(ctx context.Context) (int64, error) {
	value, ok := ctx.Value(userIDKey).(int64)
	if !ok || value <= 0 {
		return 0, domain.ErrUnauthorized
	}
	return value, nil
}

func requestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}
