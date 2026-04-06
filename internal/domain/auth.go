package domain

import "github.com/google/uuid"

// AuthPrincipal contains the authenticated local user plus shared-auth metadata.
type AuthPrincipal struct {
	UserID         uuid.UUID
	ExternalUserID string
	Email          string
	Name           string
	Token          string
}

// WorkspaceAccess describes the resolved workspace and the user's role within it.
type WorkspaceAccess struct {
	WorkspaceID uuid.UUID
	Role        string
}
