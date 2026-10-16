import "net/http"

// @RequestRoute("/gateway")
// @Public
type GatewayController struct {
	// @Needs
	gatewayService *GatewayService
}

// @PostRoute
func (controller *GatewayController) Post(request *http.Request) *http.Response {
	return controller.gatewayService.Relay(request)
}
