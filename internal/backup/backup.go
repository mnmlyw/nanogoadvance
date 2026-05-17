// Package backup — port of src/nba/include/nba/rom/backup/* and
// src/nba/src/hw/rom/backup/*.
//
// Backup is the interface every save type implements (SRAM, FLASH, EEPROM).
// Storage is in-memory only — file-backed persistence (BackupFile) lives in
// the upstream non-emulator-core layer and isn't required for tests.
package backup

import "github.com/mnmlyw/nanogoadvance/internal/scheduler"

// Backup is the interface mirroring nba::Backup.
type Backup interface {
	Reset()
	Read(addr uint32) uint8
	Write(addr uint32, value uint8)
}

// ---------------------------------------------------------------------
// SRAM — 32 KiB at 0x0E000000..0x0E007FFF (mirrored throughout).
// ---------------------------------------------------------------------

type SRAM struct {
	savePath string
	file     *BackupFile
}

// NewSRAM constructs an in-memory SRAM. Use NewSRAMWithFile to attach a
// .sav file. ⇄ SRAM::SRAM but split into two constructors because Go
// doesn't have default arguments.
func NewSRAM() *SRAM {
	s := &SRAM{}
	s.Reset()
	return s
}

func NewSRAMWithFile(savePath string) (*SRAM, error) {
	s := &SRAM{savePath: savePath}
	return s, s.resetWithError()
}

func (s *SRAM) Reset() {
	_ = s.resetWithError()
}

func (s *SRAM) resetWithError() error {
	bytes := 32768
	if s.savePath == "" {
		s.file = NewInMemory(bytes)
		return nil
	}
	f, err := OpenOrCreate(s.savePath, []int{32768}, &bytes)
	if err != nil {
		return err
	}
	s.file = f
	return nil
}

func (s *SRAM) Read(addr uint32) uint8     { return s.file.Read(uint(addr & 0x7FFF)) }
func (s *SRAM) Write(addr uint32, v uint8) { s.file.Write(uint(addr&0x7FFF), v) }

// Close flushes and closes the .sav file (if any). Safe to call on
// in-memory SRAM — it's a no-op.
func (s *SRAM) Close() error {
	if s.file == nil {
		return nil
	}
	return s.file.Close()
}

// ---------------------------------------------------------------------
// FLASH — 64K (Atmel/SST) or 128K (Macronix, banked).
// Command sequence: write 0xAA to 0x0E005555, then 0x55 to 0x0E002AAA, then
// command byte at 0x0E005555.
// ---------------------------------------------------------------------

type FlashSize int

const (
	Flash64K  FlashSize = 0
	Flash128K FlashSize = 1
)

const (
	cmdReadChipID   uint8 = 0x90
	cmdFinishChipID uint8 = 0xF0
	cmdErase        uint8 = 0x80
	cmdEraseChip    uint8 = 0x10
	cmdEraseSector  uint8 = 0x30
	cmdWriteByte    uint8 = 0xA0
	cmdSelectBank   uint8 = 0xB0
)

var flashSaveSize = [2]int{65536, 131072}

type FLASH struct {
	size     FlashSize
	savePath string
	file     *BackupFile

	currentBank   int
	phase         int
	enableChipID  bool
	enableErase   bool
	enableWrite   bool
	enableSelect  bool
}

func NewFLASH(sizeHint FlashSize) *FLASH {
	f := &FLASH{size: sizeHint}
	f.Reset()
	return f
}

func NewFLASHWithFile(savePath string, sizeHint FlashSize) (*FLASH, error) {
	f := &FLASH{size: sizeHint, savePath: savePath}
	return f, f.resetWithError()
}

func (f *FLASH) Reset() { _ = f.resetWithError() }

func (f *FLASH) resetWithError() error {
	f.currentBank = 0
	f.phase = 0
	f.enableChipID = false
	f.enableErase = false
	f.enableWrite = false
	f.enableSelect = false
	bytes := flashSaveSize[f.size]
	if f.savePath == "" {
		f.file = NewInMemory(bytes)
		return nil
	}
	bf, err := OpenOrCreate(f.savePath, []int{bytes}, &bytes)
	if err != nil {
		return err
	}
	f.file = bf
	return nil
}

func (f *FLASH) physical(index int) int { return f.currentBank*65536 + index }

func (f *FLASH) Read(addr uint32) uint8 {
	addr &= 0xFFFF
	if f.enableChipID && addr < 2 {
		if f.size == Flash128K {
			if addr == 0 {
				return 0xC2 // Macronix
			}
			return 0x09
		}
		// Flash64K — SST.
		if addr == 0 {
			return 0xBF
		}
		return 0xD4
	}
	return f.file.Read(uint(f.physical(int(addr))))
}

// Close flushes and closes the .sav file (if any).
func (f *FLASH) Close() error {
	if f.file == nil {
		return nil
	}
	return f.file.Close()
}

func (f *FLASH) Write(addr uint32, value uint8) {
	switch f.phase {
	case 0:
		if addr == 0x0E005555 && value == 0xAA {
			f.phase = 1
		}
	case 1:
		if addr == 0x0E002AAA && value == 0x55 {
			f.phase = 2
		}
	case 2:
		f.handleCommand(addr, value)
	case 3:
		f.handleExtended(addr, value)
	}
}

func (f *FLASH) handleCommand(addr uint32, value uint8) {
	if addr == 0x0E005555 {
		switch value {
		case cmdReadChipID:
			f.enableChipID = true
			f.phase = 0
		case cmdFinishChipID:
			f.enableChipID = false
			f.phase = 0
		case cmdErase:
			f.enableErase = true
			f.phase = 0
		case cmdEraseChip:
			if f.enableErase {
				f.file.MemorySet(0, f.file.Size(), 0xFF)
				f.enableErase = false
			}
			f.phase = 0
		case cmdWriteByte:
			f.enableWrite = true
			f.phase = 3
		case cmdSelectBank:
			if f.size == Flash128K {
				f.enableSelect = true
				f.phase = 3
			} else {
				f.phase = 0
			}
		}
		return
	}
	if f.enableErase && (addr&^0xF000) == 0x0E000000 && value == cmdEraseSector {
		base := int(addr & 0xF000)
		phys := f.physical(base)
		f.file.MemorySet(phys, 0x1000, 0xFF)
		f.enableErase = false
		f.phase = 0
	}
}

func (f *FLASH) handleExtended(addr uint32, value uint8) {
	if f.enableWrite {
		f.file.Write(uint(f.physical(int(addr&0xFFFF))), value)
		f.enableWrite = false
	} else if f.enableSelect && addr == 0x0E000000 {
		f.currentBank = int(value & 1)
		f.enableSelect = false
	}
	f.phase = 0
}

// ---------------------------------------------------------------------
// EEPROM — 512 B (4K-bit) or 8 KiB (64K-bit). Serial protocol on D0.
// ---------------------------------------------------------------------

type EEPROMSize int

const (
	EEPROM4K   EEPROMSize = 0
	EEPROM64K  EEPROMSize = 1
	EEPROMAuto EEPROMSize = 2 // size determined by first DMA
)

const (
	stAcceptCommand = 1 << 0
	stReadMode      = 1 << 1
	stWriteMode     = 1 << 2
	stGetAddress    = 1 << 3
	stReading       = 1 << 4
	stDummyNibble   = 1 << 5
	stWriting       = 1 << 6
	stEatDummy      = 1 << 7
	stBusy          = 1 << 8
)

var eepromAddrBits = [2]int{6, 14}
var eepromSaveSize = [2]int{512, 8192}

type EEPROM struct {
	size            EEPROMSize
	scheduler       *scheduler.Scheduler
	savePath        string
	file            *BackupFile
	state           int
	address         int
	serialBuffer    uint64
	transmittedBits int
	detectSize      bool
}

func NewEEPROM(sizeHint EEPROMSize, s *scheduler.Scheduler) *EEPROM {
	e := &EEPROM{size: sizeHint, scheduler: s}
	// Register the "EEPROM ready after write" callback so the timing
	// delay survives save-state round trip.
	s.Register(scheduler.EventClassEEPROMReady, func(uint64) {
		e.state = stAcceptCommand
	})
	e.Reset()
	return e
}

func NewEEPROMWithFile(savePath string, sizeHint EEPROMSize, s *scheduler.Scheduler) (*EEPROM, error) {
	e := &EEPROM{size: sizeHint, scheduler: s, savePath: savePath}
	s.Register(scheduler.EventClassEEPROMReady, func(uint64) {
		e.state = stAcceptCommand
	})
	return e, e.resetWithError()
}

func (e *EEPROM) Reset() { _ = e.resetWithError() }

func (e *EEPROM) resetWithError() error {
	e.state = stAcceptCommand
	e.address = 0
	e.resetSerialBuffer()
	if e.size == EEPROMAuto {
		e.size = EEPROM64K
		e.detectSize = true
	} else {
		e.detectSize = false
	}
	bytes := eepromSaveSize[e.size]
	if e.savePath == "" {
		e.file = NewInMemory(bytes)
		return nil
	}
	// Both 512B and 8KiB are valid; upstream accepts either via the
	// validSizes list.
	f, err := OpenOrCreate(e.savePath, []int{512, 8192}, &bytes)
	if err != nil {
		return err
	}
	e.file = f
	// If the on-disk file dictated a different size, sync our enum.
	if bytes == 512 {
		e.size = EEPROM4K
		e.detectSize = false
	} else if bytes == 8192 {
		e.size = EEPROM64K
		e.detectSize = false
	}
	return nil
}

// Close flushes and closes the .sav file (if any).
func (e *EEPROM) Close() error {
	if e.file == nil {
		return nil
	}
	return e.file.Close()
}

func (e *EEPROM) resetSerialBuffer() {
	e.serialBuffer = 0
	e.transmittedBits = 0
}

func (e *EEPROM) Read(_ uint32) uint8 {
	if e.state&stReading != 0 {
		if e.state&stDummyNibble != 0 {
			e.transmittedBits++
			if e.transmittedBits == 4 {
				e.state &^= stDummyNibble
				e.resetSerialBuffer()
			}
			return 0
		}
		bit := e.transmittedBits % 8
		index := e.transmittedBits / 8
		e.transmittedBits++
		if e.transmittedBits == 64 {
			e.state = stAcceptCommand
			e.resetSerialBuffer()
		}
		return (e.file.Read(uint(e.address+index)) >> (7 - bit)) & 1
	}
	if e.state&stBusy != 0 {
		return 0
	}
	return 1
}

func (e *EEPROM) Write(_ uint32, value uint8) {
	if e.state&(stReading|stBusy) != 0 {
		return
	}
	value &= 1
	e.serialBuffer = (e.serialBuffer << 1) | uint64(value)
	e.transmittedBits++

	if e.state == stAcceptCommand && e.transmittedBits == 2 {
		switch e.serialBuffer {
		case 2:
			e.state = stWriteMode | stGetAddress | stWriting | stEatDummy
		case 3:
			e.state = stReadMode | stGetAddress | stEatDummy
		}
		e.resetSerialBuffer()
		return
	}
	if e.state&stGetAddress != 0 {
		if e.transmittedBits == eepromAddrBits[e.size] {
			e.address = int(e.serialBuffer*8) & 0x1FFF
			if e.state&stWriteMode != 0 {
				e.file.MemorySet(e.address, 8, 0)
			}
			e.state &^= stGetAddress
			e.resetSerialBuffer()
		}
		return
	}
	if e.state&stWriting != 0 {
		bit := (e.transmittedBits - 1) % 8
		index := (e.transmittedBits - 1) / 8
		prev := e.file.Read(uint(e.address + index))
		e.file.Write(uint(e.address+index), prev|(value<<(7-bit)))
		if e.transmittedBits == 64 {
			e.state &^= stWriting
			e.resetSerialBuffer()
		}
		return
	}
	if e.state&stEatDummy != 0 {
		e.state &^= stEatDummy
		if e.state&stReadMode != 0 {
			e.state |= stReading | stDummyNibble
		} else if e.state&stWriteMode != 0 {
			e.state = stBusy
			e.scheduler.AddClass(101400, scheduler.EventClassEEPROMReady, 0, 0)
		}
		e.resetSerialBuffer()
	}
}

// SetSizeHint ⇄ EEPROM::SetSizeHint. Called by DMA on the first cart-bus
// EEPROM transfer once the address-bit count is known. If the existing
// BackupFile is the wrong size we re-open it via OpenOrCreate (matches
// upstream's `file = BackupFile::OpenOrCreate(save_path, {bytes}, bytes)`)
// for both file-backed and in-memory cases.
func (e *EEPROM) SetSizeHint(s int) {
	if !e.detectSize {
		return
	}
	bytes := eepromSaveSize[s]
	e.size = EEPROMSize(s)
	e.detectSize = false
	if e.file.Size() == bytes {
		return
	}
	if e.savePath == "" {
		e.file = NewInMemory(bytes)
		return
	}
	if f, err := OpenOrCreate(e.savePath, []int{bytes}, &bytes); err == nil {
		e.file = f
	} else {
		e.file = NewInMemory(bytes)
	}
}
