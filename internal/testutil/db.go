// Package testutil provides shared helpers for integration tests.
package testutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// TestDB wraps a pgxpool.Pool wired to a dedicated test database.
// Each test gets its own schema so tests don't interfere with each other.
type TestDB struct {
	Pool    *pgxpool.Pool
	Queries *sqlcgen.Queries
}

// NewTestDB connects to PostgreSQL and runs all migrations.
// It expects DATABASE_URL to be set (default: local docker-compose Postgres).
// The caller must call cleanup() when done.
func NewTestDB(t *testing.T) (*TestDB, func()) {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://sellico:sellico@localhost:5432/sellico_test?sslmode=disable"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping test database: %v (is PostgreSQL running? try: docker compose up -d postgres)", err)
	}

	// Run migrations
	if err := runMigrations(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("run migrations: %v", err)
	}

	// Truncate all tables for a clean state
	if err := truncateAll(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("truncate tables: %v", err)
	}

	queries := sqlcgen.New(pool)

	cleanup := func() {
		_ = truncateAll(ctx, pool)
		pool.Close()
	}

	return &TestDB{Pool: pool, Queries: queries}, cleanup
}

// runMigrations reads .sql files from the migrations/ directory and executes them in order.
func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	migrationsDir := findMigrationsDir()
	if migrationsDir == "" {
		return fmt.Errorf("migrations directory not found")
	}

	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	var upFiles []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			if matched, _ := filepath.Match("*.up.sql", e.Name()); matched {
				upFiles = append(upFiles, filepath.Join(migrationsDir, e.Name()))
			}
		}
	}
	sort.Strings(upFiles)

	for _, f := range upFiles {
		sql, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", f, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			// Ignore "already exists" errors for idempotent re-runs
			return fmt.Errorf("exec migration %s: %w", filepath.Base(f), err)
		}
	}
	return nil
}

// truncateAll truncates all user-created tables in the public schema.
func truncateAll(ctx context.Context, pool *pgxpool.Pool) error {
	const q = `
		DO $$
		DECLARE
			r RECORD;
		BEGIN
			FOR r IN (
				SELECT tablename FROM pg_tables
				WHERE schemaname = 'public'
				  AND tablename NOT LIKE 'schema_migrations%'
			) LOOP
				EXECUTE 'TRUNCATE TABLE public.' || quote_ident(r.tablename) || ' CASCADE';
			END LOOP;
		END $$;
	`
	_, err := pool.Exec(ctx, q)
	return err
}

// findMigrationsDir walks upward from the current file to find the migrations/ directory.
func findMigrationsDir() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}

	dir := filepath.Dir(filename)
	for i := 0; i < 10; i++ {
		candidate := filepath.Join(dir, "migrations")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	return ""
}
