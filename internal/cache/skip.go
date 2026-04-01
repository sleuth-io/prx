package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/sleuth-io/prx/internal/dirs"
	"github.com/sleuth-io/prx/internal/logger"
)

// SkipStore persists a set of skipped PRs keyed by "repo#number".
type SkipStore struct {
	mu      sync.Mutex
	skipped map[string]bool
	path    string
}

func LoadSkipStore() *SkipStore {
	path := skipPath()
	s := &SkipStore{path: path, skipped: make(map[string]bool)}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s
	}
	if err != nil {
		logger.Error("reading skip store: %v", err)
		return s
	}
	if err := json.Unmarshal(data, &s.skipped); err != nil {
		logger.Error("parsing skip store: %v", err)
	}
	logger.Info("loaded %d skipped PRs", len(s.skipped))
	return s
}

func SkipKey(repo string, number int) string {
	return fmt.Sprintf("%s#%d", repo, number)
}

func (s *SkipStore) IsSkipped(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skipped[key]
}

func (s *SkipStore) Skip(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skipped[key] = true
	if err := s.save(); err != nil {
		logger.Error("saving skip store: %v", err)
	}
}

func (s *SkipStore) Unskip(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.skipped, key)
	if err := s.save(); err != nil {
		logger.Error("saving skip store: %v", err)
	}
}

func (s *SkipStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.skipped, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

func skipPath() string {
	dir, err := dirs.GetCacheDir()
	if err != nil {
		xdg := os.Getenv("XDG_CACHE_HOME")
		if xdg == "" {
			xdg = filepath.Join(os.Getenv("HOME"), ".cache")
		}
		dir = filepath.Join(xdg, "prx")
	}
	return filepath.Join(dir, "skipped.json")
}
