package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"

	"github.com/d31ma/heimdall/internal/store"
)

// The webhook receiver turns a push into a nudge, and nothing more.
//
// It does not parse the forge's payload. Which branch moved, which files
// changed, and what the commit says are all things `Status` finds out for
// itself by fetching — and a receiver that believed the payload would be
// trusting a body an attacker chose the shape of. So the whole endpoint means
// "look now instead of at the next tick".
//
// That also makes it forge-agnostic: GitHub, GitLab, Gitea, and a curl from a
// CI job all work, because none of their differences are read.
//
// It is outside the SESAME boundary because a forge holds no session and never
// will. What replaces authorization is an HMAC over the body with a secret
// only this repository and that forge share, and the receiver can do nothing
// except cause a sync the repository's own policy already permitted.
func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	if s.Auto == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"code": "HD0503", "message": "this control plane does not accept webhooks",
		})
		return
	}

	repo, err := store.In[store.Repository](s.Store, store.Repos).Get(r.PathValue("repo"))
	if err != nil {
		// Same answer as a bad signature: distinguishing them tells an
		// unauthenticated caller which repository ids exist.
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"code": "HD0401", "message": "the webhook signature is not valid",
		})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"code": "HD0413", "message": "the webhook body is too large",
		})
		return
	}

	if !s.webhookSignatureValid(r, repo, body) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"code": "HD0401", "message": "the webhook signature is not valid",
		})
		return
	}

	// Answer before working. A forge times out in seconds and retries on a
	// slow response, and a sync is not something to do twice because the
	// acknowledgement was late. The nudge is scoped to this repository's
	// applications and lands in the Auto loop's own goroutine, so a push and
	// the ticker can never run the same sync concurrently.
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
	s.Auto.Nudge(repo.ID)
}

// webhookSignatureValid checks the HMAC a forge sends over the raw body.
//
// GitHub sends X-Hub-Signature-256 as "sha256=<hex>", GitLab sends
// X-Gitlab-Token as a bare shared secret, and Gitea sends X-Gitea-Signature as
// bare hex. All three are accepted because rejecting two of them would only
// mean an operator wires up a shell script instead.
func (s *Server) webhookSignatureValid(r *http.Request, repo store.Repository, body []byte) bool {
	if repo.WebhookSecretRef == "" || s.Secrets == nil {
		// No secret configured means no way to tell a push from a stranger,
		// and an unauthenticated sync trigger is not a feature.
		return false
	}
	secret, err := s.Secrets(r.Context(), repo.WebhookSecretRef)
	if err != nil || secret == "" {
		return false
	}

	// A plain shared token, compared in constant time.
	if token := r.Header.Get("X-Gitlab-Token"); token != "" {
		return hmac.Equal([]byte(token), []byte(secret))
	}

	signature := r.Header.Get("X-Hub-Signature-256")
	if signature == "" {
		signature = r.Header.Get("X-Gitea-Signature")
	}
	if signature == "" {
		return false
	}
	sent, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(sent, mac.Sum(nil))
}
