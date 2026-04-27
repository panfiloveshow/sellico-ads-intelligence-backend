package app

import (
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/rs/zerolog"
)

// applyMemoryLimit sets the Go runtime soft memory limit so that GC can pace
// itself before the container's hard cgroup limit triggers an OOM kill.
//
// Resolution order:
//  1. GOMEMLIMIT_BYTES env override (explicit integer, in bytes)
//  2. GOMEMLIMIT env (Go's native format: "700MiB", "1GiB", etc.) — handled by
//     the Go runtime automatically, we just log what it resolved to
//  3. cgroup v2 limit at /sys/fs/cgroup/memory.max → 80% of that
//  4. cgroup v1 limit at /sys/fs/cgroup/memory/memory.limit_in_bytes → 80%
//  5. No-op (Go default: math.MaxInt64) if nothing detected
//
// The 80% headroom leaves room for non-heap allocations (stacks, runtime
// metadata, mmap'd files) below the cgroup ceiling.
func applyMemoryLimit(logger zerolog.Logger) {
	if explicit := os.Getenv("GOMEMLIMIT_BYTES"); explicit != "" {
		n, err := strconv.ParseInt(explicit, 10, 64)
		if err != nil || n <= 0 {
			logger.Warn().Str("value", explicit).Msg("GOMEMLIMIT_BYTES invalid, ignoring")
		} else {
			prev := debug.SetMemoryLimit(n)
			logger.Info().Int64("limit_bytes", n).Int64("previous", prev).Msg("memory limit set from GOMEMLIMIT_BYTES")
			return
		}
	}

	if native := os.Getenv("GOMEMLIMIT"); native != "" {
		current := debug.SetMemoryLimit(-1)
		logger.Info().Str("env", native).Int64("resolved_bytes", current).Msg("memory limit from GOMEMLIMIT env (handled by runtime)")
		return
	}

	cgroupLimit, source, ok := readCgroupMemoryLimit()
	if !ok {
		logger.Debug().Msg("no cgroup memory limit detected, leaving Go default")
		return
	}

	const headroomNum, headroomDen = 80, 100
	target := cgroupLimit / headroomDen * headroomNum
	prev := debug.SetMemoryLimit(target)
	logger.Info().
		Str("source", source).
		Int64("cgroup_limit_bytes", cgroupLimit).
		Int64("limit_bytes", target).
		Int64("previous", prev).
		Msg("memory limit set from cgroup (80% headroom)")
}

// readCgroupMemoryLimit returns the container memory limit, the path it was
// read from, and ok=true. cgroup v2 takes precedence over v1.
//
// Returns ok=false if no limit is set (host cgroup with "max", missing file,
// or values larger than 1 EiB which usually mean "no limit").
func readCgroupMemoryLimit() (int64, string, bool) {
	const sentinelMax = int64(1) << 60

	for _, p := range []string{
		"/sys/fs/cgroup/memory.max",                 // cgroup v2 (unified)
		"/sys/fs/cgroup/memory/memory.limit_in_bytes", // cgroup v1
	} {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(raw))
		if s == "" || s == "max" {
			continue
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 || n >= sentinelMax {
			continue
		}
		return n, p, true
	}
	return 0, "", false
}

// memLimitDescription is a human-readable summary used in startup logs.
func memLimitDescription() string {
	current := debug.SetMemoryLimit(-1)
	return fmt.Sprintf("Go runtime memory limit: %d bytes (%.1f MiB)", current, float64(current)/(1024*1024))
}
