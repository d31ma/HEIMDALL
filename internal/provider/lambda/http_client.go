package lambda

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"net/http"
)

type httpClient struct {
	delegate aws.HTTPClient
}

func (c httpClient) Do(req *http.Request) (*http.Response, error) {
	return c.do(req)
}

func (c httpClient) do(req *http.Request) (*http.Response, error) {
	response, err := c.delegate.Do(req)
	if err != nil {
		return nil, err
	}
	response.Body = &boundedBody{body: response.Body, remaining: int64((functionLimits{}).PayloadBytes())}
	return response, nil
}
