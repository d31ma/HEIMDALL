package store

import (
	"fmt"
	"sort"
	"time"
)

// The activity projection: an application's operations, arranged so the
// newest page is a bounded read instead of a scan.
//
// Why it exists. FYLO answers a paged find in ascending document-id order —
// ids are TTIDs, so that is oldest first — and offers no descending option,
// no ordering by field, and no comparison operators on the strings a
// timestamp is stored as. Document ids are assigned by FYLO, so the trick of
// choosing an id that sorts newest-first is not available either. Reading
// "the newest ten" from hd-operations therefore costs a scan of every
// operation the application ever had, however few are displayed.
//
// The shape that fixes it is a chunked list. Entries are appended to a head
// chunk; when the chunk fills, the next one starts. A page reads the head
// chunk and, if it needs more, the one before it — never more than two
// documents for a page smaller than a chunk, whatever the history's size.
// Walking backwards is arithmetic (seq-1), not a search.
//
// It is a projection in the strict sense: hd-operations remains
// authoritative and this is rebuilt by folding it, so a corrupted or missing
// index is repaired rather than reconciled.

// ChunkSize bounds one chunk's entries. Two hundred keeps a chunk document
// small enough to read and patch cheaply, and means any page of up to two
// hundred touches at most two chunks.
const ChunkSize = 200

// ActivityEntry is one operation, flattened to what a history table shows.
// It carries no message body: an entry is an index row, and the operation
// document is one read away when an operator opens it.
type ActivityEntry struct {
	OperationID string    `json:"operation_id"`
	At          time.Time `json:"at"`
	Phase       string    `json:"phase"`
	Kind        string    `json:"kind"`
	Revision    string    `json:"revision"`
	PrincipalID string    `json:"principal_id,omitempty"`
	ReasonCode  string    `json:"reason_code,omitempty"`
	Message     string    `json:"message,omitempty"`
}

// ActivityChunk is a run of entries for one application, oldest first within
// the chunk. Seq counts from zero and never has gaps, which is what makes
// walking backwards arithmetic.
type ActivityChunk struct {
	ID      string          `json:"id,omitempty"`
	AppID   string          `json:"app_id"`
	Seq     int             `json:"seq"`
	Entries []ActivityEntry `json:"entries"`
}

// ActivityHead names the chunk an application is currently appending to.
// One document per application, so finding the newest page is a single
// keyed read rather than a search for the maximum sequence.
type ActivityHead struct {
	ID      string `json:"id,omitempty"`
	AppID   string `json:"app_id"`
	HeadSeq int    `json:"head_seq"`
	Total   int    `json:"total"`
}

// ActivityCursor is a position in the projection: a chunk and an index
// within it. It is durable — it survives a restart, unlike FYLO's own query
// cursors — and it is exact, so no row is shown twice or skipped when new
// operations land while an operator is paging.
type ActivityCursor struct {
	Seq   int `json:"seq"`
	Index int `json:"index"`
	// Set distinguishes "start at the newest entry" from the position
	// {0, 0}, which is a real place — the oldest entry of the first chunk.
	// Without it a seven-row history pages forever: the last cursor of the
	// walk reads as the zero value and restarts at the head.
	Set bool `json:"set"`
}

// AppendActivity records one operation in the index, creating a chunk when
// the head fills. It returns where the entry landed so a later phase change
// can patch that entry in place rather than searching for it.
func (s *Store) AppendActivity(entry ActivityEntry, appID string) (ActivityCursor, error) {
	if appID == "" {
		return ActivityCursor{}, fmt.Errorf("HD0019: an activity entry needs an application")
	}
	heads := In[ActivityHead](s, ActivityHeads)
	chunks := In[ActivityChunk](s, Activity)

	head, err := s.activityHead(appID)
	if err != nil {
		return ActivityCursor{}, err
	}

	chunk, err := s.activityChunk(appID, head.HeadSeq)
	switch {
	case err != nil:
		return ActivityCursor{}, err
	case chunk.ID == "":
		chunk = ActivityChunk{AppID: appID, Seq: head.HeadSeq}
	case len(chunk.Entries) >= ChunkSize:
		head.HeadSeq++
		chunk = ActivityChunk{AppID: appID, Seq: head.HeadSeq}
	}

	chunk.Entries = append(chunk.Entries, entry)
	position := ActivityCursor{Seq: chunk.Seq, Index: len(chunk.Entries) - 1, Set: true}

	if chunk.ID == "" {
		id, err := chunks.Put(chunk)
		if err != nil {
			return ActivityCursor{}, fmt.Errorf("HD0019: write activity chunk: %w", err)
		}
		chunk.ID = id
	} else if err := chunks.Patch(chunk.ID, map[string]any{"entries": chunk.Entries}); err != nil {
		return ActivityCursor{}, fmt.Errorf("HD0019: extend activity chunk: %w", err)
	}

	head.AppID = appID
	head.Total++
	if head.ID == "" {
		if _, err := heads.Put(head); err != nil {
			return ActivityCursor{}, fmt.Errorf("HD0019: write activity head: %w", err)
		}
	} else if err := heads.Patch(head.ID, map[string]any{
		"head_seq": head.HeadSeq, "total": head.Total,
	}); err != nil {
		return ActivityCursor{}, fmt.Errorf("HD0019: advance activity head: %w", err)
	}
	return position, nil
}

// PatchActivity replaces the entry at a known position — how an operation's
// final phase reaches the index without re-reading the history.
func (s *Store) PatchActivity(appID string, position ActivityCursor, entry ActivityEntry) error {
	chunk, err := s.activityChunk(appID, position.Seq)
	if err != nil {
		return err
	}
	if chunk.ID == "" || position.Index < 0 || position.Index >= len(chunk.Entries) {
		return fmt.Errorf("HD0019: activity position %d/%d is not in the index", position.Seq, position.Index)
	}
	chunk.Entries[position.Index] = entry
	if err := In[ActivityChunk](s, Activity).Patch(chunk.ID, map[string]any{"entries": chunk.Entries}); err != nil {
		return fmt.Errorf("HD0019: patch activity entry: %w", err)
	}
	return nil
}

// ActivityPage reads one page of an application's history, newest first,
// starting at `from` — the zero cursor means the newest entry there is.
//
// The read is bounded by the page: one chunk document, plus the one before
// it when the page spans a boundary. A million operations cost the same as a
// hundred, which is the whole point of the projection.
func (s *Store) ActivityPage(appID string, limit int, from ActivityCursor) (
	entries []ActivityEntry, next ActivityCursor, more bool, err error,
) {
	if limit <= 0 || limit > MaxPage {
		limit = MaxPage
	}
	head, err := s.activityHead(appID)
	if err != nil {
		return nil, ActivityCursor{}, false, err
	}
	if head.ID == "" {
		return nil, ActivityCursor{}, false, nil
	}

	seq, index := from.Seq, from.Index
	if from.IsZero() {
		chunk, err := s.activityChunk(appID, head.HeadSeq)
		if err != nil {
			return nil, ActivityCursor{}, false, err
		}
		seq, index = head.HeadSeq, len(chunk.Entries)-1
	}

	entries = make([]ActivityEntry, 0, limit)
	for len(entries) < limit && seq >= 0 {
		chunk, err := s.activityChunk(appID, seq)
		if err != nil {
			return nil, ActivityCursor{}, false, err
		}
		if chunk.ID == "" {
			// A gap should not happen — sequences are contiguous — but a
			// missing chunk must end the walk rather than loop forever.
			break
		}
		if index < 0 || index >= len(chunk.Entries) {
			index = len(chunk.Entries) - 1
		}
		for ; index >= 0 && len(entries) < limit; index-- {
			entries = append(entries, chunk.Entries[index])
		}
		if index >= 0 {
			// The page filled mid-chunk; the next one resumes here.
			return entries, ActivityCursor{Seq: seq, Index: index, Set: true}, true, nil
		}
		seq--
		index = -1
	}
	if seq < 0 {
		return entries, ActivityCursor{}, false, nil
	}
	return entries, ActivityCursor{Seq: seq, Index: index, Set: true}, true, nil
}

func (c ActivityCursor) IsZero() bool { return !c.Set }

func (s *Store) activityHead(appID string) (ActivityHead, error) {
	heads, err := In[ActivityHead](s, ActivityHeads).Find(Equals("app_id", appID))
	if err != nil {
		return ActivityHead{}, fmt.Errorf("HD0019: read activity head: %w", err)
	}
	if len(heads) == 0 {
		return ActivityHead{AppID: appID}, nil
	}
	// One head per application; if a rebuild raced a write, the furthest
	// along is the true one.
	sort.Slice(heads, func(i, j int) bool { return heads[i].HeadSeq > heads[j].HeadSeq })
	return heads[0], nil
}

func (s *Store) activityChunk(appID string, seq int) (ActivityChunk, error) {
	chunks, err := In[ActivityChunk](s, Activity).Find(map[string]any{"app_id": appID, "seq": seq})
	if err != nil {
		return ActivityChunk{}, fmt.Errorf("HD0019: read activity chunk %d: %w", seq, err)
	}
	if len(chunks) == 0 {
		return ActivityChunk{}, nil
	}
	return chunks[0], nil
}

// RebuildActivity folds hd-operations into the index. It is the repair path
// for a store that predates the projection, and the reason the projection is
// allowed to be lossy: nothing here is authoritative.
func (s *Store) RebuildActivity() error {
	for _, name := range []string{Activity, ActivityHeads} {
		if _, err := s.db.DropCollection(name); err != nil {
			return fmt.Errorf("HD0019: drop %s: %w", name, err)
		}
		if _, err := s.db.CreateCollection(name, "document"); err != nil {
			return fmt.Errorf("HD0019: recreate %s: %w", name, err)
		}
	}

	// Ascending id order is chronological, which is the order entries are
	// appended in, so the index comes out identical to one grown live.
	var failed error
	err := In[Operation](s, Operations).Walk(nil, func(operation Operation) bool {
		if operation.AppID == "" {
			return true
		}
		if _, err := s.AppendActivity(EntryOf(operation), operation.AppID); err != nil {
			failed = err
			return false
		}
		return true
	})
	if err != nil {
		return err
	}
	return failed
}

// EntryOf renders an operation as an index entry. One function, so a rebuilt
// index and a live one cannot disagree about what a row says.
func EntryOf(operation Operation) ActivityEntry {
	return ActivityEntry{
		OperationID: operation.ID,
		At:          operation.StartedAt,
		Phase:       string(operation.Phase),
		Kind:        kindOf(operation),
		Revision:    operation.Revision,
		PrincipalID: operation.PrincipalID,
		ReasonCode:  operation.ReasonCode,
		Message:     operation.Message,
	}
}

func kindOf(operation Operation) string {
	switch {
	case operation.DryRun:
		return "dry run"
	case operation.Rollback:
		return "rollback"
	default:
		return "sync"
	}
}
