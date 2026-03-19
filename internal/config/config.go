package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Criterion defines a single scoring dimension for PR assessment.
// Criteria are configurable — different teams can define what matters to them.
type Criterion struct {
	Name        string  `toml:"name"`
	Label       string  `toml:"label"`
	Description string  `toml:"description"`
	Weight      float64 `toml:"weight"`
}

type Config struct {
	Review     ReviewConfig     `toml:"review"`
	Criteria   []Criterion      `toml:"criteria"`
	Thresholds ThresholdsConfig `toml:"thresholds"`
}

type ReviewConfig struct {
	Model       string `toml:"model"`        // Claude model to use (e.g. "sonnet", "opus")
	MergeMethod string `toml:"merge_method"` // "merge", "squash", or "rebase"
}

type ThresholdsConfig struct {
	ApproveBelow float64 `toml:"approve_below"`
	ReviewAbove  float64 `toml:"review_above"`
}

// DefaultCriteria returns the built-in scoring dimensions, oriented around
// "how much human judgment does this PR require?" rather than code quality.
func DefaultCriteria() []Criterion {
	return []Criterion{
		{
			Name:        "blast_radius",
			Label:       "Blast",
			Description: "How much of the system could break? Consider business impact: internal tooling vs external-facing vs revenue-critical. A 1-line change to checkout is higher blast than 500 lines of test changes. Deleting large amounts of business logic is HIGH (4-5).",
			Weight:      1.0,
		},
		{
			Name:        "intent_clarity",
			Label:       "Intent",
			Description: "Is the WHY clear? Score high if the PR lacks a description, has vague intent, or the code doesn't clearly map to a stated goal. A reviewer shouldn't have to guess what problem this solves.",
			Weight:      1.0,
		},
		{
			Name:        "irreversibility",
			Label:       "Irreversible",
			Description: "How hard is this to undo? Migrations, public API changes, data model shifts, deleted business logic score high. Reversible changes (feature-flagged, additive-only, behind toggles) score low.",
			Weight:      1.0,
		},
		{
			Name:        "domain_knowledge",
			Label:       "Domain",
			Description: "How much implicit or tribal knowledge is needed to review this safely? Changes to areas with unwritten rules, complex business logic, or historical gotchas score high. Pure utility code scores low.",
			Weight:      1.0,
		},
		{
			Name:        "novelty",
			Label:       "Novelty",
			Description: "Is this introducing new patterns, new dependencies, or touching unfamiliar territory? First-time contributors to sensitive areas, new architectural patterns, or novel integrations score high. Routine changes in well-trodden paths score low.",
			Weight:      1.0,
		},
	}
}

func defaults() Config {
	return Config{
		Review:   ReviewConfig{Model: "sonnet", MergeMethod: "merge"},
		Criteria: DefaultCriteria(),
		Thresholds: ThresholdsConfig{
			ApproveBelow: 2.0,
			ReviewAbove:  3.5,
		},
	}
}

func Load() (Config, error) {
	path := defaultPath()
	cfg := defaults()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}

	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing config %s: %w", path, err)
	}

	// If criteria were provided in config, use them; otherwise keep defaults.
	// TOML array-of-tables appends, so an empty [[criteria]] section won't
	// accidentally clear the defaults — only explicit entries replace them.

	return cfg, nil
}

// CriteriaHash returns a short hash of the criteria configuration.
// Used for cache invalidation when criteria change.
func CriteriaHash(criteria []Criterion) string {
	var sb strings.Builder
	for _, c := range criteria {
		fmt.Fprintf(&sb, "%s|%s|%.2f\n", c.Name, c.Description, c.Weight)
	}
	h := sha256.Sum256([]byte(sb.String()))
	return fmt.Sprintf("%x", h[:4])
}

// Save writes cfg to the default config path, creating parent directories as needed.
func Save(cfg Config) error {
	path := defaultPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("opening config for write: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	return nil
}

func defaultPath() string {
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(xdg, "prx", "config.toml")
}
