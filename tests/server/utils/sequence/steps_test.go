package sequence_test

import (
	"errors"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
	"testing"
)

func TestFailedStepStopsBeforeLaterWork(t *testing.T) {
	failure := errors.New("local operation failed")
	calls := []string{}
	err := (sequence.Steps{Actions: []func() error{func() error { calls = append(calls, "first"); return nil }, func() error { calls = append(calls, "failed"); return failure }, func() error { calls = append(calls, "must-not-run"); return nil }}}).Run()
	if !errors.Is(err, failure) || len(calls) != 2 || calls[1] != "failed" {
		t.Fatalf("later work ran after a failed operation: %v %v", calls, err)
	}
	if err := (sequence.Steps{Actions: nil}).Run(); err != nil {
		t.Fatal(err)
	}
}
