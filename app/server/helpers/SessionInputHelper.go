// @Helper
type SessionInputHelper struct {
	// @Needs
	proxyFlowHelper *ProxyFlowHelper
	// @Needs
	proxyConfigurationHelper *ProxyConfigurationHelper
	// @Needs
	proxyBodyHelper *ProxyBodyHelper
}

func (helper *SessionInputHelper) Configure(work *ProxyRequest) {
	helper.proxyConfigurationHelper.Load(work)
}

func (helper *SessionInputHelper) Read(work *ProxyRequest) {
	helper.proxyConfigurationHelper.Load(work)
	helper.proxyFlowHelper.Run(work, helper.proxyBodyHelper.Read)
	helper.proxyFlowHelper.Run(work, helper.proxyBodyHelper.Credentials)
}

func (helper *SessionInputHelper) Session(work *ProxyRequest) {
	helper.proxyBodyHelper.Session(work)
}
