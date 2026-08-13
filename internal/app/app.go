package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/CarambaG/taskflow/internal/auth"
	"github.com/CarambaG/taskflow/internal/cache"
	"github.com/CarambaG/taskflow/internal/config"
	mysqlrepo "github.com/CarambaG/taskflow/internal/repository/mysql"
	"github.com/CarambaG/taskflow/internal/service"
	"github.com/CarambaG/taskflow/internal/transport/httpapi"
	"github.com/desertbit/closer/v4"
	"github.com/redis/go-redis/v9"
)

type App struct {
	lifecycle       closer.Closer
	server          *http.Server
	db              *sql.DB
	redis           *redis.Client
	logger          *slog.Logger
	shutdownTimeout time.Duration
}

func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (*App, error) {
	db, err := mysqlrepo.Open(ctx, cfg.MySQLDSN)
	if err != nil {
		return nil, err
	}
	taskCache, redisClient, err := cache.NewRedis(ctx, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB, cfg.TaskCacheTTL)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	store := mysqlrepo.New(db)
	tokens := auth.NewManager(cfg.JWTSecret, cfg.JWTTTL)
	services := service.New(store, store, store, taskCache, tokens, logger)
	handler := httpapi.NewHandler(services, logger, store, taskCache)
	router := httpapi.NewRouter(handler, logger, tokens, cfg.RateLimitRPS, cfg.RateLimitBurst)

	application := &App{
		lifecycle: closer.New(), db: db, redis: redisClient, logger: logger, shutdownTimeout: cfg.ShutdownTimeout,
		server: &http.Server{
			Addr: cfg.HTTPAddr, Handler: router, ReadTimeout: cfg.HTTPReadTimeout,
			WriteTimeout: cfg.HTTPWriteTimeout, IdleTimeout: cfg.HTTPIdleTimeout,
		},
	}
	registerShutdownHooks(application.lifecycle, application.shutdownHTTP, application.closeDependencies)
	return application, nil
}

func (a *App) Run() error {
	closer.CloseOnInterrupt(a.lifecycle)
	closer.Routine(a.lifecycle, func() error {
		a.logger.Info("http server started", "address", a.server.Addr)
		err := a.server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve http: %w", err)
	})
	return closer.Wait(a.lifecycle, context.Background())
}

func registerShutdownHooks(lifecycle closer.Closer, shutdownHTTP, closeDependencies func() error) {
	closer.Hook(lifecycle, func(h closer.H) {
		h.OnClosingWithErr(shutdownHTTP)
		h.OnCloseWithErr(closeDependencies)
	})
}

func (a *App) shutdownHTTP() error {
	a.logger.Info("graceful shutdown started")
	ctx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout)
	defer cancel()
	if err := a.server.Shutdown(ctx); err != nil {
		a.logger.Error("http shutdown failed", "error", err)
		return err
	}
	return nil
}

func (a *App) closeDependencies() error {
	err := errors.Join(a.redis.Close(), a.db.Close())
	if err != nil {
		a.logger.Error("dependency shutdown failed", "error", err)
		return err
	}
	a.logger.Info("graceful shutdown completed")
	return nil
}

func NewLogger(level string) *slog.Logger {
	var parsed slog.Level
	switch strings.ToLower(level) {
	case "debug":
		parsed = slog.LevelDebug
	case "warn", "warning":
		parsed = slog.LevelWarn
	case "error":
		parsed = slog.LevelError
	default:
		parsed = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parsed}))
}
