package sellico

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var ErrUnauthorized = errors.New("sellico api unauthorized")

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type User struct {
	ID    string
	Email string
	Name  string
}

type Workspace struct {
	ID         string
	AccountID  string
	Name       string
	UserID     string
	OwnerID    string
	ExternalID string
}

type Integration struct {
	ID       string
	Name     string
	Type     string
	APIKey   string
	ClientID string
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	trimmed := strings.TrimRight(baseURL, "/")

	return &Client{
		baseURL: trimmed,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) GetUser(ctx context.Context, token string) (*User, error) {
	payload, err := c.get(ctx, "/user", token)
	if err != nil {
		return nil, err
	}

	userPayload := unwrapObject(payload)
	if nested, ok := userPayload["user"]; ok {
		userPayload = unwrapObject(nested)
	}

	user := &User{
		ID:    stringifyID(userPayload["id"]),
		Email: stringify(userPayload["email"]),
		Name:  firstNonEmpty(stringify(userPayload["name"]), stringify(userPayload["full_name"])),
	}
	if user.ID == "" {
		return nil, fmt.Errorf("sellico /user response does not contain id")
	}

	return user, nil
}

func (c *Client) ListWorkspaces(ctx context.Context, token string) ([]Workspace, error) {
	payload, err := c.get(ctx, "/workspaces", token)
	if err != nil {
		return nil, err
	}

	items := unwrapList(payload)
	workspaces := make([]Workspace, 0, len(items))
	for _, item := range items {
		raw := unwrapObject(item)
		if len(raw) == 0 {
			continue
		}

		owner := unwrapObject(raw["owner"])
		workspace := Workspace{
			ID:         stringifyID(raw["id"]),
			AccountID:  stringifyID(raw["work_space_id"]),
			Name:       firstNonEmpty(stringify(raw["name"]), stringify(raw["title"]), "Sellico Workspace"),
			UserID:     stringifyID(raw["user_id"]),
			OwnerID:    stringifyID(owner["id"]),
			ExternalID: stringifyID(raw["id"]),
		}
		if workspace.ExternalID == "" {
			workspace.ExternalID = workspace.AccountID
		}
		workspaces = append(workspaces, workspace)
	}

	return workspaces, nil
}

func (c *Client) ListWorkspaceIntegrations(ctx context.Context, token, workspaceID string) ([]Integration, error) {
	payload, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/integrations", workspaceID), token)
	if err != nil {
		return nil, err
	}

	items := unwrapList(payload)
	integrations := make([]Integration, 0, len(items))
	for _, item := range items {
		integration := parseIntegration(item)
		if integration.ID == "" {
			continue
		}
		integrations = append(integrations, integration)
	}

	return integrations, nil
}

func (c *Client) GetWorkspaceIntegration(ctx context.Context, token, workspaceID, integrationID string) (*Integration, error) {
	payload, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/integrations/%s", workspaceID, integrationID), token)
	if err != nil {
		return nil, err
	}

	integration := parseIntegration(payload)
	if integration.ID == "" {
		return nil, fmt.Errorf("sellico integration %s not found", integrationID)
	}

	return &integration, nil
}

func (c *Client) get(ctx context.Context, path, token string) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("sellico api %s returned status %d", path, resp.StatusCode)
	}

	var payload any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	return unwrapPayload(payload), nil
}

func unwrapPayload(payload any) any {
	root := unwrapObject(payload)
	if data, ok := root["data"]; ok {
		return data
	}
	return payload
}

func unwrapObject(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if object, ok := value.(map[string]any); ok {
		return object
	}
	return map[string]any{}
}

func unwrapList(value any) []any {
	if value == nil {
		return nil
	}
	if list, ok := value.([]any); ok {
		return list
	}
	object := unwrapObject(value)
	if nested, ok := object["data"].([]any); ok {
		return nested
	}
	return nil
}

func stringify(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return fmt.Sprintf("%.0f", typed)
	default:
		return ""
	}
}

func stringifyID(value any) string {
	return strings.TrimSpace(stringify(value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func parseIntegration(value any) Integration {
	raw := unwrapObject(value)
	if nested, ok := raw["integration"]; ok {
		raw = unwrapObject(nested)
	}

	credentials := unwrapObject(raw["credentials"])

	apiKey := firstNonEmpty(
		stringify(raw["api_key"]),
		stringify(raw["token"]),
		stringify(raw["apiKey"]),
		stringify(credentials["api_key"]),
		stringify(credentials["token"]),
	)

	clientID := firstNonEmpty(
		stringify(raw["client_id"]),
		stringify(raw["clientId"]),
		stringify(credentials["client_id"]),
	)

	integrationType := normalizeIntegrationType(firstNonEmpty(
		stringify(raw["type"]),
		stringify(raw["marketplace"]),
		stringify(raw["service"]),
	))

	return Integration{
		ID:       stringifyID(raw["id"]),
		Name:     firstNonEmpty(stringify(raw["name"]), stringify(raw["title"]), "Sellico Integration"),
		Type:     integrationType,
		APIKey:   apiKey,
		ClientID: clientID,
	}
}

func normalizeIntegrationType(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "wildberries", "wildberries.ru", "wb":
		return "WildBerries"
	case "ozon":
		return "OZON"
	case "yandexmarket", "yandex_market", "yandex":
		return "YandexMarket"
	default:
		return value
	}
}
