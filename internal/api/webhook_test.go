package api_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d31ma/heimdall/internal/api"
	"github.com/d31ma/heimdall/internal/reconcile"
	"github.com/d31ma/heimdall/internal/store"
)

const webhookSecret = "a-shared-secret-only-the-forge-knows"

func signature(body string) string {
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// post sends one webhook and returns the status.
func postWebhook(t *testing.T, handler http.Handler, repo, body string, headers map[string]string) int {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/"+repo, strings.NewReader(body))
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code
}

// TestWebhookRequiresAValidSignature is the whole security model of an
// endpoint that has no session: an unsigned, wrongly signed, or unknown-repo
// push must not be able to trigger a deployment.
func TestWebhookRequiresAValidSignature(t *testing.T) {
	world := newWebhookWorld(t)
	handler := world.server.Handler()
	const body = `{"ref":"refs/heads/main"}`

	cases := []struct {
		name    string
		repo    string
		body    string
		headers map[string]string
		want    int
	}{
		{"a correctly signed push", world.repoID, body,
			map[string]string{"X-Hub-Signature-256": signature(body)}, http.StatusAccepted},
		{"a GitLab shared token", world.repoID, body,
			map[string]string{"X-Gitlab-Token": webhookSecret}, http.StatusAccepted},
		{"no signature at all", world.repoID, body, nil, http.StatusUnauthorized},
		{"a signature over a different body", world.repoID, body,
			map[string]string{"X-Hub-Signature-256": signature("something else")}, http.StatusUnauthorized},
		{"the wrong secret", world.repoID, body,
			map[string]string{"X-Gitlab-Token": "guessed"}, http.StatusUnauthorized},
		{"a repository that does not exist", "4VXTUNKNOWN00", body,
			map[string]string{"X-Hub-Signature-256": signature(body)}, http.StatusUnauthorized},
		{"a repository with no webhook secret configured", world.unsignedRepoID, body,
			map[string]string{"X-Hub-Signature-256": signature(body)}, http.StatusUnauthorized},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := postWebhook(t, handler, testCase.repo, testCase.body, testCase.headers); got != testCase.want {
				t.Fatalf("status = %d, want %d", got, testCase.want)
			}
		})
	}
}

// TestWebhookDoesNotDiscloseWhichRepositoriesExist: an unknown repository and
// a bad signature must be indistinguishable, or the endpoint enumerates ids
// for anyone who asks.
func TestWebhookDoesNotDiscloseWhichRepositoriesExist(t *testing.T) {
	world := newWebhookWorld(t)
	handler := world.server.Handler()

	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/4VXTUNKNOWN00", strings.NewReader("{}")))
	known := httptest.NewRecorder()
	handler.ServeHTTP(known, httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/"+world.repoID, strings.NewReader("{}")))

	if unknown.Code != known.Code || unknown.Body.String() != known.Body.String() {
		t.Fatalf("an unknown repository answers %d %q and a known one %d %q",
			unknown.Code, unknown.Body.String(), known.Code, known.Body.String())
	}
}

// TestWebhookIgnoresThePayload: the receiver is a nudge, so a body it cannot
// parse — or one an attacker shaped — must change nothing about the outcome.
func TestWebhookIgnoresThePayload(t *testing.T) {
	world := newWebhookWorld(t)
	handler := world.server.Handler()

	for _, body := range []string{"", "not json at all", `{"ref":"refs/heads/../../etc/passwd"}`} {
		if got := postWebhook(t, handler, world.repoID, body,
			map[string]string{"X-Hub-Signature-256": signature(body)}); got != http.StatusAccepted {
			t.Errorf("a signed body %q was refused with %d", body, got)
		}
	}
}

// TestWebhookIsRefusedWhenNoSecretResolverExists proves the fail-closed path:
// a control plane that cannot resolve a secret must refuse rather than skip
// the check.
func TestWebhookIsRefusedWhenNoSecretResolverExists(t *testing.T) {
	world := newWebhookWorld(t)
	world.server.Secrets = nil
	const body = "{}"

	if got := postWebhook(t, world.server.Handler(), world.repoID, body,
		map[string]string{"X-Hub-Signature-256": signature(body)}); got != http.StatusUnauthorized {
		t.Fatalf("status = %d with no secret resolver, want 401", got)
	}
}

// newWebhookWorld is deliberately lighter than newHarness: the receiver is
// outside the SESAME boundary, so a test for it needs no engine, no tenant,
// and no session — only a store and the loop it nudges.
type webhookWorld struct {
	server         *api.Server
	repoID         string
	unsignedRepoID string
}

func newWebhookWorld(t *testing.T) *webhookWorld {
	t.Helper()
	if _, err := exec.LookPath("fylo"); err != nil {
		t.Skipf("fylo binary not on PATH: %v", err)
	}

	storage, err := store.Open(filepath.Join(t.TempDir(), "fylo-root"), os.Getenv("FYLO_BINARY"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	repos := store.In[store.Repository](storage, store.Repos)
	signed, err := repos.Put(store.Repository{
		Project: "alpha", Name: "site", URL: "/tmp/repo",
		WebhookSecretRef: "vault/forge#webhook",
	})
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	unsigned, err := repos.Put(store.Repository{Project: "alpha", Name: "other", URL: "/tmp/other"})
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	return &webhookWorld{
		repoID:         signed,
		unsignedRepoID: unsigned,
		server: &api.Server{
			Version: "test",
			Store:   storage,
			// No applications exist, so a nudge is a no-op — this test is
			// about who may nudge, not what a nudge does.
			Auto: &reconcile.Auto{
				Engine: &reconcile.Engine{Store: storage},
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			},
			Secrets: func(_ context.Context, ref string) (string, error) {
				if ref == "vault/forge#webhook" {
					return webhookSecret, nil
				}
				return "", errors.New("no such secret")
			},
		},
	}
}
