package secrets_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/d31ma/heimdall/internal/secrets"
)

func TestLocalSealedStoreRoundTrips(t *testing.T) {
	resolver := &secrets.Resolver{Deployment: t.TempDir()}

	if err := resolver.Put("registry-password", "s3cret-value"); err != nil {
		t.Fatalf("put: %v", err)
	}
	value, err := resolver.Resolve(context.Background(), "local/registry-password")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if value != "s3cret-value" {
		t.Fatalf("value = %q", value)
	}

	// The sealed file must not contain the plaintext.
	raw, err := readSealed(resolver, t)
	if err != nil {
		t.Fatalf("read sealed: %v", err)
	}
	if strings.Contains(string(raw), "s3cret-value") {
		t.Fatal("the sealed file holds the plaintext")
	}
}

func readSealed(resolver *secrets.Resolver, t *testing.T) ([]byte, error) {
	t.Helper()
	// The path shape is part of the contract: <deployment>/secrets/<name>.sealed
	return os.ReadFile(resolver.Deployment + "/secrets/registry-password.sealed")
}

func TestLocalStoreRefusesTraversal(t *testing.T) {
	resolver := &secrets.Resolver{Deployment: t.TempDir()}
	for _, name := range []string{"../escape", "a/b", `a\b`} {
		if err := resolver.Put(name, "x"); err == nil {
			t.Fatalf("sealed a secret named %q", name)
		}
		if _, err := resolver.Resolve(context.Background(), "local/"+name); err == nil {
			t.Fatalf("resolved a secret named %q", name)
		}
	}
}

func TestUnknownSchemeIsRefusedWithDirections(t *testing.T) {
	resolver := &secrets.Resolver{Deployment: t.TempDir()}
	_, err := resolver.Resolve(context.Background(), "vault/db#password")
	if err == nil {
		t.Fatal("an unknown scheme resolved")
	}
	if !strings.Contains(err.Error(), "aws-sm") {
		t.Fatalf("the refusal does not name the schemes: %v", err)
	}
}

func TestAWSSecretsManager(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Amz-Target") != "secretsmanager.GetSecretValue" {
			http.Error(w, "wrong target", http.StatusBadRequest)
			return
		}
		var request struct {
			SecretID string `json:"SecretId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.SecretID != "prod/db" {
			http.Error(w, `{"__type":"ResourceNotFoundException"}`, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"SecretString": "aws-held-value"})
	}))
	defer fake.Close()

	resolver := &secrets.Resolver{
		Deployment:     t.TempDir(),
		AWSEndpoint:    fake.URL,
		AWSCredentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}
	value, err := resolver.Resolve(context.Background(), "aws-sm/us-east-1/prod/db")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if value != "aws-held-value" {
		t.Fatalf("value = %q", value)
	}
	// A missing secret is an error naming the reference, not an empty value.
	if _, err := resolver.Resolve(context.Background(), "aws-sm/us-east-1/absent"); err == nil {
		t.Fatal("a missing secret resolved to something")
	}
}

func TestGCPSecretManager(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "secrets/api-key/versions/latest:access") {
			http.Error(w, `{"error":{"code":404}}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"payload": map[string]string{
				"data": base64.StdEncoding.EncodeToString([]byte("gcp-held-value")),
			},
		})
	}))
	defer fake.Close()

	resolver := &secrets.Resolver{Deployment: t.TempDir(), GCPEndpoint: fake.URL, GCPNoAuth: true}
	value, err := resolver.Resolve(context.Background(), "gcp-sm/my-project/api-key")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if value != "gcp-held-value" {
		t.Fatalf("value = %q", value)
	}
}

// TestSOPSReferenceRules covers the refusals that need no sops binary: the
// scheme fails closed without an applying revision, outside the app
// directory, and on keys that could escape the extract expression.
func TestSOPSReferenceRules(t *testing.T) {
	resolver := &secrets.Resolver{Deployment: t.TempDir()}
	ctx := context.Background()

	if _, err := resolver.Resolve(ctx, "sops/secrets.yaml#key"); err == nil ||
		!strings.Contains(err.Error(), "during an apply") {
		t.Fatalf("no-source resolution = %v, want the apply-only refusal", err)
	}

	sourced := secrets.WithSource(ctx, secrets.Source{Read: func(context.Context, string) ([]byte, error) {
		return []byte("irrelevant"), nil
	}})
	if _, err := resolver.Resolve(sourced, "sops/../escape.yaml#key"); err == nil ||
		!strings.Contains(err.Error(), "relative") {
		t.Fatalf("traversal = %v, want the relative-path refusal", err)
	}
	if _, err := resolver.Resolve(sourced, `sops/secrets.yaml#ke"y`); err == nil ||
		!strings.Contains(err.Error(), "brackets") {
		t.Fatalf("hostile key = %v, want the quote refusal", err)
	}
}
