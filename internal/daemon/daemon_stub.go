//go:build !fuse

package daemon

import (
	"context"
	"fmt"
	"log"

	"github.com/mmilitzer/fuse-stream-mvp/internal/api"
	"github.com/mmilitzer/fuse-stream-mvp/internal/fs"
)

func Start(ctx context.Context, mountpoint string, client *api.Client) error {
	log.Println("[daemon] FUSE support not compiled in (build with -tags fuse)")
	return fmt.Errorf("FUSE support not compiled in (build with -tags fuse)")
}

func GetFS() *fs.FS {
	return nil
}

func Shutdown() {
	// No-op
}

func KeepAlive(ctx context.Context) {
	log.Println("[daemon] FUSE support not compiled in")
	<-ctx.Done()
}
