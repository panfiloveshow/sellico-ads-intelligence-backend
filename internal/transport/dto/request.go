package dto

import (
	"encoding/json"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/validate"
)

// --- Auth ---

// RegisterRequest is the DTO for POST /auth/register.
type RegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

// Validate returns field errors (empty map if valid).
func (r RegisterRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("email", r.Email)
	v.Email("email", r.Email)
	v.Required("password", r.Password)
	v.MinLength("password", r.Password, 8)
	v.MaxLength("password", r.Password, 128)
	v.Required("name", r.Name)
	v.MaxLength("name", r.Name, 255)
	return v.FieldErrors()
}

// LoginRequest is the DTO for POST /auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Validate returns field errors (empty map if valid).
func (r LoginRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("email", r.Email)
	v.Email("email", r.Email)
	v.Required("password", r.Password)
	return v.FieldErrors()
}

// RefreshTokenRequest is the DTO for POST /auth/refresh.
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Validate returns field errors (empty map if valid).
func (r RefreshTokenRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("refresh_token", r.RefreshToken)
	return v.FieldErrors()
}

// --- Workspace ---

// CreateWorkspaceRequest is the DTO for POST /workspaces.
type CreateWorkspaceRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Validate returns field errors (empty map if valid).
func (r CreateWorkspaceRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("name", r.Name)
	v.MaxLength("name", r.Name, 255)
	v.Required("slug", r.Slug)
	v.MaxLength("slug", r.Slug, 100)
	return v.FieldErrors()
}

// InviteMemberRequest is the DTO for POST /workspaces/{id}/members.
type InviteMemberRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// Validate returns field errors (empty map if valid).
func (r InviteMemberRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("email", r.Email)
	v.Email("email", r.Email)
	v.Required("role", r.Role)
	v.OneOf("role", r.Role, []string{"owner", "manager", "analyst", "viewer"})
	return v.FieldErrors()
}

// UpdateMemberRoleRequest is the DTO for PATCH /workspaces/{id}/members/{memberId}.
type UpdateMemberRoleRequest struct {
	Role string `json:"role"`
}

// Validate returns field errors (empty map if valid).
func (r UpdateMemberRoleRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("role", r.Role)
	v.OneOf("role", r.Role, []string{"owner", "manager", "analyst", "viewer"})
	return v.FieldErrors()
}

// --- Seller Cabinet ---

// CreateSellerCabinetRequest is the DTO for POST /seller-cabinets.
type CreateSellerCabinetRequest struct {
	Name     string `json:"name"`
	APIToken string `json:"api_token"`
}

// Validate returns field errors (empty map if valid).
func (r CreateSellerCabinetRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("name", r.Name)
	v.MaxLength("name", r.Name, 255)
	v.Required("api_token", r.APIToken)
	return v.FieldErrors()
}

// --- Recommendation ---

// UpdateRecommendationStatusRequest is the DTO for PATCH /recommendations/{id}.
type UpdateRecommendationStatusRequest struct {
	Status string `json:"status"`
}

// Validate returns field errors (empty map if valid).
func (r UpdateRecommendationStatusRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("status", r.Status)
	v.OneOf("status", r.Status, []string{"active", "completed", "dismissed"})
	return v.FieldErrors()
}

// --- Export ---

// CreateExportRequest is the DTO for POST /exports.
type CreateExportRequest struct {
	EntityType string          `json:"entity_type"`
	Format     string          `json:"format"`
	Filters    json.RawMessage `json:"filters,omitempty"`
}

// Validate returns field errors (empty map if valid).
func (r CreateExportRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("entity_type", r.EntityType)
	v.OneOf("entity_type", r.EntityType, []string{
		"campaigns", "campaign_stats", "phrases", "phrase_stats",
		"products", "positions", "recommendations",
	})
	v.Required("format", r.Format)
	v.OneOf("format", r.Format, []string{"csv", "xlsx"})
	return v.FieldErrors()
}

// --- Extension ---

// CreateExtensionSessionRequest is the DTO for POST /extension/sessions.
type CreateExtensionSessionRequest struct {
	ExtensionVersion string `json:"extension_version"`
}

// Validate returns field errors (empty map if valid).
func (r CreateExtensionSessionRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("extension_version", r.ExtensionVersion)
	return v.FieldErrors()
}

// ExtensionContextRequest is the DTO for POST /extension/context.
type ExtensionContextRequest struct {
	URL      string `json:"url"`
	PageType string `json:"page_type"`
}

// Validate returns field errors (empty map if valid).
func (r ExtensionContextRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("url", r.URL)
	v.Required("page_type", r.PageType)
	v.OneOf("page_type", r.PageType, []string{"search", "product"})
	return v.FieldErrors()
}
