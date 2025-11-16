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

var (
	headless = flag.Bool("headless", false, "Run in headless mode (no GUI, daemon only)")
	mount    = flag.Bool("mount", false, "Mount the filesystem and exit")
	unmount  = flag.Bool("unmount", false, "Unmount the filesystem and exit")
)

func main() {
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Handle --unmount flag
	if *unmount {
		log.Printf("Unmounting filesystem at %s...", cfg.Mountpoint)
		// Try to unmount using fusermount (Linux) or umount (macOS)
		cmd := "fusermount"
		args := []string{"-u", cfg.Mountpoint}
		if err := syscall.Exec("/usr/bin/"+cmd, append([]string{cmd}, args...), os.Environ()); err != nil {
			// Try umount on macOS
			cmd = "umount"
			if err := syscall.Exec("/sbin/"+cmd, append([]string{cmd}, cfg.Mountpoint), os.Environ()); err != nil {
				log.Fatalf("Failed to unmount: %v", err)
			}
		}
		return
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
	if err := daemon.Start(ctx, cfg.Mountpoint, client); err != nil {
		log.Fatalf("Failed to start daemon: %v", err)
	}

	// Handle --mount flag (mount and keep alive without GUI)
	if *mount {
		log.Println("Filesystem mounted. Press Ctrl+C to unmount and exit.")
		daemon.KeepAlive(ctx)
		return
	}

	// Headless mode: no GUI, just run daemon
	if *headless {
		daemon.KeepAlive(ctx)
		return
	}

	// GUI mode: launch Wails window
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
