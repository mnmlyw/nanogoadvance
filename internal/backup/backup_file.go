// backup_file.go ⇄ src/nba/include/nba/rom/backup/backup_file.hh
//
// Port of nba::BackupFile — a 1:1 memory-mapped save file. When
// auto_update is set, every write is mirrored to disk immediately, which
// matches the upstream behavior (FLASH/SRAM both have it on).
package backup

import (
	"fmt"
	"io"
	"os"
)

type BackupFile struct {
	saveSize   int
	stream     *os.File
	memory     []uint8
	AutoUpdate bool
}

// NewInMemory creates a BackupFile with no on-disk backing — used by the
// test harness and by carts where the user hasn't supplied a save path.
func NewInMemory(size int) *BackupFile {
	f := &BackupFile{
		saveSize:   size,
		memory:     make([]uint8, size),
		AutoUpdate: true,
	}
	for i := range f.memory {
		f.memory[i] = 0xFF
	}
	return f
}

// OpenOrCreate ⇄ BackupFile::OpenOrCreate.
//
// validSizes is the list of acceptable on-disk sizes for this save type
// (so e.g. FLASH-64 accepts a 65536-byte file, FLASH-128 accepts a
// 131072-byte file). defaultSize is the size to create the file at when
// none exists; if an existing file's size matches a validSize, it's
// adopted and defaultSize is updated to that match.
func OpenOrCreate(savePath string, validSizes []int, defaultSize *int) (*BackupFile, error) {
	f := &BackupFile{AutoUpdate: true}

	if fi, err := os.Stat(savePath); err == nil && fi.Mode().IsRegular() {
		// Allow trailing slack for mGBA compatibility.
		fileSize := fi.Size()
		saveSize := int(fileSize) &^ 63
		for _, vs := range validSizes {
			if vs == saveSize {
				stream, err := os.OpenFile(savePath, os.O_RDWR, 0644)
				if err != nil {
					return nil, fmt.Errorf("BackupFile: unable to open file: %s: %w", savePath, err)
				}
				f.stream = stream
				f.saveSize = saveSize
				*defaultSize = saveSize
				f.memory = make([]uint8, fileSize)
				if _, err := io.ReadFull(stream, f.memory); err != nil && err != io.ErrUnexpectedEOF {
					return nil, fmt.Errorf("BackupFile: read failed: %w", err)
				}
				return f, nil
			}
		}
	}

	// Fresh save: 0xFF-filled, truncated to defaultSize.
	stream, err := os.OpenFile(savePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("BackupFile: unable to create file: %s: %w", savePath, err)
	}
	f.stream = stream
	f.saveSize = *defaultSize
	f.memory = make([]uint8, *defaultSize)
	f.MemorySet(0, *defaultSize, 0xFF)
	return f, nil
}

func (f *BackupFile) Read(index uint) uint8 {
	if int(index) >= f.saveSize {
		panic(fmt.Sprintf("BackupFile: out-of-bounds index %d (size %d) on read", index, f.saveSize))
	}
	return f.memory[index]
}

func (f *BackupFile) Write(index uint, value uint8) {
	if int(index) >= f.saveSize {
		panic(fmt.Sprintf("BackupFile: out-of-bounds index %d (size %d) on write", index, f.saveSize))
	}
	f.memory[index] = value
	if f.AutoUpdate {
		f.Update(int(index), 1)
	}
}

func (f *BackupFile) MemorySet(index, length int, value uint8) {
	if index+length > f.saveSize {
		panic(fmt.Sprintf("BackupFile: out-of-bounds memset [%d..%d) (size %d)", index, index+length, f.saveSize))
	}
	for i := range length {
		f.memory[index+i] = value
	}
	if f.AutoUpdate {
		f.Update(index, length)
	}
}

// Update ⇄ BackupFile::Update — write a slice of memory back to disk.
func (f *BackupFile) Update(index, length int) {
	if index+length > f.saveSize {
		panic("BackupFile: out-of-bounds update")
	}
	if f.stream == nil {
		return
	}
	if _, err := f.stream.WriteAt(f.memory[index:index+length], int64(index)); err != nil {
		// Match upstream: errors during update aren't fatal — log and continue.
		fmt.Fprintf(os.Stderr, "BackupFile: write error: %v\n", err)
	}
}

func (f *BackupFile) Buffer() []uint8 { return f.memory }
func (f *BackupFile) Size() int       { return f.saveSize }

// Sync forces any buffered writes through the OS page cache to disk.
// Cheap to call; intended for shutdown / signal paths where durability
// matters more than throughput.
func (f *BackupFile) Sync() error {
	if f.stream == nil {
		return nil
	}
	return f.stream.Sync()
}

// Close flushes and closes the backing file. The upstream destructor
// implicitly handles this via std::fstream's destructor.
func (f *BackupFile) Close() error {
	if f.stream == nil {
		return nil
	}
	syncErr := f.stream.Sync()
	closeErr := f.stream.Close()
	f.stream = nil
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
