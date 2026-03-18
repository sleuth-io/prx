package app

import (
	"fmt"
	"sync"

	"github.com/sleuth-io/prx/internal/cache"
	"github.com/sleuth-io/prx/internal/config"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/logger"
)

// App holds all shared application context.
type App struct {
	Repo        string
	RepoDir     string
	CurrentUser string
	Config      config.Config
	Cache       *cache.Cache
}

func New(repoDir string) (*App, error) {
	// Run independent lookups in parallel
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error

		repo string
		user string
		cfg  config.Config
	)

	run := func(fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}

	run(func() error {
		var err error
		repo, err = github.DetectRepo(repoDir)
		return err
	})

	run(func() error {
		var err error
		user, err = github.CurrentUser()
		if err != nil {
			return fmt.Errorf("detecting current GitHub user: %w", err)
		}
		return nil
	})

	run(func() error {
		var err error
		cfg, err = config.Load()
		return err
	})

	wg.Wait()

	if len(errs) > 0 {
		return nil, errs[0]
	}

	logger.Info("repo: %s  dir: %s  user: %s", repo, repoDir, user)

	return &App{
		Repo:        repo,
		RepoDir:     repoDir,
		CurrentUser: user,
		Config:      cfg,
		Cache:       cache.Load(),
	}, nil
}
