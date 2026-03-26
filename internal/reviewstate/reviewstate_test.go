package reviewstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestContentHash(t *testing.T) {
	// Same content produces same hash.
	h1 := ContentHash("hello world")
	h2 := ContentHash("hello world")
	if h1 != h2 {
		t.Errorf("same content produced different hashes: %s vs %s", h1, h2)
	}
	// Different content produces different hash.
	h3 := ContentHash("hello world!")
	if h1 == h3 {
		t.Errorf("different content produced same hash: %s", h1)
	}
	// Hash is 16 hex chars (8 bytes).
	if len(h1) != 16 {
		t.Errorf("expected 16 hex chars, got %d: %s", len(h1), h1)
	}
}

func TestKey(t *testing.T) {
	k := Key("owner/repo", 42)
	if k != "owner/repo#42" {
		t.Errorf("unexpected key: %s", k)
	}
}

func TestHunkDigestsEqual(t *testing.T) {
	a := []HunkDigest{
		{File: "a.go", StartLine: 10, Hash: "abc"},
		{File: "b.go", StartLine: 20, Hash: "def"},
	}
	b := []HunkDigest{
		{File: "b.go", StartLine: 20, Hash: "def"},
		{File: "a.go", StartLine: 10, Hash: "abc"},
	}
	if !HunkDigestsEqual(a, b) {
		t.Error("expected equal (order-independent)")
	}

	c := []HunkDigest{
		{File: "a.go", StartLine: 10, Hash: "abc"},
		{File: "b.go", StartLine: 20, Hash: "xyz"},
	}
	if HunkDigestsEqual(a, c) {
		t.Error("expected not equal (different hash)")
	}

	if HunkDigestsEqual(a, nil) {
		t.Error("expected not equal (nil)")
	}
}

func TestCommentDigestsEqual(t *testing.T) {
	a := []CommentDigest{{ID: 1, Hash: "aaa"}, {ID: 2, Hash: "bbb"}}
	b := []CommentDigest{{ID: 2, Hash: "bbb"}, {ID: 1, Hash: "aaa"}}
	if !CommentDigestsEqual(a, b) {
		t.Error("expected equal (order-independent)")
	}

	c := []CommentDigest{{ID: 1, Hash: "aaa"}, {ID: 2, Hash: "ccc"}}
	if CommentDigestsEqual(a, c) {
		t.Error("expected not equal (different hash)")
	}
}

func TestComputeIncremental_ExactMatch(t *testing.T) {
	seen := &PRState{
		Hunks: []HunkDigest{{File: "a.go", StartLine: 10, Hash: "abc"}},
	}
	state := ComputeIncremental(
		[]string{"a.go"},
		[][]HunkInfo{{{StartLine: 10, Hash: "abc"}}},
		nil,
		seen,
	)
	if state.HunkStatus["a.go"][0] != StatusSeen {
		t.Error("expected StatusSeen for exact match")
	}
	if state.HasChanges {
		t.Error("expected no changes")
	}
}

func TestComputeIncremental_ContentMatch(t *testing.T) {
	// Same hash but different startLine (rebase shifted it).
	seen := &PRState{
		Hunks: []HunkDigest{{File: "a.go", StartLine: 10, Hash: "abc"}},
	}
	state := ComputeIncremental(
		[]string{"a.go"},
		[][]HunkInfo{{{StartLine: 15, Hash: "abc"}}},
		nil,
		seen,
	)
	if state.HunkStatus["a.go"][0] != StatusSeen {
		t.Error("expected StatusSeen for content match (rebase)")
	}
}

func TestComputeIncremental_New(t *testing.T) {
	seen := &PRState{
		Hunks: []HunkDigest{{File: "a.go", StartLine: 10, Hash: "abc"}},
	}
	state := ComputeIncremental(
		[]string{"a.go"},
		[][]HunkInfo{{{StartLine: 10, Hash: "xyz"}}},
		nil,
		seen,
	)
	if state.HunkStatus["a.go"][0] != StatusNew {
		t.Error("expected StatusNew for different hash")
	}
	if !state.HasChanges {
		t.Error("expected HasChanges=true")
	}
	if state.NewHunkCount != 1 {
		t.Errorf("expected NewHunkCount=1, got %d", state.NewHunkCount)
	}
}

func TestComputeIncremental_NewFile(t *testing.T) {
	seen := &PRState{
		Hunks: []HunkDigest{{File: "a.go", StartLine: 10, Hash: "abc"}},
	}
	state := ComputeIncremental(
		[]string{"b.go"},
		[][]HunkInfo{{{StartLine: 1, Hash: "new"}}},
		nil,
		seen,
	)
	if state.HunkStatus["b.go"][0] != StatusNew {
		t.Error("expected StatusNew for new file")
	}
}

func TestComputeIncremental_CommentSeen(t *testing.T) {
	seen := &PRState{
		Comments: []CommentDigest{{ID: 100, Hash: "aaa"}},
	}
	state := ComputeIncremental(nil, nil,
		[]CommentDigest{{ID: 100, Hash: "aaa"}},
		seen,
	)
	if state.CommentStatus[100] != CommentSeen {
		t.Error("expected CommentSeen")
	}
	if state.HasNewComments {
		t.Error("expected no new comments")
	}
}

func TestComputeIncremental_CommentNew(t *testing.T) {
	seen := &PRState{
		Comments: []CommentDigest{{ID: 100, Hash: "aaa"}},
	}
	state := ComputeIncremental(nil, nil,
		[]CommentDigest{{ID: 100, Hash: "aaa"}, {ID: 200, Hash: "bbb"}},
		seen,
	)
	if state.CommentStatus[200] != CommentNew {
		t.Error("expected CommentNew")
	}
	if state.NewCommentCount != 1 {
		t.Errorf("expected NewCommentCount=1, got %d", state.NewCommentCount)
	}
}

func TestComputeIncremental_CommentEdited(t *testing.T) {
	seen := &PRState{
		Comments: []CommentDigest{{ID: 100, Hash: "aaa"}},
	}
	state := ComputeIncremental(nil, nil,
		[]CommentDigest{{ID: 100, Hash: "bbb"}},
		seen,
	)
	if state.CommentStatus[100] != CommentEdited {
		t.Error("expected CommentEdited")
	}
	if state.EditedCommentCount != 1 {
		t.Errorf("expected EditedCommentCount=1, got %d", state.EditedCommentCount)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-state.json")

	s := &Store{path: path, states: make(map[string]*PRState)}
	state := &PRState{
		SeenAt:   time.Now(),
		Hunks:    []HunkDigest{{File: "a.go", StartLine: 10, Hash: "abc"}},
		Comments: []CommentDigest{{ID: 1, Hash: "xxx"}},
	}
	s.Set("repo#1", state)

	// Read it back.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty file")
	}

	// Create a new store from the same file.
	s2 := &Store{path: path, states: make(map[string]*PRState)}
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &s2.states); err != nil {
		t.Fatalf("parsing: %v", err)
	}

	got := s2.Get("repo#1")
	if got == nil {
		t.Fatal("expected state, got nil")
	}
	if len(got.Hunks) != 1 || got.Hunks[0].Hash != "abc" {
		t.Errorf("unexpected hunks: %+v", got.Hunks)
	}
	if len(got.Comments) != 1 || got.Comments[0].ID != 1 {
		t.Errorf("unexpected comments: %+v", got.Comments)
	}
}

func TestEviction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-state.json")

	s := &Store{path: path, states: make(map[string]*PRState)}
	old := &PRState{SeenAt: time.Now().AddDate(0, 0, -31)}
	recent := &PRState{SeenAt: time.Now()}
	s.states["old"] = old
	s.states["recent"] = recent
	_ = s.save()

	// Simulate Load which evicts old entries.
	s2 := &Store{path: path, states: make(map[string]*PRState)}
	raw, _ := os.ReadFile(path)
	_ = json.Unmarshal(raw, &s2.states)
	cutoff := time.Now().AddDate(0, 0, -evictionDays)
	for k, v := range s2.states {
		if v.SeenAt.Before(cutoff) {
			delete(s2.states, k)
		}
	}

	if s2.Get("old") != nil {
		t.Error("expected old entry to be evicted")
	}
	if s2.Get("recent") == nil {
		t.Error("expected recent entry to survive")
	}
}
