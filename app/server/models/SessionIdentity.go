type SessionIdentity struct {
	PrincipalID string `json:"principal_id,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	Assurance   string `json:"assurance,omitempty"`
}
