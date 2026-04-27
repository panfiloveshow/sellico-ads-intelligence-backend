// Command rotate-encryption-key re-encrypts every encrypted column under the
// new key version. It reads with the old key (or any key in the keyring) and
// writes with the latest key from the keyring, prefixing the ciphertext with
// the version tag (e.g. "v2:").
//
// Workflow:
//
//  1. Generate a new 32-byte key (hex):
//       openssl rand -hex 32
//
//  2. Add to the api/worker .env so the new key is available for reads:
//       ENCRYPTION_KEY=<original 32-byte key>      # legacy single-key var
//       ENCRYPTION_KEYS_V1=<original key, hex>     # required: keyring version 1
//       ENCRYPTION_KEYS_V2=<new key, hex>          # required: keyring version 2
//
//     Restart api and worker. They now decrypt either, encrypt with v2.
//
//  3. Run this command (one-shot, idempotent):
//       DATABASE_URL=... \
//       ENCRYPTION_KEYS_V1=... ENCRYPTION_KEYS_V2=... \
//       go run ./cmd/rotate-encryption-key --apply
//
//     Default is dry-run; --apply commits writes. --batch-size controls
//     transaction granularity (default 50).
//
//  4. After all rows report `version=2`, drop ENCRYPTION_KEYS_V1 from env.
//     Keep ENCRYPTION_KEY pointing at the new key (so legacy single-key
//     callers continue to work in the unlikely event of a regression).
//
// Currently the only encrypted column is seller_cabinets.encrypted_token.
// Add new tables here as the schema grows.
package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
)

func main() {
	apply := flag.Bool("apply", false, "if false (default), do a dry-run with no writes")
	batchSize := flag.Int("batch-size", 50, "rows per transaction")
	flag.Parse()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	keyring, err := loadKeyringFromEnv()
	if err != nil {
		log.Fatalf("load keyring: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer pool.Close()

	stats, err := rotateSellerCabinets(ctx, pool, keyring, *apply, *batchSize)
	if err != nil {
		log.Fatalf("rotate seller_cabinets: %v", err)
	}
	fmt.Printf("seller_cabinets: scanned=%d, already_current=%d, rotated=%d, failed=%d, dry_run=%t\n",
		stats.scanned, stats.alreadyCurrent, stats.rotated, stats.failed, !*apply)
	if stats.failed > 0 {
		os.Exit(1)
	}
}

type rotateStats struct {
	scanned        int
	alreadyCurrent int
	rotated        int
	failed         int
}

func rotateSellerCabinets(ctx context.Context, pool *pgxpool.Pool, kr *crypto.Keyring, apply bool, batchSize int) (rotateStats, error) {
	const selectSQL = `SELECT id, encrypted_token FROM seller_cabinets WHERE deleted_at IS NULL ORDER BY id`
	const updateSQL = `UPDATE seller_cabinets SET encrypted_token = $1, updated_at = NOW() WHERE id = $2 AND encrypted_token = $3`

	rows, err := pool.Query(ctx, selectSQL)
	if err != nil {
		return rotateStats{}, fmt.Errorf("select: %w", err)
	}

	type pending struct {
		id       string
		oldCT    string
		newCT    string
		hadVer   int
	}
	var batch []pending
	flush := func() error {
		if len(batch) == 0 || !apply {
			batch = batch[:0]
			return nil
		}
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		for _, p := range batch {
			tag, err := tx.Exec(ctx, updateSQL, p.newCT, p.id, p.oldCT)
			if err != nil {
				return fmt.Errorf("update %s: %w", p.id, err)
			}
			if tag.RowsAffected() != 1 {
				return fmt.Errorf("update %s: expected 1 row affected, got %d (concurrent change?)", p.id, tag.RowsAffected())
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}

	stats := rotateStats{}
	defer rows.Close()
	for rows.Next() {
		stats.scanned++
		var id, ct string
		if err := rows.Scan(&id, &ct); err != nil {
			return stats, err
		}
		// Skip if already at latest version (cheap check via prefix).
		if hasLatestVersionPrefix(ct, kr.LatestVersion()) {
			stats.alreadyCurrent++
			continue
		}
		plain, err := crypto.DecryptWithKeyring(ct, kr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "decrypt %s: %v (skipping)\n", id, err)
			stats.failed++
			continue
		}
		newCT, err := crypto.EncryptWithKeyring(plain, kr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "encrypt %s: %v\n", id, err)
			stats.failed++
			continue
		}
		batch = append(batch, pending{id: id, oldCT: ct, newCT: newCT})
		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				return stats, err
			}
			stats.rotated += batchSize
		}
	}
	if err := rows.Err(); err != nil {
		return stats, err
	}
	tail := len(batch)
	if err := flush(); err != nil {
		return stats, err
	}
	stats.rotated += tail
	return stats, nil
}

func hasLatestVersionPrefix(ct string, latest int) bool {
	prefix := fmt.Sprintf("v%d:", latest)
	return strings.HasPrefix(ct, prefix)
}

// loadKeyringFromEnv parses ENCRYPTION_KEYS_V<N>=<hex 32 bytes> for N=1..max.
func loadKeyringFromEnv() (*crypto.Keyring, error) {
	keys := map[int][]byte{}
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		name, value := kv[:eq], kv[eq+1:]
		if !strings.HasPrefix(name, "ENCRYPTION_KEYS_V") {
			continue
		}
		v, err := strconv.Atoi(strings.TrimPrefix(name, "ENCRYPTION_KEYS_V"))
		if err != nil || v <= 0 {
			continue
		}
		raw, err := hex.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("%s: not valid hex: %w", name, err)
		}
		keys[v] = raw
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no ENCRYPTION_KEYS_V<N> env vars found")
	}
	return crypto.NewKeyring(keys)
}
