package dispatch_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/d31ma/heimdall/internal/dispatch"
	"github.com/d31ma/heimdall/internal/provider"
)

func job(id, target string) dispatch.Job {
	return dispatch.Job{ID: id, TargetID: target}
}

// connect makes a target look like it has an agent that has polled at least
// once, which is what distinguishes "offline" from "never enrolled".
func connect(t *testing.T, dispatcher *dispatch.Dispatcher, target string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	dispatcher.Poll(ctx, target, 5*time.Millisecond)
}

func TestSubmitToAnUnknownTargetFailsImmediately(t *testing.T) {
	dispatcher := dispatch.New(time.Second)

	start := time.Now()
	_, err := dispatcher.Submit(context.Background(), job("j1", "tgt-1"))
	if !errors.Is(err, dispatch.ErrNoAgent) {
		t.Fatalf("err = %v, want ErrNoAgent", err)
	}
	// It must not wait out the result timeout before saying so.
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("took %s to report that no agent is connected", elapsed)
	}
}

// TestRoundTrip is the whole rendezvous: an agent polls, a sync submits, the
// agent gets the job and reports, and the sync returns the result.
func TestRoundTrip(t *testing.T) {
	dispatcher := dispatch.New(5 * time.Second)
	connect(t, dispatcher, "tgt-1")

	var agent sync.WaitGroup
	agent.Add(1)
	go func() {
		defer agent.Done()
		received, ok := dispatcher.Poll(context.Background(), "tgt-1", 2*time.Second)
		if !ok {
			t.Errorf("the agent's poll returned no job")
			return
		}
		if err := dispatcher.Complete("tgt-1", dispatch.Outcome{
			JobID:  received.ID,
			Result: provider.Result{OpID: "op-1", Applied: []provider.Operation{{Service: "web"}}},
		}); err != nil {
			t.Errorf("complete: %v", err)
		}
	}()

	outcome, err := dispatcher.Submit(context.Background(), job("j1", "tgt-1"))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if outcome.Result.OpID != "op-1" || len(outcome.Result.Applied) != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	agent.Wait()
}

// TestAJobHandedToAWaitingAgentIsNotAlsoQueued guards the race between a poll
// already parked and a submit arriving.
func TestAJobHandedToAWaitingAgentIsNotAlsoQueued(t *testing.T) {
	dispatcher := dispatch.New(5 * time.Second)
	connect(t, dispatcher, "tgt-1")

	received := make(chan dispatch.Job, 2)
	go func() {
		for i := 0; i < 2; i++ {
			if got, ok := dispatcher.Poll(context.Background(), "tgt-1", 500*time.Millisecond); ok {
				received <- got
			}
		}
	}()
	time.Sleep(50 * time.Millisecond)

	go func() {
		got := <-received
		_ = dispatcher.Complete("tgt-1", dispatch.Outcome{JobID: got.ID})
	}()

	if _, err := dispatcher.Submit(context.Background(), job("j1", "tgt-1")); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// A second poll must not also receive it.
	select {
	case duplicate := <-received:
		t.Fatalf("the same job was handed out twice: %s", duplicate.ID)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestSubmitTimesOutWhenTheAgentNeverReports(t *testing.T) {
	dispatcher := dispatch.New(150 * time.Millisecond)
	connect(t, dispatcher, "tgt-1")

	go func() {
		// Takes the job and goes silent, the way a host that lost power does.
		dispatcher.Poll(context.Background(), "tgt-1", 2*time.Second)
	}()
	time.Sleep(30 * time.Millisecond)

	_, err := dispatcher.Submit(context.Background(), job("j1", "tgt-1"))
	if !errors.Is(err, dispatch.ErrAgentTimeout) {
		t.Fatalf("err = %v, want ErrAgentTimeout", err)
	}
}

func TestCancelledSubmitDoesNotStrandTheAgent(t *testing.T) {
	dispatcher := dispatch.New(5 * time.Second)
	connect(t, dispatcher, "tgt-1")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if _, err := dispatcher.Submit(ctx, job("j1", "tgt-1")); err == nil {
		t.Fatal("a cancelled submit returned success")
	}

	// A late result from the agent must not block or panic.
	if err := dispatcher.Complete("tgt-1", dispatch.Outcome{JobID: "j1"}); err != nil {
		t.Fatalf("a late result errored: %v", err)
	}
}

// TestAnAgentCannotReportForAnotherTarget: an agent authenticates as one
// target, and accepting a result for another would let one host report on
// another's deployments.
func TestAnAgentCannotReportForAnotherTarget(t *testing.T) {
	dispatcher := dispatch.New(2 * time.Second)
	connect(t, dispatcher, "tgt-1")

	go func() {
		dispatcher.Poll(context.Background(), "tgt-1", time.Second)
	}()
	time.Sleep(30 * time.Millisecond)

	done := make(chan error, 1)
	go func() {
		_, err := dispatcher.Submit(context.Background(), job("j1", "tgt-1"))
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)

	if err := dispatcher.Complete("tgt-2", dispatch.Outcome{JobID: "j1"}); err == nil {
		t.Fatal("an agent for tgt-2 reported a result for a tgt-1 job")
	}
	<-done
}

func TestPollReturnsEmptyRatherThanHangingForever(t *testing.T) {
	dispatcher := dispatch.New(time.Second)

	start := time.Now()
	if _, ok := dispatcher.Poll(context.Background(), "tgt-1", 100*time.Millisecond); ok {
		t.Fatal("an idle poll returned a job")
	}
	// An empty return is what keeps the connection fresh through proxies.
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("the poll waited %s past its own deadline", elapsed)
	}
}

func TestQueueIsBounded(t *testing.T) {
	dispatcher := dispatch.New(2 * time.Second)
	connect(t, dispatcher, "tgt-1")

	// Fill the queue with submits nobody will service.
	var started sync.WaitGroup
	for i := 0; i < 20; i++ {
		started.Add(1)
		go func(n int) {
			started.Done()
			_, _ = dispatcher.Submit(context.Background(), job(string(rune('a'+n)), "tgt-1"))
		}(i)
	}
	started.Wait()
	time.Sleep(200 * time.Millisecond)

	// The next submit must be refused rather than growing the queue.
	_, err := dispatcher.Submit(context.Background(), job("overflow", "tgt-1"))
	if err == nil {
		t.Fatal("the queue accepted work without limit")
	}
}

func TestLastSeenTurnsOfflineIntoSomethingActionable(t *testing.T) {
	dispatcher := dispatch.New(time.Second)
	if _, ever := dispatcher.LastSeen("tgt-1"); ever {
		t.Fatal("a target with no agent reports a last-seen time")
	}
	connect(t, dispatcher, "tgt-1")

	seen, ever := dispatcher.LastSeen("tgt-1")
	if !ever || seen.IsZero() {
		t.Fatal("a polled target has no last-seen time")
	}
}
