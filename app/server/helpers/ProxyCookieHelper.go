import (
	"net/http"
	"net/url"
	"time"
)

// @Helper
type ProxyCookieHelper struct {
	// @Needs
	proxyFlowHelper *ProxyFlowHelper
	// @Needs
	proxyResponseHelper *ProxyResponseHelper
}

func (helper *ProxyCookieHelper) Read(work *ProxyRequest) {
	helper.read(work)
}

func (helper *ProxyCookieHelper) Require(work *ProxyRequest) {
	helper.proxyFlowHelper.Require(work, work.Bearer != "", helper.proxyResponseHelper.Error(http.StatusUnauthorized, "HD0401", "not signed in"))
}

func (helper *ProxyCookieHelper) Issue(work *ProxyRequest) {
	seconds := helper.seconds(work.Session.ExpiresAt)
	cookie := &http.Cookie{Name: helper.name(), Value: work.Session.SessionID + "." + work.Session.SessionSecret, Path: helper.cookiePath(), HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: seconds, Secure: helper.secure(work.Settings.SecureCookie)}
	failure := cookie.Valid()
	helper.proxyFlowHelper.Failure(work, failure, ProxyError{Code: "HD0602", Message: "the control plane returned an invalid session cookie"})
	helper.proxyFlowHelper.Run(work, func(work *ProxyRequest) { work.Cookie = cookie.String() })
}

func (helper *ProxyCookieHelper) Clear() string {
	return helper.cleared().String()
}

func (helper *ProxyCookieHelper) read(work *ProxyRequest) {
	cookie, failure := work.Incoming.Cookie(helper.name())
	if failure != nil {
		return
	}
	work.Bearer, _ = url.PathUnescape(cookie.Value)
}

func (helper *ProxyCookieHelper) seconds(value string) int {
	deadline, failure := time.Parse(time.RFC3339Nano, value)
	return helper.lifetime(deadline, failure)
}

func (helper *ProxyCookieHelper) lifetime(deadline time.Time, failure error) int {
	if failure != nil {
		return helper.defaultLifetime()
	}
	return int(max(helper.minimumLifetime(), time.Until(deadline).Seconds()))
}

func (helper *ProxyCookieHelper) secure(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}

func (helper *ProxyCookieHelper) cleared() *http.Cookie {
	return &http.Cookie{Name: helper.name(), Path: helper.cookiePath(), HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1}
}

func (helper *ProxyCookieHelper) name() string             { return "hd_session" }
func (helper *ProxyCookieHelper) defaultLifetime() int     { return 3600 }
func (helper *ProxyCookieHelper) minimumLifetime() float64 { return 60 }

func (helper *ProxyCookieHelper) cookiePath() string { return "/" }
