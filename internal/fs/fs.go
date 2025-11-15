package fs

// FS will implement read-only FUSE filesystem with cgofuse in M2.
// This is a placeholder stub for M1.

type FS struct {
	mountpoint string
}

func New(mountpoint string) *FS {
	return &FS{mountpoint: mountpoint}
}
