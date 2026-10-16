package lambda_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/d31ma/heimdall/app/server/services/provider/lambda"
)

func TestLambdaCredentialReferenceFailsClosedBeforeProviderRequests(t *testing.T) {
	cases := []struct {
		name            string
		raw             string
		resolverError   error
		withoutResolver bool
		valid           bool
	}{
		{name: "resolver unavailable", withoutResolver: true},
		{name: "resolution failed", resolverError: errors.New("private resolver detail")},
		{name: "invalid JSON", raw: "private malformed input"},
		{name: "missing access key", raw: `{"secret_access_key":"fixture-secret"}`},
		{name: "missing secret key", raw: `{"access_key_id":"fixture-access"}`},
		{name: "valid reference", raw: `{"access_key_id":"fixture-access","secret_access_key":"fixture-secret","session_token":"fixture-token"}`, valid: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			h, fake, adapter := harness(t)
			adapter.StaticCredentials = nil
			h.Target.CredentialRef = "vault/provider-test"
			calls := 0
			adapter.SecretResolver = func(_ context.Context, reference string) (string, error) {
				calls++
				if reference != h.Target.CredentialRef {
					t.Fatal("credential reference changed")
				}
				return test.raw, test.resolverError
			}
			if test.withoutResolver {
				adapter.SecretResolver = nil
			}
			_, err := adapter.Plan(context.Background(), h.Target, functionSpec("credential-reference"))
			fake.mu.Lock()
			requests := fake.requests
			fake.mu.Unlock()
			if test.valid {
				if err != nil || requests == 0 || calls != 1 {
					t.Fatalf("valid credential reference: requests=%d calls=%d error=%v", requests, calls, err)
				}
				return
			}
			var diagnostic *lambda.Diagnostic
			if !errors.As(err, &diagnostic) || diagnostic.Code != "HD0411" || requests != 0 {
				t.Fatalf("credential failure was not closed: requests=%d error=%v", requests, err)
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "fixture-") {
				t.Fatal("credential or resolver input leaked into diagnostic")
			}
			wantCalls := 1
			if test.withoutResolver {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("resolver called %d times, want %d", calls, wantCalls)
			}
		})
	}
}
