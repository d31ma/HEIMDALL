// Package observe is the metrics pipeline for Docker Engine targets: scrape →
// ring buffer → minute rollups. It is deliberately not a time-series
// database — that is a non-negotiable — it is one bounded ring per instance
// in memory and one small rollup document per instance-minute in FYLO, with
// everything older than a day deleted.
//
// Provider-native metrics (CloudWatch and friends) arrive with the cloud
// adapters in Phase 4 and bypass this entirely: the adapter's Metrics will
// answer from the cloud's own store, which is the "provider-native first"
// rule doing its job.
package observe

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/store"
)

// scrapeInterval is how often every running instance is sampled. Fifteen
// seconds matches what `docker stats` refreshes at and keeps a day of
// rollups small.
const scrapeInterval = 15 * time.Second

// ringSize bounds the in-memory raw samples per instance: an hour at the
// scrape interval. Older than that, the minute rollups answer.
const ringSize = 240

// defaultRetention is how long rollups live unless HD_METRICS_RETENTION says
// otherwise. The exit criterion is 24 hours of metrics; a little slack keeps
// the edge of the window populated.
const defaultRetention = 25 * time.Hour

// MaxRetention bounds the knob at two weeks. Past that, the honest tool is a
// real time-series database pointed at the workloads — which this pipeline is
// deliberately not.
const MaxRetention = 14 * 24 * time.Hour

// MinRetention is the floor: the product promises a day of metrics.
const MinRetention = 24 * time.Hour

// point is one raw scrape.
type point struct {
	at         time.Time
	cpuPercent float64
	memBytes   float64
	memLimit   uint64
	netRx      float64
	netTx      float64
	blockRead  float64
	blockWrite float64
	pids       float64
	throttled  float64
	netErrors  float64
}

// ring is a fixed-size buffer of the newest raw samples for one instance.
type ring struct {
	points [ringSize]point
	next   int
	filled bool
}

func (r *ring) push(p point) {
	r.points[r.next] = p
	r.next = (r.next + 1) % ringSize
	if r.next == 0 {
		r.filled = true
	}
}

// ordered returns samples oldest-first.
func (r *ring) ordered() []point {
	if !r.filled {
		return append([]point(nil), r.points[:r.next]...)
	}
	out := make([]point, 0, ringSize)
	out = append(out, r.points[r.next:]...)
	out = append(out, r.points[:r.next]...)
	return out
}

// Rollup is one instance-minute, the durable shape. Average and max both
// matter: the average says what a minute cost, the max says whether it
// spiked — and a chart drawn from averages alone hides exactly the spike an
// operator is hunting.
type Rollup struct {
	ID         string    `json:"id,omitempty"`
	AppID      string    `json:"app_id"`
	Service    string    `json:"service"`
	InstanceID string    `json:"instance_id"`
	Minute     time.Time `json:"minute"`
	CPUAvg     float64   `json:"cpu_avg"`
	CPUMax     float64   `json:"cpu_max"`
	MemAvg     float64   `json:"mem_avg"`
	MemMax     float64   `json:"mem_max"`
	MemLimit   uint64    `json:"mem_limit,omitempty"`
	// Network and block are per-minute deltas, not counters: the page draws
	// rates, and asking it to differentiate counters would put arithmetic in
	// the least-tested tier.
	NetRx      float64 `json:"net_rx"`
	NetTx      float64 `json:"net_tx"`
	BlockRead  float64 `json:"block_read"`
	BlockWrite float64 `json:"block_write"`
	// PidsMax is a gauge folded by max — a leak shows in the peak, and an
	// average would smooth away exactly the growth being hunted. Throttled
	// and NetErrors are per-minute deltas like the counters above.
	PidsMax      float64 `json:"pids_max,omitempty"`
	CPUThrottled float64 `json:"cpu_throttled,omitempty"`
	NetErrors    float64 `json:"net_errors,omitempty"`
}

// Accumulator is the ring-and-fold half of the pipeline, free of any store:
// push samples in, get completed minute rollups out. The control plane's
// collector and the agent both use it, so the two pipelines cannot drift.
type Accumulator struct {
	mu    sync.Mutex
	rings map[string]*ring // by instance id
	// lastCounter remembers each instance's previous cumulative network and
	// block counters so a scrape can store deltas.
	lastCounter map[string]point
	// lastMinute is the last minute already rolled up per instance.
	lastMinute map[string]time.Time

	// Now is injectable so a test can force minutes to complete.
	Now func() time.Time
}

func (a *Accumulator) clock() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now().UTC()
}

// Push records one scrape and returns any minute rollups it completed.
func (a *Accumulator) Push(appID string, instance provider.Instance, series provider.Series) []Rollup {
	if len(series.CPUPercent) == 0 {
		return nil
	}
	sample := point{
		at:         series.CPUPercent[0].At,
		cpuPercent: series.CPUPercent[0].Value,
		memLimit:   series.MemoryLimit,
	}
	if len(series.MemoryBytes) > 0 {
		sample.memBytes = series.MemoryBytes[0].Value
	}
	if len(series.NetRxBytes) > 0 {
		sample.netRx = series.NetRxBytes[0].Value
	}
	if len(series.NetTxBytes) > 0 {
		sample.netTx = series.NetTxBytes[0].Value
	}
	if len(series.BlockRead) > 0 {
		sample.blockRead = series.BlockRead[0].Value
	}
	if len(series.BlockWrite) > 0 {
		sample.blockWrite = series.BlockWrite[0].Value
	}
	if len(series.Pids) > 0 {
		sample.pids = series.Pids[0].Value
	}
	if len(series.CPUThrottled) > 0 {
		sample.throttled = series.CPUThrottled[0].Value
	}
	if len(series.NetErrors) > 0 {
		sample.netErrors = series.NetErrors[0].Value
	}

	a.mu.Lock()
	if a.rings == nil {
		a.rings = map[string]*ring{}
		a.lastCounter = map[string]point{}
		a.lastMinute = map[string]time.Time{}
	}
	buffer := a.rings[instance.Ref.Instance]
	if buffer == nil {
		buffer = &ring{}
		a.rings[instance.Ref.Instance] = buffer
	}
	buffer.push(sample)
	points := buffer.ordered()
	last := a.lastMinute[instance.Ref.Instance]
	a.mu.Unlock()

	return a.completeMinutes(appID, instance, points, last)
}

// Fresh returns the raw ring for an instance, newest samples last, from a
// given time onward.
func (a *Accumulator) Fresh(instanceID string, from time.Time) []point {
	a.mu.Lock()
	defer a.mu.Unlock()
	buffer := a.rings[instanceID]
	if buffer == nil {
		return nil
	}
	var out []point
	for _, p := range buffer.ordered() {
		if p.at.Before(from) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// Collector owns the control plane's scrape loop over directly reachable
// targets, and the durable rollup writes for both pipelines.
type Collector struct {
	Store     *store.Store
	Providers map[string]provider.Provider
	Logger    *slog.Logger
	Interval  time.Duration
	// Retention is how long rollups live and how far back a served series
	// reaches. Zero means the default day; serve validates the bounds.
	Retention time.Duration

	Accumulator Accumulator

	// Now is injectable so a test can force minutes to complete.
	Now func() time.Time
}

func (c *Collector) interval() time.Duration {
	if c.Interval > 0 {
		return c.Interval
	}
	return scrapeInterval
}

func (c *Collector) retention() time.Duration {
	if c.Retention > 0 {
		return c.Retention
	}
	return defaultRetention
}

func (c *Collector) clock() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now().UTC()
}

// accumulator keeps the injected clock in sync lazily.
func (c *Collector) accumulator() *Accumulator {
	if c.Accumulator.Now == nil && c.Now != nil {
		c.Accumulator.Now = c.Now
	}
	return &c.Accumulator
}

// Run scrapes until the context ends.
func (c *Collector) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval())
	defer ticker.Stop()
	sweep := time.NewTicker(time.Hour)
	defer sweep.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Once(ctx)
		case <-sweep.C:
			c.prune()
		}
	}
}

// Once runs one scrape pass over every application on a directly reachable
// Docker target.
//
// ponytail: agent targets are skipped. Their pipeline is the agent scraping
// its own engine into a local ring and shipping rollups on its poll — add it
// when a fleet needs history for hosts the control plane cannot dial; the
// instantaneous adapter path already answers for them.
func (c *Collector) Once(ctx context.Context) {
	apps, err := store.In[store.Application](c.Store, store.Applications).Find(nil)
	if err != nil {
		c.Logger.Error("metrics scrape could not list applications", "error", err)
		return
	}
	targets := store.In[store.Target](c.Store, store.Targets)

	for _, app := range apps {
		target, err := targets.Get(app.TargetID)
		if err != nil || target.AgentID != "" {
			continue
		}
		adapter, ok := c.Providers[target.Provider]
		if !ok {
			continue
		}
		instances, err := adapter.Instances(ctx, target.Ref(), app.AppRef())
		if err != nil {
			// A host being down is not an event worth a log line every 15s.
			continue
		}
		for _, instance := range instances {
			c.scrape(ctx, adapter, target, app, instance)
		}
	}
}

func (c *Collector) scrape(
	ctx context.Context,
	adapter provider.Provider,
	target store.Target,
	app store.Application,
	instance provider.Instance,
) {
	series, err := adapter.Metrics(ctx, target.Ref(), instance.Ref, provider.Window{})
	if err != nil {
		return
	}
	c.Ingest(c.accumulator().Push(app.ID, instance, series))
}

// Ingest writes completed rollups durably. The collector's own scrapes and
// the agent-shipped batches both land here.
//
// It is idempotent per instance-minute: an agent whose shipment succeeded
// but whose acknowledgement was lost will send the same batch again, and a
// duplicated minute draws as a doubled point on a chart — subtle enough that
// nobody would file it, wrong enough to mislead during an incident.
func (c *Collector) Ingest(rollups []Rollup) {
	if len(rollups) == 0 {
		return
	}
	collection := store.In[Rollup](c.Store, store.Rollups)
	for _, rollup := range rollups {
		existing, err := collection.Find(map[string]any{
			"instance_id": rollup.InstanceID,
			"minute":      rollup.Minute.Format(timeFormat),
		})
		if err == nil && len(existing) > 0 {
			continue
		}
		if _, err := collection.Put(rollup); err != nil {
			c.Logger.Error("write rollup", "instance", rollup.InstanceID, "error", err)
		}
	}
}

// timeFormat is how time.Time marshals into a FYLO document, so an equality
// query on a time field compares like with like.
const timeFormat = "2006-01-02T15:04:05Z07:00"

// completeMinutes folds any complete minutes newer than the last rolled one.
func (a *Accumulator) completeMinutes(
	appID string,
	instance provider.Instance,
	points []point,
	lastRolled time.Time,
) []Rollup {
	if len(points) == 0 {
		return nil
	}
	currentMinute := a.clock().Truncate(time.Minute)

	byMinute := map[time.Time][]point{}
	for _, p := range points {
		minute := p.at.Truncate(time.Minute)
		// Only minutes that are over and not yet rolled.
		if !minute.Before(currentMinute) || !minute.After(lastRolled) {
			continue
		}
		byMinute[minute] = append(byMinute[minute], p)
	}
	if len(byMinute) == 0 {
		return nil
	}

	newest := lastRolled
	out := make([]Rollup, 0, len(byMinute))
	for minute, bucket := range byMinute {
		out = append(out, fold(appID, instance, minute, bucket,
			a.previousCounters(instance.Ref.Instance, bucket)))
		if minute.After(newest) {
			newest = minute
		}
	}

	a.mu.Lock()
	a.lastMinute[instance.Ref.Instance] = newest
	a.mu.Unlock()
	return out
}

// previousCounters returns the counter baseline for delta computation: the
// stored last counters, or the first sample of the bucket for a new instance.
func (a *Accumulator) previousCounters(instanceID string, bucket []point) point {
	a.mu.Lock()
	defer a.mu.Unlock()
	previous, ok := a.lastCounter[instanceID]
	if !ok {
		previous = bucket[0]
	}
	a.lastCounter[instanceID] = bucket[len(bucket)-1]
	return previous
}

// fold reduces one minute's samples to a rollup.
func fold(appID string, instance provider.Instance, minute time.Time, bucket []point, baseline point) Rollup {
	rollup := Rollup{
		AppID: appID, Service: instance.Ref.Service, InstanceID: instance.Ref.Instance,
		Minute: minute,
	}
	for _, p := range bucket {
		rollup.CPUAvg += p.cpuPercent
		rollup.MemAvg += p.memBytes
		if p.cpuPercent > rollup.CPUMax {
			rollup.CPUMax = p.cpuPercent
		}
		if p.memBytes > rollup.MemMax {
			rollup.MemMax = p.memBytes
		}
		if p.memLimit > 0 {
			rollup.MemLimit = p.memLimit
		}
		if p.pids > rollup.PidsMax {
			rollup.PidsMax = p.pids
		}
	}
	count := float64(len(bucket))
	rollup.CPUAvg /= count
	rollup.MemAvg /= count

	newest := bucket[len(bucket)-1]
	rollup.NetRx = counterDelta(newest.netRx, baseline.netRx)
	rollup.NetTx = counterDelta(newest.netTx, baseline.netTx)
	rollup.BlockRead = counterDelta(newest.blockRead, baseline.blockRead)
	rollup.BlockWrite = counterDelta(newest.blockWrite, baseline.blockWrite)
	rollup.CPUThrottled = counterDelta(newest.throttled, baseline.throttled)
	rollup.NetErrors = counterDelta(newest.netErrors, baseline.netErrors)
	return rollup
}

// counterDelta handles a counter reset: a restarted container starts its
// counters at zero, and a negative delta drawn on a chart reads as data loss.
func counterDelta(now, before float64) float64 {
	if now < before {
		return now
	}
	return now - before
}

// prune deletes rollups older than the retention window. Rollups are a
// projection: losing one loses a chart's history, never truth.
func (c *Collector) prune() {
	rollups := store.In[Rollup](c.Store, store.Rollups)
	all, err := rollups.Find(nil)
	if err != nil {
		return
	}
	cutoff := c.clock().Add(-c.retention())
	for _, rollup := range all {
		if rollup.Minute.Before(cutoff) {
			_ = rollups.Delete(rollup.ID)
		}
	}
}

// SeriesFor answers a metrics request: rollup minutes for depth, the raw
// ring for the freshest hour. Average carries the line; max rides along so
// the chart can show spikes.
func (c *Collector) SeriesFor(instanceRef provider.InstanceRef, window provider.Window) (provider.Series, error) {
	series := provider.Series{Ref: instanceRef}
	from := window.From
	if from.IsZero() {
		// The default window is the retention window: keeping rollups the
		// chart never reaches would be storage without a reader.
		from = c.clock().Add(-c.retention())
	}

	rollups, err := store.In[Rollup](c.Store, store.Rollups).
		Find(store.Equals("instance_id", instanceRef.Instance))
	if err != nil {
		return series, err
	}
	for _, rollup := range rollups {
		if rollup.Minute.Before(from) {
			continue
		}
		series.CPUPercent = append(series.CPUPercent, provider.Sample{At: rollup.Minute, Value: rollup.CPUAvg})
		series.MemoryBytes = append(series.MemoryBytes, provider.Sample{At: rollup.Minute, Value: rollup.MemAvg})
		series.NetRxBytes = append(series.NetRxBytes, provider.Sample{At: rollup.Minute, Value: rollup.NetRx})
		series.NetTxBytes = append(series.NetTxBytes, provider.Sample{At: rollup.Minute, Value: rollup.NetTx})
		series.BlockRead = append(series.BlockRead, provider.Sample{At: rollup.Minute, Value: rollup.BlockRead})
		series.BlockWrite = append(series.BlockWrite, provider.Sample{At: rollup.Minute, Value: rollup.BlockWrite})
		series.Pids = append(series.Pids, provider.Sample{At: rollup.Minute, Value: rollup.PidsMax})
		series.CPUThrottled = append(series.CPUThrottled, provider.Sample{At: rollup.Minute, Value: rollup.CPUThrottled})
		series.NetErrors = append(series.NetErrors, provider.Sample{At: rollup.Minute, Value: rollup.NetErrors})
		if rollup.MemLimit > 0 {
			series.MemoryLimit = rollup.MemLimit
		}
	}
	sortSamples(&series)

	// The freshest points come straight from the ring — the last minute has
	// not rolled up yet, and a live chart that stops a minute ago looks
	// broken during exactly the deploy it should be narrating.
	fresh := c.accumulator().Fresh(instanceRef.Instance, from)

	lastRolled := time.Time{}
	if n := len(series.CPUPercent); n > 0 {
		lastRolled = series.CPUPercent[n-1].At.Add(time.Minute)
	}
	for _, p := range fresh {
		if p.at.Before(lastRolled) || p.at.Before(from) {
			continue
		}
		series.CPUPercent = append(series.CPUPercent, provider.Sample{At: p.at, Value: p.cpuPercent})
		series.MemoryBytes = append(series.MemoryBytes, provider.Sample{At: p.at, Value: p.memBytes})
		// Pids is the only other gauge in the ring; the counters stay
		// per-minute deltas, so their freshest point waits for the rollup.
		series.Pids = append(series.Pids, provider.Sample{At: p.at, Value: p.pids})
		if p.memLimit > 0 {
			series.MemoryLimit = p.memLimit
		}
	}
	return series, nil
}

func sortSamples(series *provider.Series) {
	byTime := func(samples []provider.Sample) {
		for i := 1; i < len(samples); i++ {
			for j := i; j > 0 && samples[j].At.Before(samples[j-1].At); j-- {
				samples[j], samples[j-1] = samples[j-1], samples[j]
			}
		}
	}
	byTime(series.CPUPercent)
	byTime(series.MemoryBytes)
	byTime(series.NetRxBytes)
	byTime(series.NetTxBytes)
	byTime(series.BlockRead)
	byTime(series.BlockWrite)
	byTime(series.Pids)
	byTime(series.CPUThrottled)
	byTime(series.NetErrors)
}
