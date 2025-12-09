//go:build gui

package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/mmilitzer/fuse-stream-mvp/frontend"
	"github.com/mmilitzer/fuse-stream-mvp/internal/api"
	"github.com/mmilitzer/fuse-stream-mvp/internal/daemon"
	"github.com/mmilitzer/fuse-stream-mvp/internal/lifecycle"
	"github.com/mmilitzer/fuse-stream-mvp/internal/logging"
	"github.com/mmilitzer/fuse-stream-mvp/internal/signals"
	"github.com/mmilitzer/fuse-stream-mvp/pkg/config"
	"github.com/mmilitzer/fuse-stream-mvp/ui"
)

func main() {
	// Initialize file logging first so we can diagnose Finder-launch issues
	if err := logging.Init(""); err != nil {
		log.Printf("Warning: Failed to initialize file logging: %v", err)
		// Continue anyway - we'll just use stdout/stderr
	}
	defer logging.Close()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		log.Fatal("FSMVP_CLIENT_ID and FSMVP_CLIENT_SECRET must be set (via config.toml or env vars)")
	}

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create API client
	client := api.NewClient(cfg.APIBase, cfg.ClientID, cfg.ClientSecret)

	// Setup signal handler for graceful shutdown
	// On macOS, SIGTERM will trigger NSApp terminate (proper termination flow)
	// SIGINT (Ctrl+C) will cancel the context
	signals.SetupSignalHandler(func() {
		log.Println("[main] Received shutdown signal")
		cancel()
	})

	// Start daemon services (FUSE mount) in background
	if err := daemon.Start(ctx, cfg.Mountpoint, client, cfg); err != nil {
		log.Fatalf("Failed to start daemon: %v", err)
	}

	// Set up lifecycle observer for foreground/background events
	var cleanupLifecycle func()
	cleanupLifecycle, err = lifecycle.ObserveActivation(func(active bool) {
		if active {
			log.Printf("[lifecycle] App became active (foreground)")
		} else {
			log.Printf("[lifecycle] App resigned active (background)")
			
			// Optionally: Test filesystem responsiveness after going to background
			// This can help detect latent deadlocks early
			fs := daemon.GetFS()
			if fs != nil && fs.Mounted() {
				go func() {
					mp := fs.Mountpoint()
					log.Printf("[lifecycle] Testing FS responsiveness at %s", mp)
					if _, err := os.Stat(mp); err != nil {
						log.Printf("[lifecycle] WARNING: FS unresponsive after resign active: %v", err)
					} else {
						log.Printf("[lifecycle] FS responsive after resign active")
					}
				}()
			}
		}
	})
	if err != nil {
		log.Printf("Warning: Failed to observe lifecycle events: %v", err)
		// Continue anyway
		cleanupLifecycle = nil
	}

	// Launch Wails GUI
	app := ui.NewApp(client)

	err = wails.Run(&options.App{
		Title:  "FuseStream MVP",
		Width:  900,
		Height: 700,
		AssetServer: &assetserver.Options{
			Assets: frontend.Assets,
		},
		OnStartup: app.Startup,
		OnBeforeClose: func(ctx context.Context) bool {
			// Check if there are active uploads
			if !app.HasActiveUploads() {
				// No uploads, allow close immediately
				log.Println("[main] No active uploads, allowing window close")
				return false // false = allow close
			}
			
			// Active uploads detected - show confirmation on main thread asynchronously
			log.Println("[main] Active uploads detected, user will be prompted")
			go app.ShowQuitConfirmation()
			return true // true = prevent close for now, will quit programmatically if user confirms
		},
		OnShutdown: func(ctx context.Context) {
			log.Println("[main] Shutting down application")
			
			// Stop lifecycle observer FIRST to prevent any new fs.Stat() calls
			// during unmount (which would deadlock)
			if cleanupLifecycle != nil {
				log.Println("[main] Stopping lifecycle observer")
				cleanupLifecycle()
			}
			
			// Trigger daemon shutdown - the daemon's goroutine will handle
			// unmounting asynchronously. We don't wait for it to complete here
			// because OnShutdown might be running on the main thread, and calling
			// fs.host.Unmount() synchronously from the main thread can deadlock
			// with the FUSE event loop.
			cancel()
			
			// Give the daemon a moment to start unmounting before the app exits
			time.Sleep(100 * time.Millisecond)
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		log.Fatalf("Error launching app: %v", err)
	}
}
