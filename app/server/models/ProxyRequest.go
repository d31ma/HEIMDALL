import (
	"context"
	"net/http"
	"os"
)

type ProxyRequest struct {
	ConfigurationFile *os.File
	CertificateFile   *os.File
	Certificate       []byte
	Incoming          *http.Request
	Settings          ProxySettings
	Credentials       LoginCredentials
	Session           LoginSession
	Call              GatewayCall
	Bearer            string
	Cookie            string
	Header            http.Header
	Context           context.Context
	Cancel            context.CancelFunc
	Outgoing          *http.Request
	Client            *http.Client
	Upstream          *http.Response
	Body              []byte
	Limit             int64
	Failure           error
	Answer            *http.Response
}
