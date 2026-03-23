package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/sleuth-io/prx/internal/dirs"
)

var fileLogger *log.Logger

func Init() error {
	dir, err := dirs.GetCacheDir()
	if err != nil {
		return fmt.Errorf("determining cache dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}
	path := filepath.Join(dir, "prx.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("opening log file %s: %w", path, err)
	}
	fileLogger = log.New(f, "", log.LstdFlags)
	fileLogger.Printf("=== prx started ===")
	return nil
}

func Debug(format string, args ...any) {
	if fileLogger != nil {
		fileLogger.Printf("[DEBUG] "+format, args...)
	}
}

func Info(format string, args ...any) {
	if fileLogger != nil {
		fileLogger.Printf("[INFO]  "+format, args...)
	}
}

func Error(format string, args ...any) {
	if fileLogger != nil {
		fileLogger.Printf("[ERROR] "+format, args...)
	}
}
