import (
	"encoding/json"
	"net/http"
	"slices"
)

// @Service
type SessionService struct {
	// @Needs
	proxyFlowHelper *ProxyFlowHelper
	// @Needs
	sessionInputHelper *SessionInputHelper
	// @Needs
	proxyCookieHelper *ProxyCookieHelper
	// @Needs
	controlPlaneTransportHelper *ControlPlaneTransportHelper
	// @Needs
	proxyResponseHelper *ProxyResponseHelper
}

func (service *SessionService) SignIn(request *http.Request) *http.Response {
	work := &ProxyRequest{Incoming: request, Limit: service.responseLimit()}
	service.sessionInputHelper.Read(work)
	service.login(work)
	return service.proxyFlowHelper.Answer(work)
}

func (service *SessionService) SignOut(request *http.Request) *http.Response {
	work := &ProxyRequest{Incoming: request, Limit: service.responseLimit()}
	service.sessionInputHelper.Configure(work)
	service.proxyFlowHelper.Run(work, service.proxyCookieHelper.Read)
	service.proxyFlowHelper.Run(work, service.logoutReady)
	service.proxyFlowHelper.Run(work, service.logoutRequest)
	service.controlPlaneTransportHelper.Exchange(work)
	return service.proxyResponseHelper.JSON(http.StatusOK, SessionStatus{Status: service.loggedOut()}, http.Header{service.cookieHeader(): {service.proxyCookieHelper.Clear()}})
}

func (service *SessionService) AuditSignIn(request *http.Request) *http.Response {
	work := &ProxyRequest{Incoming: request, Limit: service.responseLimit()}
	service.sessionInputHelper.Configure(work)
	service.auditCredentials(work)
	service.login(work)
	return service.auditAnswer(work)
}

func (service *SessionService) login(work *ProxyRequest) {
	service.proxyFlowHelper.Run(work, service.credentials)
	service.proxyFlowHelper.Run(work, service.loginRequest)
	service.controlPlaneTransportHelper.Exchange(work)
	service.proxyFlowHelper.Run(work, service.loginRefusal)
	service.proxyFlowHelper.Run(work, service.sessionInputHelper.Session)
	service.proxyFlowHelper.Run(work, service.proxyCookieHelper.Issue)
	service.proxyFlowHelper.Run(work, service.loginAnswer)
}

func (service *SessionService) credentials(work *ProxyRequest) {
	service.proxyFlowHelper.Require(work, work.Credentials.Identifier != "" && work.Credentials.Password != "", service.proxyResponseHelper.Error(http.StatusBadRequest, service.invalidInputCode(), "identifier and password are required"))
}

func (service *SessionService) loginRequest(work *ProxyRequest) {
	work.Call = GatewayCall{Method: http.MethodPost, Path: "/api/v1/auth/login"}
	payload, failure := json.Marshal(work.Credentials)
	service.proxyFlowHelper.Failure(work, failure, ProxyError{Code: service.invalidInputCode(), Message: "the request could not be encoded"})
	work.Body = payload
	work.Header = http.Header{service.contentTypeHeader(): {"application/json"}}
}

func (service *SessionService) loginRefusal(work *ProxyRequest) {
	if work.Upstream.StatusCode != http.StatusOK {
		work.Answer = service.proxyResponseHelper.Raw(work.Upstream.StatusCode, work.Body, nil)
	}
}

func (service *SessionService) loginAnswer(work *ProxyRequest) {
	identity := SessionIdentity{PrincipalID: work.Session.PrincipalID, ExpiresAt: work.Session.ExpiresAt, Assurance: work.Session.Assurance}
	work.Answer = service.proxyResponseHelper.JSON(http.StatusOK, identity, http.Header{service.cookieHeader(): {work.Cookie}})
}

func (service *SessionService) logoutReady(work *ProxyRequest) {
	if work.Bearer == "" {
		work.Answer = service.proxyResponseHelper.JSON(http.StatusOK, SessionStatus{Status: service.loggedOut()}, nil)
	}
}

func (service *SessionService) logoutRequest(work *ProxyRequest) {
	work.Call = GatewayCall{Method: http.MethodPost, Path: "/api/v1/auth/logout"}
	work.Body = nil
	work.Header = http.Header{"Authorization": {"Bearer " + work.Bearer}}
}

func (service *SessionService) auditCredentials(work *ProxyRequest) {
	if work.Settings.AuditLogin == nil {
		work.Answer = service.proxyResponseHelper.Raw(http.StatusNotFound, []byte("not enabled"), http.Header{service.contentTypeHeader(): {service.plainTextType()}})
		return
	}
	work.Credentials = *work.Settings.AuditLogin
}

func (service *SessionService) auditAnswer(work *ProxyRequest) *http.Response {
	service.auditRedirect(work)
	service.auditFailure(work)
	return work.Answer
}

func (service *SessionService) auditFailure(work *ProxyRequest) {
	if !slices.Contains([]int{http.StatusFound, http.StatusNotFound}, work.Answer.StatusCode) {
		work.Answer = service.proxyResponseHelper.Raw(http.StatusBadGateway, []byte("login failed"), http.Header{service.contentTypeHeader(): {service.plainTextType()}})
	}
}

func (service *SessionService) auditRedirect(work *ProxyRequest) {
	if work.Answer.StatusCode == http.StatusOK {
		work.Answer = service.proxyResponseHelper.Raw(http.StatusFound, nil, http.Header{"Location": {"/"}, service.cookieHeader(): {work.Cookie}})
	}
}

func (service *SessionService) cookieHeader() string { return "Set-Cookie" }
func (service *SessionService) responseLimit() int64 { return 1048576 }

func (service *SessionService) invalidInputCode() string { return "HD0601" }

func (service *SessionService) contentTypeHeader() string { return "Content-Type" }

func (service *SessionService) plainTextType() string { return "text/plain" }

func (service *SessionService) loggedOut() string { return "logged out" }
