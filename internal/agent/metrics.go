package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/d31ma/heimdall/internal/observe"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/provider/docker"
)

// The agent's half of the metrics pipeline. The control plane cannot scrape a
// host it cannot dial, so the agent scrapes its own Engine into the same
// Accumulator the control plane uses and ships completed minute rollups over
// its mTLS connection. Raw samples never travel: sixty samples a minute per
// container over a WAN is exactly the chatter rollups exist to avoid.

// scrapeInterval matches the control plane's, so charts from both pipelines
// have the same texture.
const scrapeInterval = 15 * time.Second

// shipInterval is how often completed rollups are posted. Batching a few
// minutes costs chart freshness beyond the rollup delay it already has;
// shipping every scrape wastes round trips. One minute matches the rollup
// grain.
const shipInterval = time.Minute

// WireRollup is a rollup on its way to the control plane. It names the
// application by project and name — an agent knows its containers' labels,
// not the control plane's application ids; the ingest route resolves them.
type WireRollup struct {
	Project string `json:"project"`
	App     string `json:"app"`
	observe.Rollup
}

// runMetrics scrapes and ships until the context ends. It never fails the
// agent: observability lost is a log line, deployments lost is an outage.
func (a *Agent) runMetrics(ctx context.Context) {
	adapter := &docker.Provider{}
	target := provider.Target{ID: a.Credentials.TargetID, Provider: "docker", Endpoint: a.Endpoint}
	accumulator := &observe.Accumulator{}

	scrape := time.NewTicker(scrapeInterval)
	defer scrape.Stop()
	ship := time.NewTicker(shipInterval)
	defer ship.Stop()

	var outbox []WireRollup

	for {
		select {
		case <-ctx.Done():
			return

		case <-scrape.C:
			instances, err := adapter.ManagedInstances(ctx, target)
			if err != nil {
				continue
			}
			for _, instance := range instances {
				series, err := adapter.Metrics(ctx, target, instance.Ref, provider.Window{})
				if err != nil {
					continue
				}
				// AppID stays empty on the wire; the control plane resolves
				// it from project and app at ingest.
				for _, rollup := range accumulator.Push("", instance, series) {
					outbox = append(outbox, WireRollup{
						Project: instance.Ref.Project, App: instance.Ref.App, Rollup: rollup,
					})
				}
			}

		case <-ship.C:
			if len(outbox) == 0 {
				continue
			}
			if err := a.shipRollups(ctx, outbox); err != nil {
				a.log().Warn("rollups not shipped; will retry with the next batch", "error", err)
				// Bounded retry: keep at most an hour of rollups. Past that
				// the control plane has been away long enough that dropping
				// history beats growing without limit.
				if len(outbox) > 60*4 {
					outbox = outbox[len(outbox)-60*4:]
				}
				continue
			}
			outbox = outbox[:0]
		}
	}
}

func (a *Agent) shipRollups(ctx context.Context, rollups []WireRollup) error {
	body, err := json.Marshal(map[string]any{"rollups": rollups})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.Credentials.URL+"/api/v1/agent/rollups", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := a.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HD0394: the control plane answered %d to a rollup batch", response.StatusCode)
	}
	return nil
}
