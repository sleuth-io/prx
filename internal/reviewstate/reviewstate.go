package reviewstate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/sleuth-io/prx/internal/dirs"
	"github.com/sleuth-io/prx/internal/logger"
)

// HunkDigest is a content hash of a single diff hunk's raw lines.
type HunkDigest struct {
	File      string `json:"f"`
	StartLine int    `json:"s"`
	Hash      string `json:"h"`
}

// CommentDigest tracks a comment by its GitHub ID and body hash.
type CommentDigest struct {
	ID   int    `json:"id"`
	Hash string `json:"h"`
}

// PRState captures what a reviewer has seen for a single PR.
type PRState struct {
	SeenAt   time.Time       `json:"seen_at"`
	Hunks    []HunkDigest    `json:"hunks"`
	Comments []CommentDigest `json:"comments"`
}

// Store is the persisted collection of review states.
type Store struct {
	mu     sync.Mutex
	states map[string]*PRState
	path   string
}

const evictionDays = 30

// Load reads the review state store from disk, evicting entries older than 30 days.
func Load() *Store {
	path := storePath()
	s := &Store{path: path, states: make(map[string]*PRState)}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s
	}
	if err != nil {
		logger.Error("reading review state: %v", err)
		return s
	}
	if err := json.Unmarshal(data, &s.states); err != nil {
		logger.Error("parsing review state: %v", err)
		return s
	}

	// Evict stale entries.
	cutoff := time.Now().AddDate(0, 0, -evictionDays)
	for k, v := range s.states {
		if v.SeenAt.Before(cutoff) {
			delete(s.states, k)
		}
	}

	logger.Info("loaded %d review states", len(s.states))
	return s
}

// Get returns the review state for a PR, or nil if not found.
func (s *Store) Get(key string) *PRState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[key]
}

// Set stores the review state for a PR and persists to disk.
func (s *Store) Set(key string, state *PRState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[key] = state
	if err := s.save(); err != nil {
		logger.Error("saving review state: %v", err)
	}
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.states, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

// Key returns the store key for a PR.
func Key(repo string, prNumber int) string {
	return fmt.Sprintf("%s#%d", repo, prNumber)
}

// ContentHash returns a truncated SHA256 hex string for the given content.
func ContentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:8])
}

// HunkDigestsEqual returns true if two hunk digest slices have the same content (order-independent).
func HunkDigestsEqual(a, b []HunkDigest) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, d := range a {
		set[d.File+"\x00"+fmt.Sprintf("%d", d.StartLine)+"\x00"+d.Hash] = true
	}
	for _, d := range b {
		if !set[d.File+"\x00"+fmt.Sprintf("%d", d.StartLine)+"\x00"+d.Hash] {
			return false
		}
	}
	return true
}

// CommentDigestsEqual returns true if two comment digest slices have the same content (order-independent).
func CommentDigestsEqual(a, b []CommentDigest) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, d := range a {
		set[fmt.Sprintf("%d\x00%s", d.ID, d.Hash)] = true
	}
	for _, d := range b {
		if !set[fmt.Sprintf("%d\x00%s", d.ID, d.Hash)] {
			return false
		}
	}
	return true
}

// HunkStatus represents whether a hunk is seen or new.
type HunkStatus int

const (
	StatusSeen HunkStatus = iota
	StatusNew
)

// CommentStatus represents the state of a comment relative to last review.
type CommentStatus int

const (
	CommentSeen CommentStatus = iota
	CommentNew
	CommentEdited
)

// IncrementalState holds the computed diff between current PR state and last-seen state.
type IncrementalState struct {
	HunkStatus         map[string]map[int]HunkStatus // file -> hunkIdx -> status
	CommentStatus      map[int]CommentStatus         // GitHub comment ID -> status
	HasChanges         bool
	HasNewComments     bool
	NewHunkCount       int
	NewCommentCount    int
	EditedCommentCount int
}

// ComputeIncremental compares current files and comments against a previously seen state.
func ComputeIncremental(
	fileNames []string,
	fileHunks [][]HunkInfo,
	currentComments []CommentDigest,
	seen *PRState,
) *IncrementalState {
	state := &IncrementalState{
		HunkStatus:    make(map[string]map[int]HunkStatus),
		CommentStatus: make(map[int]CommentStatus),
	}

	// Build seen hunk lookup: file -> []HunkDigest
	seenByFile := make(map[string][]HunkDigest)
	for _, d := range seen.Hunks {
		seenByFile[d.File] = append(seenByFile[d.File], d)
	}

	// Match hunks
	for fi, fileName := range fileNames {
		hunks := fileHunks[fi]
		state.HunkStatus[fileName] = make(map[int]HunkStatus)
		seenHunks := seenByFile[fileName]

		for hi, h := range hunks {
			status := matchHunk(h, seenHunks)
			state.HunkStatus[fileName][hi] = status
			if status == StatusNew {
				state.NewHunkCount++
				state.HasChanges = true
			}
		}
	}

	// Build seen comment lookup: ID -> CommentDigest
	seenComments := make(map[int]CommentDigest)
	for _, d := range seen.Comments {
		seenComments[d.ID] = d
	}

	// Match comments
	for _, c := range currentComments {
		if c.ID == 0 {
			// No GitHub ID (shouldn't happen but be safe)
			state.CommentStatus[c.ID] = CommentNew
			state.NewCommentCount++
			state.HasNewComments = true
			continue
		}
		if prev, ok := seenComments[c.ID]; ok {
			if prev.Hash == c.Hash {
				state.CommentStatus[c.ID] = CommentSeen
			} else {
				state.CommentStatus[c.ID] = CommentEdited
				state.EditedCommentCount++
				state.HasNewComments = true
			}
		} else {
			state.CommentStatus[c.ID] = CommentNew
			state.NewCommentCount++
			state.HasNewComments = true
		}
	}

	return state
}

// HunkInfo is a minimal hunk descriptor for matching purposes.
type HunkInfo struct {
	StartLine int
	Hash      string
}

func matchHunk(h HunkInfo, seen []HunkDigest) HunkStatus {
	// Tier 1: exact match (file, startLine, hash)
	for _, s := range seen {
		if s.StartLine == h.StartLine && s.Hash == h.Hash {
			return StatusSeen
		}
	}
	// Tier 2: content match (same hash, different position)
	for _, s := range seen {
		if s.Hash == h.Hash {
			return StatusSeen
		}
	}
	return StatusNew
}

// SortedHunkDigests returns a sorted copy of hunk digests for deterministic comparison.
func SortedHunkDigests(digests []HunkDigest) []HunkDigest {
	sorted := make([]HunkDigest, len(digests))
	copy(sorted, digests)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].File != sorted[j].File {
			return sorted[i].File < sorted[j].File
		}
		return sorted[i].StartLine < sorted[j].StartLine
	})
	return sorted
}

func storePath() string {
	dir, err := dirs.GetCacheDir()
	if err != nil {
		xdg := os.Getenv("XDG_CACHE_HOME")
		if xdg == "" {
			xdg = filepath.Join(os.Getenv("HOME"), ".cache")
		}
		dir = filepath.Join(xdg, "prx")
	}
	return filepath.Join(dir, "review-state.json")
}
