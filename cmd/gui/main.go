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
	"github.com/mmilitzer/fuse-stream-mvp/pkg/config"
	"github.com/mmilitzer/fuse-stream-mvp/ui"
)

func main() {
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
