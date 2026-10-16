import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// @Helper
type ControlPlaneTransportHelper struct {
	// @Needs
	proxyFlowHelper *ProxyFlowHelper
	// @Needs
	proxyResponseHelper *ProxyResponseHelper
}

func (helper *ControlPlaneTransportHelper) Exchange(work *ProxyRequest) {
	helper.proxyFlowHelper.Run(work, helper.client)
	helper.proxyFlowHelper.Run(work, helper.certificate)
	helper.proxyFlowHelper.Run(work, helper.readCertificate)
	helper.proxyFlowHelper.Run(work, helper.trustCertificate)
	helper.closeCertificate(work)
	helper.proxyFlowHelper.Run(work, helper.request)
	helper.proxyFlowHelper.Run(work, helper.send)
	helper.proxyFlowHelper.Run(work, helper.read)
	helper.classify(work)
	helper.close(work)
	helper.cancel(work)
}

func (helper *ControlPlaneTransportHelper) client(work *ProxyRequest) {
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableKeepAlives: true}
	work.Client = &http.Client{Transport: transport, CheckRedirect: helper.redirect}
}

func (helper *ControlPlaneTransportHelper) certificate(work *ProxyRequest) {
	if work.Settings.CAFile == "" {
		return
	}
	helper.loadCertificate(work)
}

func (helper *ControlPlaneTransportHelper) loadCertificate(work *ProxyRequest) {
	file, failure := os.Open(work.Settings.CAFile)
	helper.proxyFlowHelper.Failure(work, failure, helper.failure())
	work.CertificateFile = file
}

func (helper *ControlPlaneTransportHelper) readCertificate(work *ProxyRequest) {
	if work.CertificateFile == nil {
		return
	}
	helper.readCertificateFile(work)
}

func (helper *ControlPlaneTransportHelper) readCertificateFile(work *ProxyRequest) {
	certificate, failure := io.ReadAll(io.LimitReader(work.CertificateFile, helper.fileLimit()+1))
	helper.proxyFlowHelper.Failure(work, failure, helper.failure())
	helper.proxyFlowHelper.Require(work, int64(len(certificate)) <= helper.fileLimit(), helper.proxyResponseHelper.Error(http.StatusBadGateway, helper.unreachableCode(), "the configured control plane CA exceeds the byte limit"))
	work.Certificate = certificate
}

func (helper *ControlPlaneTransportHelper) trustCertificate(work *ProxyRequest) {
	if work.CertificateFile == nil {
		return
	}
	helper.trust(work, work.Certificate)
}

func (helper *ControlPlaneTransportHelper) closeCertificate(work *ProxyRequest) {
	if work.CertificateFile != nil {
		work.CertificateFile.Close()
	}
}

func (helper *ControlPlaneTransportHelper) fileLimit() int64 { return 65536 }

func (helper *ControlPlaneTransportHelper) trust(work *ProxyRequest, certificate []byte) {
	roots := x509.NewCertPool()
	helper.proxyFlowHelper.Require(work, roots.AppendCertsFromPEM(certificate), helper.proxyResponseHelper.Error(http.StatusBadGateway, helper.unreachableCode(), "the configured control plane CA is invalid"))
	work.Client.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
}

func (helper *ControlPlaneTransportHelper) request(work *ProxyRequest) {
	work.Context, work.Cancel = context.WithTimeout(work.Incoming.Context(), time.Duration(work.Settings.TimeoutMillis)*time.Millisecond)
	outgoing, failure := http.NewRequestWithContext(work.Context, work.Call.Method, work.Settings.APIAddress+work.Call.Path, bytes.NewReader(work.Body))
	helper.proxyFlowHelper.Failure(work, failure, helper.failure())
	work.Outgoing = outgoing
	helper.proxyFlowHelper.Run(work, helper.headers)
}

func (helper *ControlPlaneTransportHelper) headers(work *ProxyRequest) {
	work.Outgoing.Header = work.Header
}

func (helper *ControlPlaneTransportHelper) send(work *ProxyRequest) {
	response, failure := work.Client.Do(work.Outgoing)
	helper.proxyFlowHelper.Failure(work, failure, helper.failure())
	work.Upstream = response
	work.Failure = failure
}

func (helper *ControlPlaneTransportHelper) read(work *ProxyRequest) {
	body, failure := io.ReadAll(io.LimitReader(work.Upstream.Body, work.Limit+1))
	helper.proxyFlowHelper.Failure(work, failure, helper.failure())
	helper.proxyFlowHelper.Require(work, int64(len(body)) <= work.Limit, helper.proxyResponseHelper.Error(http.StatusBadGateway, helper.unreachableCode(), "the control plane response exceeds the byte limit"))
	helper.proxyFlowHelper.Require(work, utf8.Valid(body), helper.proxyResponseHelper.Error(http.StatusBadGateway, helper.unreachableCode(), "the control plane response is not valid UTF-8"))
	work.Body = body
	work.Failure = failure
}

func (helper *ControlPlaneTransportHelper) close(work *ProxyRequest) {
	if work.Upstream != nil {
		work.Upstream.Body.Close()
	}
}

func (helper *ControlPlaneTransportHelper) cancel(work *ProxyRequest) {
	if work.Cancel != nil {
		work.Cancel()
	}
}

func (helper *ControlPlaneTransportHelper) redirect(request *http.Request, previous []*http.Request) error {
	return http.ErrUseLastResponse
}

func (helper *ControlPlaneTransportHelper) failure() ProxyError {
	return ProxyError{Code: helper.unreachableCode(), Message: "the control plane is unreachable or did not complete its response"}
}

func (helper *ControlPlaneTransportHelper) classify(work *ProxyRequest) {
	if !slices.Contains([]bool{errors.Is(work.Failure, context.DeadlineExceeded), strings.HasSuffix(work.Call.Path, "/invoke")}, false) {
		work.Answer = helper.proxyResponseHelper.Error(http.StatusGatewayTimeout, "HD0408", "invocation timed out; execution may continue; do not retry automatically")
	}
}

func (helper *ControlPlaneTransportHelper) unreachableCode() string { return "HD0602" }
