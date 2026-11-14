package domain

// ManagerAccess is a request-local CRM identity. It contains no credential or role fallback.
type ManagerAccess struct {
	ExternalUserID      string
	ExternalWorkspaceID string
	Permissions         []string
	// True only when the fresh CRM profile explicitly says is_service_account=false.
	PersonalAccountVerified bool
}

func (a ManagerAccess) Allows(permission string) bool {
	for _, slug := range a.Permissions {
		if slug == permission {
			return true
		}
	}
	return false
}
