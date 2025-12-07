// Package fetcher provides HTTP streaming and caching implementations for remote files.
package fetcher

import (
	"context"
	"io"
)

// BackingStore represents a backing storage mechanism for a single remote file.
// It supports multiple concurrent opens (ref counting) and stateless ReadAt calls.
type BackingStore interface {
	// Size returns the total size of the file in bytes.
	Size() int64

	// ReadAt reads len(p) bytes at offset off (or fewer at EOF).
	// Blocks until data is available or an error occurs.
	// Must be safe for concurrent calls from multiple goroutines.
	ReadAt(ctx context.Context, p []byte, off int64) (n int, err error)

	// IncRef increments the reference count (called on FUSE Open).
	IncRef()

	// DecRef decrements the reference count (called on FUSE Release).
	// Returns the new reference count.
	DecRef() int32

	// RefCount returns the current reference count.
	RefCount() int32

	// Close releases underlying resources.
	// May delete temp files if refCount == 0.
	Close() error
}

// FetchMode specifies the backing store implementation to use.
type FetchMode string

const (
	// FetchModeRangeLRU uses HTTP Range requests with in-memory LRU caching.
	FetchModeRangeLRU FetchMode = "range-lru"

	// FetchModeTempFile downloads the entire file sequentially to a temp file.
	FetchModeTempFile FetchMode = "temp-file"
)

// StoreOptions configures backing store behavior.
type StoreOptions struct {
	// Mode specifies which backing store implementation to use.
	Mode FetchMode

	// TempDir specifies where to store temporary files (for temp-file mode).
	TempDir string

	// ChunkSize specifies the chunk size for fetching (default: 4MB).
	ChunkSize int

	// MaxConcurrentRequests limits concurrent HTTP requests (default: 4).
	MaxConcurrentRequests int

	// CacheSize specifies the LRU cache size in chunks (default: 8).
	CacheSize int
}

// DefaultStoreOptions returns the default store options.
func DefaultStoreOptions() StoreOptions {
	return StoreOptions{
		Mode:                  FetchModeRangeLRU,
		TempDir:               "",
		ChunkSize:             4 * 1024 * 1024, // 4MB
		MaxConcurrentRequests: 4,
		CacheSize:             8,
	}
}

// NewBackingStore creates a new backing store for the given URL and size.
func NewBackingStore(ctx context.Context, url string, size int64, opts StoreOptions) (BackingStore, error) {
	switch opts.Mode {
	case FetchModeTempFile:
		return NewTempFileStore(ctx, url, size, opts)
	case FetchModeRangeLRU, "":
		return NewRangeLRUStore(ctx, url, size, opts)
	default:
		return NewRangeLRUStore(ctx, url, size, opts)
	}
}

// Ensure EOF is available for implementations
var _ = io.EOF
