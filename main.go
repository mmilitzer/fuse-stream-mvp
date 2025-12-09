//go:build gui

package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/mmilitzer/fuse-stream-mvp/frontend"
	"github.com/mmilitzer/fuse-stream-mvp/internal/api"
	"github.com/mmilitzer/fuse-stream-mvp/internal/daemon"
	"github.com/mmilitzer/fuse-stream-mvp/internal/dialog"
	"github.com/mmilitzer/fuse-stream-mvp/internal/lifecycle"
	"github.com/mmilitzer/fuse-stream-mvp/internal/logging"
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

	// Handle OS signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal")
		cancel()
	}()

	// Start daemon services (FUSE mount) in background
	if err := daemon.Start(ctx, cfg.Mountpoint, client, cfg); err != nil {
		log.Fatalf("Failed to start daemon: %v", err)
	}

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
		OnBeforeClose: func(ctx context.Context) bool {
			// Check if there are active uploads
			if app.HasActiveUploads() {
				log.Println("[main] Active uploads detected - asking user for confirmation")
				// Show confirmation dialog and return user's choice
				if dialog.ConfirmQuit() {
					log.Println("[main] User confirmed quit - proceeding with shutdown")
					return true // User wants to quit
				}
				log.Println("[main] User cancelled quit - keeping window open")
				return false // User wants to cancel - keep window open
			}
			// No active uploads - allow close immediately
			log.Println("[main] No active uploads - allowing window close")
			return true
		},
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
