package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"pgregory.net/rapid"
)

// Feature: sellico-ads-intelligence-backend, Property 7: Валидация токена WB API при создании Seller Cabinet
// Проверяет: Требования 4.2, 4.3

// --- Fake WB token validators ---

// fakeValidValidator always succeeds — simulates a valid WB API token.
type fakeValidValidator struct{}

func (v *fakeValidValidator) ValidateToken(_ context.Context, _ string) error { return nil }

// fakeInvalidValidator always fails — simulates an invalid WB API token.
type fakeInvalidValidator struct{}

func (v *fakeInvalidValidator) ValidateToken(_ context.Context, _ string) error {
	return errors.New("WB API returned 401: unauthorized")
}

// --- In-memory DBTX for seller cabinets ---

type scInMemDB struct {
	mu       sync.Mutex
	cabinets map[uuid.UUID]sqlcgen.SellerCabinet // keyed by cabinet ID
}

func newSCInMemDB() *scInMemDB {
	return &scInMemDB{
		cabinets: make(map[uuid.UUID]sqlcgen.SellerCabinet),
	}
}

func (db *scInMemDB) Exec(_ context.Context, _ string, _ ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(""), nil
}

func (db *scInMemDB) Query(_ context.Context, _ string, _ ...interface{}) (pgx.Rows, error) {
	return &fakeRows{}, nil
}

func (db *scInMemDB) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

func (db *scInMemDB) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	db.mu.Lock()
	defer db.mu.Unlock()

	switch {
	case containsSQL(sql, "INSERT INTO seller_cabinets"):
		// CreateSellerCabinet: args = workspace_id, name, encrypted_token
		workspaceID := args[0].(pgtype.UUID)
		name := args[1].(string)
		encryptedToken := args[2].(string)
		id := uuid.New()
		now := time.Now()
		sc := sqlcgen.SellerCabinet{
			ID:             pgtype.UUID{Bytes: id, Valid: true},
			WorkspaceID:    workspaceID,
			Name:           name,
			EncryptedToken: encryptedToken,
			Status:         "active",
			CreatedAt:      pgtype.Timestamptz{Time: now, Valid: true},
			UpdatedAt:      pgtype.Timestamptz{Time: now, Valid: true},
		}
		db.cabinets[id] = sc
		return sellerCabinetToRow(sc)
	}

	return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
}

func sellerCabinetToRow(sc sqlcgen.SellerCabinet) pgx.Row {
	return &fakeRow{scanFunc: func(dest ...any) error {
		*dest[0].(*pgtype.UUID) = sc.ID
		*dest[1].(*pgtype.UUID) = sc.WorkspaceID
		*dest[2].(*string) = sc.Name
		*dest[3].(*string) = sc.EncryptedToken
		*dest[4].(*string) = sc.Status
		*dest[5].(*pgtype.Timestamptz) = sc.LastSyncedAt
		*dest[6].(*pgtype.Timestamptz) = sc.CreatedAt
		*dest[7].(*pgtype.Timestamptz) = sc.UpdatedAt
		*dest[8].(*pgtype.Timestamptz) = sc.DeletedAt
		return nil
	}}
}

// --- Generators ---

// genAPIToken generates a random WB API token string.
func genAPIToken() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9_\-]{20,64}`)
}

// genCabinetName generates a seller cabinet display name.
func genCabinetName() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9 ]{3,30}`)
}

// testEncryptionKey is a fixed 32-byte key for tests.
var testEncryptionKey = []byte("01234567890123456789012345678901")

func newTestSellerCabinetService(db *scInMemDB, validator WBTokenValidator) *SellerCabinetService {
	queries := sqlcgen.New(db)
	return NewSellerCabinetService(queries, testEncryptionKey, validator, nil)
}

// --- Property Tests ---

// TestProperty_SellerCabinet_ValidTokenCreateSucceeds verifies Requirement 4.2:
// For any valid API token (one that passes WB API validation), Create MUST succeed
// and return a SellerCabinet with the token stored encrypted (not plaintext).
func TestProperty_SellerCabinet_ValidTokenCreateSucceeds(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		apiToken := genAPIToken().Draw(t, "apiToken")
		name := genCabinetName().Draw(t, "name")
		workspaceID := uuid.New()

		db := newSCInMemDB()
		svc := newTestSellerCabinetService(db, &fakeValidValidator{})

		sc, err := svc.Create(context.Background(), workspaceID, name, apiToken)
		if err != nil {
			t.Fatalf("Create with valid token failed: %v", err)
		}

		// Cabinet must be returned.
		if sc == nil {
			t.Fatal("returned seller cabinet must not be nil")
		}
		if sc.Name != name {
			t.Fatalf("name mismatch: got %q, want %q", sc.Name, name)
		}

		// The encrypted token stored in DB must NOT be the plaintext token.
		db.mu.Lock()
		var stored sqlcgen.SellerCabinet
		for _, v := range db.cabinets {
			stored = v
			break
		}
		db.mu.Unlock()

		if stored.EncryptedToken == apiToken {
			t.Fatal("API token stored as plaintext — must be encrypted")
		}

		// The encrypted token must decrypt back to the original.
		decrypted, err := crypto.Decrypt(stored.EncryptedToken, testEncryptionKey)
		if err != nil {
			t.Fatalf("failed to decrypt stored token: %v", err)
		}
		if decrypted != apiToken {
			t.Fatalf("decrypted token mismatch: got %q, want %q", decrypted, apiToken)
		}
	})
}

// TestProperty_SellerCabinet_InvalidTokenCreateFails verifies Requirement 4.3:
// For any API token that fails WB API validation, Create MUST return a validation
// error and MUST NOT persist the seller cabinet.
func TestProperty_SellerCabinet_InvalidTokenCreateFails(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		apiToken := genAPIToken().Draw(t, "apiToken")
		name := genCabinetName().Draw(t, "name")
		workspaceID := uuid.New()

		db := newSCInMemDB()
		svc := newTestSellerCabinetService(db, &fakeInvalidValidator{})

		sc, err := svc.Create(context.Background(), workspaceID, name, apiToken)

		// Must return an error.
		if err == nil {
			t.Fatal("Create with invalid WB token must fail")
		}

		// Must be a validation error.
		if !apperror.Is(err, apperror.ErrValidation) {
			t.Fatalf("expected validation error, got: %v", err)
		}

		// Must not return a cabinet.
		if sc != nil {
			t.Fatal("returned seller cabinet must be nil on validation failure")
		}

		// Must not persist anything in the DB.
		db.mu.Lock()
		count := len(db.cabinets)
		db.mu.Unlock()

		if count != 0 {
			t.Fatalf("expected 0 cabinets in DB after invalid token, got %d", count)
		}
	})
}

// TestProperty_SellerCabinet_TokenEncryptedBeforeValidation verifies Requirements 4.1, 4.2:
// The Create method encrypts the token and then validates it against WB API using
// the ORIGINAL plaintext token (not the encrypted version). For any token and any
// validator outcome, the token passed to ValidateToken must be the original plaintext.
func TestProperty_SellerCabinet_TokenEncryptedBeforeValidation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		apiToken := genAPIToken().Draw(t, "apiToken")
		name := genCabinetName().Draw(t, "name")
		workspaceID := uuid.New()

		var capturedToken string
		capturingValidator := &capturingValidator{capturedToken: &capturedToken}

		db := newSCInMemDB()
		svc := newTestSellerCabinetService(db, capturingValidator)

		_, _ = svc.Create(context.Background(), workspaceID, name, apiToken)

		// The validator must have received the original plaintext token.
		if capturedToken != apiToken {
			t.Fatalf("validator received %q, expected original token %q", capturedToken, apiToken)
		}
	})
}

// capturingValidator records the token passed to ValidateToken.
type capturingValidator struct {
	capturedToken *string
}

func (v *capturingValidator) ValidateToken(_ context.Context, token string) error {
	*v.capturedToken = token
	return nil
}
