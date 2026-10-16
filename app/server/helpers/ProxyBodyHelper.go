import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"time"
)

// @Helper
type ProxyBodyHelper struct {
	// @Needs
	proxyFlowHelper *ProxyFlowHelper
	// @Needs
	proxyResponseHelper *ProxyResponseHelper
}

func (helper *ProxyBodyHelper) Read(work *ProxyRequest) {
	body, failure := io.ReadAll(io.LimitReader(work.Incoming.Body, helper.limit()+1))
	helper.proxyFlowHelper.Failure(work, failure, ProxyError{Code: helper.invalidInputCode(), Message: "the request body could not be read"})
	helper.proxyFlowHelper.Require(work, int64(len(body)) <= helper.limit(), helper.large())
	work.Body = body
}

func (helper *ProxyBodyHelper) Gateway(work *ProxyRequest) {
	failure := json.Unmarshal(helper.empty(work.Body), &work.Call)
	helper.invalid(work, failure)
}

func (helper *ProxyBodyHelper) Credentials(work *ProxyRequest) {
	failure := json.Unmarshal(helper.empty(work.Body), &work.Credentials)
	helper.invalid(work, failure)
}

func (helper *ProxyBodyHelper) Session(work *ProxyRequest) {
	failure := json.Unmarshal(helper.empty(work.Body), &work.Session)
	helper.proxyFlowHelper.Failure(work, failure, ProxyError{Code: helper.invalidResponseCode(), Message: "the control plane returned invalid JSON"})
	helper.proxyFlowHelper.Run(work, helper.sessionFields)
	helper.proxyFlowHelper.Run(work, helper.sessionExpiry)
}

func (helper *ProxyBodyHelper) invalid(work *ProxyRequest, failure error) {
	if failure != nil {
		work.Answer = helper.proxyResponseHelper.Error(http.StatusBadRequest, helper.invalidInputCode(), "the request body is not valid JSON")
	}
}

func (helper *ProxyBodyHelper) empty(body []byte) []byte {
	if len(body) == 0 {
		return []byte("{}")
	}
	return body
}

func (helper *ProxyBodyHelper) large() *http.Response {
	return helper.proxyResponseHelper.Error(http.StatusRequestEntityTooLarge, "HD0605", "the request body is too large")
}

func (helper *ProxyBodyHelper) limit() int64 { return 1048576 }

func (helper *ProxyBodyHelper) invalidInputCode() string { return "HD0601" }

func (helper *ProxyBodyHelper) sessionFields(work *ProxyRequest) {
	permitted := !slices.Contains([]string{work.Session.SessionID, work.Session.SessionSecret, work.Session.PrincipalID}, "")
	helper.proxyFlowHelper.Require(work, permitted, helper.proxyResponseHelper.Error(http.StatusBadGateway, helper.invalidResponseCode(), "the control plane returned an invalid session response"))
}

func (helper *ProxyBodyHelper) sessionExpiry(work *ProxyRequest) {
	_, failure := time.Parse(time.RFC3339Nano, work.Session.ExpiresAt)
	helper.proxyFlowHelper.Failure(work, failure, ProxyError{Code: helper.invalidResponseCode(), Message: "the control plane returned an invalid session expiry"})
}

func (helper *ProxyBodyHelper) invalidResponseCode() string { return "HD0602" }
