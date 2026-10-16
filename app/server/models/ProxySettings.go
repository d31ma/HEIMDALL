type ProxySettings struct {
	APIAddress    string            `json:"apiUrl"`
	CAFile        string            `json:"caFile"`
	SecureCookie  *bool             `json:"secureCookie"`
	TimeoutMillis int64             `json:"timeoutMs"`
	AuditLogin    *LoginCredentials `json:"auditLogin"`
}
