//go:build gui

package main

import (
	"context"
	"log"

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
		OnShutdown: func(ctx context.Context) {
			// Stop lifecycle observer first
			if cleanupLifecycle != nil {
				log.Println("[main] Stopping lifecycle observer")
				cleanupLifecycle()
			}
			
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
