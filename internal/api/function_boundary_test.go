package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/d31ma/heimdall/internal/provider/lambda"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFunctionBodyRejectsTrailingUnknownAndOversizedInput(t *testing.T) {
	for _, body := range []string{`{"payload":{}} {}`, `{"payload":{},"target":"caller-selected"}`, strings.Repeat(" ", 33)} {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		var input invocationInput
		err := (&strictBody{request: request, limit: 32}).Decode(&input)
		if err == nil {
			t.Fatalf("accepted unbounded or ambiguous request: %q", body)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"payload":null}`))
	var input invocationInput
	if err := (&strictBody{request: request, limit: 32}).Decode(&input); err != nil {
		t.Fatal(err)
	}
}

func TestFunctionFailureRedactsUnknownAndProjectsSafeDiagnostic(t *testing.T) {
	for _, test := range []struct {
		name    string
		err     error
		status  int
		present string
		absent  string
	}{
		{"unknown", errors.New("SDK response contains secret-value-marker"), http.StatusInternalServerError, "HD0500", "secret-value-marker"},
		{"typed", &lambda.Diagnostic{Code: "HD0414", Message: "ownership changed"}, http.StatusConflict, "ownership changed", "secret-value-marker"},
		{"timeout", context.DeadlineExceeded, http.StatusGatewayTimeout, "execution may continue", "secret-value-marker"},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := httptest.NewRecorder()
			(functionError{}).Write(writer, test.err)
			if writer.Code != test.status || !strings.Contains(writer.Body.String(), test.present) || strings.Contains(writer.Body.String(), test.absent) {
				t.Fatalf("unsafe or wrong failure: %d %s", writer.Code, writer.Body.String())
			}
		})
	}
}

func TestFiniteFunctionLogsCloseOnReadBoundAndPriorFailures(t *testing.T) {
	for _, test := range []struct {
		name      string
		reader    *closingLogFixture
		prior     error
		limit     int
		wantError bool
	}{
		{"finite", &closingLogFixture{Reader: strings.NewReader("native-log")}, nil, 32, false},
		{"oversized", &closingLogFixture{Reader: strings.NewReader("native-log")}, nil, 4, true},
		{"prior", &closingLogFixture{Reader: strings.NewReader("must-not-read")}, errors.New("SDK failure"), 32, true},
		{"close", &closingLogFixture{Reader: strings.NewReader("native-log"), closeErr: errors.New("close failure")}, nil, 32, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := (&boundedLog{Reader: test.reader, Limit: test.limit}).Read(test.prior)
			if (err != nil) != test.wantError || !test.reader.closed {
				t.Fatalf("wrong finite read/close result: %s %v", data, err)
			}
			if test.prior != nil && len(data) != 0 {
				t.Fatal("read after native failure")
			}
		})
	}
	if _, err := (&boundedLog{Limit: 32}).Read(nil); err == nil {
		t.Fatal("accepted nil native reader")
	}
}

type closingLogFixture struct {
	io.Reader
	closed   bool
	closeErr error
}

func (r *closingLogFixture) Close() error { r.closed = true; return r.closeErr }

func TestRegisteredProviderFailureKeepsSafeDiagnostics(t *testing.T) {
	writer := httptest.NewRecorder()
	(&Server{}).fail(writer, errors.New("SDK secret-value-marker"))
	if writer.Code != http.StatusInternalServerError || strings.Contains(writer.Body.String(), "secret-value-marker") {
		t.Fatal("legacy handler leaked native error")
	}
	writer = httptest.NewRecorder()
	(&Server{}).fail(writer, &lambda.Diagnostic{Code: "HD0414", Message: "ownership changed"})
	if writer.Code != http.StatusConflict || !strings.Contains(writer.Body.String(), "ownership changed") {
		t.Fatal("lost safe native diagnostic")
	}
}

func TestInvocationContractDescribesTransientBoundedInput(t *testing.T) {
	operation := map[string]any{}
	(invocationContract{}).Apply(operation, Route{Method: http.MethodPost, Pattern: (invocationContract{}).Pattern()})
	data, err := json.Marshal(operation)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"262144", "additionalProperties", "function_error", "504", "do not retry automatically"} {
		if !strings.Contains(string(data), value) {
			t.Fatalf("contract omitted %s", value)
		}
	}
	unrelated := map[string]any{}
	(invocationContract{}).Apply(unrelated, Route{Method: http.MethodPost, Pattern: "/api/v1/projects/{project}/apps/{app}/run"})
	if len(unrelated) != 0 {
		t.Fatal("function body changed job contract")
	}
}

func TestFunctionBodyDeadlineBoundsIngressOnRealHTTPConnection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input invocationInput
		err := (&strictBody{writer: w, request: r, limit: 32, until: time.Now().Add(100 * time.Millisecond)}).Decode(&input)
		if err == nil {
			t.Error("accepted body without EOF")
		}
		(functionError{}).Write(w, err)
	}))
	defer server.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	written := make(chan struct{})
	go func() { _, _ = writer.Write([]byte(`{"payload":{}}`)); close(written) }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, reader)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "read deadline ended") {
		t.Fatalf("unbounded body ingress: %d %s", response.StatusCode, data)
	}
	<-written
}
