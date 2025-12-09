package appdelegate

// CheckActiveUploadsFunc is a function type that checks if there are active uploads
type CheckActiveUploadsFunc func() bool

// UnmountFunc is a function type that handles unmounting the filesystem
type UnmountFunc func() error
