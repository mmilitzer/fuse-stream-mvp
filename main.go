//go:build gui

package main

import (
	"context"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/mmilitzer/fuse-stream-mvp/frontend"
	"github.com/mmilitzer/fuse-stream-mvp/internal/api"
	"github.com/mmilitzer/fuse-stream-mvp/internal/appdelegate"
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

	// Install macOS app delegate for proper termination handling
	// This handles Cmd+Q, Quit menu, and window close events properly
	appdelegate.Install(
		func() bool {
			// Check if there are active uploads
			app := ui.GetAppInstance()
			if app == nil {
				return false
			}
			return app.HasActiveUploads()
		},
		func() error {
			// Unmount filesystem before termination
			log.Println("[main] App delegate requested unmount")
			return daemon.UnmountFS()
		},
	)

	// Set up lifecycle observer for foreground/background events
	cleanupLifecycle, err := lifecycle.ObserveActivation(func(active bool) {
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
	} else {
		defer cleanupLifecycle()
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
		// OnBeforeClose: removed - macOS app delegate now handles termination properly
		// This prevents the deadlock caused by dispatch_sync on the main thread
		OnShutdown: func(ctx context.Context) {
			log.Println("[main] Shutting down application")
			cancel() // Trigger daemon shutdown
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		log.Fatalf("Error launching app: %v", err)
	}
}
