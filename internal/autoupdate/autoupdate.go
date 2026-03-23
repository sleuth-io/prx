package autoupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/creativeprojects/go-selfupdate"

	"github.com/sleuth-io/prx/internal/buildinfo"
	"github.com/sleuth-io/prx/internal/dirs"
	"github.com/sleuth-io/prx/internal/logger"
)

const (
	GithubOwner       = "sleuth-io"
	GithubRepo        = "prx"
	checkInterval     = 24 * time.Hour
	updateCacheFile   = "last-update-check"
	pendingUpdateFile = "pending-update.json"
	updateTimeout     = 30 * time.Second
)

// pendingUpdate represents a pending update marker file
type pendingUpdate struct {
	Version   string `json:"version"`
	AssetURL  string `json:"asset_url"`
	AssetName string `json:"asset_name"`
}

// isEnvTrue checks if an environment variable is set to a truthy value
func isEnvTrue(key string) bool {
	val := os.Getenv(key)
	switch val {
	case "1", "true", "TRUE", "yes", "YES", "on", "ON":
		return true
	}
	return false
}

// isDevBuild returns true if this is a development build
func isDevBuild() bool {
	v := buildinfo.Version
	return v == "dev" || v == "" || strings.Contains(v, "-dirty")
}

// gitDescribeRe matches git describe output: v1.2.3-N-gHASH(-dirty)
var gitDescribeRe = regexp.MustCompile(`^(v?\d+\.\d+\.\d+)-\d+-g[0-9a-f]+(-dirty)?$`)

// IsNewerThanRelease reports whether the current build version is at or ahead
// of releaseVersion. Git describe versions like "0.2.0-9-g2ccb74f" mean
// "9 commits ahead of 0.2.0" and should be considered newer than 0.2.0.
func IsNewerThanRelease(currentRaw, releaseVersion string) bool {
	currentRaw = strings.TrimPrefix(currentRaw, "v")
	releaseVersion = strings.TrimPrefix(releaseVersion, "v")

	// If current matches git describe format, extract the base tag
	if m := gitDescribeRe.FindStringSubmatch("v" + currentRaw); m != nil {
		baseTag := strings.TrimPrefix(m[1], "v")
		baseV, err := semver.NewVersion(baseTag)
		if err != nil {
			return false
		}
		relV, err := semver.NewVersion(releaseVersion)
		if err != nil {
			return false
		}
		// Base tag >= release means we're ahead (we have extra commits on top)
		// Base tag == release means we're strictly ahead (N > 0 commits)
		return !baseV.LessThan(relV)
	}

	// Plain semver comparison
	curV, err := semver.NewVersion(currentRaw)
	if err != nil {
		return false
	}
	relV, err := semver.NewVersion(releaseVersion)
	if err != nil {
		return false
	}
	return !curV.LessThan(relV)
}

// pendingUpdatePath returns the path to the pending update marker file
func pendingUpdatePath() (string, error) {
	cacheDir, err := dirs.GetCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, pendingUpdateFile), nil
}

// ApplyPendingUpdate checks for a pending update marker and applies it.
// This should be called at startup before CheckAndUpdateInBackground.
// The fast path (no marker) is a single os.Stat call.
// Returns true if an update was applied (caller should re-exec).
func ApplyPendingUpdate() bool {
	if isEnvTrue("DISABLE_AUTOUPDATER") || isDevBuild() {
		return false
	}

	markerPath, err := pendingUpdatePath()
	if err != nil {
		return false
	}

	// Fast path: no marker means no pending update
	if _, err := os.Stat(markerPath); os.IsNotExist(err) {
		return false
	}

	data, err := os.ReadFile(markerPath)
	if err != nil {
		_ = os.Remove(markerPath)
		return false
	}

	var pending pendingUpdate
	if err := json.Unmarshal(data, &pending); err != nil {
		_ = os.Remove(markerPath)
		return false
	}

	// Check if we're already at or ahead of the pending version
	if IsNewerThanRelease(buildinfo.Version, pending.Version) {
		_ = os.Remove(markerPath)
		return false
	}

	// Get path to current executable for replacement
	execPath, err := os.Executable()
	if err != nil {
		_ = os.Remove(markerPath)
		return false
	}

	// Apply the update with a timeout
	ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
	defer cancel()

	// Suppress stdout during update
	restoreStdout := suppressStdout()
	defer restoreStdout()
	err = selfupdate.UpdateTo(ctx, pending.AssetURL, pending.AssetName, execPath)

	// Always remove the marker
	_ = os.Remove(markerPath)

	if err != nil {
		logger.Error("failed to apply pending update: version=%s error=%v", pending.Version, err)
		return false
	}

	logger.Info("applied pending update: old=%s new=%s", buildinfo.Version, pending.Version)
	return true
}

// ClearPendingUpdate removes the pending update marker file.
func ClearPendingUpdate() {
	markerPath, err := pendingUpdatePath()
	if err != nil {
		return
	}
	_ = os.Remove(markerPath)
}

// CheckAndUpdateInBackground checks for updates and installs them automatically if found.
// It only checks once per day (tracked via cache file).
// This function returns immediately and doesn't block.
func CheckAndUpdateInBackground() {
	go func() {
		_ = checkAndUpdate()
	}()
}

// checkAndUpdate performs the actual update check and installation.
// Phase 1: Detect latest release, write marker, attempt UpdateTo.
// If the goroutine gets killed mid-download, the marker survives for Phase 2.
func checkAndUpdate() error {
	if isEnvTrue("DISABLE_AUTOUPDATER") {
		return nil
	}

	currentVersion := buildinfo.Version
	if currentVersion == "dev" || currentVersion == "" {
		return nil
	}

	if !shouldCheck() {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
	defer cancel()

	source, _ := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	updater, _ := selfupdate.NewUpdater(selfupdate.Config{
		Source: source,
	})

	release, found, err := updater.DetectLatest(ctx, selfupdate.ParseSlug(fmt.Sprintf("%s/%s", GithubOwner, GithubRepo)))
	if err != nil {
		return err
	}
	if !found {
		_ = updateCheckTimestamp()
		return nil
	}

	if IsNewerThanRelease(currentVersion, release.Version()) {
		_ = updateCheckTimestamp()
		return nil
	}

	if err := writePendingUpdate(release); err != nil {
		logger.Error("failed to write pending update marker: %v", err)
	}

	_ = updateCheckTimestamp()

	restoreStdout := suppressStdout()
	defer restoreStdout()

	err = updater.UpdateTo(ctx, release, "")

	if err != nil {
		return err
	}

	ClearPendingUpdate()

	logger.Info("autoupdate completed: old=%s new=%s", currentVersion, release.Version())

	return nil
}

// writePendingUpdate writes the pending update marker file
func writePendingUpdate(release *selfupdate.Release) error {
	markerPath, err := pendingUpdatePath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(markerPath), 0755); err != nil {
		return err
	}

	pending := pendingUpdate{
		Version:   release.Version(),
		AssetURL:  release.AssetURL,
		AssetName: release.AssetName,
	}

	data, err := json.Marshal(pending)
	if err != nil {
		return err
	}

	return os.WriteFile(markerPath, data, 0644)
}

// shouldCheck returns true if we should check for updates
func shouldCheck() bool {
	cacheDir, err := dirs.GetCacheDir()
	if err != nil {
		return true
	}

	lastCheckFile := filepath.Join(cacheDir, updateCacheFile)

	info, err := os.Stat(lastCheckFile)
	if err != nil {
		return true
	}

	return time.Since(info.ModTime()) > checkInterval
}

// updateCheckTimestamp updates the timestamp of the last update check
func updateCheckTimestamp() error {
	cacheDir, err := dirs.GetCacheDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}

	lastCheckFile := filepath.Join(cacheDir, updateCacheFile)

	return os.WriteFile(lastCheckFile, []byte(time.Now().Format(time.RFC3339)), 0644)
}
