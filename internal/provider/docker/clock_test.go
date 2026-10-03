package docker_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/provider/docker"
	"github.com/d31ma/heimdall/internal/provider/docker/dockertest"
	"github.com/d31ma/heimdall/internal/spec"
)

// clockWorld wires the agent's clock to a fake Engine whose containers run
// and then exit, which is what a batch job does.
func clockWorld(t *testing.T) (*docker.AgentClock, *dockertest.Engine, provider.AppRef) {
	t.Helper()
	engine := dockertest.New()
	t.Cleanup(engine.Close)
	engine.ExitImmediately = true

	adapter := &docker.Provider{}
	target := provider.Target{ID: "tgt", Provider: "docker", Region: "alpha", Endpoint: engine.URL()}
	clock := &docker.AgentClock{
		Provider:  adapter,
		Target:    target,
		StatePath: filepath.Join(t.TempDir(), "schedules.json"),
	}
	adapter.Clock = clock
	return clock, engine, provider.AppRef{Project: "alpha", App: "checkout"}
}

func job(name, image, cron string) spec.Service {
	service := spec.Service{
		Name: name, Image: image, Replicas: 1,
		Schedule: &spec.Schedule{Cron: cron, Timezone: "UTC", Concurrency: "forbid"},
	}
	return service
}

// TestDueScheduleRuns is the agent clock doing its one job: at the appointed
// minute a container runs, and the run is recorded.
func TestDueScheduleRuns(t *testing.T) {
	clock, engine, app := clockWorld(t)
	ctx := context.Background()

	// Every minute, so the next firing is always within the minute.
	if err := clock.Register(ctx, app, job("backup", "ghcr.io/example/backup:1", "* * * * *"), "abc1234", "sha256:spec"); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Wind the clock past the next firing rather than waiting for it.
	registrations, err := clock.Registrations(ctx, app)
	if err != nil {
		t.Fatalf("registrations: %v", err)
	}
	next := registrations["backup"].NextRunAt
	if next.IsZero() {
		t.Fatal("a cron schedule computed no next run")
	}
	clock.Now = func() time.Time { return next.Add(time.Second) }

	clock.Tick = 10 * time.Millisecond
	runCtx, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	go clock.Run(runCtx)

	deadline := time.Now().Add(2 * time.Second)
	for {
		registrations, err = clock.Registrations(ctx, app)
		if err != nil {
			t.Fatalf("registrations: %v", err)
		}
		if registrations["backup"].LastStatus == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the schedule never ran: %+v", registrations["backup"])
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()

	engine.Mu.Lock()
	defer engine.Mu.Unlock()
	if len(engine.Containers) == 0 {
		t.Fatal("the run recorded a success but no container was created")
	}
	for _, container := range engine.Containers {
		if container.Labels[docker.LabelRevision] != "abc1234" {
			t.Errorf("a run container carries revision %q; an execution must link to its commit",
				container.Labels[docker.LabelRevision])
		}
	}
}

// TestFailedRunIsRecordedAsFailed: a job that exits non-zero is a failure an
// operator can see, not a silent one.
func TestFailedRunIsRecordedAsFailed(t *testing.T) {
	clock, engine, app := clockWorld(t)
	engine.ExitCodeFor = map[string]int{"broken": 2}
	ctx := context.Background()

	if err := clock.Register(ctx, app, job("report", "ghcr.io/example/broken:1", "* * * * *"), "abc1234", "sha256:spec"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := clock.RunNow(ctx, app, "report"); err != nil {
		t.Fatalf("run now: %v", err)
	}
	clock.Tick = 10 * time.Millisecond
	runCtx, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	go clock.Run(runCtx)

	deadline := time.Now().Add(2 * time.Second)
	for {
		registrations, err := clock.Registrations(ctx, app)
		if err != nil {
			t.Fatalf("registrations: %v", err)
		}
		if status := registrations["report"].LastStatus; status == "failed" {
			break
		} else if status == "succeeded" {
			t.Fatal("a run that exited 2 was recorded as a success")
		}
		if time.Now().After(deadline) {
			t.Fatal("the failed run was never recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestUnregisterLeavesNothingBehind is the 3am footgun: a pruned job must
// leave neither a registration that fires nor containers that look like it
// still exists.
func TestUnregisterLeavesNothingBehind(t *testing.T) {
	clock, engine, app := clockWorld(t)
	ctx := context.Background()

	if err := clock.Register(ctx, app, job("backup", "ghcr.io/example/backup:1", "0 3 * * *"), "abc1234", "sha256:spec"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := clock.RunNow(ctx, app, "backup"); err != nil {
		t.Fatalf("run now: %v", err)
	}
	if err := clock.Unregister(ctx, app, "backup"); err != nil {
		t.Fatalf("unregister: %v", err)
	}

	registrations, err := clock.Registrations(ctx, app)
	if err != nil {
		t.Fatalf("registrations: %v", err)
	}
	if _, survived := registrations["backup"]; survived {
		t.Fatal("the registration outlived the service; it fires at 03:00 forever")
	}
	engine.Mu.Lock()
	defer engine.Mu.Unlock()
	if len(engine.Containers) != 0 {
		t.Fatalf("%d run containers outlived the schedule", len(engine.Containers))
	}
}

// TestRegistrationsSurviveAnAgentRestart: the clock is the only scheduler on
// a Docker host, so its state cannot live only in memory.
func TestRegistrationsSurviveAnAgentRestart(t *testing.T) {
	clock, engine, app := clockWorld(t)
	ctx := context.Background()

	if err := clock.Register(ctx, app, job("backup", "ghcr.io/example/backup:1", "0 3 * * *"), "abc1234", "sha256:spec"); err != nil {
		t.Fatalf("register: %v", err)
	}

	restarted := &docker.AgentClock{
		Provider:  &docker.Provider{},
		Target:    provider.Target{ID: "tgt", Provider: "docker", Region: "alpha", Endpoint: engine.URL()},
		StatePath: clock.StatePath,
	}
	registrations, err := restarted.Registrations(ctx, app)
	if err != nil {
		t.Fatalf("registrations after restart: %v", err)
	}
	if registrations["backup"].Cron != "0 3 * * *" {
		t.Fatalf("after a restart the agent holds %+v; the nightly backup silently stopped",
			registrations["backup"])
	}
}
