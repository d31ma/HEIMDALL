import "net/http"

// @RequestRoute("/audit-login")
// @Public
type AuditLoginController struct {
	// @Needs
	sessionService *SessionService
}

// @GetRoute
func (controller *AuditLoginController) Get(request *http.Request) *http.Response {
	return controller.sessionService.AuditSignIn(request)
}
