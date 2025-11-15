package main

import (
	"context"
	"flag"
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

var headless = flag.Bool("headless", false, "Run in headless mode (no GUI, daemon only)")

func main() {
	flag.Parse()

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

	// Handle OS signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal")
		cancel()
	}()

	// Start daemon services (FUSE mount + HTTP API) in background
	if err := daemon.Start(ctx); err != nil {
		log.Fatalf("Failed to start daemon: %v", err)
	}

	// Headless mode: no GUI, just run daemon
	if *headless {
		daemon.KeepAlive(ctx)
		return
	}

	// GUI mode: launch Wails window
	client := api.NewClient(cfg.APIBase, cfg.ClientID, cfg.ClientSecret)
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
