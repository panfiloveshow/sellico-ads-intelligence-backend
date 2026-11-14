package sellico

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

var ErrManagerScopeDenied = errors.New("manager workspace access denied")

var managerPositiveID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

// VerifyManagerAccess always checks the personal session and membership afresh.
// A service account, a local role, or a caller-supplied user ID cannot authorize an action.
func (c *Client) VerifyManagerAccess(ctx context.Context, token, workspaceID string) (*domain.ManagerAccess, error) {
	if c == nil || c.httpClient == nil || c.baseURL == "" {
		return nil, errors.New("manager identity source is not configured")
	}
	if !managerPositiveID.MatchString(workspaceID) || token == "" || len(token) > 8192 || strings.ContainsAny(token, " \t\r\n") {
		return nil, ErrManagerScopeDenied
	}
	profile, err := c.managerRead(ctx, "/user", token)
	if err != nil {
		return nil, err
	}
	userID := managerID(profile["id"])
	if userID == "" {
		return nil, errors.New("manager identity source returned an invalid user")
	}
	service, ok := profile["is_service_account"].(bool)
	if !ok {
		return nil, errors.New("manager identity source did not confirm a personal account type")
	}
	if service {
		return nil, ErrManagerScopeDenied
	}
	membership, err := c.managerRead(ctx, "/workspaces/"+workspaceID+"/user-permissions", token)
	if err != nil {
		return nil, err
	}
	if managerID(membership["user_id"]) != userID {
		return nil, errors.New("manager identity and membership do not match")
	}
	for _, key := range []string{"workspace_id", "work_space_id"} {
		if value, ok := membership[key]; ok && managerID(value) != workspaceID {
			return nil, errors.New("manager membership belongs to another workspace")
		}
	}
	items, ok := membership["permissions"].([]any)
	if !ok {
		return nil, errors.New("manager membership has no authoritative permissions list")
	}
	permissions := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		slug, ok := item.(string)
		if object, isObject := item.(map[string]any); isObject {
			slug, ok = object["slug"].(string)
		}
		if !ok || slug == "" || strings.TrimSpace(slug) != slug {
			return nil, errors.New("manager membership contains an invalid permission")
		}
		if !seen[slug] {
			seen[slug] = true
			permissions = append(permissions, slug)
		}
	}
	return &domain.ManagerAccess{ExternalUserID: userID, ExternalWorkspaceID: workspaceID, Permissions: permissions, PersonalAccountVerified: true}, nil
}

func managerID(value any) string {
	var id string
	switch v := value.(type) {
	case string:
		id = v
	case json.Number:
		id = v.String()
	}
	if !managerPositiveID.MatchString(id) {
		return ""
	}
	return id
}

func (c *Client) managerRead(ctx context.Context, path, token string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, errors.New("manager identity source is not configured")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	client := *c.httpClient
	if client.Timeout <= 0 || client.Timeout > 8*time.Second {
		client.Timeout = 8 * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("manager identity source is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound {
		return nil, ErrManagerScopeDenied
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manager identity source returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1_048_577))
	if err != nil || len(raw) > 1_048_576 {
		return nil, errors.New("manager identity response could not be verified")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var body map[string]any
	if err := decoder.Decode(&body); err != nil || body == nil {
		return nil, errors.New("manager identity source returned an invalid response")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("manager identity source returned trailing data")
	}
	return body, nil
}
