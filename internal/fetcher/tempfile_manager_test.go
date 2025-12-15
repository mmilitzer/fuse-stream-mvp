package fetcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupStaleFiles(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "fsmvp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create some fake stale temp files
	staleFiles := []string{
		filepath.Join(tempDir, "fsmvp-old1.tmp"),
		filepath.Join(tempDir, "fsmvp-old2.tmp"),
		filepath.Join(tempDir, "fsmvp-old3.tmp"),
	}

	for _, path := range staleFiles {
		if err := os.WriteFile(path, []byte("test data"), 0600); err != nil {
			t.Fatalf("Failed to create stale file %s: %v", path, err)
		}
	}

	// Also create a non-matching file that should NOT be deleted
	nonMatchingFile := filepath.Join(tempDir, "other-file.txt")
	if err := os.WriteFile(nonMatchingFile, []byte("keep me"), 0600); err != nil {
		t.Fatalf("Failed to create non-matching file: %v", err)
	}

	// Create manager and cleanup
	manager := &TempFileManager{
		tempDir: tempDir,
		files:   make(map[string]*TempFileInfo),
	}

	if err := manager.CleanupStaleFiles(); err != nil {
		t.Fatalf("CleanupStaleFiles failed: %v", err)
	}

	// Check that stale files were deleted
	for _, path := range staleFiles {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("Stale file %s was not deleted", path)
		}
	}

	// Check that non-matching file still exists
	if _, err := os.Stat(nonMatchingFile); os.IsNotExist(err) {
		t.Error("Non-matching file was incorrectly deleted")
	}
}

func TestRegisterAndUnregister(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "fsmvp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	manager := &TempFileManager{
		tempDir: tempDir,
		files:   make(map[string]*TempFileInfo),
	}

	path := "/tmp/test.tmp"
	size := int64(1024)

	// Register
	manager.Register(path, size, nil)

	if len(manager.files) != 1 {
		t.Errorf("Expected 1 file registered, got %d", len(manager.files))
	}

	if info, exists := manager.files[path]; !exists {
		t.Error("File was not registered")
	} else {
		if info.Size != size {
			t.Errorf("Expected size %d, got %d", size, info.Size)
		}
	}

	// Unregister
	manager.Unregister(path)

	if len(manager.files) != 0 {
		t.Errorf("Expected 0 files after unregister, got %d", len(manager.files))
	}
}

func TestUpdateAccess(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "fsmvp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	manager := &TempFileManager{
		tempDir: tempDir,
		files:   make(map[string]*TempFileInfo),
	}

	path := "/tmp/test.tmp"
	size := int64(1024)

	manager.Register(path, size, nil)
	
	originalTime := manager.files[path].LastAccess
	time.Sleep(10 * time.Millisecond)
	
	manager.UpdateAccess(path)
	
	newTime := manager.files[path].LastAccess
	
	if !newTime.After(originalTime) {
		t.Error("LastAccess time was not updated")
	}
}

func TestEnsureSpaceAvailable(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "fsmvp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	manager := &TempFileManager{
		tempDir: tempDir,
		files:   make(map[string]*TempFileInfo),
	}

	// Test with a small file size (should succeed)
	smallSize := int64(1024) // 1KB
	if err := manager.EnsureSpaceAvailable(smallSize); err != nil {
		t.Errorf("EnsureSpaceAvailable failed for small file: %v", err)
	}

	// Register multiple files to test eviction
	for i := 0; i < 5; i++ {
		path := filepath.Join(tempDir, "fsmvp-test-file"+string(rune('0'+i))+".tmp")
		// Create actual files
		if err := os.WriteFile(path, []byte("test data"), 0600); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		manager.Register(path, int64(100), nil)
		time.Sleep(1 * time.Millisecond) // Ensure different access times
	}

	if manager.GetFileCount() != 5 {
		t.Errorf("Expected 5 files, got %d", manager.GetFileCount())
	}
}

func TestMaxTempFiles(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "fsmvp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	manager := &TempFileManager{
		tempDir: tempDir,
		files:   make(map[string]*TempFileInfo),
	}

	// Register MaxTempFiles files
	for i := 0; i < MaxTempFiles; i++ {
		path := filepath.Join(tempDir, "fsmvp-test-"+string(rune('a'+i))+".tmp")
		// Create actual files so eviction can delete them
		if err := os.WriteFile(path, []byte("test data"), 0600); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		manager.Register(path, int64(100), nil)
		time.Sleep(1 * time.Millisecond) // Ensure different access times
	}

	if manager.GetFileCount() != MaxTempFiles {
		t.Errorf("Expected %d files, got %d", MaxTempFiles, manager.GetFileCount())
	}

	// Try to ensure space for another file - should succeed by evicting old ones
	if err := manager.EnsureSpaceAvailable(int64(100)); err != nil {
		t.Errorf("EnsureSpaceAvailable failed: %v", err)
	}

	// After eviction, should have fewer files
	if manager.GetFileCount() >= MaxTempFiles {
		t.Errorf("Expected fewer than %d files after eviction, got %d", MaxTempFiles, manager.GetFileCount())
	}
}

func TestCleanupAllFiles(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "fsmvp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	manager := &TempFileManager{
		tempDir: tempDir,
		files:   make(map[string]*TempFileInfo),
	}

	// Create and register several temp files
	testFiles := make([]string, 5)
	for i := 0; i < 5; i++ {
		path := filepath.Join(tempDir, "fsmvp-cleanup-test-"+string(rune('a'+i))+".tmp")
		testFiles[i] = path
		
		// Create actual files
		if err := os.WriteFile(path, []byte("test data for cleanup"), 0600); err != nil {
			t.Fatalf("Failed to create test file %s: %v", path, err)
		}
		
		// Register with manager
		manager.Register(path, int64(100), nil)
	}

	// Verify files are registered
	if manager.GetFileCount() != 5 {
		t.Errorf("Expected 5 files registered, got %d", manager.GetFileCount())
	}

	// Verify files exist on disk
	for _, path := range testFiles {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Test file %s should exist before cleanup", path)
		}
	}

	// Call CleanupAllFiles
	if err := manager.CleanupAllFiles(); err != nil {
		t.Errorf("CleanupAllFiles failed: %v", err)
	}

	// Verify all files were deleted from disk
	for _, path := range testFiles {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("File %s should be deleted after CleanupAllFiles", path)
		}
	}

	// Verify manager's tracking map is cleared
	if manager.GetFileCount() != 0 {
		t.Errorf("Expected 0 files after CleanupAllFiles, got %d", manager.GetFileCount())
	}
}

func TestCleanupAllFiles_WithNonExistentFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "fsmvp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	manager := &TempFileManager{
		tempDir: tempDir,
		files:   make(map[string]*TempFileInfo),
	}

	// Register a file that doesn't actually exist on disk
	nonExistentPath := filepath.Join(tempDir, "fsmvp-nonexistent.tmp")
	manager.Register(nonExistentPath, int64(100), nil)

	// Create another file that does exist
	existingPath := filepath.Join(tempDir, "fsmvp-existing.tmp")
	if err := os.WriteFile(existingPath, []byte("test data"), 0600); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	manager.Register(existingPath, int64(100), nil)

	// CleanupAllFiles should handle the non-existent file gracefully
	if err := manager.CleanupAllFiles(); err != nil {
		t.Errorf("CleanupAllFiles should not fail when a file doesn't exist: %v", err)
	}

	// Verify the existing file was deleted
	if _, err := os.Stat(existingPath); !os.IsNotExist(err) {
		t.Errorf("Existing file should be deleted")
	}

	// Verify tracking map is cleared
	if manager.GetFileCount() != 0 {
		t.Errorf("Expected 0 files after CleanupAllFiles, got %d", manager.GetFileCount())
	}
}
