package backup

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpenOrCreateFresh — no file on disk → create at defaultSize.
func TestOpenOrCreateFresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh.sav")
	size := 32768
	f, err := OpenOrCreate(path, []int{32768}, &size)
	if err != nil {
		t.Fatalf("OpenOrCreate: %v", err)
	}
	defer f.Close()
	if f.Size() != 32768 {
		t.Errorf("size = %d, want 32768", f.Size())
	}
	// Fresh save must be 0xFF-filled.
	if f.Read(0) != 0xFF || f.Read(32767) != 0xFF {
		t.Errorf("fresh save not 0xFF-filled")
	}
}

// TestOpenOrCreateAdoptsExistingSize — when an existing file matches
// any validSize, that size is adopted and the file is loaded
// (not truncated). Catches the regression that motivated this round:
// FLASH was passing a single-element validSizes and silently wiping
// 128K saves opened with a 64K hint.
func TestOpenOrCreateAdoptsExistingSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing128k.sav")
	// Seed a 128 KiB file with a recognizable marker byte.
	seed := make([]byte, 131072)
	for i := range seed {
		seed[i] = 0xAB
	}
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatal(err)
	}

	hint := 65536 // pretend the game DB hint was 64K
	f, err := OpenOrCreate(path, []int{65536, 131072}, &hint)
	if err != nil {
		t.Fatalf("OpenOrCreate: %v", err)
	}
	defer f.Close()
	if f.Size() != 131072 {
		t.Errorf("expected size promoted to 131072 (on-disk size), got %d", f.Size())
	}
	if hint != 131072 {
		t.Errorf("defaultSize pointer should be updated to 131072, got %d", hint)
	}
	// Existing content must be preserved (not wiped to 0xFF).
	if f.Read(0) != 0xAB || f.Read(131071) != 0xAB {
		t.Errorf("existing content wiped: byte0=%#x byteLast=%#x", f.Read(0), f.Read(131071))
	}
}

// TestOpenOrCreateNotMatching — file exists but its size matches none
// of validSizes → fresh save at defaultSize.
func TestOpenOrCreateNotMatching(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wrong-size.sav")
	if err := os.WriteFile(path, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	hint := 32768
	f, err := OpenOrCreate(path, []int{32768}, &hint)
	if err != nil {
		t.Fatalf("OpenOrCreate: %v", err)
	}
	defer f.Close()
	if f.Size() != 32768 {
		t.Errorf("size = %d, want 32768 (defaultSize, since 4096 didn't match)", f.Size())
	}
}

// TestSyncAndClose — Sync + Close should both succeed on a freshly
// opened file (no real assertion of fsync, but exercises the path).
func TestSyncAndClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sync.sav")
	size := 512
	f, err := OpenOrCreate(path, []int{512}, &size)
	if err != nil {
		t.Fatalf("OpenOrCreate: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Errorf("Sync: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Close on an already-closed file is a no-op (not an error).
	if err := f.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestFLASHDetectsBothSizes — full FLASH-cart round trip exercises
// the multi-size validSizes plumbing. Catches the bug fixed when we
// reverted from single-element to flashSaveSize[:].
func TestFLASHDetectsBothSizes(t *testing.T) {
	dir := t.TempDir()

	// Seed a 128K file.
	path := filepath.Join(dir, "flash128.sav")
	seed := make([]byte, 131072)
	for i := range seed {
		seed[i] = 0xCC
	}
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatal(err)
	}
	// Open with the 64K hint — the FLASH ctor should promote to 128K.
	f, err := NewFLASHWithFile(path, Flash64K)
	if err != nil {
		t.Fatalf("NewFLASHWithFile: %v", err)
	}
	defer f.Close()
	if f.size != Flash128K {
		t.Errorf("FlashSize promotion failed: got %v, want Flash128K", f.size)
	}
	if f.file.Size() != 131072 {
		t.Errorf("file.Size = %d, want 131072", f.file.Size())
	}
	// Content preserved.
	if f.file.Read(0) != 0xCC {
		t.Errorf("content wiped on FLASH open")
	}
}
