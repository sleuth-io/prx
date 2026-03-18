package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Review     ReviewConfig     `toml:"review"`
	Weights    WeightsConfig    `toml:"weights"`
	Thresholds ThresholdsConfig `toml:"thresholds"`
}

type ReviewConfig struct {
	MaxDiffChars int `toml:"max_diff_chars"`
}

type WeightsConfig struct {
	BlastRadius  float64 `toml:"blast_radius"`
	TestCoverage float64 `toml:"test_coverage"`
	Sensitivity  float64 `toml:"sensitivity"`
	Complexity   float64 `toml:"complexity"`
	ScopeFocus   float64 `toml:"scope_focus"`
}

type ThresholdsConfig struct {
	ApproveBelow float64 `toml:"approve_below"`
	ReviewAbove  float64 `toml:"review_above"`
}

func defaults() Config {
	return Config{
		Review: ReviewConfig{MaxDiffChars: 30000},
		Weights: WeightsConfig{
			BlastRadius:  1.0,
			TestCoverage: 1.0,
			Sensitivity:  1.0,
			Complexity:   1.0,
			ScopeFocus:   1.0,
		},
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
	return cfg, nil
}

func defaultPath() string {
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(xdg, "prx", "config.toml")
}
