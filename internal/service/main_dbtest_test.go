package service

import (
	"os"
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
)

// TestMain tears down the shared throwaway Postgres cluster that the
// *_dbtest_test.go files boot via testdb.New.
func TestMain(m *testing.M) {
	code := m.Run()
	testdb.Shutdown()
	os.Exit(code)
}
