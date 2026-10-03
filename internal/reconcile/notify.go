package reconcile

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/d31ma/heimdall/internal/store"
)

// Outbound webhooks: HEIMDALL tells other systems a sync finished. One POST
// per subscriber per completed operation, HMAC-signed the same way inbound
// webhooks are verified, fire-and-forget with a bounded timeout — a slow
// subscriber must never slow a deploy.

// OutboundWebhook is one subscriber, in hd-outbound-webhooks.
type OutboundWebhook struct {
	ID      string `json:"id,omitempty"`
	Project string `json:"project"`
	URL     string `json:"url"`
	// SecretRef names the HMAC key — a reference like every credential.
	SecretRef string `json:"secret_ref,omitempty"`
	// Events filters: empty means every completed operation.
	Events []string `json:"events,omitempty"`
}

// Notify posts a completed operation to every matching subscriber. Called at
// the end of a sync; errors are logged by omission — the operation document
// is the record, and a webhook is a courtesy.
func (e *Engine) Notify(operation store.Operation) {
	if e.Store == nil || e.Secrets == nil {
		return
	}
	subscribers, err := store.In[OutboundWebhook](e.Store, store.OutboundWebhooks).
		Find(map[string]any{"project": operation.Project})
	if err != nil || len(subscribers) == 0 {
		return
	}

	kind := "sync"
	switch {
	case operation.DryRun:
		kind = "dry_run"
	case operation.Rollback:
		kind = "rollback"
	}
	event := kind + "." + string(operation.Phase)

	body, err := json.Marshal(map[string]any{
		"event":     event,
		"operation": operation,
		"sent_at":   time.Now().UTC(),
	})
	if err != nil {
		return
	}

	for _, subscriber := range subscribers {
		if !subscriber.matches(event) {
			continue
		}
		go e.deliver(subscriber, body)
	}
}

func (w OutboundWebhook) matches(event string) bool {
	if len(w.Events) == 0 {
		return true
	}
	for _, wanted := range w.Events {
		if wanted == event {
			return true
		}
	}
	return false
}

func (e *Engine) deliver(subscriber OutboundWebhook, body []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, subscriber.URL, bytes.NewReader(body))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "heimdall-webhook")

	if subscriber.SecretRef != "" {
		secret, err := e.Secrets(ctx, subscriber.SecretRef)
		if err == nil && secret != "" {
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write(body)
			// The same header shape GitHub uses, so every receiver library
			// already knows how to verify it.
			request.Header.Set("X-Heimdall-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		}
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return
	}
	_ = response.Body.Close()
}
