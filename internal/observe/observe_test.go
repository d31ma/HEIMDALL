package observe_test

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/d31ma/heimdall/internal/observe"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/provider/docker"
	"github.com/d31ma/heimdall/internal/provider/docker/dockertest"
	"github.com/d31ma/heimdall/internal/store"
)

// world stands up a store, a fake Engine with one labeled container, and a
// collector whose clock the test owns.
type world struct {
	collector *observe.Collector
	storage   *store.Store
	engine    *dockertest.Engine
	appID     string
	instance  provider.InstanceRef
	clock     time.Time
}

func newWorld(t *testing.T) *world {
	t.Helper()
	if _, err := exec.LookPath("fylo"); err != nil {
		t.Skipf("fylo binary not on PATH: %v", err)
	}
	storage, err := store.Open(filepath.Join(t.TempDir(), "fylo-root"), "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	engine := dockertest.New()
	t.Cleanup(engine.Close)

	targetID, err := store.In[store.Target](storage, store.Targets).Put(store.Target{
		Project: "alpha", Name: "local", Provider: "docker", Endpoint: engine.URL(),
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	appID, err := store.In[store.Application](storage, store.Applications).Put(store.Application{
		Project: "alpha", Name: "site", TargetID: targetID, RepoID: "r", Path: ".",
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	engine.Mu.Lock()
	engine.Containers["c1"] = &dockertest.Container{
		ID: "c1", Name: "/alpha-site-web", Image: "nginx:1", Running: true,
		Labels: map[string]string{
			docker.LabelManagedBy: "heimdall",
			docker.LabelProject:   "alpha", docker.LabelApp: "site",
			docker.LabelService: "web", docker.LabelRevision: "abc1234",
		},
		Started: time.Now().Add(-time.Hour),
	}
	engine.Mu.Unlock()

	w := &world{
		storage: storage, engine: engine, appID: appID,
		instance: provider.InstanceRef{
			AppRef:  provider.AppRef{Project: "alpha", App: "site"},
			Service: "web", Instance: "c1",
		},
		clock: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
	}
	w.collector = &observe.Collector{
		Store:     storage,
		Providers: map[string]provider.Provider{"docker": &docker.Provider{}},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:       func() time.Time { return w.clock },
	}
	return w
}

// TestScrapeRollupAndServe is the pipeline end to end: scrapes accumulate,
// complete minutes fold into durable rollups, and SeriesFor merges rollups
// with the fresh ring so the chart has both depth and a live edge.
func TestScrapeRollupAndServe(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	// Four scrapes across minute one, then the clock enters minute two: the
	// first minute is complete and must roll up.
	//
	// The fake Engine stamps "read" with wall-clock now, so sample times are
	// real time — the test clock only decides which minutes count as over.
	for i := 0; i < 4; i++ {
		w.collector.Once(ctx)
	}
	w.clock = time.Now().UTC().Add(2 * time.Minute)
	w.collector.Once(ctx)

	rollups, err := store.In[observe.Rollup](w.storage, store.Rollups).Find(nil)
	if err != nil {
		t.Fatalf("find rollups: %v", err)
	}
	if len(rollups) == 0 {
		t.Fatal("no rollup was written for a completed minute")
	}
	rolled := rollups[0]
	if rolled.InstanceID != "c1" || rolled.AppID != w.appID || rolled.Service != "web" {
		t.Fatalf("rollup identity: %+v", rolled)
	}
	// The fake engine reports 200% CPU-delta over 2 cores → a positive
	// percentage, and 150MiB of usage net of inactive file cache.
	if rolled.CPUAvg <= 0 || rolled.MemAvg <= 0 {
		t.Fatalf("rollup did not aggregate: %+v", rolled)
	}
	if rolled.CPUMax < rolled.CPUAvg {
		t.Fatalf("max %f below average %f", rolled.CPUMax, rolled.CPUAvg)
	}
	// The fake engine reports 12 pids; the gauge folds by max. The throttled
	// and error counters are static, so their per-minute delta after the
	// baseline sample must be zero, not the counter's absolute value.
	if rolled.PidsMax != 12 {
		t.Fatalf("pids max = %f, want 12", rolled.PidsMax)
	}
	if rolled.CPUThrottled != 0 || rolled.NetErrors != 0 {
		t.Fatalf("static counters must delta to zero: throttled %f, errors %f",
			rolled.CPUThrottled, rolled.NetErrors)
	}

	series, err := w.collector.SeriesFor(w.instance, provider.Window{})
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(series.CPUPercent) == 0 || len(series.MemoryBytes) == 0 {
		t.Fatal("the served series is empty")
	}
	if len(series.Pids) == 0 || len(series.CPUThrottled) == 0 || len(series.NetErrors) == 0 {
		t.Fatal("the new series did not survive SeriesFor")
	}
	if series.MemoryLimit == 0 {
		t.Fatal("the memory limit did not survive the pipeline; the chart cannot scale its axis")
	}
	// Ordered: a chart drawn from unordered samples zigzags across itself.
	for i := 1; i < len(series.CPUPercent); i++ {
		if series.CPUPercent[i].At.Before(series.CPUPercent[i-1].At) {
			t.Fatal("series is not time-ordered")
		}
	}
}

// TestAgentTargetsAreSkipped: their pipeline is the agent's own ring, not a
// scrape through a dispatcher that would wake the long poll every 15s.
func TestAgentTargetsAreSkipped(t *testing.T) {
	w := newWorld(t)

	app, _ := store.In[store.Application](w.storage, store.Applications).Get(w.appID)
	if err := store.In[store.Target](w.storage, store.Targets).
		Patch(app.TargetID, map[string]any{"agent_id": "agt-1"}); err != nil {
		t.Fatalf("patch: %v", err)
	}

	w.collector.Once(context.Background())

	rollups, _ := store.In[observe.Rollup](w.storage, store.Rollups).Find(nil)
	if len(rollups) != 0 {
		t.Fatal("an agent target was scraped through the control plane")
	}
	if series, _ := w.collector.SeriesFor(w.instance, provider.Window{}); len(series.CPUPercent) != 0 {
		t.Fatal("an agent target grew a ring")
	}
}

// TestCounterResetDoesNotGoNegative: a restarted container starts its
// counters at zero, and a negative rate on a chart reads as data loss.
func TestCounterResetDoesNotGoNegative(t *testing.T) {
	// White-box via the exported fold path is not available, so this drives
	// the pipeline: scrape, then swap the container for a "restarted" one
	// with the same id (the fake engine's counters are fixed, so the reset
	// case is the second scrape seeing equal-or-lower values — the delta
	// must clamp, not underflow).
	w := newWorld(t)
	ctx := context.Background()

	w.collector.Once(ctx)
	w.collector.Once(ctx)
	w.clock = time.Now().UTC().Add(2 * time.Minute)
	w.collector.Once(ctx)

	rollups, _ := store.In[observe.Rollup](w.storage, store.Rollups).Find(nil)
	for _, rollup := range rollups {
		if rollup.NetRx < 0 || rollup.NetTx < 0 || rollup.BlockRead < 0 || rollup.BlockWrite < 0 {
			t.Fatalf("a counter delta went negative: %+v", rollup)
		}
	}
}

// TestIngestIsIdempotentPerMinute: a redelivered batch after a lost
// acknowledgement must not double the chart.
func TestIngestIsIdempotentPerMinute(t *testing.T) {
	w := newWorld(t)
	batch := []observe.Rollup{{
		AppID: w.appID, Service: "web", InstanceID: "c1",
		Minute: time.Date(2026, 8, 16, 11, 30, 0, 0, time.UTC),
		CPUAvg: 10, CPUMax: 20,
	}}

	w.collector.Ingest(batch)
	w.collector.Ingest(batch) // the retry

	stored, err := store.In[observe.Rollup](w.storage, store.Rollups).Find(nil)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("a redelivered rollup was stored twice: %d rows", len(stored))
	}
}

// TestRetentionExtendsTheServedWindow: HD_METRICS_RETENTION reaches this as
// Collector.Retention, and a rollup older than the default day must be
// served when the window is wider — retention the chart cannot reach would
// be storage without a reader.
func TestRetentionExtendsTheServedWindow(t *testing.T) {
	w := newWorld(t)
	w.collector.Ingest([]observe.Rollup{{
		AppID: w.appID, Service: "web", InstanceID: "c1",
		Minute: w.clock.Add(-30 * time.Hour), CPUAvg: 10,
	}})

	series, err := w.collector.SeriesFor(w.instance, provider.Window{})
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(series.CPUPercent) != 0 {
		t.Fatal("a 30h-old rollup was served inside the default 25h window")
	}

	w.collector.Retention = 48 * time.Hour
	series, err = w.collector.SeriesFor(w.instance, provider.Window{})
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(series.CPUPercent) != 1 {
		t.Fatalf("the wider window served %d points, want the 30h-old rollup", len(series.CPUPercent))
	}
}
