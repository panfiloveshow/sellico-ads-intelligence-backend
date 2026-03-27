package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/jwt"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"pgregory.net/rapid"
)

// Feature: sellico-ads-intelligence-backend, Property 1: Аутентификация — round-trip регистрации и логина
// Проверяет: Требования 1.1, 1.2, 19.4

// --- In-memory DBTX mock ---

// fakeRow implements pgx.Row for returning a single user or refresh token row.
type fakeRow struct {
	scanFunc func(dest ...any) error
}

func (r *fakeRow) Scan(dest ...any) error { return r.scanFunc(dest...) }

// fakeRows implements pgx.Rows (used by ListUsers etc., not needed for auth but required by interface).
type fakeRows struct{}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Next() bool                                   { return false }
func (r *fakeRows) Scan(_ ...any) error                          { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

// inMemDB is a fake DBTX that stores users and refresh tokens in memory.
// It intercepts SQL queries by pattern-matching the query string prefix.
type inMemDB struct {
	mu            sync.Mutex
	users         map[string]sqlcgen.User         // keyed by email
	refreshTokens map[string]sqlcgen.RefreshToken // keyed by token_hash
}

func newInMemDB() *inMemDB {
	return &inMemDB{
		users:         make(map[string]sqlcgen.User),
		refreshTokens: make(map[string]sqlcgen.RefreshToken),
	}
}

func (db *inMemDB) Exec(_ context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	switch {
	case containsSQL(sql, "UPDATE refresh_tokens SET revoked"):
		// RevokeRefreshToken: args[0] = id (pgtype.UUID)
		id := args[0].(pgtype.UUID)
		for hash, rt := range db.refreshTokens {
			if rt.ID == id {
				rt.Revoked = true
				db.refreshTokens[hash] = rt
				break
			}
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case containsSQL(sql, "DELETE FROM refresh_tokens"):
		return pgconn.NewCommandTag("DELETE 0"), nil
	}
	return pgconn.NewCommandTag(""), nil
}

func (db *inMemDB) Query(_ context.Context, _ string, _ ...interface{}) (pgx.Rows, error) {
	return &fakeRows{}, nil
}

func (db *inMemDB) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

func (db *inMemDB) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	db.mu.Lock()
	defer db.mu.Unlock()

	switch {
	case containsSQL(sql, "FROM users WHERE email"):
		// GetUserByEmail
		email := args[0].(string)
		u, ok := db.users[email]
		if !ok {
			return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
		}
		return userToRow(u)

	case containsSQL(sql, "INSERT INTO users"):
		// CreateUser: args = email, password_hash, name
		email := args[0].(string)
		passwordHash := args[1].(string)
		name := args[2].(string)
		id := uuid.New()
		now := time.Now()
		u := sqlcgen.User{
			ID:           pgtype.UUID{Bytes: id, Valid: true},
			Email:        email,
			PasswordHash: passwordHash,
			Name:         name,
			CreatedAt:    pgtype.Timestamptz{Time: now, Valid: true},
			UpdatedAt:    pgtype.Timestamptz{Time: now, Valid: true},
		}
		db.users[email] = u
		return userToRow(u)

	case containsSQL(sql, "INSERT INTO refresh_tokens"):
		// CreateRefreshToken: args = user_id, token_hash, expires_at
		userID := args[0].(pgtype.UUID)
		tokenHash := args[1].(string)
		expiresAt := args[2].(pgtype.Timestamptz)
		id := uuid.New()
		now := time.Now()
		rt := sqlcgen.RefreshToken{
			ID:        pgtype.UUID{Bytes: id, Valid: true},
			UserID:    userID,
			TokenHash: tokenHash,
			ExpiresAt: expiresAt,
			Revoked:   false,
			CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		}
		db.refreshTokens[tokenHash] = rt
		return refreshTokenToRow(rt)

	case containsSQL(sql, "FROM refresh_tokens") && containsSQL(sql, "token_hash"):
		// GetRefreshTokenByHash
		tokenHash := args[0].(string)
		rt, ok := db.refreshTokens[tokenHash]
		if !ok || rt.Revoked {
			return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
		}
		return refreshTokenToRow(rt)
	}

	return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
}

func containsSQL(sql, substr string) bool {
	return len(sql) >= len(substr) && indexOf(sql, substr) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func userToRow(u sqlcgen.User) pgx.Row {
	return &fakeRow{scanFunc: func(dest ...any) error {
		*dest[0].(*pgtype.UUID) = u.ID
		*dest[1].(*string) = u.Email
		*dest[2].(*string) = u.PasswordHash
		*dest[3].(*string) = u.Name
		*dest[4].(*pgtype.Timestamptz) = u.CreatedAt
		*dest[5].(*pgtype.Timestamptz) = u.UpdatedAt
		return nil
	}}
}

func refreshTokenToRow(rt sqlcgen.RefreshToken) pgx.Row {
	return &fakeRow{scanFunc: func(dest ...any) error {
		*dest[0].(*pgtype.UUID) = rt.ID
		*dest[1].(*pgtype.UUID) = rt.UserID
		*dest[2].(*string) = rt.TokenHash
		*dest[3].(*pgtype.Timestamptz) = rt.ExpiresAt
		*dest[4].(*bool) = rt.Revoked
		*dest[5].(*pgtype.Timestamptz) = rt.CreatedAt
		return nil
	}}
}

// --- Generators ---

// genEmail generates a valid email address.
func genEmail() *rapid.Generator[string] {
	return rapid.Custom[string](func(t *rapid.T) string {
		local := rapid.StringMatching(`[a-z][a-z0-9]{2,15}`).Draw(t, "local")
		domain := rapid.StringMatching(`[a-z]{3,10}`).Draw(t, "domain")
		tld := rapid.SampledFrom([]string{"com", "org", "net", "io", "ru"}).Draw(t, "tld")
		return local + "@" + domain + "." + tld
	})
}

// genPassword generates a password of 8-32 printable ASCII characters.
func genPassword() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9!@#$%^&*]{8,32}`)
}

// genName generates a user display name.
func genName() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z]{2,20}`)
}

func newTestAuthService(db *inMemDB) *AuthService {
	queries := sqlcgen.New(db)
	return NewAuthService(queries, "test-jwt-secret-key-32bytes!!", 15*time.Minute, 7*24*time.Hour)
}

// --- Property Tests ---

// TestProperty_Auth_RegisterReturnsValidTokens verifies Requirement 1.1:
// For any valid email, password, and name, Register MUST succeed and return
// a non-empty access token and refresh token. The access token MUST be a valid
// JWT containing the correct user_id.
func TestProperty_Auth_RegisterReturnsValidTokens(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		email := genEmail().Draw(t, "email")
		password := genPassword().Draw(t, "password")
		name := genName().Draw(t, "name")

		db := newInMemDB()
		svc := newTestAuthService(db)

		tokens, err := svc.Register(context.Background(), email, password, name)
		if err != nil {
			t.Fatalf("Register(%q) failed: %v", email, err)
		}

		if tokens.AccessToken == "" {
			t.Fatal("access token must not be empty")
		}
		if tokens.RefreshToken == "" {
			t.Fatal("refresh token must not be empty")
		}

		// Access token must be a valid JWT with correct user_id.
		claims, err := jwt.ValidateToken(tokens.AccessToken, "test-jwt-secret-key-32bytes!!")
		if err != nil {
			t.Fatalf("access token validation failed: %v", err)
		}
		if claims.TokenType != "access" {
			t.Fatalf("expected token type 'access', got %q", claims.TokenType)
		}
		if claims.UserID == uuid.Nil {
			t.Fatal("access token must contain a non-nil user_id")
		}
	})
}

// TestProperty_Auth_RegisterLoginRoundTrip verifies Requirements 1.1, 1.2:
// For any valid credentials, after Register the same credentials MUST succeed
// with Login, and Login MUST return valid tokens for the same user.
func TestProperty_Auth_RegisterLoginRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		email := genEmail().Draw(t, "email")
		password := genPassword().Draw(t, "password")
		name := genName().Draw(t, "name")

		db := newInMemDB()
		svc := newTestAuthService(db)
		ctx := context.Background()

		// Register.
		regTokens, err := svc.Register(ctx, email, password, name)
		if err != nil {
			t.Fatalf("Register failed: %v", err)
		}

		regClaims, err := jwt.ValidateToken(regTokens.AccessToken, "test-jwt-secret-key-32bytes!!")
		if err != nil {
			t.Fatalf("register access token invalid: %v", err)
		}

		// Login with same credentials.
		loginTokens, err := svc.Login(ctx, email, password)
		if err != nil {
			t.Fatalf("Login(%q) failed after Register: %v", email, err)
		}

		loginClaims, err := jwt.ValidateToken(loginTokens.AccessToken, "test-jwt-secret-key-32bytes!!")
		if err != nil {
			t.Fatalf("login access token invalid: %v", err)
		}

		// Both tokens must reference the same user.
		if regClaims.UserID != loginClaims.UserID {
			t.Fatalf("user_id mismatch: register=%s, login=%s", regClaims.UserID, loginClaims.UserID)
		}
	})
}

// TestProperty_Auth_LoginFailsWithWrongPassword verifies Requirement 1.2:
// For any valid registration, Login with a DIFFERENT password MUST fail.
func TestProperty_Auth_LoginFailsWithWrongPassword(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		email := genEmail().Draw(t, "email")
		password := genPassword().Draw(t, "password")
		wrongPassword := genPassword().Draw(t, "wrongPassword")
		name := genName().Draw(t, "name")

		// Ensure passwords differ.
		if password == wrongPassword {
			return // skip this case (astronomically unlikely but handle gracefully)
		}

		db := newInMemDB()
		svc := newTestAuthService(db)
		ctx := context.Background()

		_, err := svc.Register(ctx, email, password, name)
		if err != nil {
			t.Fatalf("Register failed: %v", err)
		}

		_, err = svc.Login(ctx, email, wrongPassword)
		if err == nil {
			t.Fatal("Login must fail with wrong password")
		}
	})
}

// TestProperty_Auth_PasswordStoredAsArgon2idHash verifies Requirement 19.4:
// After Register, the password stored in the database MUST be an argon2id hash,
// NOT the plaintext password. The hash MUST verify against the original password.
func TestProperty_Auth_PasswordStoredAsArgon2idHash(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		email := genEmail().Draw(t, "email")
		password := genPassword().Draw(t, "password")
		name := genName().Draw(t, "name")

		db := newInMemDB()
		svc := newTestAuthService(db)

		_, err := svc.Register(context.Background(), email, password, name)
		if err != nil {
			t.Fatalf("Register failed: %v", err)
		}

		// Inspect the stored user directly.
		db.mu.Lock()
		storedUser, ok := db.users[email]
		db.mu.Unlock()

		if !ok {
			t.Fatal("user not found in store after Register")
		}

		// Password must NOT be stored as plaintext.
		if storedUser.PasswordHash == password {
			t.Fatal("password stored as plaintext — must be hashed")
		}

		// Must be a valid argon2id hash.
		if !isArgon2idHash(storedUser.PasswordHash) {
			t.Fatalf("stored hash is not argon2id format: %q", storedUser.PasswordHash[:min(40, len(storedUser.PasswordHash))])
		}

		// Hash must verify against original password.
		match, err := crypto.VerifyPassword(password, storedUser.PasswordHash)
		if err != nil {
			t.Fatalf("VerifyPassword failed: %v", err)
		}
		if !match {
			t.Fatal("stored argon2id hash does not verify against original password")
		}
	})
}

// TestProperty_Auth_DuplicateEmailRejected verifies Requirement 1.1:
// Registering with the same email twice MUST fail on the second attempt.
func TestProperty_Auth_DuplicateEmailRejected(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		email := genEmail().Draw(t, "email")
		password1 := genPassword().Draw(t, "password1")
		password2 := genPassword().Draw(t, "password2")
		name := genName().Draw(t, "name")

		db := newInMemDB()
		svc := newTestAuthService(db)
		ctx := context.Background()

		_, err := svc.Register(ctx, email, password1, name)
		if err != nil {
			t.Fatalf("first Register failed: %v", err)
		}

		_, err = svc.Register(ctx, email, password2, name)
		if err == nil {
			t.Fatal("second Register with same email must fail")
		}
	})
}

// isArgon2idHash checks if a string looks like a valid argon2id encoded hash.
func isArgon2idHash(s string) bool {
	return len(s) > 20 && s[:10] == "$argon2id$"
}

// Feature: sellico-ads-intelligence-backend, Property 2: Токены — refresh round-trip и инвалидация
// Проверяет: Требования 1.3, 1.4, 1.5, 1.6

// TestProperty_Auth_RefreshRoundTrip verifies Requirement 1.3:
// After Register, calling RefreshToken with the returned refresh token MUST succeed
// and return a new valid access token and a new refresh token. The new access token
// MUST contain the same user_id. The new refresh token MUST be different from the old one.
//
// **Validates: Requirements 1.3**
func TestProperty_Auth_RefreshRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		email := genEmail().Draw(t, "email")
		password := genPassword().Draw(t, "password")
		name := genName().Draw(t, "name")

		db := newInMemDB()
		svc := newTestAuthService(db)
		ctx := context.Background()

		// Register to get initial tokens.
		regTokens, err := svc.Register(ctx, email, password, name)
		if err != nil {
			t.Fatalf("Register failed: %v", err)
		}

		regClaims, err := jwt.ValidateToken(regTokens.AccessToken, "test-jwt-secret-key-32bytes!!")
		if err != nil {
			t.Fatalf("register access token invalid: %v", err)
		}

		// Refresh using the returned refresh token.
		newTokens, err := svc.RefreshToken(ctx, regTokens.RefreshToken)
		if err != nil {
			t.Fatalf("RefreshToken failed: %v", err)
		}

		if newTokens.AccessToken == "" {
			t.Fatal("new access token must not be empty")
		}
		if newTokens.RefreshToken == "" {
			t.Fatal("new refresh token must not be empty")
		}

		// New refresh token must differ from old one.
		if newTokens.RefreshToken == regTokens.RefreshToken {
			t.Fatal("new refresh token must be different from old refresh token")
		}

		// New access token must contain the same user_id.
		newClaims, err := jwt.ValidateToken(newTokens.AccessToken, "test-jwt-secret-key-32bytes!!")
		if err != nil {
			t.Fatalf("new access token invalid: %v", err)
		}
		if newClaims.UserID != regClaims.UserID {
			t.Fatalf("user_id mismatch: register=%s, refresh=%s", regClaims.UserID, newClaims.UserID)
		}
	})
}

// TestProperty_Auth_RefreshInvalidatesOldToken verifies Requirements 1.3, 1.6:
// After a successful RefreshToken call, the OLD refresh token MUST be rejected
// on subsequent RefreshToken calls (it was revoked).
//
// **Validates: Requirements 1.3, 1.6**
func TestProperty_Auth_RefreshInvalidatesOldToken(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		email := genEmail().Draw(t, "email")
		password := genPassword().Draw(t, "password")
		name := genName().Draw(t, "name")

		db := newInMemDB()
		svc := newTestAuthService(db)
		ctx := context.Background()

		regTokens, err := svc.Register(ctx, email, password, name)
		if err != nil {
			t.Fatalf("Register failed: %v", err)
		}

		oldRefresh := regTokens.RefreshToken

		// Refresh once — this should revoke the old token.
		_, err = svc.RefreshToken(ctx, oldRefresh)
		if err != nil {
			t.Fatalf("first RefreshToken failed: %v", err)
		}

		// Attempt to use the old refresh token again — must fail.
		_, err = svc.RefreshToken(ctx, oldRefresh)
		if err == nil {
			t.Fatal("RefreshToken with revoked old token must fail")
		}
	})
}

// TestProperty_Auth_LogoutInvalidatesRefreshToken verifies Requirements 1.4, 1.6:
// After Logout with a refresh token, that same refresh token MUST be rejected
// by RefreshToken.
//
// **Validates: Requirements 1.4, 1.6**
func TestProperty_Auth_LogoutInvalidatesRefreshToken(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		email := genEmail().Draw(t, "email")
		password := genPassword().Draw(t, "password")
		name := genName().Draw(t, "name")

		db := newInMemDB()
		svc := newTestAuthService(db)
		ctx := context.Background()

		regTokens, err := svc.Register(ctx, email, password, name)
		if err != nil {
			t.Fatalf("Register failed: %v", err)
		}

		// Logout — should invalidate the refresh token.
		err = svc.Logout(ctx, regTokens.RefreshToken)
		if err != nil {
			t.Fatalf("Logout failed: %v", err)
		}

		// Attempt to refresh with the invalidated token — must fail.
		_, err = svc.RefreshToken(ctx, regTokens.RefreshToken)
		if err == nil {
			t.Fatal("RefreshToken after Logout must fail")
		}
	})
}

// TestProperty_Auth_LogoutIdempotent verifies Requirement 1.4:
// Calling Logout twice with the same token MUST NOT return an error on the second call.
//
// **Validates: Requirements 1.4**
func TestProperty_Auth_LogoutIdempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		email := genEmail().Draw(t, "email")
		password := genPassword().Draw(t, "password")
		name := genName().Draw(t, "name")

		db := newInMemDB()
		svc := newTestAuthService(db)
		ctx := context.Background()

		regTokens, err := svc.Register(ctx, email, password, name)
		if err != nil {
			t.Fatalf("Register failed: %v", err)
		}

		// First logout.
		err = svc.Logout(ctx, regTokens.RefreshToken)
		if err != nil {
			t.Fatalf("first Logout failed: %v", err)
		}

		// Second logout with same token — must not error.
		err = svc.Logout(ctx, regTokens.RefreshToken)
		if err != nil {
			t.Fatalf("second Logout must be idempotent, got error: %v", err)
		}
	})
}

// TestProperty_Auth_RefreshWithRandomStringFails verifies Requirements 1.5, 1.6:
// Calling RefreshToken with a random non-JWT string MUST fail.
//
// **Validates: Requirements 1.5, 1.6**
func TestProperty_Auth_RefreshWithRandomStringFails(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		randomStr := rapid.StringMatching(`[A-Za-z0-9]{10,64}`).Draw(t, "randomToken")

		db := newInMemDB()
		svc := newTestAuthService(db)
		ctx := context.Background()

		_, err := svc.RefreshToken(ctx, randomStr)
		if err == nil {
			t.Fatalf("RefreshToken with random string %q must fail", randomStr)
		}
	})
}
