package sellico

import (
	"context"
	"errors"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"strings"
)

// VerifyManagerIntegration reads the personal, scoped CRM endpoint afresh.
// It does not fetch credentials, create a cabinet or synchronize anything.
func (c *Client) VerifyManagerIntegration(ctx context.Context, token, workspaceID, integrationID string) (*domain.ManagerIntegration, error) {
	if c == nil || c.httpClient == nil || c.baseURL == "" {
		return nil, errors.New("manager integration source is not configured")
	}
	if !managerPositiveID.MatchString(workspaceID) || !managerPositiveID.MatchString(integrationID) || token == "" || len(token) > 8192 || strings.ContainsAny(token, " \t\r\n") {
		return nil, ErrManagerScopeDenied
	}
	body, err := c.managerRead(ctx, "/workspaces/"+workspaceID+"/integrations/"+integrationID, token)
	if err != nil {
		return nil, err
	}
	if managerID(body["id"]) != integrationID || managerID(body["work_space_id"]) != workspaceID || body["status"] != "active" {
		return nil, ErrManagerScopeDenied
	}
	market := ""
	switch strings.ToLower(strings.TrimSpace(stringify(body["type"]))) {
	case "wildberries", "wb":
		market = "wb"
	case "ozon":
		market = "ozon"
	}
	if market == "" {
		return nil, ErrManagerScopeDenied
	}
	name, _ := body["name"].(string)
	return &domain.ManagerIntegration{ID: integrationID, WorkspaceID: workspaceID, Marketplace: market, Name: name}, nil
}
