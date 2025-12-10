//go:build !gui

package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/mmilitzer/fuse-stream-mvp/internal/api"
	"github.com/mmilitzer/fuse-stream-mvp/internal/daemon"
	"github.com/mmilitzer/fuse-stream-mvp/pkg/config"
)

var (
	mount   = flag.Bool("mount", false, "Mount the filesystem and keep alive (default mode)")
	unmount = flag.Bool("unmount", false, "Unmount the filesystem and exit")
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
		
		// Try diskutil on macOS first, then umount
		if err := exec.Command("diskutil", "unmount", "force", cfg.Mountpoint).Run(); err != nil {
			log.Printf("diskutil failed, trying umount: %v", err)
			if err := exec.Command("umount", "-f", cfg.Mountpoint).Run(); err != nil {
				log.Fatalf("Failed to unmount: %v", err)
			}
		}
		
		log.Printf("Successfully unmounted %s", cfg.Mountpoint)
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

	// Start daemon services (FUSE mount)
	if err := daemon.Start(ctx, cfg.Mountpoint, client, cfg); err != nil {
		log.Fatalf("Failed to start daemon: %v", err)
	}

	// Keep alive mode: mount and wait for signal
	log.Println("Filesystem mounted. Press Ctrl+C to unmount and exit.")
	log.Printf("Mountpoint: %s", cfg.Mountpoint)
	daemon.KeepAlive(ctx)
}
