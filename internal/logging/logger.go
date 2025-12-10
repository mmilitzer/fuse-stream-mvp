// Package logging provides persistent file logging for diagnosing issues
// when the app is launched from Finder without console access.
// 
// This logger uses an async, non-blocking design to prevent deadlocks when
// called from FUSE operations or other performance-critical code paths.
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
	maxLogFileSize   = 10 * 1024 * 1024 // 10MB
	maxLogFiles      = 5                // Keep 5 rotated log files
	logChannelBuffer = 1000             // Buffer up to 1000 log messages
)

var (
	globalLogger *FileLogger
	mu           sync.Mutex
)

// logMessage represents a single log message to be written asynchronously.
type logMessage struct {
	data []byte
}

// FileLogger handles persistent logging to a file with rotation.
// It uses an asynchronous write model to prevent blocking FUSE operations.
type FileLogger struct {
	logPath   string
	file      *os.File
	mu        sync.Mutex
	size      int64
	logChan   chan logMessage
	done      chan struct{}
	wg        sync.WaitGroup
	stdout    io.Writer // Reference to stdout for direct writes
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
		logChan: make(chan logMessage, logChannelBuffer),
		done:    make(chan struct{}),
		stdout:  os.Stdout,
	}

	if err := logger.openLogFile(); err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	// Start async writer goroutine
	logger.wg.Add(1)
	go logger.writerLoop()

	globalLogger = logger

	// Redirect Go's standard logger to use async writer
	log.SetOutput(&asyncWriter{logger: logger})
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)

	log.Printf("[logging] File logging initialized at: %s", logPath)

	return nil
}

// Close closes the log file and stops the async writer.
func Close() error {
	mu.Lock()
	defer mu.Unlock()

	if globalLogger == nil {
		return nil
	}

	log.Printf("[logging] Closing log file")

	// Signal writer to stop
	close(globalLogger.done)
	
	// Wait for writer to finish processing remaining messages
	globalLogger.wg.Wait()

	globalLogger.mu.Lock()
	if globalLogger.file != nil {
		err := globalLogger.file.Close()
		globalLogger.file = nil
		globalLogger.mu.Unlock()
		if err != nil {
			return err
		}
	} else {
		globalLogger.mu.Unlock()
	}

	globalLogger = nil
	return nil
}

// openLogFile opens (or creates) the log file.
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

	return nil
}

// asyncWriter implements io.Writer and sends log messages to the async channel.
// This prevents blocking FUSE operations when logging.
type asyncWriter struct {
	logger *FileLogger
}

func (aw *asyncWriter) Write(p []byte) (n int, err error) {
	// Make a copy of the data since p's backing array may be reused
	data := make([]byte, len(p))
	copy(data, p)

	// Also write to stdout immediately (non-blocking)
	aw.logger.stdout.Write(data)

	// Try to send to channel (truly non-blocking with default case)
	msg := logMessage{data: data}
	
	select {
	case aw.logger.logChan <- msg:
		// Message queued successfully
	default:
		// Channel full - drop message immediately to prevent ANY blocking
		// This is CRITICAL to ensure FUSE operations never stall
		// The message was already written to stdout above, so it's not lost
	}

	return len(p), nil
}

// writerLoop is the async goroutine that writes log messages to the file.
func (l *FileLogger) writerLoop() {
	defer l.wg.Done()

	for {
		select {
		case msg := <-l.logChan:
			l.writeToFile(msg.data)
		case <-l.done:
			// Drain remaining messages before stopping
			for {
				select {
				case msg := <-l.logChan:
					l.writeToFile(msg.data)
				default:
					return
				}
			}
		}
	}
}

// writeToFile writes data to the log file (called from writer goroutine only).
func (l *FileLogger) writeToFile(data []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file == nil {
		return
	}

	n, err := l.file.Write(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[logging] Error writing to log file: %v\n", err)
		return
	}

	l.size += int64(n)

	// Check if we need to rotate
	if l.size >= maxLogFileSize {
		if rotateErr := l.rotateLogsLocked(); rotateErr != nil {
			fmt.Fprintf(os.Stderr, "[logging] Failed to rotate logs: %v\n", rotateErr)
		} else {
			l.size = 0
		}
	}
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
