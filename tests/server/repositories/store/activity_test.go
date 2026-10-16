package store_test

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/d31ma/heimdall/app/server/repositories/store"
)

// TestActivityPagesInBoundedReads is the projection's contract: pages come
// back newest-first, cover every entry exactly once, and each page reads a
// chunk or two rather than the history.
func TestActivityPagesInBoundedReads(t *testing.T) {
	if _, err := exec.LookPath("fylo"); err != nil {
		t.Skipf("fylo binary not on PATH: %v", err)
	}
	storage, err := store.Open(filepath.Join(t.TempDir(), "fylo-root"), "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	const total = 45
	for i := 0; i < total; i++ {
		if _, err := storage.AppendActivity(store.ActivityEntry{
			OperationID: "op-" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			At:          base.Add(time.Duration(i) * time.Minute),
			Phase:       "succeeded", Kind: "sync",
		}, "app-1"); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	// A second application shares the collection and must not appear.
	if _, err := storage.AppendActivity(store.ActivityEntry{OperationID: "other", At: base}, "app-2"); err != nil {
		t.Fatalf("append other: %v", err)
	}

	seen := map[string]bool{}
	cursor := store.ActivityCursor{}
	var previous time.Time
	for page := 0; page < 12; page++ {
		entries, next, more, err := storage.ActivityPage("app-1", 10, cursor)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			if entry.OperationID == "other" {
				t.Fatal("the index leaked another application's entry")
			}
			if seen[entry.OperationID] {
				t.Fatalf("page %d repeated %s", page, entry.OperationID)
			}
			seen[entry.OperationID] = true
			if !previous.IsZero() && entry.At.After(previous) {
				t.Fatalf("page %d is not newest-first", page)
			}
			previous = entry.At
		}
		if !more {
			break
		}
		cursor = next
	}
	if len(seen) != total {
		t.Fatalf("paging saw %d of %d entries", len(seen), total)
	}
}

// TestActivityCrossesChunkBoundaries: the index is only interesting past one
// chunk, which is where the sequence walk and the head advance earn their
// keep.
func TestActivityCrossesChunkBoundaries(t *testing.T) {
	if _, err := exec.LookPath("fylo"); err != nil {
		t.Skipf("fylo binary not on PATH: %v", err)
	}
	storage, err := store.Open(filepath.Join(t.TempDir(), "fylo-root"), "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	total := store.ChunkSize + 60 // two chunks and a bit of a third
	for i := 0; i < total; i++ {
		if _, err := storage.AppendActivity(store.ActivityEntry{
			OperationID: fmt.Sprintf("op-%04d", i),
			At:          base.Add(time.Duration(i) * time.Second),
			Phase:       "succeeded",
		}, "app-1"); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	seen := map[string]bool{}
	cursor := store.ActivityCursor{}
	pages := 0
	for {
		entries, next, more, err := storage.ActivityPage("app-1", 50, cursor)
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		if len(entries) == 0 {
			break
		}
		pages++
		for _, entry := range entries {
			if seen[entry.OperationID] {
				t.Fatalf("page %d repeated %s across a chunk boundary", pages, entry.OperationID)
			}
			seen[entry.OperationID] = true
		}
		if !more {
			break
		}
		cursor = next
	}
	if len(seen) != total {
		t.Fatalf("paging across chunks saw %d of %d entries", len(seen), total)
	}
	// Newest first: the very first entry of page one is the last appended.
	first, _, _, err := storage.ActivityPage("app-1", 1, store.ActivityCursor{})
	if err != nil || len(first) != 1 {
		t.Fatalf("first page: %v", err)
	}
	if want := fmt.Sprintf("op-%04d", total-1); first[0].OperationID != want {
		t.Fatalf("the newest entry is %s, want %s", first[0].OperationID, want)
	}
}

// TestActivityRebuildsFromOperations: the index is a projection, so a store
// that predates it — or one whose index was dropped — repairs by folding the
// authoritative collection.
func TestActivityRebuildsFromOperations(t *testing.T) {
	if _, err := exec.LookPath("fylo"); err != nil {
		t.Skipf("fylo binary not on PATH: %v", err)
	}
	storage, err := store.Open(filepath.Join(t.TempDir(), "fylo-root"), "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	operations := store.In[store.Operation](storage, store.Operations)
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		if _, err := operations.Put(store.Operation{
			AppID: "app-1", Project: "alpha", App: "site",
			Phase: store.PhaseSucceeded, StartedAt: base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}

	if err := storage.RebuildActivity(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	entries, _, _, err := storage.ActivityPage("app-1", 20, store.ActivityCursor{})
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if len(entries) != 12 {
		t.Fatalf("a rebuilt index holds %d of 12 operations", len(entries))
	}
	if !entries[0].At.After(entries[len(entries)-1].At) {
		t.Fatal("a rebuilt index is not newest-first")
	}

	// Rebuilding twice is not twenty-four entries.
	if err := storage.RebuildActivity(); err != nil {
		t.Fatalf("second rebuild: %v", err)
	}
	again, _, _, err := storage.ActivityPage("app-1", 50, store.ActivityCursor{})
	if err != nil || len(again) != 12 {
		t.Fatalf("rebuilding twice holds %d entries (err %v)", len(again), err)
	}
}

// TestPatchActivityUpdatesInPlace: an operation's final phase has to reach
// the index, or every row reads as still running.
func TestPatchActivityUpdatesInPlace(t *testing.T) {
	if _, err := exec.LookPath("fylo"); err != nil {
		t.Skipf("fylo binary not on PATH: %v", err)
	}
	storage, err := store.Open(filepath.Join(t.TempDir(), "fylo-root"), "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	at := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	position, err := storage.AppendActivity(store.ActivityEntry{
		OperationID: "op-1", At: at, Phase: "applying", Kind: "sync",
	}, "app-1")
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := storage.PatchActivity("app-1", position, store.ActivityEntry{
		OperationID: "op-1", At: at, Phase: "succeeded", Kind: "sync", Message: "done",
	}); err != nil {
		t.Fatalf("patch: %v", err)
	}

	entries, _, _, err := storage.ActivityPage("app-1", 10, store.ActivityCursor{})
	if err != nil || len(entries) != 1 {
		t.Fatalf("page: %v (%d entries)", err, len(entries))
	}
	if entries[0].Phase != "succeeded" || entries[0].Message != "done" {
		t.Fatalf("the patched entry reads %+v", entries[0])
	}
}
