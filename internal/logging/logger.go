// Package logging provides persistent file logging for diagnosing issues
// when the app is launched from Finder without console access.
package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	maxLogFileSize = 10 * 1024 * 1024 // 10MB
	maxLogFiles    = 5                // Keep 5 rotated log files
)

var (
	globalLogger *FileLogger
	mu           sync.Mutex
)

// FileLogger handles persistent logging to a file with rotation.
type FileLogger struct {
	logPath  string
	file     *os.File
	mu       sync.Mutex
	size     int64
	multiOut io.Writer // Writes to both file and stdout
}

// Init initializes the global file logger.
// logDir is the directory where logs will be stored (e.g., ~/Library/Logs/FuseStream)
// If logDir is empty, it defaults to ~/Library/Logs/FuseStream on macOS.
func Init(logDir string) error {
	mu.Lock()
	defer mu.Unlock()

	if globalLogger != nil {
		return nil // Already initialized
	}

	// Default log directory on macOS
	if logDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %w", err)
		}
		logDir = filepath.Join(homeDir, "Library", "Logs", "FuseStream")
	}

	// Create log directory if it doesn't exist
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory %s: %w", logDir, err)
	}

	logPath := filepath.Join(logDir, "fusestream.log")

	logger := &FileLogger{
		logPath: logPath,
	}

	if err := logger.openLogFile(); err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	globalLogger = logger

	// Redirect Go's standard logger to use both file and stdout
	log.SetOutput(logger.multiOut)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)

	log.Printf("[logging] File logging initialized at: %s", logPath)

	return nil
}

// Close closes the log file.
func Close() error {
	mu.Lock()
	defer mu.Unlock()

	if globalLogger == nil {
		return nil
	}

	globalLogger.mu.Lock()
	defer globalLogger.mu.Unlock()

	if globalLogger.file != nil {
		log.Printf("[logging] Closing log file")
		if err := globalLogger.file.Close(); err != nil {
			return err
		}
		globalLogger.file = nil
	}

	globalLogger = nil
	return nil
}

// openLogFile opens (or creates) the log file and sets up multi-writer.
func (l *FileLogger) openLogFile() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Close existing file if any
	if l.file != nil {
		l.file.Close()
	}

	// Check if file exists and get its size
	info, err := os.Stat(l.logPath)
	if err == nil {
		l.size = info.Size()
		// Rotate if file is too large
		if l.size >= maxLogFileSize {
			if err := l.rotateLogsLocked(); err != nil {
				return fmt.Errorf("failed to rotate logs: %w", err)
			}
			l.size = 0
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat log file: %w", err)
	}

	// Open log file for append (create if doesn't exist)
	file, err := os.OpenFile(l.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file %s: %w", l.logPath, err)
	}

	l.file = file

	// Create multi-writer that writes to both file and stdout
	l.multiOut = io.MultiWriter(os.Stdout, &trackingWriter{logger: l})

	return nil
}

// trackingWriter wraps the file writer and tracks bytes written for rotation.
type trackingWriter struct {
	logger *FileLogger
}

func (tw *trackingWriter) Write(p []byte) (n int, err error) {
	tw.logger.mu.Lock()
	defer tw.logger.mu.Unlock()

	if tw.logger.file == nil {
		return 0, fmt.Errorf("log file not open")
	}

	n, err = tw.logger.file.Write(p)
	if err != nil {
		return n, err
	}

	tw.logger.size += int64(n)

	// Check if we need to rotate
	if tw.logger.size >= maxLogFileSize {
		if rotateErr := tw.logger.rotateLogsLocked(); rotateErr != nil {
			// Log rotation failed, but we already wrote the data
			// Just print to stderr and continue
			fmt.Fprintf(os.Stderr, "Failed to rotate logs: %v\n", rotateErr)
		} else {
			tw.logger.size = 0
		}
	}

	return n, nil
}

// rotateLogsLocked rotates log files (must be called with lock held).
// fusestream.log -> fusestream.log.1
// fusestream.log.1 -> fusestream.log.2
// ...
// fusestream.log.4 -> deleted
func (l *FileLogger) rotateLogsLocked() error {
	// Close current file
	if l.file != nil {
		if err := l.file.Close(); err != nil {
			return fmt.Errorf("failed to close log file: %w", err)
		}
		l.file = nil
	}

	// Delete oldest log file if it exists
	oldestLog := fmt.Sprintf("%s.%d", l.logPath, maxLogFiles-1)
	os.Remove(oldestLog) // Ignore error if file doesn't exist

	// Rotate existing log files
	for i := maxLogFiles - 2; i >= 1; i-- {
		oldName := fmt.Sprintf("%s.%d", l.logPath, i)
		newName := fmt.Sprintf("%s.%d", l.logPath, i+1)
		os.Rename(oldName, newName) // Ignore error if file doesn't exist
	}

	// Rotate current log file
	if err := os.Rename(l.logPath, l.logPath+".1"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to rename current log: %w", err)
	}

	// Open new log file
	file, err := os.OpenFile(l.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to create new log file: %w", err)
	}

	l.file = file

	// Write rotation marker
	marker := fmt.Sprintf("\n=== Log rotated at %s ===\n\n", time.Now().Format(time.RFC3339))
	l.file.WriteString(marker)

	return nil
}
