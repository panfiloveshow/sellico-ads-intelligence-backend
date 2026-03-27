package app

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// Dependencies contains shared runtime dependencies for API and worker processes.
type Dependencies struct {
	Config  *config.Config
	Logger  zerolog.Logger
	DB      *pgxpool.Pool
	Redis   *redis.Client
	Queries *sqlcgen.Queries
}

func NewDependencies(ctx context.Context, cfg *config.Config) (*Dependencies, error) {
	logger, err := newLogger(cfg.LogLevel)
	if err != nil {
		return nil, err
	}

	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	redisClient := redis.NewClient(redisOpts)
	if err := redisClient.Ping(ctx).Err(); err != nil {
		db.Close()
		_ = redisClient.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &Dependencies{
		Config:  cfg,
		Logger:  logger,
		DB:      db,
		Redis:   redisClient,
		Queries: sqlcgen.New(db),
	}, nil
}

func (d *Dependencies) Ready(ctx context.Context) error {
	if err := d.DB.Ping(ctx); err != nil {
		return fmt.Errorf("database not ready: %w", err)
	}
	if err := d.Redis.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis not ready: %w", err)
	}
	return nil
}

func (d *Dependencies) Close() error {
	d.DB.Close()
	return d.Redis.Close()
}

func newLogger(level string) (zerolog.Logger, error) {
	parsed, err := zerolog.ParseLevel(level)
	if err != nil {
		return zerolog.Logger{}, fmt.Errorf("parse log level: %w", err)
	}
	zerolog.SetGlobalLevel(parsed)
	return zerolog.New(os.Stdout).With().Timestamp().Logger(), nil
}
