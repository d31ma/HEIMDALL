import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
)

// @Helper
type ProxyConfigurationHelper struct {
	// @Needs
	proxyFlowHelper *ProxyFlowHelper
	// @Needs
	proxyResponseHelper *ProxyResponseHelper
}

func (helper *ProxyConfigurationHelper) Load(work *ProxyRequest) {
	helper.proxyFlowHelper.Run(work, helper.open)
	helper.proxyFlowHelper.Run(work, helper.read)
	helper.proxyFlowHelper.Run(work, helper.parse)
	helper.proxyFlowHelper.Run(work, helper.address)
	helper.proxyFlowHelper.Run(work, helper.timeout)
	helper.proxyFlowHelper.Run(work, helper.ceiling)
	helper.close(work)
}

func (helper *ProxyConfigurationHelper) open(work *ProxyRequest) {
	file, failure := os.Open("server/data/bff.json")
	if failure != nil {
		work.Answer = helper.missing()
		return
	}
	work.ConfigurationFile = file
}

func (helper *ProxyConfigurationHelper) read(work *ProxyRequest) {
	body, failure := io.ReadAll(io.LimitReader(work.ConfigurationFile, helper.fileLimit()+1))
	if slices.Contains([]bool{failure != nil, int64(len(body)) > helper.fileLimit()}, true) {
		work.Answer = helper.missing()
		return
	}
	work.Body = body
}

func (helper *ProxyConfigurationHelper) close(work *ProxyRequest) {
	if work.ConfigurationFile != nil {
		work.ConfigurationFile.Close()
	}
}

func (helper *ProxyConfigurationHelper) fileLimit() int64 { return 65536 }

func (helper *ProxyConfigurationHelper) parse(work *ProxyRequest) {
	failure := json.Unmarshal(work.Body, &work.Settings)
	if failure != nil {
		work.Answer = helper.missing()
	}
}

func (helper *ProxyConfigurationHelper) address(work *ProxyRequest) {
	work.Settings.APIAddress = strings.TrimRight(work.Settings.APIAddress, "/")
}

func (helper *ProxyConfigurationHelper) timeout(work *ProxyRequest) {
	if work.Settings.TimeoutMillis <= 0 {
		work.Settings.TimeoutMillis = helper.maximumTimeout()
	}
}

func (helper *ProxyConfigurationHelper) missing() *http.Response {
	return helper.proxyResponseHelper.Error(http.StatusServiceUnavailable, "HD0600", "the web tier is not configured; server/data/bff.json is missing or unreadable")
}

func (helper *ProxyConfigurationHelper) maximumTimeout() int64 { return 25000 }

func (helper *ProxyConfigurationHelper) ceiling(work *ProxyRequest) {
	work.Settings.TimeoutMillis = min(work.Settings.TimeoutMillis, helper.maximumTimeout())
}
