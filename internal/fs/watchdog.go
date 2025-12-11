//go:build fuse

package fs

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"time"
)

// opKey uniquely identifies a FUSE operation in flight
type opKey struct {
	kind string
	id   uint64
}

// watchdog tracks in-flight FUSE operations and detects stalls
type watchdog struct {
	mu       sync.Mutex
	inflight map[opKey]time.Time
	seq      atomic.Uint64
	logDir   string
	enabled  bool
	stopCh   chan struct{}
	stopped  chan struct{}
}

// newWatchdog creates a new watchdog that monitors FUSE operations
func newWatchdog(logDir string) *watchdog {
	if logDir == "" {
		// Default to ~/Library/Logs/FuseStream on macOS, ~/.local/share/FuseStream on Linux
		home, err := os.UserHomeDir()
		if err == nil {
			logDir = filepath.Join(home, "Library", "Logs", "FuseStream")
		} else {
			logDir = os.TempDir()
		}
	}
	
	// Create log directory if it doesn't exist
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("[watchdog] Warning: failed to create log directory %s: %v", logDir, err)
	}
	
	w := &watchdog{
		inflight: make(map[opKey]time.Time),
		logDir:   logDir,
		stopCh:   make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	
	return w
}

// start begins the watchdog monitoring loop
func (w *watchdog) start() {
	w.mu.Lock()
	if w.enabled {
		w.mu.Unlock()
		return
	}
	w.enabled = true
	w.mu.Unlock()
	
	go w.monitor()
	log.Printf("[watchdog] Started monitoring FUSE operations (logs: %s)", w.logDir)
}

// stop stops the watchdog monitoring loop
func (w *watchdog) stop() {
	w.mu.Lock()
	if !w.enabled {
		w.mu.Unlock()
		return
	}
	w.enabled = false
	w.mu.Unlock()
	
	close(w.stopCh)
	<-w.stopped
	log.Println("[watchdog] Stopped monitoring FUSE operations")
}

// enter marks the start of a FUSE operation and returns a cleanup function
// Usage: defer w.enter("Read", fh)()
func (w *watchdog) enter(kind string) func() {
	if !w.enabled {
		return func() {}
	}
	
	id := w.seq.Add(1)
	key := opKey{kind: kind, id: id}
	
	w.mu.Lock()
	w.inflight[key] = time.Now()
	w.mu.Unlock()
	
	return func() {
		w.mu.Lock()
		delete(w.inflight, key)
		w.mu.Unlock()
	}
}

// monitor runs in a goroutine and checks for stalled operations every 2 seconds
func (w *watchdog) monitor() {
	defer close(w.stopped)
	
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	
	const stallThreshold = 10 * time.Second
	
	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.checkForStalls(stallThreshold)
		}
	}
}

// checkForStalls checks if any operations have been running for too long
func (w *watchdog) checkForStalls(threshold time.Duration) {
	w.mu.Lock()
	now := time.Now()
	stalled := make([]string, 0)
	
	for key, startTime := range w.inflight {
		duration := now.Sub(startTime)
		if duration > threshold {
			stalled = append(stalled, fmt.Sprintf("%s#%d (running for %v)", key.kind, key.id, duration))
		}
	}
	w.mu.Unlock()
	
	if len(stalled) > 0 {
		log.Printf("[watchdog] WARNING: Detected %d stalled operations:", len(stalled))
		for _, op := range stalled {
			log.Printf("[watchdog]   - %s", op)
		}
		
		// Write stack dump to file
		stackFile := filepath.Join(w.logDir, fmt.Sprintf("stacks-%s.log", time.Now().Format("20060102-150405")))
		if err := w.writeStackDump(stackFile); err != nil {
			log.Printf("[watchdog] Error writing stack dump: %v", err)
		} else {
			log.Printf("[watchdog] Stack dump written to: %s", stackFile)
		}
	}
}

// writeStackDump writes a goroutine stack dump to the specified file
func (w *watchdog) writeStackDump(filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("create stack file: %w", err)
	}
	defer f.Close()
	
	// Write header
	fmt.Fprintf(f, "FUSE Stream Watchdog Stack Dump\n")
	fmt.Fprintf(f, "Generated: %s\n\n", time.Now().Format(time.RFC3339))
	
	// Write current in-flight operations
	w.mu.Lock()
	fmt.Fprintf(f, "In-flight operations (%d):\n", len(w.inflight))
	for key, startTime := range w.inflight {
		fmt.Fprintf(f, "  - %s#%d (started %v ago)\n", key.kind, key.id, time.Since(startTime))
	}
	w.mu.Unlock()
	
	fmt.Fprintf(f, "\nGoroutine stacks:\n")
	fmt.Fprintf(f, "==================\n\n")
	
	// Write all goroutine stacks (verbose mode with 2)
	profile := pprof.Lookup("goroutine")
	if profile == nil {
		return fmt.Errorf("goroutine profile not available")
	}
	
	if err := profile.WriteTo(f, 2); err != nil {
		return fmt.Errorf("write goroutine profile: %w", err)
	}
	
	return nil
}

// dumpStacks writes a stack dump immediately (for SIGQUIT handler)
func (w *watchdog) dumpStacks() {
	stackFile := filepath.Join(w.logDir, fmt.Sprintf("stacks-signal-%s.log", time.Now().Format("20060102-150405")))
	if err := w.writeStackDump(stackFile); err != nil {
		log.Printf("[watchdog] Error writing stack dump on signal: %v", err)
	} else {
		log.Printf("[watchdog] Stack dump (on signal) written to: %s", stackFile)
	}
}
