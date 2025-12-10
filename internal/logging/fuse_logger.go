// Package logging provides non-blocking logging for FUSE operations.
// This is CRITICAL to prevent FUSE deadlocks - we must NEVER block in a FUSE callback.
package logging

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	fuseLogBufferSize = 10000 // Large ring buffer to handle bursts
	fuseLogFlushDelay = 100 * time.Millisecond
)

// fuseLogEntry represents a single FUSE log message
type fuseLogEntry struct {
	timestamp time.Time
	message   string
}

// FUSELogger provides a non-blocking logger specifically for FUSE operations.
// It uses a ring buffer with drop-on-full semantics to ensure FUSE callbacks
// NEVER block on logging.
type FUSELogger struct {
	logPath  string
	file     *os.File
	stdout   *os.File
	mu       sync.Mutex
	
	// Ring buffer for async logging
	buffer   chan fuseLogEntry
	done     chan struct{}
	wg       sync.WaitGroup
	
	// Statistics
	dropped  uint64
	written  uint64
}

var (
	globalFUSELogger *FUSELogger
	fuseMu           sync.Mutex
)

// InitFUSELogger initializes the global FUSE logger.
// This logger writes to ~/Library/Logs/FuseStream/fuse.log and NEVER blocks.
func InitFUSELogger() error {
	fuseMu.Lock()
	defer fuseMu.Unlock()
	
	if globalFUSELogger != nil {
		return nil // Already initialized
	}
	
	// Get log directory (must be outside FUSE volume)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %w", err)
	}
	logDir := filepath.Join(homeDir, "Library", "Logs", "FuseStream")
	
	// Create log directory if it doesn't exist
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory %s: %w", logDir, err)
	}
	
	logPath := filepath.Join(logDir, "fuse.log")
	
	// Open log file
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open FUSE log file: %w", err)
	}
	
	logger := &FUSELogger{
		logPath: logPath,
		file:    file,
		stdout:  os.Stdout,
		buffer:  make(chan fuseLogEntry, fuseLogBufferSize),
		done:    make(chan struct{}),
	}
	
	// Start async writer goroutine
	logger.wg.Add(1)
	go logger.writerLoop()
	
	globalFUSELogger = logger
	
	// Write initialization marker
	logger.logNonBlocking("[FUSE] Logger initialized at %s", time.Now().Format(time.RFC3339))
	
	return nil
}

// CloseFUSELogger closes the FUSE logger and flushes remaining messages.
func CloseFUSELogger() error {
	fuseMu.Lock()
	defer fuseMu.Unlock()
	
	if globalFUSELogger == nil {
		return nil
	}
	
	logger := globalFUSELogger
	globalFUSELogger = nil
	
	// Signal writer to stop
	close(logger.done)
	
	// Wait for writer to finish
	logger.wg.Wait()
	
	// Close file
	logger.mu.Lock()
	defer logger.mu.Unlock()
	
	if logger.file != nil {
		// Write final stats
		if logger.dropped > 0 {
			fmt.Fprintf(logger.file, "[FUSE] Logger closed - written: %d, dropped: %d messages\n",
				logger.written, logger.dropped)
		} else {
			fmt.Fprintf(logger.file, "[FUSE] Logger closed - written: %d messages\n", logger.written)
		}
		
		err := logger.file.Close()
		logger.file = nil
		return err
	}
	
	return nil
}

// logNonBlocking sends a log message to the async writer.
// This function NEVER blocks - if the buffer is full, it drops the message.
// This is CRITICAL for FUSE callbacks.
func (l *FUSELogger) logNonBlocking(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	entry := fuseLogEntry{
		timestamp: time.Now(),
		message:   message,
	}
	
	// Try to send to buffer - NEVER block
	select {
	case l.buffer <- entry:
		// Message queued successfully
	default:
		// Buffer full - drop message to prevent blocking
		// This is correct behavior for FUSE callbacks
		l.dropped++
	}
}

// writerLoop runs in a separate goroutine and writes log messages to disk.
func (l *FUSELogger) writerLoop() {
	defer l.wg.Done()
	
	// Use a ticker to batch writes for better performance
	ticker := time.NewTicker(fuseLogFlushDelay)
	defer ticker.Stop()
	
	batch := make([]fuseLogEntry, 0, 100)
	
	for {
		select {
		case entry := <-l.buffer:
			batch = append(batch, entry)
			
			// Flush if batch is getting large
			if len(batch) >= 100 {
				l.flushBatch(batch)
				batch = batch[:0]
			}
			
		case <-ticker.C:
			// Periodic flush
			if len(batch) > 0 {
				l.flushBatch(batch)
				batch = batch[:0]
			}
			
		case <-l.done:
			// Drain remaining messages
			for {
				select {
				case entry := <-l.buffer:
					batch = append(batch, entry)
				default:
					if len(batch) > 0 {
						l.flushBatch(batch)
					}
					return
				}
			}
		}
	}
}

// flushBatch writes a batch of log entries to disk AND stdout.
func (l *FUSELogger) flushBatch(batch []fuseLogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	
	if l.file == nil {
		return
	}
	
	for _, entry := range batch {
		line := fmt.Sprintf("%s %s\n",
			entry.timestamp.Format("2006-01-02 15:04:05.000000"),
			entry.message)
		
		// Write to file
		_, err := l.file.WriteString(line)
		if err != nil {
			// Can't log errors without potentially blocking, so just count them
			continue
		}
		
		// Also write to stdout (we do it here in the async writer to avoid blocking FUSE ops)
		if l.stdout != nil {
			l.stdout.WriteString(line)
		}
		
		l.written++
	}
	
	// Sync to disk periodically
	l.file.Sync()
}

// FUSELog logs a message from a FUSE callback using the non-blocking logger.
// This function NEVER blocks and is safe to call from any FUSE operation.
// 
// CRITICAL: We do NOT call log.Printf here because even though it goes through
// an async logger, that logger writes to stdout synchronously which CAN block
// if stdout is redirected or if the TTY buffer is full. This would cause FUSE
// operations to stall.
func FUSELog(format string, args ...interface{}) {
	fuseMu.Lock()
	logger := globalFUSELogger
	fuseMu.Unlock()
	
	if logger != nil {
		logger.logNonBlocking(format, args...)
	}
	
	// DO NOT call log.Printf from FUSE callbacks - it can block on stdout!
	// The FUSE logger will handle all output asynchronously
}

// FUSETraceNonBlocking wraps a FUSE operation with entry/exit logging using non-blocking logger.
// This is CRITICAL for diagnosing deadlocks - it shows exactly where operations stall.
//
// Usage:
//   defer FUSETraceNonBlocking("Read", "fh=%d off=%d len=%d", fh, offset, len(buff))()
func FUSETraceNonBlocking(operation string, format string, args ...interface{}) func() {
	t0 := time.Now()
	
	// Log entry
	if format != "" {
		FUSELog("[FUSE] %s ENTER %s", operation, fmt.Sprintf(format, args...))
	} else {
		FUSELog("[FUSE] %s ENTER", operation)
	}
	
	// Return cleanup function that logs exit with duration
	return func() {
		dur := time.Since(t0)
		FUSELog("[FUSE] %s EXIT dur=%v", operation, dur)
		
		// Warn if operation took too long (potential stall)
		if dur > 5*time.Second {
			FUSELog("[FUSE] WARNING: %s took %v (potential stall/deadlock)", operation, dur)
		}
	}
}
