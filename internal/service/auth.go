package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/jwt"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// AuthTokens holds the token pair returned after authentication.
type AuthTokens struct {
	AccessToken  string
	RefreshToken string
}

// AuthService handles registration, login, token refresh and logout.
type AuthService struct {
	queries         *sqlcgen.Queries
	jwtSecret       string
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
}

// NewAuthService creates a new AuthService.
func NewAuthService(
	queries *sqlcgen.Queries,
	jwtSecret string,
	accessTokenTTL time.Duration,
	refreshTokenTTL time.Duration,
) *AuthService {
	return &AuthService{
		queries:         queries,
		jwtSecret:       jwtSecret,
		accessTokenTTL:  accessTokenTTL,
		refreshTokenTTL: refreshTokenTTL,
	}
}

// Register creates a new user and returns a JWT token pair.
func (s *AuthService) Register(ctx context.Context, email, password, name string) (*AuthTokens, error) {
	// Check if user already exists.
	_, err := s.queries.GetUserByEmail(ctx, email)
	if err == nil {
		return nil, apperror.New(apperror.ErrConflict, "email already registered")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrInternal, "failed to check existing user")
	}

	// Hash password with argon2id.
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to hash password")
	}

	// Create user.
	user, err := s.queries.CreateUser(ctx, sqlcgen.CreateUserParams{
		Email:        email,
		PasswordHash: hash,
		Name:         name,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to create user")
	}

	userID := uuidFromPgtype(user.ID)
	return s.generateTokenPair(ctx, userID)
}

// Login verifies credentials and returns a JWT token pair.
func (s *AuthService) Login(ctx context.Context, email, password string) (*AuthTokens, error) {
	user, err := s.queries.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrUnauthorized, "invalid email or password")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to find user")
	}

	match, err := crypto.VerifyPassword(password, user.PasswordHash)
	if err != nil || !match {
		return nil, apperror.New(apperror.ErrUnauthorized, "invalid email or password")
	}

	userID := uuidFromPgtype(user.ID)
	return s.generateTokenPair(ctx, userID)
}

// RefreshToken validates the old refresh token, revokes it, and issues a new pair.
func (s *AuthService) RefreshToken(ctx context.Context, refreshTokenStr string) (*AuthTokens, error) {
	// Validate the JWT structure and signature.
	claims, err := jwt.ValidateToken(refreshTokenStr, s.jwtSecret)
	if err != nil {
		return nil, apperror.New(apperror.ErrUnauthorized, "invalid or expired refresh token")
	}
	if claims.TokenType != "refresh" {
		return nil, apperror.New(apperror.ErrUnauthorized, "invalid token type")
	}

	// Look up the token hash in DB.
	tokenHash := jwt.HashToken(refreshTokenStr)
	storedToken, err := s.queries.GetRefreshTokenByHash(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrUnauthorized, "refresh token not found or revoked")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to look up refresh token")
	}

	// Check expiration at DB level.
	if storedToken.ExpiresAt.Time.Before(time.Now()) {
		return nil, apperror.New(apperror.ErrUnauthorized, "refresh token expired")
	}

	// Revoke the old token.
	if err := s.queries.RevokeRefreshToken(ctx, storedToken.ID); err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to revoke old refresh token")
	}

	userID := uuidFromPgtype(storedToken.UserID)
	return s.generateTokenPair(ctx, userID)
}

// Logout revokes the given refresh token.
func (s *AuthService) Logout(ctx context.Context, refreshTokenStr string) error {
	tokenHash := jwt.HashToken(refreshTokenStr)
	storedToken, err := s.queries.GetRefreshTokenByHash(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		// Already revoked or doesn't exist — treat as success.
		return nil
	}
	if err != nil {
		return apperror.New(apperror.ErrInternal, "failed to look up refresh token")
	}

	if err := s.queries.RevokeRefreshToken(ctx, storedToken.ID); err != nil {
		return apperror.New(apperror.ErrInternal, "failed to revoke refresh token")
	}
	return nil
}

// GetMe returns the currently authenticated user profile.
func (s *AuthService) GetMe(ctx context.Context, userID uuid.UUID) (*domain.User, error) {
	user, err := s.queries.GetUserByID(ctx, uuidToPgtype(userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "user not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get user")
	}

	result := domain.User{
		ID:           uuidFromPgtype(user.ID),
		Email:        user.Email,
		PasswordHash: user.PasswordHash,
		Name:         user.Name,
		CreatedAt:    user.CreatedAt.Time,
		UpdatedAt:    user.UpdatedAt.Time,
	}
	return &result, nil
}

// generateTokenPair creates a new access + refresh token pair and stores the refresh hash.
func (s *AuthService) generateTokenPair(ctx context.Context, userID uuid.UUID) (*AuthTokens, error) {
	accessToken, err := jwt.GenerateAccessToken(userID, s.jwtSecret, s.accessTokenTTL)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to generate access token")
	}

	refreshToken, _, err := jwt.GenerateRefreshToken(userID, s.jwtSecret, s.refreshTokenTTL)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to generate refresh token")
	}

	// Store refresh token hash in DB.
	tokenHash := jwt.HashToken(refreshToken)
	_, err = s.queries.CreateRefreshToken(ctx, sqlcgen.CreateRefreshTokenParams{
		UserID:    uuidToPgtype(userID),
		TokenHash: tokenHash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(s.refreshTokenTTL), Valid: true},
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to store refresh token")
	}

	return &AuthTokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

// uuidToPgtype converts a google/uuid.UUID to pgtype.UUID.
func uuidToPgtype(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// uuidFromPgtype converts a pgtype.UUID to google/uuid.UUID.
func uuidFromPgtype(id pgtype.UUID) uuid.UUID {
	if !id.Valid {
		return uuid.Nil
	}
	return uuid.UUID(id.Bytes)
}
