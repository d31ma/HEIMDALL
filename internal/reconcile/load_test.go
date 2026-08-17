package reconcile_test

// The load smoke: 500 applications across 4 targets, synced through the real
// engine against fake Docker Engines, reporting sync latency percentiles.
// Gated behind HD_LOAD=1 because it takes minutes, not milliseconds — CI runs
// correctness, an operator runs this before trusting a big fleet.
//
// Run with: HD_LOAD=1 go test ./internal/reconcile/ -run LoadSmoke -v -timeout 30m

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/d31ma/heimdall/internal/provider/docker/dockertest"
	"github.com/d31ma/heimdall/internal/reconcile"
	"github.com/d31ma/heimdall/internal/store"
)

func TestLoadSmoke(t *testing.T) {
	if os.Getenv("HD_LOAD") != "1" {
		t.Skip("set HD_LOAD=1 to run the load smoke")
	}
	world := newWorld(t)

	const targetCount = 4
	const appCount = 500

	targets := make([]string, targetCount)
	for i := range targets {
		engine := dockertest.New()
		t.Cleanup(engine.Close)
		id, err := store.In[store.Target](world.storage, store.Targets).Put(store.Target{
			Project: "alpha", Name: fmt.Sprintf("load-%d", i),
			Provider: "docker", Endpoint: engine.URL(),
		})
		if err != nil {
			t.Fatalf("target: %v", err)
		}
		targets[i] = id
	}

	template, err := store.In[store.Application](world.storage, store.Applications).Get(world.appID)
	if err != nil {
		t.Fatalf("template app: %v", err)
	}
	apps := make([]string, appCount)
	for i := range apps {
		app := template
		app.ID = ""
		app.Name = fmt.Sprintf("app-%03d", i)
		app.TargetID = targets[i%targetCount]
		id, err := store.In[store.Application](world.storage, store.Applications).Put(app)
		if err != nil {
			t.Fatalf("app %d: %v", i, err)
		}
		apps[i] = id
	}

	latencies := make([]time.Duration, 0, appCount)
	started := time.Now()
	for _, appID := range apps {
		begin := time.Now()
		if _, err := world.engine.Sync(context.Background(), reconcile.Request{AppID: appID}); err != nil {
			t.Fatalf("sync %s: %v", appID, err)
		}
		latencies = append(latencies, time.Since(begin))
	}
	total := time.Since(started)

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	percentile := func(p float64) time.Duration {
		return latencies[int(float64(len(latencies)-1)*p)]
	}
	t.Logf("synced %d apps across %d targets in %s", appCount, targetCount, total.Round(time.Millisecond))
	t.Logf("sync latency p50=%s p95=%s p99=%s max=%s",
		percentile(0.50).Round(time.Millisecond), percentile(0.95).Round(time.Millisecond),
		percentile(0.99).Round(time.Millisecond), percentile(1.0).Round(time.Millisecond))

	// The SLO the plan asks to be measured against. Generous on a laptop
	// against fakes; the number that matters is the one this logs on the
	// operator's own hardware.
	if p99 := percentile(0.99); p99 > 5*time.Second {
		t.Fatalf("p99 sync latency %s exceeds the 5s SLO", p99)
	}
}
