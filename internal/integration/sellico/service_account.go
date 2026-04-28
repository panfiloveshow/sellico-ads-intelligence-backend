package sellico

// Service-account API surface — endpoints from PlaceSales / Sellico documented in
// the financial-dashboard reference project (rules.md / backandrules.md).
//
// Key contract:
//
//   - The backend itself holds a Sellico account flagged with `is_service_account = true`.
//     Its bearer token (or email+password to obtain one via /login) is configured via
//     env: either SELLICO_API_TOKEN (preferred, static) or SELLICO_EMAIL + SELLICO_PASSWORD.
//
//   - With that token the backend can list and read full credentials of every
//     integration in any workspace, and verify any user's permissions in any workspace.
//
//   - User-token endpoints (GetUser, ListWorkspaces, ListWorkspaceIntegrations) remain
//     in client.go for backwards-compatibility with code that still threads user tokens.
//     New code should prefer the service-account methods below.
//
// All requests follow the same base envelope decode path (`unwrapPayload` / `unwrapList`)
// as the user-token methods, so Laravel's `data` wrappers are handled transparently.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// IntegrationFull mirrors the response of GET /api/get-integration/{id} —
// it carries every credential field the upstream may include for any
// marketplace type, so callers can pick what's relevant to them.
type IntegrationFull struct {
	ID                      string
	WorkspaceID             string
	Name                    string
	Type                    string
	Description             string
	APIKey                  string
	ClientID                string
	PerformanceAPIKey       string
	PerformanceClientSecret string
	Status                  string
	AccountStatus           string
	StatusDescription       string
	IsPremium               bool
	CreatedAt               string
	UpdatedAt               string
}

// LoginResponse is the shape returned by POST /api/login. The user object is
// kept as a generic map so callers can introspect arbitrary fields (e.g.
// `is_service_account`) without a brittle struct contract.
type LoginResponse struct {
	AccessToken  string
	TokenType    string
	User         map[string]any
	Integrations []Integration
}

// Login obtains a personal access token via POST /api/login. Used at startup
// (or first request) by the ServiceTokenManager when only email+password are
// configured.
//
// SECURITY: never log the password or the request body; on error we return
// a redacted error so a stack trace doesn't accidentally leak credentials.
func (c *Client) Login(ctx context.Context, email, password string) (*LoginResponse, error) {
	body, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		return nil, fmt.Errorf("sellico login: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/login", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("sellico login: new request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Don't leak err.Error() — net/http errors can carry the URL but not body;
		// still, be conservative.
		return nil, fmt.Errorf("sellico login: transport error")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("sellico login: status %d", resp.StatusCode)
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("sellico login: decode: %w", err)
	}

	token := stringify(raw["access_token"])
	if token == "" {
		return nil, fmt.Errorf("sellico login: response missing access_token")
	}

	out := &LoginResponse{
		AccessToken: token,
		TokenType:   stringify(raw["token_type"]),
		User:        unwrapObject(raw["user"]),
	}
	if list, ok := raw["integrations"].([]any); ok {
		out.Integrations = make([]Integration, 0, len(list))
		for _, item := range list {
			if integration := parseIntegration(item); integration.ID != "" {
				out.Integrations = append(out.Integrations, integration)
			}
		}
	}
	return out, nil
}

// CurrentUser fetches /api/user using a user-supplied bearer. Used by the
// permission middleware to verify that the X-User-Id header matches the
// owner of the supplied X-Token. Returns the raw user map so callers can
// read arbitrary fields (id, is_service_account, ...).
func (c *Client) CurrentUser(ctx context.Context, token string) (map[string]any, error) {
	payload, err := c.get(ctx, "/user", token)
	if err != nil {
		return nil, err
	}
	user := unwrapObject(payload)
	if nested, ok := user["user"]; ok {
		user = unwrapObject(nested)
	}
	if stringifyID(user["id"]) == "" {
		return nil, fmt.Errorf("sellico /user: response missing id")
	}
	return user, nil
}

// CollectorIntegrations calls GET /api/collector/integrations with the
// service-account bearer. This is the canonical endpoint for service-account
// "collector" workloads (per backandrules.md, "Integration Status Fields"
// section, dated 2026-04-02): it returns every integration across the
// platform in a single response, no per-workspace round-trips needed.
//
// Workhorse of the auto-discovery worker job: the worker calls this once,
// groups results by WorkspaceID, and upserts into the local seller_cabinets
// table — much cheaper than the older /get-integrations/{ws} call which
// required one HTTP round-trip per known workspace.
func (c *Client) CollectorIntegrations(ctx context.Context, serviceToken string) ([]IntegrationFull, error) {
	payload, err := c.get(ctx, "/collector/integrations", serviceToken)
	if err != nil {
		return nil, err
	}
	items := unwrapList(payload)
	out := make([]IntegrationFull, 0, len(items))
	for _, item := range items {
		integration := parseIntegrationFull(item)
		if integration.ID == "" {
			continue
		}
		out = append(out, integration)
	}
	return out, nil
}

// GetIntegrations calls GET /api/get-integrations/{workspace?} with the
// service-account bearer. DEPRECATED in favour of CollectorIntegrations,
// which is the modern equivalent that doesn't require a workspace param.
// Kept for compatibility with code that still wants the per-workspace
// fetch shape.
//
// Pass an empty workspaceID to fetch ALL integrations across the platform.
func (c *Client) GetIntegrations(ctx context.Context, serviceToken, workspaceID string) ([]IntegrationFull, error) {
	path := "/get-integrations"
	if workspaceID != "" {
		path = path + "/" + workspaceID
	}
	payload, err := c.get(ctx, path, serviceToken)
	if err != nil {
		return nil, err
	}
	items := unwrapList(payload)
	out := make([]IntegrationFull, 0, len(items))
	for _, item := range items {
		integration := parseIntegrationFull(item)
		if integration.ID == "" {
			continue
		}
		out = append(out, integration)
	}
	return out, nil
}

// GetIntegration calls GET /api/get-integration/{id} with the service-account
// bearer. The /get-integrations list endpoint may strip secrets like api_key,
// performance_api_key, etc. for premium gating; this single-resource endpoint
// returns everything.
func (c *Client) GetIntegration(ctx context.Context, serviceToken, integrationID string) (*IntegrationFull, error) {
	if strings.TrimSpace(integrationID) == "" {
		return nil, fmt.Errorf("sellico GetIntegration: empty integrationID")
	}
	payload, err := c.get(ctx, "/get-integration/"+integrationID, serviceToken)
	if err != nil {
		return nil, err
	}
	integration := parseIntegrationFull(payload)
	if integration.ID == "" {
		return nil, fmt.Errorf("sellico /get-integration/%s: response missing id", integrationID)
	}
	return &integration, nil
}

// CheckPermissionParams is the payload for POST /api/check-permission.
// All four fields are required by the upstream; the caller is responsible
// for passing the user's personal token (NOT the service-account token).
type CheckPermissionParams struct {
	UserToken   string // user's personal access token (NOT service-account)
	UserID      string // sellico user_id (must match the token's owner)
	WorkspaceID string // sellico work_space_id
	Permission  string // permission slug, e.g. "integrations.view"
}

// CheckPermission verifies that a user holds a given permission slug in a
// workspace, by proxying to Sellico's /check-permission endpoint with the
// service-account token in the Authorization header and the user payload
// in the request body.
//
// Returns true ONLY when Sellico responds {"valid": true}; any error
// (including transport failures, 4xx, 5xx, malformed JSON) returns false —
// fail-closed semantics, matching the reference PermissionService behaviour.
func (c *Client) CheckPermission(ctx context.Context, serviceToken string, p CheckPermissionParams) (bool, error) {
	body, err := json.Marshal(map[string]string{
		"token":      p.UserToken,
		"user":       p.UserID,
		"workspace":  p.WorkspaceID,
		"permission": p.Permission,
	})
	if err != nil {
		return false, fmt.Errorf("sellico check-permission: marshal: %w", err)
	}

	// Per docs the verb is GET with a body, but Go's http package and proxies
	// dislike GET+body. The Laravel reference also uses GET, so we mirror it
	// exactly. Many HTTP middlewares strip GET bodies; if Sellico does, the
	// upstream is in control of switching to POST and we'll mirror.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/check-permission", bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("sellico check-permission: new request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+serviceToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("sellico check-permission: transport: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return false, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("sellico check-permission: status %d", resp.StatusCode)
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return false, fmt.Errorf("sellico check-permission: decode: %w", err)
	}
	valid, _ := raw["valid"].(bool)
	return valid, nil
}

// ActivityPayload is the body of POST /api/workspaces/{ws}/activities.
// Required: Action. The other fields are optional but Title and Description
// help the audit log read like prose; Meta is a free-form bag for entity
// references.
type ActivityPayload struct {
	Action      string         // required, e.g. "integrations.view"
	Title       string         // optional
	Description string         // optional
	Meta        map[string]any // optional
}

// CreateActivity records an audit-log entry in Sellico, attributed to the
// user identified by `userToken` (NOT the service-account token — Sellico
// uses the bearer to determine `user_id` for the activity row).
//
// The reference middleware fires this from `terminate()` (after-response),
// so failures here should never block the user request — the caller is
// expected to log+swallow errors.
func (c *Client) CreateActivity(ctx context.Context, userToken, workspaceID string, p ActivityPayload) error {
	if strings.TrimSpace(workspaceID) == "" {
		return fmt.Errorf("sellico CreateActivity: empty workspaceID")
	}
	if strings.TrimSpace(p.Action) == "" {
		return fmt.Errorf("sellico CreateActivity: empty action")
	}

	body, err := json.Marshal(map[string]any{
		"action":      p.Action,
		"title":       p.Title,
		"description": p.Description,
		"meta":        p.Meta,
	})
	if err != nil {
		return fmt.Errorf("sellico CreateActivity: marshal: %w", err)
	}

	url := fmt.Sprintf("%s/workspaces/%s/activities", c.baseURL, workspaceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sellico CreateActivity: new request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sellico CreateActivity: transport: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sellico CreateActivity: status %d", resp.StatusCode)
	}
	return nil
}

// parseIntegrationFull is the richer counterpart of parseIntegration — it
// extracts every credential field the upstream may include, matching the
// /get-integration/{id} response shape from backandrules.md.
func parseIntegrationFull(value any) IntegrationFull {
	raw := unwrapObject(value)
	if nested, ok := raw["integration"]; ok {
		raw = unwrapObject(nested)
	}
	credentials := unwrapObject(raw["credentials"])

	apiKey := firstNonEmpty(
		stringify(raw["api_key"]),
		stringify(raw["token"]),
		stringify(credentials["api_key"]),
		stringify(credentials["token"]),
	)
	clientID := firstNonEmpty(
		stringify(raw["client_id"]),
		stringify(raw["clientId"]),
		stringify(credentials["client_id"]),
	)
	perfKey := firstNonEmpty(
		stringify(raw["performance_api_key"]),
		stringify(credentials["performance_api_key"]),
	)
	perfSecret := firstNonEmpty(
		stringify(raw["performance_client_secret"]),
		stringify(credentials["performance_client_secret"]),
	)

	isPremium := false
	if v, ok := raw["is_premium"].(bool); ok {
		isPremium = v
	} else if s := stringify(raw["is_premium"]); s != "" {
		if parsed, err := strconv.ParseBool(s); err == nil {
			isPremium = parsed
		}
	}

	return IntegrationFull{
		ID:                      stringifyID(raw["id"]),
		WorkspaceID:             stringifyID(raw["work_space_id"]),
		Name:                    firstNonEmpty(stringify(raw["name"]), stringify(raw["title"])),
		Type:                    normalizeIntegrationType(stringify(raw["type"])),
		Description:             stringify(raw["description"]),
		APIKey:                  apiKey,
		ClientID:                clientID,
		PerformanceAPIKey:       perfKey,
		PerformanceClientSecret: perfSecret,
		Status:                  stringify(raw["status"]),
		AccountStatus:           stringify(raw["account_status"]),
		StatusDescription:       stringify(raw["status_description"]),
		IsPremium:               isPremium,
		CreatedAt:               stringify(raw["created_at"]),
		UpdatedAt:               stringify(raw["updated_at"]),
	}
}
