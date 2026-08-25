package sellico

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// PermissionChecker answers "does this user hold this CRM permission in this
// workspace" by proxying Client.CheckPermission with the service-account
// token, caching verdicts for a short TTL (matching the Laravel services).
type PermissionChecker struct {
	client *Client
	tokens *ServiceTokenManager
	ttl    time.Duration

	mu    sync.Mutex
	cache map[string]permissionVerdict
}

type permissionVerdict struct {
	allowed bool
	expires time.Time
}

func NewPermissionChecker(client *Client, tokens *ServiceTokenManager, ttl time.Duration) *PermissionChecker {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &PermissionChecker{
		client: client,
		tokens: tokens,
		ttl:    ttl,
		cache:  map[string]permissionVerdict{},
	}
}

// HasPermission implements middleware.PermissionChecker.
// Verdicts (both allow and deny) are cached; transport errors are not.
func (p *PermissionChecker) HasPermission(ctx context.Context, userToken, userID, workspaceRef, permission string) (bool, error) {
	tokenHash := sha256.Sum256([]byte(userToken))
	key := workspaceRef + "|" + permission + "|" + hex.EncodeToString(tokenHash[:8])

	p.mu.Lock()
	if v, ok := p.cache[key]; ok && time.Now().Before(v.expires) {
		p.mu.Unlock()
		return v.allowed, nil
	}
	p.mu.Unlock()

	serviceToken, err := p.tokens.Get(ctx)
	if err != nil {
		return false, err
	}

	params := CheckPermissionParams{
		UserToken:   userToken,
		UserID:      userID,
		WorkspaceID: workspaceRef,
		Permission:  permission,
	}

	allowed, err := p.client.CheckPermission(ctx, serviceToken, params)
	if errors.Is(err, ErrUnauthorized) {
		// Протух сервисный токен — перелогин и одна повторная попытка.
		p.tokens.Invalidate()
		if serviceToken, err = p.tokens.Get(ctx); err != nil {
			return false, err
		}
		allowed, err = p.client.CheckPermission(ctx, serviceToken, params)
	}
	if err != nil {
		return false, err
	}

	p.mu.Lock()
	p.cache[key] = permissionVerdict{allowed: allowed, expires: time.Now().Add(p.ttl)}
	// ponytail: карта растёт без выселения; при >10k записей чистим целиком —
	// дешевле LRU, а кеш прогреется за TTL заново.
	if len(p.cache) > 10_000 {
		p.cache = map[string]permissionVerdict{}
	}
	p.mu.Unlock()

	return allowed, nil
}
