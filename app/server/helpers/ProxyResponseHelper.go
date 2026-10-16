import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"unicode/utf8"
)

// @Helper
type ProxyResponseHelper struct{}

func (helper *ProxyResponseHelper) JSON(status int, value any, header http.Header) *http.Response {
	body := helper.encode(value)
	return helper.Raw(status, body, header)
}

func (helper *ProxyResponseHelper) Raw(status int, body []byte, header http.Header) *http.Response {
	headers := helper.headers(header)
	helper.contentType(headers)
	return helper.bounded(status, body, headers)
}

func (helper *ProxyResponseHelper) Error(status int, code string, message string) *http.Response {
	return helper.JSON(status, ProxyError{Code: code, Message: message}, nil)
}

func (helper *ProxyResponseHelper) encode(value any) []byte {
	body, failure := json.Marshal(value)
	if failure != nil {
		return []byte(`{"code":"HD0602","message":"the web tier could not encode its response"}`)
	}
	return body
}

func (helper *ProxyResponseHelper) headers(header http.Header) http.Header {
	if header == nil {
		return http.Header{}
	}
	return header.Clone()
}

func (helper *ProxyResponseHelper) contentType(header http.Header) {
	if header.Get(helper.typeHeader()) == "" {
		header.Set(helper.typeHeader(), "application/json; charset=utf-8")
	}
}

func (helper *ProxyResponseHelper) bounded(status int, body []byte, header http.Header) *http.Response {
	frame, _ := json.Marshal(ProxyFrame{Status: status, Headers: header, Body: string(body)})
	if slices.Contains([]bool{utf8.Valid(body), len(frame) <= helper.frameLimit()}, false) {
		return helper.Error(http.StatusBadGateway, "HD0602", "the control plane response is invalid UTF-8 or exceeds the encoded response limit")
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(bytes.NewReader(body))}
}

func (helper *ProxyResponseHelper) frameLimit() int { return 15728640 }

func (helper *ProxyResponseHelper) typeHeader() string { return "Content-Type" }
