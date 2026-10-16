import "net/http"

// @RequestRoute("/session")
// @Public
type SessionController struct {
	// @Needs
	sessionService *SessionService
}

// @PostRoute
func (controller *SessionController) Post(request *http.Request) *http.Response {
	return controller.sessionService.SignIn(request)
}

// @DeleteRoute
func (controller *SessionController) Delete(request *http.Request) *http.Response {
	return controller.sessionService.SignOut(request)
}
