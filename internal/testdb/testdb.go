// Package testdb boots ONE throwaway PostgreSQL cluster per test process
// (initdb + pg_ctl, no Docker) and hands every test its own database cloned
// from a fully migrated template, so integration tests run against the real
// schema without any external setup.
//
// Usage:
//
//	func TestSomething(t *testing.T) {
//	    pool := testdb.New(t) // skips when postgres binaries are absent
//	    ...
//	}
//
// The package that uses testdb must wire shutdown in TestMain:
//
//	func TestMain(m *testing.M) {
//	    code := m.Run()
//	    testdb.Shutdown()
//	    os.Exit(code)
//	}
package testdb

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	clusterUser  = "postgres"
	templateName = "template_migrated"
)

type clusterState struct {
	dataDir    string
	sockDir    string
	port       int
	started    bool
	skipReason string
	err        error
}

var (
	once      sync.Once
	cluster   clusterState
	dbCounter atomic.Int64
	// createMu serializes CREATE DATABASE ... TEMPLATE calls: Postgres refuses
	// to clone a template that another backend is concurrently cloning.
	createMu sync.Mutex
)

// New returns a pgxpool.Pool connected to a fresh database cloned from the
// migrated template. The first call boots the shared cluster. Tests calling
// New may run in parallel — every test gets its own database. Databases are
// not dropped individually; Shutdown kills the whole cluster.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	once.Do(startCluster)
	if cluster.skipReason != "" {
		t.Skip(cluster.skipReason)
	}
	if cluster.err != nil {
		t.Fatalf("testdb: cluster bootstrap failed: %v", cluster.err)
	}

	dbName := fmt.Sprintf("test%d", dbCounter.Add(1))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	createMu.Lock()
	err := withAdminConn(ctx, func(conn *pgx.Conn) error {
		_, execErr := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", dbName, templateName))
		return execErr
	})
	createMu.Unlock()
	if err != nil {
		t.Fatalf("testdb: create database %s: %v", dbName, err)
	}

	pool, err := pgxpool.New(ctx, dsn(dbName))
	if err != nil {
		t.Fatalf("testdb: connect to %s: %v", dbName, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// Shutdown stops the shared cluster and removes its directories. Safe to call
// when the cluster never started. Meant for TestMain.
func Shutdown() {
	if !cluster.started {
		cleanupDirs()
		return
	}
	pgCtl, err := exec.LookPath("pg_ctl")
	if err == nil {
		cmd := exec.Command(pgCtl, "-D", cluster.dataDir, "-m", "immediate", "-w", "-t", "30", "stop")
		_ = cmd.Run()
	}
	cluster.started = false
	cleanupDirs()
}

func cleanupDirs() {
	if cluster.dataDir != "" {
		_ = os.RemoveAll(cluster.dataDir)
	}
	if cluster.sockDir != "" {
		_ = os.RemoveAll(cluster.sockDir)
	}
}

func dsn(dbName string) string {
	return fmt.Sprintf("postgres://%s@127.0.0.1:%d/%s?sslmode=disable", clusterUser, cluster.port, dbName)
}

func withAdminConn(ctx context.Context, fn func(*pgx.Conn) error) error {
	conn, err := pgx.Connect(ctx, dsn("postgres"))
	if err != nil {
		return fmt.Errorf("admin connect: %w", err)
	}
	defer conn.Close(context.Background())
	return fn(conn)
}

// startCluster is the sync.Once body: initdb → pg_ctl start → template DB →
// migrations. Any failure lands in cluster.err (or skipReason when the
// machine simply has no postgres binaries).
func startCluster() {
	initdb, err := exec.LookPath("initdb")
	if err != nil {
		cluster.skipReason = "postgres binaries not available (initdb not in PATH)"
		return
	}
	pgCtl, err := exec.LookPath("pg_ctl")
	if err != nil {
		cluster.skipReason = "postgres binaries not available (pg_ctl not in PATH)"
		return
	}

	// Data dir can live anywhere; the SOCKET dir must be short — unix socket
	// paths are capped at ~104 bytes on macOS and postgres refuses long ones.
	cluster.dataDir, err = os.MkdirTemp("", "sellico-pgtest-")
	if err != nil {
		cluster.err = fmt.Errorf("mkdir data dir: %w", err)
		return
	}
	cluster.sockDir, err = os.MkdirTemp("/tmp", "pgsock")
	if err != nil {
		cluster.err = fmt.Errorf("mkdir socket dir: %w", err)
		return
	}

	out, err := exec.Command(initdb,
		"-D", cluster.dataDir,
		"-U", clusterUser,
		"-A", "trust",
		"--no-sync",
		"--encoding=UTF8",
		"--locale=C",
	).CombinedOutput()
	if err != nil {
		cluster.err = fmt.Errorf("initdb: %w\n%s", err, out)
		return
	}

	cluster.port, err = freePort()
	if err != nil {
		cluster.err = fmt.Errorf("pick free port: %w", err)
		return
	}

	// Durability off across the board: the cluster is disposable by design.
	postgresOpts := fmt.Sprintf(
		"-p %d -c listen_addresses=127.0.0.1 -k %s -c fsync=off -c synchronous_commit=off -c full_page_writes=off -c max_connections=200",
		cluster.port, cluster.sockDir,
	)
	logFile := filepath.Join(cluster.dataDir, "postmaster.log")
	out, err = exec.Command(pgCtl,
		"-D", cluster.dataDir,
		"-o", postgresOpts,
		"-l", logFile,
		"-w", "-t", "60",
		"start",
	).CombinedOutput()
	if err != nil {
		serverLog, _ := os.ReadFile(logFile)
		cluster.err = fmt.Errorf("pg_ctl start: %w\n%s\n%s", err, out, serverLog)
		return
	}
	cluster.started = true

	if err := prepareTemplate(); err != nil {
		cluster.err = err
	}
}

// prepareTemplate creates the template database and runs all migrations into
// it exactly once; every test database is a cheap file-level clone of it.
func prepareTemplate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if err := withAdminConn(ctx, func(conn *pgx.Conn) error {
		_, execErr := conn.Exec(ctx, "CREATE DATABASE "+templateName)
		return execErr
	}); err != nil {
		return fmt.Errorf("create template db: %w", err)
	}

	migrationsDir := findMigrationsDir()
	if migrationsDir == "" {
		return fmt.Errorf("migrations directory not found")
	}

	// Prefer the golang-migrate CLI (the same tool production deploys use);
	// fall back to executing the sorted *.up.sql files directly.
	if migrateBin, err := exec.LookPath("migrate"); err == nil {
		out, runErr := exec.Command(migrateBin,
			"-path", migrationsDir,
			"-database", dsn(templateName),
			"up",
		).CombinedOutput()
		if runErr == nil {
			return nil
		}
		// Fall through to the file runner with context in the error chain.
		if fileErr := runMigrationFiles(ctx, migrationsDir); fileErr != nil {
			return fmt.Errorf("migrate CLI failed (%v: %s) and file fallback failed: %w", runErr, out, fileErr)
		}
		return nil
	}
	return runMigrationFiles(ctx, migrationsDir)
}

// runMigrationFiles executes every *.up.sql in lexical order against the
// template database (multi-statement files run over the simple protocol).
func runMigrationFiles(ctx context.Context, migrationsDir string) error {
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	var upFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".up.sql") {
			upFiles = append(upFiles, filepath.Join(migrationsDir, entry.Name()))
		}
	}
	sort.Strings(upFiles)

	conn, err := pgx.Connect(ctx, dsn(templateName))
	if err != nil {
		return fmt.Errorf("connect to template: %w", err)
	}
	defer conn.Close(context.Background())

	for _, file := range upFiles {
		sql, readErr := os.ReadFile(file)
		if readErr != nil {
			return fmt.Errorf("read migration %s: %w", file, readErr)
		}
		if _, execErr := conn.Exec(ctx, string(sql)); execErr != nil {
			return fmt.Errorf("exec migration %s: %w", filepath.Base(file), execErr)
		}
	}
	return nil
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if closeErr := listener.Close(); closeErr != nil {
		return 0, closeErr
	}
	return port, nil
}

// findMigrationsDir walks upward from this source file to the repo's
// migrations/ directory (same approach as internal/testutil).
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
