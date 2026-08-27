package sellico

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// NotificationPayload is the body of POST /api/workspaces/{ws}/notifications
// (CRM storeExternal): a bell/telegram/email notification delivered through
// the CRM's own channel preferences (quiet hours, anti-flood).
type NotificationPayload struct {
	UserIDs   []int64        // CRM user ids (users.external_user_id on our side)
	Type      string         // e.g. "ads-ai-applied"
	Title     string         // required
	Message   string         // required
	ActionURL string         // optional deep link into the CRM UI
	Data      map[string]any // optional extra payload
}

// SendWorkspaceNotification pushes one notification to CRM workspace members.
// workspaceID is the CRM workspace id (external_workspace_id on our side).
// Requires a service-account token. Callers must treat failures as
// best-effort: log and continue, never block the calling job.
func (c *Client) SendWorkspaceNotification(ctx context.Context, serviceToken, workspaceID string, p NotificationPayload) error {
	if strings.TrimSpace(workspaceID) == "" {
		return fmt.Errorf("sellico SendWorkspaceNotification: empty workspaceID")
	}
	if len(p.UserIDs) == 0 {
		return fmt.Errorf("sellico SendWorkspaceNotification: no recipients")
	}
	payload := map[string]any{
		"user_ids": p.UserIDs,
		"type":     p.Type,
		"title":    p.Title,
		"message":  p.Message,
	}
	if p.ActionURL != "" {
		payload["action_url"] = p.ActionURL
	}
	if len(p.Data) > 0 {
		payload["data"] = p.Data
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("sellico SendWorkspaceNotification: marshal: %w", err)
	}

	url := c.baseURL + "/workspaces/" + workspaceID + "/notifications"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sellico SendWorkspaceNotification: new request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+serviceToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sellico SendWorkspaceNotification: transport: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sellico SendWorkspaceNotification: status %d", resp.StatusCode)
	}
	return nil
}
