type LoginCredentials struct {
	Identifier      string `json:"identifier"`
	Password        string `json:"password"`
	OneTimePassword string `json:"totp"`
}
