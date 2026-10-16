import (
	"net/http"
	"slices"
	"strings"
)

// @Helper
type GatewayPathHelper struct {
	// @Needs
	proxyFlowHelper *ProxyFlowHelper
	// @Needs
	proxyResponseHelper *ProxyResponseHelper
}

func (helper *GatewayPathHelper) Validate(work *ProxyRequest) {
	helper.proxyFlowHelper.Run(work, helper.length)
	helper.proxyFlowHelper.Run(work, helper.prefix)
	helper.proxyFlowHelper.Run(work, helper.characters)
	helper.proxyFlowHelper.Run(work, helper.pathname)
}

func (helper *GatewayPathHelper) length(work *ProxyRequest) {
	helper.require(work, len(work.Call.Path) > 0 && len(work.Call.Path) <= helper.limit())
}

func (helper *GatewayPathHelper) prefix(work *ProxyRequest) {
	helper.require(work, strings.HasPrefix(work.Call.Path, "/api/v1/"))
}

func (helper *GatewayPathHelper) characters(work *ProxyRequest) {
	helper.require(work, !strings.Contains(work.Call.Path, "..") && !strings.Contains(work.Call.Path, "\\"))
}

func (helper *GatewayPathHelper) pathname(work *ProxyRequest) {
	path := strings.SplitN(work.Call.Path, "?", 2)[0]
	helper.proxyFlowHelper.Run(work, func(work *ProxyRequest) { helper.aliases(work, path) })
	helper.proxyFlowHelper.Run(work, func(work *ProxyRequest) { helper.forbidden(work, path) })
}

func (helper *GatewayPathHelper) aliases(work *ProxyRequest, path string) {
	permitted := !slices.Contains([]bool{strings.ContainsAny(path, "%#"), strings.Contains(path, "//"), slices.Contains(strings.Split(path, "/"), "."), strings.ContainsFunc(path, helper.control)}, true)
	helper.require(work, permitted)
}

func (helper *GatewayPathHelper) forbidden(work *ProxyRequest, path string) {
	helper.require(work, !slices.Contains(helper.forbiddenPaths(), path))
}

func (helper *GatewayPathHelper) control(value rune) bool {
	return value <= ' ' || value == helper.deleteCharacter()
}

func (helper *GatewayPathHelper) require(work *ProxyRequest, permitted bool) {
	helper.proxyFlowHelper.Require(work, permitted, helper.proxyResponseHelper.Error(http.StatusBadRequest, "HD0604", "path must be a plain path under /api/v1/ and may not name the session routes"))
}

func (helper *GatewayPathHelper) forbiddenPaths() []string {
	return []string{"/api/v1/auth/login", "/api/v1/auth/logout", "/api/v1/auth/device/start", "/api/v1/auth/device/token", "/api/v1/auth/device/refresh"}
}

func (helper *GatewayPathHelper) limit() int { return 2048 }

func (helper *GatewayPathHelper) deleteCharacter() rune { return 127 }
