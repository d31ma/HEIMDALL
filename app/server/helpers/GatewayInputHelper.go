import (
	"net/http"
	"slices"
	"strings"
)

// @Helper
type GatewayInputHelper struct {
	// @Needs
	proxyFlowHelper *ProxyFlowHelper
	// @Needs
	proxyConfigurationHelper *ProxyConfigurationHelper
	// @Needs
	proxyCookieHelper *ProxyCookieHelper
	// @Needs
	proxyBodyHelper *ProxyBodyHelper
	// @Needs
	gatewayPathHelper *GatewayPathHelper
}

func (helper *GatewayInputHelper) Read(work *ProxyRequest) {
	helper.proxyConfigurationHelper.Load(work)
	helper.proxyFlowHelper.Run(work, helper.proxyCookieHelper.Read)
	helper.proxyFlowHelper.Run(work, helper.proxyCookieHelper.Require)
	helper.proxyFlowHelper.Run(work, helper.proxyBodyHelper.Read)
	helper.proxyFlowHelper.Run(work, helper.proxyBodyHelper.Gateway)
	helper.proxyFlowHelper.Run(work, helper.method)
	helper.proxyFlowHelper.Run(work, helper.gatewayPathHelper.Validate)
}

func (helper *GatewayInputHelper) method(work *ProxyRequest) {
	work.Call.Method = strings.ToUpper(helper.defaultMethod(work.Call.Method))
	helper.proxyFlowHelper.Require(work, slices.Contains(helper.methods(), work.Call.Method), helper.proxyFlowHelper.Refusal(http.StatusBadRequest, "HD0603", "the request method is not allowed"))
}

func (helper *GatewayInputHelper) defaultMethod(method string) string {
	if method == "" {
		return http.MethodGet
	}
	return method
}

func (helper *GatewayInputHelper) methods() []string {
	return []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete}
}
