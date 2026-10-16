import "net/http"

// @Helper
type ProxyFlowHelper struct {
	// @Needs
	proxyResponseHelper *ProxyResponseHelper
}

func (helper *ProxyFlowHelper) Run(work *ProxyRequest, step func(*ProxyRequest)) {
	helper.run(work, step)
}

func (helper *ProxyFlowHelper) Answer(work *ProxyRequest) *http.Response {
	return helper.answer(work)
}

func (helper *ProxyFlowHelper) Failure(work *ProxyRequest, failure error, model ProxyError) {
	helper.failure(work, failure, model)
}

func (helper *ProxyFlowHelper) Require(work *ProxyRequest, permitted bool, response *http.Response) {
	helper.require(work, permitted, response)
}

func (helper *ProxyFlowHelper) run(work *ProxyRequest, step func(*ProxyRequest)) {
	if work.Answer == nil {
		step(work)
	}
}

func (helper *ProxyFlowHelper) answer(work *ProxyRequest) *http.Response {
	if work.Answer != nil {
		return work.Answer
	}
	return helper.proxyResponseHelper.Error(http.StatusBadGateway, "HD0602", "the control plane did not return a complete response")
}

func (helper *ProxyFlowHelper) failure(work *ProxyRequest, failure error, model ProxyError) {
	if failure != nil {
		work.Answer = helper.proxyResponseHelper.JSON(http.StatusBadGateway, model, nil)
	}
}

func (helper *ProxyFlowHelper) require(work *ProxyRequest, permitted bool, response *http.Response) {
	if !permitted {
		work.Answer = response
	}
}

func (helper *ProxyFlowHelper) Refusal(status int, code string, message string) *http.Response {
	return helper.proxyResponseHelper.Error(status, code, message)
}
