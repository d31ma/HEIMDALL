package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/d31ma/heimdall/internal/cron"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

// AgentClock is the Docker target's scheduler: the piece of HEIMDALL that
// does own a clock, and the only one — it runs on the host beside the Engine,
// so it keeps firing while the control plane restarts, fails over, or is
// simply unreachable. The control plane never evaluates a cron expression.
//
// Registrations are persisted, because an agent restart must not silently
// stop a nightly backup until the next sync happens to run.
type AgentClock struct {
	// Provider runs the containers. It is the same adapter the agent deploys
	// with, so a run gets the same labels, secrets, and resource limits a
	// long-running service would.
	Provider *Provider
	// Target is the local Engine.
	Target provider.Target
	// StatePath is where registrations are written. Empty keeps them in
	// memory only, which is what tests want and no host does.
	StatePath string
	// Tick is how often due schedules are evaluated. Zero uses 30s: a minute
	// is cron's own resolution, and half of it keeps a run from landing a
	// minute late.
	Tick time.Duration
	// Now is injectable so tests do not race the clock they are testing.
	Now    func() time.Time
	Logger *slog.Logger
	// Registries resolves pull credentials, exactly as an apply does.
	Registries provider.RegistryResolver
	// KeepRuns bounds the execution history kept per service. Zero keeps ten:
	// enough to see a pattern, few enough that a minutely job does not fill a
	// disk with exited containers.
	KeepRuns int

	mu      sync.Mutex
	entries map[string]*scheduleEntry
	loaded  bool
}

// scheduleEntry is one registration plus the run in flight, if any.
type scheduleEntry struct {
	App     provider.AppRef `json:"app"`
	Service spec.Service    `json:"service"`
	State   Registration    `json:"state"`

	// running is the container id of the run in flight; attempts counts the
	// tries this occurrence has had, for retries.
	running   string    `json:"-"`
	startedAt time.Time `json:"-"`
	attempts  int       `json:"-"`
}

func (c *AgentClock) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now().UTC()
}

func (c *AgentClock) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

func (c *AgentClock) keepRuns() int {
	if c.KeepRuns > 0 {
		return c.KeepRuns
	}
	return 10
}

func entryKey(app provider.AppRef, service string) string {
	return app.Project + "/" + app.App + "/" + service
}

// Registrations implements Clock.
func (c *AgentClock) Registrations(_ context.Context, app provider.AppRef) (map[string]Registration, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.load(); err != nil {
		return nil, err
	}
	out := map[string]Registration{}
	for _, entry := range c.entries {
		if entry.App == app {
			out[entry.Service.Name] = entry.State
		}
	}
	return out, nil
}

// Register implements Clock. It is called by an apply, so it must be
// idempotent: re-registering an unchanged schedule keeps the run history and
// the next firing time rather than resetting them.
func (c *AgentClock) Register(
	_ context.Context, app provider.AppRef, service spec.Service, revision, specHash string,
) error {
	if service.Schedule == nil {
		return fmt.Errorf("HD0341: service %s has no schedule to register", service.Name)
	}
	location, err := time.LoadLocation(service.Schedule.Timezone)
	if err != nil {
		return fmt.Errorf("HD0341: service %s names timezone %q, which this host does not know: %w",
			service.Name, service.Schedule.Timezone, err)
	}
	serviceHash, err := hashService(service)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.load(); err != nil {
		return err
	}

	key := entryKey(app, service.Name)
	entry, existing := c.entries[key]
	if !existing {
		entry = &scheduleEntry{App: app}
		c.entries[key] = entry
	}
	entry.Service = service
	entry.State.Cron = service.Schedule.Cron
	entry.State.Timezone = service.Schedule.Timezone
	entry.State.Revision = revision
	entry.State.SpecHash = specHash
	entry.State.ServiceHash = serviceHash

	if service.Schedule.Cron == "" {
		// A manual-only job: nothing fires until someone asks for a run.
		entry.State.NextRunAt = time.Time{}
		return c.save()
	}
	expression, err := cron.Parse(service.Schedule.Cron)
	if err != nil {
		return fmt.Errorf("HD0341: service %s: %w", service.Name, err)
	}
	entry.State.NextRunAt = expression.Next(c.now(), location)
	return c.save()
}

// Unregister implements Clock. It removes the registration and the run
// history with it: a pruned job must leave nothing behind that fires, and
// nothing that looks like it still exists.
func (c *AgentClock) Unregister(ctx context.Context, app provider.AppRef, service string) error {
	c.mu.Lock()
	entry, found := c.entries[entryKey(app, service)]
	if found {
		delete(c.entries, entryKey(app, service))
	}
	saveErr := c.save()
	c.mu.Unlock()
	if !found {
		return saveErr
	}

	// Remove the exited run containers so Instances stops reporting a job
	// that no longer exists.
	engine, err := c.Provider.connect(c.Target)
	if err != nil {
		return err
	}
	runs, err := c.runsFor(ctx, engine, app, entry.Service.Name)
	if err != nil {
		return err
	}
	for _, run := range runs {
		_ = engine.stopContainer(ctx, run.ID, stopTimeout)
		if err := engine.removeContainer(ctx, run.ID); err != nil && !isNotFound(err) {
			return err
		}
	}
	return saveErr
}

// RunNow starts one execution immediately, whatever the schedule says. It is
// what `heimdall run` reaches, and the only way a manual-only job ever runs.
func (c *AgentClock) RunNow(ctx context.Context, app provider.AppRef, service string) error {
	c.mu.Lock()
	entry, found := c.entries[entryKey(app, service)]
	c.mu.Unlock()
	if !found {
		return fmt.Errorf("HD0342: %s/%s is not a scheduled service on this target", app.App, service)
	}
	return c.start(ctx, entry)
}

// Run evaluates due schedules until the context is cancelled. It never
// returns an error for a failed run: a job that fails is a recorded failure,
// not a reason to stop scheduling every other job on the host.
func (c *AgentClock) Run(ctx context.Context) {
	tick := c.Tick
	if tick <= 0 {
		tick = 30 * time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		c.evaluate(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// evaluate is one pass: settle the runs in flight, then start what is due.
func (c *AgentClock) evaluate(ctx context.Context) {
	c.mu.Lock()
	if err := c.load(); err != nil {
		c.mu.Unlock()
		c.log().Error("schedule state unreadable", "error", err)
		return
	}
	entries := make([]*scheduleEntry, 0, len(c.entries))
	for _, entry := range c.entries {
		entries = append(entries, entry)
	}
	c.mu.Unlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].Service.Name < entries[j].Service.Name })

	for _, entry := range entries {
		if err := c.settle(ctx, entry); err != nil {
			c.log().Error("reading a scheduled run failed", "service", entry.Service.Name, "error", err)
			continue
		}
		if entry.State.NextRunAt.IsZero() || c.now().Before(entry.State.NextRunAt) {
			continue
		}
		c.fire(ctx, entry)
	}
}

// settle reads the run in flight and records its outcome, killing one that
// has outlived its timeout.
func (c *AgentClock) settle(ctx context.Context, entry *scheduleEntry) error {
	if entry.running == "" {
		return nil
	}
	engine, err := c.Provider.connect(c.Target)
	if err != nil {
		return err
	}
	inspected, err := engine.inspectContainer(ctx, entry.running)
	if err != nil {
		if isNotFound(err) {
			entry.running = ""
			return nil
		}
		return err
	}

	timeout := time.Duration(entry.Service.Schedule.TimeoutMS) * time.Millisecond
	if inspected.State.Running {
		if timeout > 0 && c.now().Sub(entry.startedAt) > timeout {
			c.log().Warn("a scheduled run outlived its timeout and was killed",
				"service", entry.Service.Name, "timeout", timeout)
			_ = engine.stopContainer(ctx, entry.running, stopTimeout)
			return c.record(entry, "failed")
		}
		return nil
	}

	status := "succeeded"
	if inspected.State.ExitCode != 0 {
		status = "failed"
	}
	if status == "failed" && entry.attempts <= entry.Service.Schedule.Retries {
		c.log().Warn("a scheduled run failed; retrying",
			"service", entry.Service.Name, "exit_code", inspected.State.ExitCode,
			"attempt", entry.attempts, "retries", entry.Service.Schedule.Retries)
		entry.running = ""
		return c.start(ctx, entry)
	}
	return c.record(entry, status)
}

// record closes out one occurrence and computes the next firing time.
func (c *AgentClock) record(entry *scheduleEntry, status string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry.running = ""
	entry.attempts = 0
	entry.State.LastRunAt = c.now()
	entry.State.LastStatus = status
	return c.save()
}

// fire starts the occurrence that is due, honouring concurrency and deadline.
func (c *AgentClock) fire(ctx context.Context, entry *scheduleEntry) {
	due := entry.State.NextRunAt
	advance := func() {
		expression, err := cron.Parse(entry.State.Cron)
		if err != nil {
			return
		}
		location, err := time.LoadLocation(entry.State.Timezone)
		if err != nil {
			location = time.UTC
		}
		c.mu.Lock()
		entry.State.NextRunAt = expression.Next(c.now(), location)
		_ = c.save()
		c.mu.Unlock()
	}

	// A run that cannot start soon enough is skipped rather than started
	// late: a nightly report that lands at noon is worse than one that
	// visibly did not run.
	if deadline := time.Duration(entry.Service.Schedule.DeadlineMS) * time.Millisecond; deadline > 0 &&
		c.now().Sub(due) > deadline {
		c.log().Warn("a scheduled run missed its deadline and was skipped",
			"service", entry.Service.Name, "due", due, "deadline", deadline)
		_ = c.record(entry, "skipped")
		advance()
		return
	}

	if entry.running != "" {
		switch entry.Service.Schedule.Concurrency {
		case "allow":
			// Fall through and start another.
		case "replace":
			engine, err := c.Provider.connect(c.Target)
			if err == nil {
				_ = engine.stopContainer(ctx, entry.running, stopTimeout)
				_ = engine.removeContainer(ctx, entry.running)
			}
			entry.running = ""
		default: // forbid, the default
			c.log().Warn("a scheduled run was still in flight; this occurrence was skipped",
				"service", entry.Service.Name)
			_ = c.record(entry, "skipped")
			advance()
			return
		}
	}

	if err := c.start(ctx, entry); err != nil {
		c.log().Error("starting a scheduled run failed", "service", entry.Service.Name, "error", err)
		_ = c.record(entry, "failed")
	}
	advance()
}

// start creates and starts one run container, and prunes the history behind
// it. Each run is its own container so the execution history is the Engine's
// own record rather than a second store to keep in sync.
func (c *AgentClock) start(ctx context.Context, entry *scheduleEntry) error {
	engine, err := c.Provider.connect(c.Target)
	if err != nil {
		return err
	}
	if err := c.prune(ctx, engine, entry.App, entry.Service.Name); err != nil {
		return err
	}

	plan := provider.Plan{
		App: entry.App, Revision: entry.State.Revision, SpecHash: entry.State.SpecHash,
	}
	// A job must not be restarted by the Engine when it exits: finishing is
	// the point.
	service := entry.Service
	service.Restart = ""

	name := fmt.Sprintf("%s-run-%d", containerName(entry.App, service.Name), c.now().UnixNano())
	id, err := c.Provider.createAndStart(ctx, engine, c.Target, plan, service, name, c.Registries)
	if err != nil {
		return err
	}

	c.mu.Lock()
	entry.running = id
	entry.startedAt = c.now()
	entry.attempts++
	c.mu.Unlock()
	c.log().Info("scheduled run started", "service", service.Name, "container", name)
	return nil
}

// runsFor lists the run containers of one scheduled service, newest first.
func (c *AgentClock) runsFor(
	ctx context.Context, engine *engine, app provider.AppRef, service string,
) ([]containerSummary, error) {
	summaries, err := engine.listContainers(ctx, map[string][]string{
		"label": {
			LabelManagedBy + "=" + managedBy,
			LabelProject + "=" + app.Project,
			LabelApp + "=" + app.App,
			LabelService + "=" + service,
		},
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Created > summaries[j].Created })
	return summaries, nil
}

// prune keeps the execution history bounded. Exited containers are the
// history, so they are kept deliberately — but not forever.
func (c *AgentClock) prune(ctx context.Context, engine *engine, app provider.AppRef, service string) error {
	runs, err := c.runsFor(ctx, engine, app, service)
	if err != nil {
		return err
	}
	keep := c.keepRuns()
	for index, run := range runs {
		if index < keep-1 || run.State == "running" {
			continue
		}
		if err := engine.removeContainer(ctx, run.ID); err != nil && !isNotFound(err) {
			return err
		}
	}
	return nil
}

// load reads the persisted registrations once. The caller holds the lock.
func (c *AgentClock) load() error {
	if c.loaded {
		return nil
	}
	c.entries = map[string]*scheduleEntry{}
	c.loaded = true
	if c.StatePath == "" {
		return nil
	}
	raw, err := os.ReadFile(c.StatePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("HD0343: read schedule state: %w", err)
	}
	var stored []*scheduleEntry
	if err := json.Unmarshal(raw, &stored); err != nil {
		return fmt.Errorf("HD0343: parse schedule state: %w", err)
	}
	for _, entry := range stored {
		c.entries[entryKey(entry.App, entry.Service.Name)] = entry
	}
	return nil
}

// save persists the registrations. The caller holds the lock.
func (c *AgentClock) save() error {
	if c.StatePath == "" {
		return nil
	}
	stored := make([]*scheduleEntry, 0, len(c.entries))
	for _, entry := range c.entries {
		stored = append(stored, entry)
	}
	sort.Slice(stored, func(i, j int) bool {
		return entryKey(stored[i].App, stored[i].Service.Name) < entryKey(stored[j].App, stored[j].Service.Name)
	})
	encoded, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("HD0343: encode schedule state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(c.StatePath), 0o700); err != nil {
		return fmt.Errorf("HD0343: create schedule state directory: %w", err)
	}
	if err := os.WriteFile(c.StatePath, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("HD0343: write schedule state: %w", err)
	}
	return nil
}
