package cache

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sleuth-io/prx/internal/ai"
	"github.com/sleuth-io/prx/internal/logger"
)

type Entry struct {
	Assessment ai.Assessment `json:"assessment"`
	CachedAt   time.Time     `json:"cached_at"`
}

type Cache struct {
	mu      sync.Mutex
	entries map[string]Entry
	path    string
}

func Load() *Cache {
	path := cachePath()
	c := &Cache{path: path, entries: make(map[string]Entry)}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c
	}
	if err != nil {
		logger.Error("reading cache: %v", err)
		return c
	}
	if err := json.Unmarshal(data, &c.entries); err != nil {
		logger.Error("parsing cache: %v", err)
	}
	logger.Info("loaded %d cached assessments", len(c.entries))
	return c
}

func (c *Cache) Get(key string) (ai.Assessment, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	return e.Assessment, ok
}

func (c *Cache) Set(key string, a ai.Assessment) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = Entry{Assessment: a, CachedAt: time.Now()}
	if err := c.save(); err != nil {
		logger.Error("saving cache: %v", err)
	}
}

func (c *Cache) save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c.entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0644)
}

// Key returns a cache key that changes when the diff or review comments change.
// Key returns a cache key that invalidates when the diff or reviews change.
func Key(repo string, prNumber int, diff, reviews string) string {
	h := sha256.Sum256([]byte(diff + "\x00" + reviews))
	return fmt.Sprintf("%s#%d#%x", repo, prNumber, h[:8])
}

func cachePath() string {
	xdg := os.Getenv("XDG_CACHE_HOME")
	if xdg == "" {
		xdg = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	return filepath.Join(xdg, "prx", "assessments.json")
}
