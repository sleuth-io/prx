package app

import (
	"fmt"
	"sync"

	"github.com/sleuth-io/prx/internal/cache"
	"github.com/sleuth-io/prx/internal/config"
	"github.com/sleuth-io/prx/internal/github"
	"github.com/sleuth-io/prx/internal/logger"
	"github.com/sleuth-io/prx/internal/skills"
)

// RepoContext is the per-repo handle. It bundles the repo identity with a
// back-pointer to shared application state (user, config, cache).
type RepoContext struct {
	Repo    string // GitHub owner/name, e.g. "sleuth-io/prx"
	RepoDir string // local filesystem path
	App     *App
}

// App holds shared application context that is the same across all repos.
type App struct {
	Repos       []*RepoContext
	CurrentUser string
	Config      config.Config
	Cache       *cache.Cache
	Skills      []skills.Skill
}

func New(repoDirs []string) (*App, error) {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error

		repos = make([]*RepoContext, len(repoDirs))
		user  string
		cfg   config.Config
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

	// Detect each repo in parallel.
	for i, dir := range repoDirs {
		i, dir := i, dir
		run(func() error {
			repo, err := github.DetectRepo(dir)
			if err != nil {
				return err
			}
			mu.Lock()
			repos[i] = &RepoContext{Repo: repo, RepoDir: dir}
			mu.Unlock()
			return nil
		})
	}

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

	for _, r := range repos {
		logger.Info("repo: %s  dir: %s", r.Repo, r.RepoDir)
	}
	logger.Info("user: %s", user)

	discovered := skills.Discover()
	logger.Info("skills: discovered %d skills", len(discovered))

	a := &App{
		Repos:       repos,
		CurrentUser: user,
		Config:      cfg,
		Cache:       cache.Load(),
		Skills:      discovered,
	}

	// Wire back-pointers.
	for _, r := range repos {
		r.App = a
	}

	return a, nil
}
