// Package bus — port of src/nba/src/bus/bus.{hh,cc}.
//
// Upstream uses C++ templates (`Read<T>` / `Write<T>`) to dispatch by access
// width. Go has no templates, so we have explicit Read/Write{Byte,Half,Word}
// paths — each one handles every memory region directly without delegating
// to narrower widths. This is critical: the 8-bit palette/VRAM byte-duplication
// quirk MUST NOT leak into 16- or 32-bit writes.
package bus

import (
	"encoding/binary"

	"github.com/mnmlyw/nanogoadvance/internal/scheduler"
)

// Access flags — match nba::core::Bus::Access. Treated as a bitmask.
type Access int

const (
	AccessNonsequential Access = 0
	AccessSequential    Access = 1
	AccessCode          Access = 2
	AccessDma           Access = 4
	AccessLock          Access = 8
)

// IOHandler dispatches I/O register reads/writes (address page 0x04).
type IOHandler interface {
	IORead8(addr uint32) (uint8, bool)
	IORead16(addr uint32) (uint16, bool)
	IORead32(addr uint32) (uint32, bool)
	IOWrite8(addr uint32, v uint8) bool
	IOWrite16(addr uint32, v uint16) bool
	IOWrite32(addr uint32, v uint32) bool
}

// Backup is the slice of internal/backup.Backup the bus actually needs.
// Defined here to avoid an import cycle (internal/bus must not depend on
// the backup package since the core wires them together).
type Backup interface {
	Read(addr uint32) uint8
	Write(addr uint32, value uint8)
}

// GPIO is the slice of internal/gpio.GPIO the bus needs to route the cart
// GPIO range (0x080000C4-C8) — mirrors nba::GPIO. Defined here to avoid an
// import cycle.
type GPIO interface {
	IsReadable() bool
	Read(addr uint32) uint8
	Write(addr uint32, value uint8)
}

// Waitcnt ⇄ Bus::Hardware::waitcnt (bus/io.hh). Holds the latched WAITCNT
// register fields that drive UpdateWaitStateTable.
type Waitcnt struct {
	SRAM     uint8
	WS0      [2]uint8 // [0]=N (2 bits), [1]=S (1 bit)
	WS1      [2]uint8
	WS2      [2]uint8
	PHI      uint8 // PHI terminal output (2 bits)
	Prefetch bool
	CGB      bool
}

// HaltControl ⇄ HaltControl enum in bus/io.hh.
type HaltControl int

const (
	HaltControlRun HaltControl = iota
	HaltControlHalt
	HaltControlStop
)

type Bus struct {
	Scheduler *scheduler.Scheduler

	BIOS    [0x4000]uint8
	EWRAM   [0x40000]uint8
	IWRAM   [0x8000]uint8
	Palette [0x400]uint8
	VRAM    [0x18000]uint8
	OAM     [0x400]uint8
	ROM     []uint8

	BIOSLatch uint32

	// ROMMask ⇄ ROM::rom_mask. Set to 0x01FFFFFF by default; for carts
	// flagged `mirror: true` in the game DB, this is the rounded-up
	// power-of-two cart size minus 1, which makes reads past the cart
	// wrap inside the ROM.
	ROMMask uint32

	// ROMAddressLatch ⇄ ROM::rom_address_latch. Auto-increments on
	// sequential reads; non-sequential reads overwrite it. Open-bus
	// reads past the cart return (latch >> 1).
	ROMAddressLatch uint32

	// SRAM/FLASH live at 0x0E000000-0x0FFFFFFF; only one is active per cart.
	BackupSRAM Backup

	// EEPROM lives on the cart bus at a cart-size-dependent address mask
	// (>=16MB ROM → 0x01FFFF00; smaller → 0x01000000). Only one of
	// BackupSRAM / BackupEEPROM is non-nil per cart.
	BackupEEPROM Backup
	EEPROMMask   uint32

	// Cart GPIO (RTC, solar, etc.). Nil for carts without a GPIO chip.
	GPIO GPIO

	// WAITCNT ⇄ Bus::Hardware::waitcnt + bus->UpdateWaitStateTable().
	Waitcnt Waitcnt

	// HALTCNT / POSTFLG ⇄ Bus::Hardware fields.
	Haltcnt HaltControl
	Postflg uint8

	// PrefetchBufferWasDisabled ⇄ Bus::Hardware::prefetch_buffer_was_disabled.
	// Set when WAITCNT.prefetch transitions 1->0; consumed by Bus.Prefetch.
	PrefetchBufferWasDisabled bool

	// Prefetch buffer ⇄ Bus::prefetch (bus/bus.hh).
	prefetch prefetchBuffer

	// LastAccess ⇄ Bus::last_access. Drives the "switching out of DMA mode
	// forces a non-sequential ROM access" rule.
	LastAccess Access

	// CPUStateHook / CPUR15Hook give Bus.Prefetch / Bus.StopPrefetch the
	// bits of ARM state they need (CPSR.thumb and r15) without creating an
	// arm→bus→arm import cycle. Wired by core.New.
	CPUStateHook func() bool
	CPUR15Hook   func() uint32

	// CPUFetchedOpcodeHook ⇄ ARM7TDMI::GetFetchedOpcode(slot).
	// Used by ReadOpenBus to synthesise open-bus values from the pipe.
	CPUFetchedOpcodeHook func(slot int) uint32

	// DMAOpenBusHook ⇄ DMA::GetOpenBusValue. Returns the DMA latch
	// (last word the DMA controller transferred). Used by ReadOpenBus
	// when last_access has the Dma flag set.
	DMAOpenBusHook func() uint32

	// PPUSpriteVRAMBoundaryHook ⇄ PPU::GetSpriteVRAMBoundary. Returns
	// 0x14000 for bitmap modes (3/4/5), 0x10000 otherwise. Used by VRAM
	// byte-write logic to decide BG-region (byte-duplicate) vs OBJ-region
	// (drop). Defaults to 0x10000 if unset.
	PPUSpriteVRAMBoundaryHook func() uint32

	// DMAStateHook / DMARunHook expose the DMA controller's running-state
	// and Run() entry to the bus, so CPU memory accesses can drain DMA
	// and CPU internal cycles can run in parallel with DMA until the
	// next bus access. Wired by core.New to break the bus→dma→bus cycle.
	DMAStateHook func() bool
	DMARunHook   func() int64

	// PPUSyncHook + DidAccess* hooks model PPU/CPU memory contention.
	// PPUSyncHook brings the PPU state machines up to the current
	// scheduler timestamp; each DidAccess* returns true if the PPU
	// touched that memory region in the current cycle. ⇄ upstream's
	// hw.ppu.Sync() + DidAccess{PRAM,VRAM_BG,VRAM_OBJ,OAM}.
	PPUSyncHook          func()
	DidAccessPRAMHook    func() bool
	DidAccessVRAMBGHook  func() bool
	DidAccessVRAMOBJHook func() bool
	DidAccessOAMHook     func() bool

	// ParallelInternalCPUCycleLimit ⇄ Bus::parallel_internal_cpu_cycle_limit.
	// Number of CPU internal cycles that can run free (zero scheduler
	// cost) while DMA still occupies the bus. Bus.Idle decrements it;
	// any Read/Write resets it to 0.
	ParallelInternalCPUCycleLimit int64

	devices []IOHandler

	wait16 [2][16]int64
	wait32 [2][16]int64
}

// prefetchBuffer ⇄ Bus::prefetch struct in bus/bus.hh. Tracks the in-flight
// gamepak prefetch (active, capacity, countdown, addresses).
type prefetchBuffer struct {
	active      bool
	thumb       bool
	opcodeWidth uint32
	capacity    int
	count       int
	countdown   int64
	duty        int64
	lastAddress uint32 // address of the slot currently being fetched
	headAddress uint32 // address of the next slot to dispense
}

func New(s *scheduler.Scheduler) *Bus {
	b := &Bus{Scheduler: s}
	// Defaults matching upstream's initializer-list for wait16/wait32. The
	// ROM (pages 0x8-0xD) and SRAM (0xE/0xF) rows are filled in by
	// UpdateWaitStateTable; these only set the fixed non-cart rows.
	b.wait16 = [2][16]int64{
		{1, 1, 3, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 1},
		{1, 1, 3, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 1},
	}
	b.wait32 = [2][16]int64{
		{1, 1, 6, 1, 1, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 1},
		{1, 1, 6, 1, 1, 2, 2, 1, 0, 0, 0, 0, 0, 0, 0, 1},
	}
	b.UpdateWaitStateTable()
	return b
}

// UpdateWaitStateTable ⇄ Bus::UpdateWaitStateTable (bus/timing.cc).
//
// Recomputes wait16/wait32 for the cart ROM (WS0/WS1/WS2 mirrored across
// pages 0x8..0xD) and SRAM (pages 0xE/0xF) from the current Waitcnt
// register, using the same nseq/seq tables as upstream.
func (b *Bus) UpdateWaitStateTable() {
	nseq := [4]int64{5, 4, 3, 9}
	seq0 := [2]int64{3, 2}
	seq1 := [2]int64{5, 2}
	seq2 := [2]int64{9, 2}
	const n = 0 // Access::Nonsequential
	const s = 1 // Access::Sequential
	w := &b.Waitcnt
	sram := nseq[w.SRAM]
	for i := range 2 {
		// ROM WS0/WS1/WS2 — 16-bit non-sequential
		b.wait16[n][0x8+i] = nseq[w.WS0[n]]
		b.wait16[n][0xA+i] = nseq[w.WS1[n]]
		b.wait16[n][0xC+i] = nseq[w.WS2[n]]
		// ROM WS0/WS1/WS2 — 16-bit sequential
		b.wait16[s][0x8+i] = seq0[w.WS0[s]]
		b.wait16[s][0xA+i] = seq1[w.WS1[s]]
		b.wait16[s][0xC+i] = seq2[w.WS2[s]]
		// ROM 32-bit non-sequential = 1N + 1S
		b.wait32[n][0x8+i] = b.wait16[n][0x8] + b.wait16[s][0x8]
		b.wait32[n][0xA+i] = b.wait16[n][0xA] + b.wait16[s][0xA]
		b.wait32[n][0xC+i] = b.wait16[n][0xC] + b.wait16[s][0xC]
		// ROM 32-bit sequential = 2S
		b.wait32[s][0x8+i] = b.wait16[s][0x8] * 2
		b.wait32[s][0xA+i] = b.wait16[s][0xA] * 2
		b.wait32[s][0xC+i] = b.wait16[s][0xC] * 2
		// SRAM
		b.wait16[n][0xE+i] = sram
		b.wait32[n][0xE+i] = sram
		b.wait16[s][0xE+i] = sram
		b.wait32[s][0xE+i] = sram
	}
}

// WriteWaitcnt0 / WriteWaitcnt1 ⇄ the WAITCNT writes in io.cc (case 0x204
// and 0x205). Split into two byte writes because that's how the IO dispatch
// is structured.
func (b *Bus) WriteWaitcnt0(value uint8) {
	w := &b.Waitcnt
	w.SRAM = value & 3
	w.WS0[0] = (value >> 2) & 3
	w.WS0[1] = (value >> 4) & 1
	w.WS1[0] = (value >> 5) & 3
	w.WS1[1] = (value >> 7) & 1
	b.UpdateWaitStateTable()
}

func (b *Bus) WriteWaitcnt1(value uint8) {
	w := &b.Waitcnt
	prefetchOld := w.Prefetch
	w.WS2[0] = value & 3
	w.WS2[1] = (value >> 2) & 1
	w.PHI = (value >> 3) & 3
	w.Prefetch = (value>>6)&1 != 0
	if prefetchOld && !w.Prefetch {
		b.PrefetchBufferWasDisabled = true
	}
	b.UpdateWaitStateTable()
}

// ReadWaitcnt0 / ReadWaitcnt1 ⇄ the WAITCNT reads in io.cc.
func (b *Bus) ReadWaitcnt0() uint8 {
	w := &b.Waitcnt
	return w.SRAM |
		(w.WS0[0] << 2) | (w.WS0[1] << 4) |
		(w.WS1[0] << 5) | (w.WS1[1] << 7)
}

func (b *Bus) ReadWaitcnt1() uint8 {
	w := &b.Waitcnt
	v := w.WS2[0] | (w.WS2[1] << 2) | (w.PHI << 3)
	if w.Prefetch {
		v |= 64
	}
	if w.CGB {
		v |= 128
	}
	return v
}

func (b *Bus) RegisterIO(d IOHandler) { b.devices = append(b.devices, d) }

// Reset ⇄ Bus::Reset (bus.cc). Zeros WRAM/IRAM and the IO register
// shadow state — PRAM/VRAM/OAM are owned by the PPU in upstream and
// reset by PPU::Reset, so we leave them alone here (PPU.Reset will
// re-zero them right after via its slice views).
func (b *Bus) Reset() {
	for i := range b.EWRAM {
		b.EWRAM[i] = 0
	}
	for i := range b.IWRAM {
		b.IWRAM[i] = 0
	}
	b.BIOSLatch = 0
	b.Haltcnt = HaltControlRun
	b.Postflg = 0
	b.Waitcnt = Waitcnt{}
	b.PrefetchBufferWasDisabled = false
	b.prefetch = prefetchBuffer{}
	b.LastAccess = 0
	b.ROMAddressLatch = 0
	b.UpdateWaitStateTable()
}

// IODevices returns the registered IO handler slice. Used by the upstream
// CoreBase::Peek*IO API parity wrappers.
func (b *Bus) IODevices() []IOHandler { return b.devices }

func (b *Bus) LoadBIOS(data []byte) { copy(b.BIOS[:], data) }
func (b *Bus) LoadROM(rom []byte) {
	b.ROM = rom
	b.ROMMask = 0x01FFFFFF
}

// SetROMMirror ⇄ ROM::ROM(... rom_mask) for mirror=true carts. Rounds the
// cart size up to a power of two and uses it as the address mask, so
// reads past the cart wrap inside the ROM (Classic NES Series quirk).
func (b *Bus) SetROMMirror() {
	size := uint32(1)
	for size < uint32(len(b.ROM)) {
		size <<= 1
	}
	b.ROMMask = size - 1
}

// ReadOpenBus ⇄ Bus::ReadOpenBus (bus.cc:298). Returns the synthesised
// open-bus value at the aligned address. The caller passes the
// already-Align<T> address; the function applies the `(addr & 3) << 3`
// shift internally so byte/half/word reads land on the right slot.
func (b *Bus) ReadOpenBus(addr uint32) uint32 {
	shift := (addr & 3) << 3
	if b.LastAccess&AccessDma != 0 {
		if b.DMAOpenBusHook != nil {
			return b.DMAOpenBusHook() >> shift
		}
		return 0
	}
	if b.CPUFetchedOpcodeHook == nil {
		return 0
	}
	var word uint32
	if b.CPUStateHook != nil && b.CPUStateHook() {
		r15 := uint32(0)
		if b.CPUR15Hook != nil {
			r15 = b.CPUR15Hook()
		}
		switch r15 >> 24 {
		case 0x02, 0x05, 0x06, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D:
			w := b.CPUFetchedOpcodeHook(1)
			word = w | (w << 16)
		case 0x00, 0x07:
			if r15&2 == 0 {
				word = b.CPUFetchedOpcodeHook(0) | (b.CPUFetchedOpcodeHook(1) << 16)
			} else {
				w := b.CPUFetchedOpcodeHook(1)
				word = w | (w << 16)
			}
		case 0x03:
			if r15&2 == 0 {
				word = b.CPUFetchedOpcodeHook(0) | (b.CPUFetchedOpcodeHook(1) << 16)
			} else {
				word = b.CPUFetchedOpcodeHook(1) | (b.CPUFetchedOpcodeHook(0) << 16)
			}
		}
	} else {
		word = b.CPUFetchedOpcodeHook(1)
	}
	return word >> shift
}

// readBIOS ⇄ Bus::ReadBIOS. Returns the latched BIOS value unless the
// CPU is currently executing inside the BIOS (r15 < 0x4000) — in which
// case it refreshes the latch from the actual ROM. Out-of-range reads
// fall through to open-bus.
func (b *Bus) readBIOS(addr uint32) uint32 {
	if addr >= 0x4000 {
		return b.ReadOpenBus(addr)
	}
	shift := (addr & 3) << 3
	if b.CPUR15Hook != nil && b.CPUR15Hook() < 0x4000 {
		aligned := addr &^ 3
		b.BIOSLatch = binary.LittleEndian.Uint32(b.BIOS[aligned:])
	}
	return b.BIOSLatch >> shift
}

// GetHostAddress ⇄ Bus::GetHostAddress<T>(address, size). Returns a slice
// pointing directly into the GBA's memory at `addr` covering `size` bytes,
// or nil if the address range can't be resolved. Used by the MP2K HLE
// engine to pluck sample/instrument data straight out of the cart.
// GetHostAddress ⇄ Bus::GetHostAddress (bus.cc:354-398). Returns a
// host-pointer slice into the requested guest-memory region, or nil
// if the request falls outside an addressable area. Used by MP2K HLE
// for direct sample/instrument reads. Masks match upstream's 0x00FF'FFFF
// (BIOS/EWRAM/IWRAM) and 0x01FF'FFFF (ROM) so that the bounds check
// rejects any address beyond the actual mapped region — including
// the EWRAM/IWRAM mirror windows that upstream also rejects.
func (b *Bus) GetHostAddress(addr uint32, size int) []uint8 {
	page := (addr >> 24) & 0xF
	switch page {
	case 0x0:
		off := addr & 0x00FFFFFF
		if int(off)+size > len(b.BIOS) {
			return nil
		}
		return b.BIOS[off : int(off)+size]
	case 0x2:
		off := addr & 0x00FFFFFF
		if int(off)+size > len(b.EWRAM) {
			return nil
		}
		return b.EWRAM[off : int(off)+size]
	case 0x3:
		off := addr & 0x00FFFFFF
		if int(off)+size > len(b.IWRAM) {
			return nil
		}
		return b.IWRAM[off : int(off)+size]
	case 0x8, 0x9, 0xA, 0xB, 0xC, 0xD:
		off := addr & 0x01FFFFFF
		if int(off)+size > len(b.ROM) {
			return nil
		}
		return b.ROM[off : int(off)+size]
	}
	return nil
}

// ---------------------------------------------------------------------
// Reads — width-specific, no delegation.
// ---------------------------------------------------------------------

func (b *Bus) ReadByte(addr uint32, access Access) uint8 {
	b.drainDMAOnAccess(access)
	page := (addr >> 24) & 0xF
	if page >= 0x8 && page <= 0xD {
		seq := 0
		if access&AccessSequential != 0 {
			seq = 1
		}
		if addr&0x1FFFF == 0 || (b.LastAccess&AccessDma != 0 && access&AccessDma == 0) {
			seq = 0
		}
		b.Prefetch(addr, access&AccessCode != 0, b.wait16[seq][page])
	} else {
		b.stepAt(page, addr, access, 8)
	}
	b.LastAccess = access
	return b.readByteAt(addr)
}

func (b *Bus) ReadHalf(addr uint32, access Access) uint16 {
	b.drainDMAOnAccess(access)
	page := (addr >> 24) & 0xF
	if page >= 0x8 && page <= 0xD {
		// ROM region — route through Prefetch so the gamepak prefetch
		// buffer is updated. Matches bus.cc case 0x08..0x0D, including
		// the "page-boundary or DMA->non-DMA forces non-sequential" rule.
		seq := 0
		if access&AccessSequential != 0 {
			seq = 1
		}
		if addr&0x1FFFF == 0 || (b.LastAccess&AccessDma != 0 && access&AccessDma == 0) {
			seq = 0
		}
		b.Prefetch(addr, access&AccessCode != 0, b.wait16[seq][page])
	} else {
		b.stepAt(page, addr, access, 16)
	}
	b.LastAccess = access
	addr &= ^uint32(1)
	switch page {
	case 0x0:
		// BIOS reads are gated on r15 < 0x4000 (BIOS protection).
		return uint16(b.readBIOS(addr))
	case 0x2:
		return binary.LittleEndian.Uint16(b.EWRAM[addr&0x3FFFF:])
	case 0x3:
		return binary.LittleEndian.Uint16(b.IWRAM[addr&0x7FFF:])
	case 0x4:
		for _, d := range b.devices {
			if v, ok := d.IORead16(addr); ok {
				return v
			}
		}
		return uint16(b.ReadOpenBus(addr))
	case 0x5:
		return binary.LittleEndian.Uint16(b.Palette[addr&0x3FF:])
	case 0x6:
		off := addr & 0x1FFFF
		// In bitmap modes, OBJ-region reads of [0x18000..0x1FFFF] fold
		// into [0x10000..0x17FFF]; if the folded address falls inside
		// the BG region (< boundary), upstream returns 0 (not memory).
		if off >= 0x18000 {
			off &= 0x17FFF
			boundary := uint32(0x10000)
			if b.PPUSpriteVRAMBoundaryHook != nil {
				boundary = b.PPUSpriteVRAMBoundaryHook()
			}
			if off < boundary {
				return 0
			}
		}
		return binary.LittleEndian.Uint16(b.VRAM[off:])
	case 0x7:
		return binary.LittleEndian.Uint16(b.OAM[addr&0x3FF:])
	case 0x8, 0x9, 0xA, 0xB, 0xC, 0xD:
		off := addr & 0x01FFFFFE
		if b.GPIO != nil && off >= 0xC4 && off <= 0xC8 && b.GPIO.IsReadable() {
			return uint16(b.GPIO.Read(off))
		}
		if b.BackupEEPROM != nil && (off&b.EEPROMMask) == b.EEPROMMask {
			return uint16(b.BackupEEPROM.Read(0))
		}
		// rom_address_latch + rom_mask logic ⇄ ROM::ReadROM16.
		if access&AccessSequential == 0 {
			b.ROMAddressLatch = off & b.ROMMask
		}
		var data uint16
		if int(b.ROMAddressLatch)+1 < len(b.ROM) {
			data = binary.LittleEndian.Uint16(b.ROM[b.ROMAddressLatch:])
		} else {
			data = uint16(b.ROMAddressLatch >> 1)
		}
		b.ROMAddressLatch = (b.ROMAddressLatch + 2) & b.ROMMask
		return data
	case 0xE, 0xF:
		// SRAM/FLASH reads return the byte replicated through the halfword
		// (per nba: `value *= 0x0101`).
		if b.BackupSRAM != nil {
			v := uint16(b.BackupSRAM.Read(addr))
			return v | v<<8
		}
		return 0xFFFF
	}
	return uint16(b.ReadOpenBus(addr))
}

func (b *Bus) ReadWord(addr uint32, access Access) uint32 {
	b.drainDMAOnAccess(access)
	page := (addr >> 24) & 0xF
	if page >= 0x8 && page <= 0xD {
		seq := 0
		if access&AccessSequential != 0 {
			seq = 1
		}
		if addr&0x1FFFF == 0 || (b.LastAccess&AccessDma != 0 && access&AccessDma == 0) {
			seq = 0
		}
		b.Prefetch(addr, access&AccessCode != 0, b.wait32[seq][page])
	} else {
		b.stepAt(page, addr, access, 32)
	}
	b.LastAccess = access
	addr &= ^uint32(3)
	switch page {
	case 0x0:
		// Word BIOS read — latch refresh handled inside readBIOS.
		return b.readBIOS(addr)
	case 0x2:
		return binary.LittleEndian.Uint32(b.EWRAM[addr&0x3FFFF:])
	case 0x3:
		return binary.LittleEndian.Uint32(b.IWRAM[addr&0x7FFF:])
	case 0x4:
		for _, d := range b.devices {
			if v, ok := d.IORead32(addr); ok {
				return v
			}
		}
		return b.ReadOpenBus(addr)
	case 0x5:
		return binary.LittleEndian.Uint32(b.Palette[addr&0x3FF:])
	case 0x6:
		off := addr & 0x1FFFF
		if off >= 0x18000 {
			off &= 0x17FFF
			boundary := uint32(0x10000)
			if b.PPUSpriteVRAMBoundaryHook != nil {
				boundary = b.PPUSpriteVRAMBoundaryHook()
			}
			if off < boundary {
				return 0
			}
		}
		return binary.LittleEndian.Uint32(b.VRAM[off:])
	case 0x7:
		return binary.LittleEndian.Uint32(b.OAM[addr&0x3FF:])
	case 0x8, 0x9, 0xA, 0xB, 0xC, 0xD:
		off := addr & 0x01FFFFFC
		if b.GPIO != nil && off >= 0xC4 && off <= 0xC8 && b.GPIO.IsReadable() {
			lo := uint32(b.GPIO.Read(off))
			hi := uint32(b.GPIO.Read(off + 2))
			return lo | hi<<16
		}
		if b.BackupEEPROM != nil && (off&b.EEPROMMask) == b.EEPROMMask {
			lo := uint32(b.BackupEEPROM.Read(0))
			hi := uint32(b.BackupEEPROM.Read(0))
			return lo | hi<<16
		}
		// rom_address_latch + rom_mask logic ⇄ ROM::ReadROM32.
		if access&AccessSequential == 0 {
			b.ROMAddressLatch = off & b.ROMMask
		}
		var data uint32
		if int(b.ROMAddressLatch)+3 < len(b.ROM) {
			data = binary.LittleEndian.Uint32(b.ROM[b.ROMAddressLatch:])
		} else {
			lsw := uint16(b.ROMAddressLatch >> 1)
			msw := lsw + 1
			data = uint32(lsw) | uint32(msw)<<16
		}
		b.ROMAddressLatch = (b.ROMAddressLatch + 4) & b.ROMMask
		return data
	case 0xE, 0xF:
		// `value *= 0x01010101` per upstream — replicate byte across word.
		if b.BackupSRAM != nil {
			v := uint32(b.BackupSRAM.Read(addr))
			return v | v<<8 | v<<16 | v<<24
		}
		return 0xFFFFFFFF
	}
	return b.ReadOpenBus(addr)
}

func (b *Bus) readByteAt(addr uint32) uint8 {
	switch (addr >> 24) & 0xF {
	case 0x0:
		return uint8(b.readBIOS(addr))
	case 0x2:
		return b.EWRAM[addr&0x3FFFF]
	case 0x3:
		return b.IWRAM[addr&0x7FFF]
	case 0x4:
		for _, d := range b.devices {
			if v, ok := d.IORead8(addr); ok {
				return v
			}
		}
		return uint8(b.ReadOpenBus(addr))
	case 0x5:
		return b.Palette[addr&0x3FF]
	case 0x6:
		off := addr & 0x1FFFF
		if off >= 0x18000 {
			off &= 0x17FFF
			boundary := uint32(0x10000)
			if b.PPUSpriteVRAMBoundaryHook != nil {
				boundary = b.PPUSpriteVRAMBoundaryHook()
			}
			if off < boundary {
				return 0
			}
		}
		return b.VRAM[off]
	case 0x7:
		return b.OAM[addr&0x3FF]
	case 0x8, 0x9, 0xA, 0xB, 0xC, 0xD:
		off := addr & 0x01FFFFFF
		// Upstream Bus::Read<u8> on cart ROM goes through ReadROM16 then
		// shifts — so the GPIO range is reachable from byte reads too.
		if b.GPIO != nil && (off&^1) >= 0xC4 && (off&^1) <= 0xC8 && b.GPIO.IsReadable() {
			return b.GPIO.Read(off&^1) >> ((off & 1) << 3)
		}
		if int(off) < len(b.ROM) {
			return b.ROM[off]
		}
		// Open-bus: halfword at (off & ~1) is (off >> 1); byte = HW>>(off&1*8).
		hw := (off &^ 1) >> 1
		shift := (off & 1) << 3
		return uint8(hw >> shift)
	case 0xE, 0xF:
		if b.BackupSRAM != nil {
			return b.BackupSRAM.Read(addr)
		}
		return 0xFF
	}
	return uint8(b.ReadOpenBus(addr))
}

// ---------------------------------------------------------------------
// Writes — width-specific, no delegation.
// ---------------------------------------------------------------------

func (b *Bus) WriteByte(addr uint32, v uint8, access Access) {
	b.drainDMAOnAccess(access)
	page := (addr >> 24) & 0xF
	b.stepAt(page, addr, access, 8)
	b.LastAccess = access
	switch page {
	case 0x2:
		b.EWRAM[addr&0x3FFFF] = v
	case 0x3:
		b.IWRAM[addr&0x7FFF] = v
	case 0x4:
		for _, d := range b.devices {
			if d.IOWrite8(addr, v) {
				return
			}
		}
	case 0x5:
		// 8-bit palette writes duplicate the byte across both halves of the
		// halfword (BG/OBJ palette is 16-bit aligned).
		off := addr & 0x3FE
		b.Palette[off] = v
		b.Palette[off+1] = v
	case 0x6:
		off := addr & 0x1FFFF
		if off >= 0x18000 {
			off &= 0x17FFF
		}
		// BG-region byte writes duplicate (`value * 0x0101`); OBJ-region
		// byte writes drop. The boundary is mode-dependent (0x14000 in
		// bitmap modes, 0x10000 otherwise) per upstream
		// PPU::GetSpriteVRAMBoundary.
		boundary := uint32(0x10000)
		if b.PPUSpriteVRAMBoundaryHook != nil {
			boundary = b.PPUSpriteVRAMBoundaryHook()
		}
		if off < boundary {
			b.VRAM[off&^1] = v
			b.VRAM[(off&^1)+1] = v
		}
	case 0x7:
		// 8-bit OAM writes are dropped on hardware.
	case 0x8, 0x9, 0xA, 0xB, 0xC, 0xD:
		// Upstream byte writes to ROM go through WriteROM with `value * 0x0101`
		// — the GPIO chip is the only writable thing in this region (besides
		// EEPROM, which lives at a higher address and is handled separately).
		off := addr & 0x01FFFFFE
		if b.GPIO != nil && off >= 0xC4 && off <= 0xC8 {
			b.GPIO.Write(off, v)
		}
	case 0xE, 0xF:
		if b.BackupSRAM != nil {
			b.BackupSRAM.Write(addr, v)
		}
	}
}

func (b *Bus) WriteHalf(addr uint32, v uint16, access Access) {
	b.drainDMAOnAccess(access)
	page := (addr >> 24) & 0xF
	b.stepAt(page, addr, access, 16)
	b.LastAccess = access
	raw := addr // preserve for SRAM (upstream skips alignment there)
	addr &= ^uint32(1)
	switch page {
	case 0x2:
		binary.LittleEndian.PutUint16(b.EWRAM[addr&0x3FFFF:], v)
	case 0x3:
		binary.LittleEndian.PutUint16(b.IWRAM[addr&0x7FFF:], v)
	case 0x4:
		for _, d := range b.devices {
			if d.IOWrite16(addr, v) {
				return
			}
		}
	case 0x5:
		binary.LittleEndian.PutUint16(b.Palette[addr&0x3FF:], v)
	case 0x6:
		off := addr & 0x1FFFF
		if off >= 0x18000 {
			off &= 0x17FFF
			boundary := uint32(0x10000)
			if b.PPUSpriteVRAMBoundaryHook != nil {
				boundary = b.PPUSpriteVRAMBoundaryHook()
			}
			if off < boundary {
				return // drop OBJ-region write that mirrors into BG region
			}
		}
		binary.LittleEndian.PutUint16(b.VRAM[off:], v)
	case 0x7:
		binary.LittleEndian.PutUint16(b.OAM[addr&0x3FF:], v)
	case 0x8, 0x9, 0xA, 0xB, 0xC, 0xD:
		off := addr & 0x01FFFFFE
		if b.GPIO != nil && off >= 0xC4 && off <= 0xC8 {
			b.GPIO.Write(off, uint8(v))
		}
		if b.BackupEEPROM != nil && (off&b.EEPROMMask) == b.EEPROMMask {
			b.BackupEEPROM.Write(0, uint8(v))
		}
	case 0xE, 0xF:
		// nba: `value >>= (address & 1) << 3` then write low byte —
		// uses the raw, unaligned address.
		if b.BackupSRAM != nil {
			shift := (raw & 1) << 3
			b.BackupSRAM.Write(raw, uint8(v>>shift))
		}
	}
}

func (b *Bus) WriteWord(addr uint32, v uint32, access Access) {
	b.drainDMAOnAccess(access)
	page := (addr >> 24) & 0xF
	b.stepAt(page, addr, access, 32)
	b.LastAccess = access
	raw := addr // preserve for SRAM (no alignment there)
	addr &= ^uint32(3)
	switch page {
	case 0x2:
		binary.LittleEndian.PutUint32(b.EWRAM[addr&0x3FFFF:], v)
	case 0x3:
		binary.LittleEndian.PutUint32(b.IWRAM[addr&0x7FFF:], v)
	case 0x4:
		for _, d := range b.devices {
			if d.IOWrite32(addr, v) {
				return
			}
		}
		// Fall back to two halfword writes if no IO device claimed the word.
		for _, d := range b.devices {
			if d.IOWrite16(addr, uint16(v)) {
				break
			}
		}
		for _, d := range b.devices {
			if d.IOWrite16(addr+2, uint16(v>>16)) {
				break
			}
		}
	case 0x5:
		binary.LittleEndian.PutUint32(b.Palette[addr&0x3FF:], v)
	case 0x6:
		off := addr & 0x1FFFF
		if off >= 0x18000 {
			off &= 0x17FFF
			boundary := uint32(0x10000)
			if b.PPUSpriteVRAMBoundaryHook != nil {
				boundary = b.PPUSpriteVRAMBoundaryHook()
			}
			if off < boundary {
				return
			}
		}
		binary.LittleEndian.PutUint32(b.VRAM[off:], v)
	case 0x7:
		binary.LittleEndian.PutUint32(b.OAM[addr&0x3FF:], v)
	case 0x8, 0x9, 0xA, 0xB, 0xC, 0xD:
		// 32-bit ROM writes are split into two halfword writes by upstream.
		off := addr & 0x01FFFFFC
		if b.GPIO != nil {
			if off >= 0xC4 && off <= 0xC8 {
				b.GPIO.Write(off, uint8(v))
			}
			if off+2 >= 0xC4 && off+2 <= 0xC8 {
				b.GPIO.Write(off+2, uint8(v>>16))
			}
		}
	case 0xE, 0xF:
		if b.BackupSRAM != nil {
			shift := (raw & 3) << 3
			b.BackupSRAM.Write(raw, uint8(v>>shift))
		}
	}
}

// Idle ⇄ Bus::Idle (bus/timing.cc). Internal CPU cycles can run in
// parallel with DMA — when DMA is active we set
// ParallelInternalCPUCycleLimit to the DMA duration; subsequent Idle()
// calls decrement the limit instead of advancing the scheduler. Once
// the limit hits 0 we resume normal Step(1) accounting.
func (b *Bus) Idle() {
	if b.DMAStateHook != nil && b.DMAStateHook() && b.DMARunHook != nil {
		b.ParallelInternalCPUCycleLimit = b.DMARunHook()
	}
	if b.ParallelInternalCPUCycleLimit == 0 {
		b.Step(1)
	} else {
		b.ParallelInternalCPUCycleLimit--
	}
}

// drainDMAOnAccess ⇄ the first two lines of every upstream Bus::Read /
// Bus::Write: if DMA is running and this isn't a DMA / Lock access, run
// the DMA to completion before continuing. Always resets the
// parallel-cycle limit afterwards.
func (b *Bus) drainDMAOnAccess(access Access) {
	if access&(AccessDma|AccessLock) == 0 &&
		b.DMAStateHook != nil && b.DMAStateHook() &&
		b.DMARunHook != nil {
		b.DMARunHook()
	}
	b.ParallelInternalCPUCycleLimit = 0
}

// Step ⇄ Bus::Step (bus/timing.cc). Advances the scheduler and walks the
// prefetch countdown so completed prefetches are visible to subsequent
// reads.
func (b *Bus) Step(cycles int64) {
	b.Scheduler.Advance(cycles)
	if b.prefetch.active {
		b.prefetch.countdown -= cycles
		for b.prefetch.countdown <= 0 {
			b.prefetch.count++
			if b.Waitcnt.Prefetch && b.prefetch.count < b.prefetch.capacity {
				b.prefetch.lastAddress += b.prefetch.opcodeWidth
				b.prefetch.countdown += b.prefetch.duty
			} else {
				break
			}
		}
	}
}

// Prefetch ⇄ Bus::Prefetch (bus/timing.cc). Manages the gamepak prefetch
// buffer for code fetches in the ROM region.
func (b *Bus) Prefetch(address uint32, code bool, cycles int64) {
	if !code {
		b.StopPrefetch()
		b.Step(cycles)
		return
	}
	if b.prefetch.active {
		// Case #1: requested address is the first entry in the buffer.
		if b.prefetch.count != 0 && address == b.prefetch.headAddress {
			b.prefetch.count--
			b.prefetch.headAddress += b.prefetch.opcodeWidth
			b.Step(1)
			return
		}
		// Case #2: requested address is currently being prefetched.
		if b.prefetch.countdown > 0 && address == b.prefetch.lastAddress {
			b.Step(b.prefetch.countdown)
			b.prefetch.headAddress = b.prefetch.lastAddress
			b.prefetch.count = 0
			return
		}
	}

	page := address >> 24
	b.StopPrefetch()

	// Case #3: hit the cart. If prefetch was just disabled, force the
	// access non-sequential (matches upstream's @todo).
	if b.PrefetchBufferWasDisabled {
		if cycles == b.wait16[1][page] {
			cycles = b.wait16[0][8]
		} else if cycles == b.wait32[1][page] {
			cycles = b.wait32[0][8]
		}
		b.PrefetchBufferWasDisabled = false
	}
	b.Step(cycles)

	if b.Waitcnt.Prefetch {
		// Engage prefetch — burst keeps refilling until the buffer is full.
		// Need the thumb flag from the CPU; we get it via the CPUStateHook.
		thumb := false
		if b.CPUStateHook != nil {
			thumb = b.CPUStateHook()
		}
		b.prefetch.active = true
		b.prefetch.count = 0
		b.prefetch.thumb = thumb
		if thumb {
			b.prefetch.opcodeWidth = 2
			b.prefetch.capacity = 8
			b.prefetch.duty = b.wait16[1][page]
		} else {
			b.prefetch.opcodeWidth = 4
			b.prefetch.capacity = 4
			b.prefetch.duty = b.wait32[1][page]
		}
		b.prefetch.countdown = b.prefetch.duty
		b.prefetch.lastAddress = address + b.prefetch.opcodeWidth
		b.prefetch.headAddress = b.prefetch.lastAddress
	}
}

// StopPrefetch ⇄ Bus::StopPrefetch (bus/timing.cc). Tears down the
// prefetch buffer and pays the half-cycle penalty if the CPU was caught
// mid-halfword-fetch.
func (b *Bus) StopPrefetch() {
	if !b.prefetch.active {
		return
	}
	if b.CPUR15Hook != nil {
		r15 := b.CPUR15Hook()
		if r15 >= 0x08000000 && r15 <= 0x0DFFFFFF {
			halfDutyPlusOne := (b.prefetch.duty >> 1) + 1
			c := b.prefetch.countdown
			if c == 1 || (!b.prefetch.thumb && c == halfDutyPlusOne) {
				b.Step(1)
			}
		}
	}
	b.prefetch.active = false
}

// CPUStateHook / CPUR15Hook ⇄ the bits of nba::ARM7TDMI::state that
// upstream's timing.cc accesses directly. Wired by core.New to break the
// import cycle the other direction would create.
var _ = false // keep gofmt from re-collapsing the docstrings above

func (b *Bus) step(page uint32, access Access, width int) {
	b.stepAt(page, 0, access, width)
}

// stepAt is the address-aware variant — needed for VRAM where BG vs OBJ
// contention depends on the offset relative to the sprite VRAM boundary.
func (b *Bus) stepAt(page uint32, addr uint32, access Access, width int) {
	seq := 0
	if access&AccessSequential != 0 {
		seq = 1
	}
	// PPU memory contention: PRAM/VRAM/OAM accesses share the bus with
	// the PPU's render pipeline. Each cycle we step, we Sync the PPU and
	// loop while it still touches the same resource.
	switch page {
	case 0x5: // PRAM
		cycles := 1
		if width == 32 {
			cycles = 2
		}
		b.StopPrefetch()
		b.contendedSteps(cycles, b.DidAccessPRAMHook)
		return
	case 0x6: // VRAM — BG vs OBJ contention split at sprite boundary
		cycles := 1
		if width == 32 {
			cycles = 2
		}
		b.StopPrefetch()
		off := addr & 0x1FFFF
		boundary := uint32(0x10000)
		if b.PPUSpriteVRAMBoundaryHook != nil {
			boundary = b.PPUSpriteVRAMBoundaryHook()
		}
		hook := b.DidAccessVRAMBGHook
		if off >= boundary {
			hook = b.DidAccessVRAMOBJHook
		}
		b.contendedSteps(cycles, hook)
		return
	case 0x7: // OAM
		b.StopPrefetch()
		b.contendedSteps(1, b.DidAccessOAMHook)
		return
	}
	// Cart-ROM region: apply the same "force non-sequential" rule reads
	// use — page-boundary crossing or DMA→non-DMA mode switch on an
	// already-running CPU drops sequential access.
	if page >= 0x8 && page <= 0xD {
		if addr&0x1FFFF == 0 || (b.LastAccess&AccessDma != 0 && access&AccessDma == 0) {
			seq = 0
		}
	}
	cycles := int64(0)
	switch width {
	case 8, 16:
		cycles = b.wait16[seq][page]
	case 32:
		cycles = b.wait32[seq][page]
	}
	// Non-ROM-code accesses to the cart still flush the prefetch buffer,
	// matching upstream's Read<T> path for non-ROM regions and ROM data
	// reads.
	b.StopPrefetch()
	b.Step(cycles)
}

// contendedSteps ⇄ the cycle-accurate Bus::ReadPRAM/VRAM/OAM contention
// loop. Steps for `cycles` cycles, but each Step is repeated as long as
// the PPU is still accessing the same resource on the same cycle.
func (b *Bus) contendedSteps(cycles int, didAccess func() bool) {
	if didAccess == nil || b.PPUSyncHook == nil {
		// Hooks unavailable (e.g. headless test); fall back to plain Step.
		b.Step(int64(cycles))
		return
	}
	for range cycles {
		for {
			b.Step(1)
			b.PPUSyncHook()
			if !didAccess() {
				break
			}
		}
	}
}
