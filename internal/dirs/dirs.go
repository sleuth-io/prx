package dirs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// GetCacheDir returns the platform-specific cache directory for prx.
func GetCacheDir() (string, error) {
	if d := os.Getenv("PRX_CACHE_DIR"); d != "" {
		return d, nil
	}

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir, err = getFallbackCacheDir()
		if err != nil {
			return "", fmt.Errorf("failed to determine cache directory: %w", err)
		}
	}
	return filepath.Join(cacheDir, "prx"), nil
}

func getFallbackCacheDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir, "Library", "Caches"), nil
	case "linux":
		if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
			return xdg, nil
		}
		return filepath.Join(homeDir, ".cache"), nil
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return d, nil
		}
		return filepath.Join(homeDir, "AppData", "Local"), nil
	default:
		return filepath.Join(homeDir, ".cache"), nil
	}
}
