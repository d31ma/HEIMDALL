import "net/http"

type ProxyFrame struct {
	Status  int         `json:"status"`
	Headers http.Header `json:"headers"`
	Body    string      `json:"body"`
}
