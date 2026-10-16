import (
	"bytes"
	"net/http"
)

// @Service
type GatewayService struct {
	// @Needs
	proxyFlowHelper *ProxyFlowHelper
	// @Needs
	gatewayInputHelper *GatewayInputHelper
	// @Needs
	controlPlaneTransportHelper *ControlPlaneTransportHelper
	// @Needs
	proxyResponseHelper *ProxyResponseHelper
}

func (service *GatewayService) Relay(request *http.Request) *http.Response {
	work := &ProxyRequest{Incoming: request, Limit: service.responseLimit()}
	service.gatewayInputHelper.Read(work)
	service.proxyFlowHelper.Run(work, service.body)
	service.proxyFlowHelper.Run(work, service.timeout)
	service.proxyFlowHelper.Run(work, service.headers)
	service.controlPlaneTransportHelper.Exchange(work)
	service.proxyFlowHelper.Run(work, service.answer)
	return service.proxyFlowHelper.Answer(work)
}

func (service *GatewayService) body(work *ProxyRequest) {
	work.Body = service.presentBody(work.Call.Body)
	service.getBody(work)
}

func (service *GatewayService) getBody(work *ProxyRequest) {
	if work.Call.Method == http.MethodGet {
		work.Body = nil
	}
}

func (service *GatewayService) presentBody(body []byte) []byte {
	if bytes.Equal(body, []byte("null")) {
		return nil
	}
	return body
}

func (service *GatewayService) timeout(work *ProxyRequest) {
	if work.Call.TimeoutMillis > 0 {
		work.Settings.TimeoutMillis = min(work.Call.TimeoutMillis, work.Settings.TimeoutMillis)
	}
}

func (service *GatewayService) headers(work *ProxyRequest) {
	work.Header = http.Header{"Authorization": {"Bearer " + work.Bearer}}
	service.contentType(work)
}

func (service *GatewayService) contentType(work *ProxyRequest) {
	if len(work.Body) > 0 {
		work.Header.Set(service.contentTypeHeader(), "application/json")
	}
}

func (service *GatewayService) answer(work *ProxyRequest) {
	work.Answer = service.proxyResponseHelper.Raw(work.Upstream.StatusCode, service.responseBody(work.Body), service.responseHeader(work.Upstream))
}

func (service *GatewayService) responseHeader(upstream *http.Response) http.Header {
	return http.Header{service.contentTypeHeader(): upstream.Header.Values(service.contentTypeHeader())}
}

func (service *GatewayService) responseBody(body []byte) []byte {
	if len(body) == 0 {
		return []byte("{}")
	}
	return body
}

func (service *GatewayService) responseLimit() int64 { return 8388608 }

func (service *GatewayService) contentTypeHeader() string { return "Content-Type" }
