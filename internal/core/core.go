// Package core — port of src/nba/src/nba.cpp (CoreBase). Owns the
// scheduler, bus, CPU, IRQ controller, DMA, PPU, and the run loop.
package core

import (
	"encoding/binary"
	"fmt"

	"github.com/mnmlyw/nanogoadvance/internal/apu"
	"github.com/mnmlyw/nanogoadvance/internal/apu/hle"
	"github.com/mnmlyw/nanogoadvance/internal/arm"
	"github.com/mnmlyw/nanogoadvance/internal/backup"
	"github.com/mnmlyw/nanogoadvance/internal/bus"
	"github.com/mnmlyw/nanogoadvance/internal/common"
	"github.com/mnmlyw/nanogoadvance/internal/dma"
	"github.com/mnmlyw/nanogoadvance/internal/gpio"
	"github.com/mnmlyw/nanogoadvance/internal/irq"
	"github.com/mnmlyw/nanogoadvance/internal/keypad"
	"github.com/mnmlyw/nanogoadvance/internal/ppu"
	"github.com/mnmlyw/nanogoadvance/internal/rom"
	"github.com/mnmlyw/nanogoadvance/internal/scheduler"
	"github.com/mnmlyw/nanogoadvance/internal/timer"
)

const CyclesPerFrame = 280896

// busHostAddressAdapter satisfies hle.BusHostAddress. The trivial adapter
// is needed because hle defines the interface in a different package.
type busHostAddressAdapter struct{ b *bus.Bus }

func (a busHostAddressAdapter) GetHostAddress(addr uint32, size int) []uint8 {
	return a.b.GetHostAddress(addr, size)
}

type Core struct {
	Sched  *scheduler.Scheduler
	Bus    *bus.Bus
	CPU    *arm.ARM7TDMI
	IRQ    *irq.IRQ
	DMA    *dma.DMA
	PPU    *ppu.PPU
	APU    *apu.APU
	MP2K   *hle.MP2K
	Timer  *timer.Timer
	Keypad *keypad.KeyPad

	stubIO  *stubDevice
	hasBIOS bool

	// hleAudioHook ⇄ Core::hle_audio_hook — PC at which the cart's
	// SoundMainRAM is entered. 0xFFFFFFFF when MP2K HLE is disabled or
	// the SoundMain CRC wasn't found in the ROM.
	hleAudioHook uint32

	// config ⇄ std::shared_ptr<Config> on the upstream Core. Used to
	// drive audio resampler choice + MP2K behavior.
	config Config
}

func New() *Core {
	s := scheduler.New()
	b := bus.New(s)
	cpu := arm.New(b)
	// Hook CPU state into the bus so timing.cc-style Prefetch/StopPrefetch
	// can see thumb mode and r15 without a back-import.
	b.CPUStateHook = func() bool { return cpu.State.CPSR.Thumb() }
	b.CPUR15Hook = func() uint32 { return cpu.State.Reg[15] }
	b.CPUFetchedOpcodeHook = func(slot int) uint32 { return cpu.Pipe.Opcode[slot] }
	// PPU is constructed below — wire the sprite boundary hook after.
	// DMA state/run hooks are wired after dma.New below.
	irqc := irq.New(cpu, s)
	d := dma.New(b, irqc, s)
	// Bus.Idle and Bus.Read/Write need to drive DMA without an
	// import cycle. Match upstream's Bus::Idle / Bus::Read pattern.
	b.DMAStateHook = d.IsRunning
	b.DMARunHook = d.Run
	b.DMAOpenBusHook = d.GetOpenBusValue
	p := ppu.New(s, irqc, d)
	b.PPUSpriteVRAMBoundaryHook = func() uint32 {
		if p.DISPCNT.Mode >= 3 {
			return 0x14000
		}
		return 0x10000
	}
	b.PPUSyncHook = p.Sync
	b.DidAccessPRAMHook = p.DidAccessPRAM
	b.DidAccessVRAMBGHook = p.DidAccessVRAM_BG
	b.DidAccessVRAMOBJHook = p.DidAccessVRAM_OBJ
	b.DidAccessOAMHook = p.DidAccessOAM
	p.VRAM = b.VRAM[:]
	p.Palette = b.Palette[:]
	p.OAM = b.OAM[:]
	a := apu.New(s)
	a.SetDMARequester(d)
	mp2k := hle.New(busHostAddressAdapter{b})
	a.SetMP2K(mp2k)
	tm := timer.New(s, irqc, a)
	k := keypad.New(s, irqc)
	c := &Core{Sched: s, Bus: b, CPU: cpu, IRQ: irqc, DMA: d, PPU: p, APU: a, MP2K: mp2k, Timer: tm, Keypad: k, hleAudioHook: 0xFFFFFFFF}
	c.stubIO = &stubDevice{irq: irqc, dma: d, timer: tm, keypad: k, bus: b, cpu: cpu}
	// Register the SIO_transfer_done class callback so a scheduled
	// completion event survives save/load round-trip.
	s.Register(scheduler.EventClassSIOTransferDone, func(uint64) { c.stubIO.onSIOTransferDone() })
	// Order matters: most-specific handlers first; PPU/APU claim their IO
	// ranges, then the stub handles everything else.
	b.RegisterIO(p)
	b.RegisterIO(a)
	b.RegisterIO(c.stubIO)
	return c
}

func (c *Core) LoadROM(data []byte) {
	c.LoadROMWithSave(data, "")
}

// LoadROMWithSave is the file-backed variant. savePath is the path to the
// .sav file; pass "" for in-memory only (the test harness uses this).
//
// ⇄ ROMLoader::Load in src/platform/core/src/loader/rom.cc — the bits
// that actually feed the emulator core (file/GPIO/backup wiring).
func (c *Core) LoadROMWithSave(data []byte, savePath string) {
	c.Bus.LoadROM(data)

	// First, consult the game DB. If the cart matches a known entry, the
	// DB tells us the backup type + GPIO devices; otherwise fall back to
	// SRAM_V / FLASH_V / EEPROM_V magic-string detection.
	game := rom.LookupGameDB(data)
	backupType := game.BackupType
	if backupType == rom.BackupDetect {
		backupType = rom.DetectBackupType(data)
	}

	// Mirror=true carts (Classic NES Series, Famicom Mini) — power-of-two
	// mirror the ROM in the cart address window.
	if game.Mirror {
		c.Bus.SetROMMirror()
	}

	switch backupType {
	case rom.BackupSRAM:
		if savePath != "" {
			s, err := backup.NewSRAMWithFile(savePath)
			if err == nil {
				c.Bus.BackupSRAM = s
			} else {
				c.Bus.BackupSRAM = backup.NewSRAM()
			}
		} else {
			c.Bus.BackupSRAM = backup.NewSRAM()
		}
	case rom.BackupFlash64:
		if savePath != "" {
			f, err := backup.NewFLASHWithFile(savePath, backup.Flash64K)
			if err == nil {
				c.Bus.BackupSRAM = f
			} else {
				c.Bus.BackupSRAM = backup.NewFLASH(backup.Flash64K)
			}
		} else {
			c.Bus.BackupSRAM = backup.NewFLASH(backup.Flash64K)
		}
	case rom.BackupFlash128:
		if savePath != "" {
			f, err := backup.NewFLASHWithFile(savePath, backup.Flash128K)
			if err == nil {
				c.Bus.BackupSRAM = f
			} else {
				c.Bus.BackupSRAM = backup.NewFLASH(backup.Flash128K)
			}
		} else {
			c.Bus.BackupSRAM = backup.NewFLASH(backup.Flash128K)
		}
	case rom.BackupEEPROMDetect, rom.BackupEEPROM4, rom.BackupEEPROM64:
		// EEPROM lives on the cart bus, not at 0xE/0xF. Wire it through
		// Bus.BackupEEPROM and compute the address mask the way upstream
		// does in ROM::ROM (rom.hh).
		size := backup.EEPROMAuto
		if backupType == rom.BackupEEPROM4 {
			size = backup.EEPROM4K
		} else if backupType == rom.BackupEEPROM64 {
			size = backup.EEPROM64K
		}
		var e *backup.EEPROM
		if savePath != "" {
			ep, err := backup.NewEEPROMWithFile(savePath, size, c.Sched)
			if err == nil {
				e = ep
			} else {
				e = backup.NewEEPROM(size, c.Sched)
			}
		} else {
			e = backup.NewEEPROM(size, c.Sched)
		}
		c.Bus.BackupEEPROM = e
		if len(data) >= 0x01000001 {
			c.Bus.EEPROMMask = 0x01FFFF00
		} else {
			c.Bus.EEPROMMask = 0x01000000
		}
	}

	// Wire up cart GPIO based on the game DB. Carts not in the DB get no
	// GPIO at all (the chip is invisible to ROM reads), matching upstream.
	if game.GPIO != rom.GPIONone {
		g := gpio.New()
		if game.GPIO&rom.GPIORTC != 0 {
			g.Attach(gpio.NewRTC(c.IRQ))
		}
		if game.GPIO&rom.GPIOSolarSensor != 0 {
			g.Attach(gpio.NewSolarSensor())
		}
		c.Bus.GPIO = g
	}
}

func (c *Core) LoadBIOS(b []byte) {
	c.Bus.LoadBIOS(b)
	c.hasBIOS = true
}

// Reset ⇄ Core::Reset (core.cc). Resets every subsystem in the same order
// as upstream and applies the skip-BIOS boot trampoline when configured
// (or when no BIOS is loaded — same fallback semantics as before).
func (c *Core) Reset() {
	c.Sched.Reset()
	c.CPU.Reset()
	c.IRQ.Reset()
	c.DMA.Reset()
	c.Timer.Reset()
	c.APU.Reset()
	c.PPU.Reset()
	c.Bus.Reset()
	c.stubIO.Reset()
	c.Keypad.Reset()

	// Skip-BIOS ⇄ Core::SkipBootScreen — config-driven in upstream, but
	// for compatibility we also fall back to skip-BIOS when no BIOS was
	// loaded (test ROMs need this).
	if c.config.SkipBIOS || !c.hasBIOS {
		c.skipBootScreen()
	}

	// MP2K HLE hook (config-driven, same as upstream Core::Reset).
	if c.config.Audio.MP2KHLEEnable {
		c.MP2K.UseCubicFilter = c.config.Audio.MP2KHLECubic
		c.MP2K.ForceReverb = c.config.Audio.MP2KHLEForceReverb
		c.hleAudioHook = c.SearchSoundMainRAM()
	} else {
		c.hleAudioHook = 0xFFFFFFFF
	}
}

// skipBootScreen ⇄ Core::SkipBootScreen. Pre-initialises the CPU as if
// the BIOS had already handed off to the cart entry.
func (c *Core) skipBootScreen() {
	c.CPU.SwitchMode(arm.MODE_SYS)
	c.CPU.State.Bank[arm.BANK_SVC][5] = 0x03007FE0
	c.CPU.State.Bank[arm.BANK_IRQ][5] = 0x03007FA0
	c.CPU.State.Reg[13] = 0x03007F00
	c.CPU.State.Reg[15] = 0x08000000
}

// SearchSoundMainRAM ⇄ Core::SearchSoundMainRAM. Scans the cart ROM for
// the SoundMain prologue (matched by CRC32 over 48 bytes), then reads the
// SoundMainRAM pointer at offset 0x74 to find the audio mixer entrypoint.
// Returns 0xFFFFFFFF if not found.
func (c *Core) SearchSoundMainRAM() uint32 {
	const soundMainCRC32 = 0x27EA7FCF
	const soundMainLength = 48
	romData := c.Bus.ROM
	if len(romData) < soundMainLength {
		return 0xFFFFFFFF
	}
	limit := len(romData) - soundMainLength
	for address := 0; address <= limit; address += 2 {
		if common.CRC32(romData[address:address+soundMainLength]) == soundMainCRC32 {
			ptr := binary.LittleEndian.Uint32(romData[address+0x74:])
			// Account for ARM/Thumb prologue + the standard 2-instruction
			// prologue skip upstream uses (sizeof(u16)*2 thumb, u32*2 arm).
			if ptr&1 != 0 {
				ptr &^= 1
				ptr += 4
			} else {
				ptr &^= 3
				ptr += 8
			}
			return ptr
		}
	}
	return 0xFFFFFFFF
}

// EnableMP2KHLE turns on the MP2K HLE engine. Call after LoadROM. Looks
// up the SoundMainRAM hook address; until then the engine stays dormant.
func (c *Core) EnableMP2KHLE(useCubic, forceReverb bool) {
	c.MP2K.UseCubicFilter = useCubic
	c.MP2K.ForceReverb = forceReverb
	c.hleAudioHook = c.SearchSoundMainRAM()
}

// ---------------------------------------------------------------------
// Upstream CoreBase-shaped API.
//
// These mirror the virtual methods on nba::CoreBase (include/nba/core.hh).
// They're thin wrappers — the existing Go-style accessors stay too.
// ---------------------------------------------------------------------

// Attach (BIOS variant) ⇄ CoreBase::Attach(std::vector<u8> const& bios).
func (c *Core) Attach(bios []byte) { c.LoadBIOS(bios) }

// AttachROM ⇄ CoreBase::Attach(ROM&& rom). The Go variant takes raw bytes
// because we don't have a separate ROM struct yet.
func (c *Core) AttachROM(data []byte) { c.LoadROM(data) }

// SetKeyStatus ⇄ CoreBase::SetKeyStatus.
func (c *Core) SetKeyStatus(key keypad.Key, pressed bool) {
	c.Keypad.SetKeyStatus(key, pressed)
}

// Run ⇄ CoreBase::Run(cycles) — matches upstream's loop shape: DMA is no
// longer explicitly drained at the top of every iteration; the Bus.Read/
// Write inside cpu.Run() drains DMA on each memory access (drainDMAOnAccess).
// While the CPU is halted, we keep DMA flushed and the scheduler ticking.
func (c *Core) Run(cycles int64) {
	target := c.Sched.Now() + cycles
	for c.Sched.Now() < target {
		if c.Bus.Haltcnt == bus.HaltControlRun {
			if c.hleAudioHook != 0xFFFFFFFF && c.CPU.State.Reg[15] == c.hleAudioHook {
				if siAddr := c.Bus.GetHostAddress(0x03007FF0, 4); siAddr != nil {
					addr := binary.LittleEndian.Uint32(siAddr)
					if si := c.Bus.GetHostAddress(addr, 0x9C0); si != nil {
						c.MP2K.SoundMainRAM(parseSoundInfo(si))
					}
				}
			}
			c.CPU.Run()
			continue
		}
		// Halted: drain DMA and keep stepping until an IRQ wakes the CPU.
		// Step by the scheduler's remaining-cycle-count each iteration so
		// we advance straight to the next event instead of 1-cycle ticks
		// (matches upstream core.cc:99: bus.Step(GetRemainingCycleCount())).
		for c.Sched.Now() < target && !c.IRQ.ShouldUnhaltCPU() {
			if c.DMA.IsRunning() {
				c.DMA.Run()
				if c.IRQ.ShouldUnhaltCPU() {
					break
				}
			}
			c.Bus.Step(c.Sched.RemainingCycleCount())
		}
		if c.IRQ.ShouldUnhaltCPU() {
			c.Bus.Step(1)
			c.Bus.Haltcnt = bus.HaltControlRun
		}
	}
}

// PeekByteIO / PeekHalfIO / PeekWordIO ⇄ CoreBase::Peek*IO. Read an IO
// register without the timing side effects of a real bus access.
func (c *Core) PeekByteIO(addr uint32) uint8 {
	for _, d := range c.Bus.IODevices() {
		if v, ok := d.IORead8(addr); ok {
			return v
		}
	}
	return 0
}

func (c *Core) PeekHalfIO(addr uint32) uint16 {
	for _, d := range c.Bus.IODevices() {
		if v, ok := d.IORead16(addr); ok {
			return v
		}
	}
	return 0
}

func (c *Core) PeekWordIO(addr uint32) uint32 {
	for _, d := range c.Bus.IODevices() {
		if v, ok := d.IORead32(addr); ok {
			return v
		}
	}
	return 0
}

// GetPRAM / GetVRAM / GetOAM ⇄ CoreBase::Get* (raw memory views).
func (c *Core) GetPRAM() []uint8 { return c.Bus.Palette[:] }
func (c *Core) GetVRAM() []uint8 { return c.Bus.VRAM[:] }
func (c *Core) GetOAM() []uint8  { return c.Bus.OAM[:] }

// GetBGHOFS / GetBGVOFS ⇄ CoreBase::GetBG*OFS.
func (c *Core) GetBGHOFS(id int) uint16 { return c.PPU.BGHOFS[id] }
func (c *Core) GetBGVOFS(id int) uint16 { return c.PPU.BGVOFS[id] }

// RunForOneFrame ⇄ CoreBase::RunForOneFrame.
func (c *Core) RunForOneFrame() { c.Run(CyclesPerFrame) }

// RunFrame ⇄ CoreBase::RunForOneFrame. Delegates to Run.
func (c *Core) RunFrame() { c.Run(CyclesPerFrame) }

// parseSoundInfo decodes the binary SoundInfo layout from a GBA memory
// slice. Matches the byte offsets used by upstream's reinterpret_cast.
func parseSoundInfo(b []uint8) hle.SoundInfo {
	var si hle.SoundInfo
	si.Magic = binary.LittleEndian.Uint32(b[0:])
	si.PCMDMACounter = b[4]
	si.Reverb = b[5]
	si.MaxChannels = b[6]
	si.MasterVolume = b[7]
	copy(si.Unknown0[:], b[8:16])
	si.PCMSamplesPerVBlank = int32(binary.LittleEndian.Uint32(b[16:]))
	si.PCMSampleRate = int32(binary.LittleEndian.Uint32(b[20:]))
	for i := 0; i < 14; i++ {
		si.Unknown1[i] = binary.LittleEndian.Uint32(b[24+i*4:])
	}
	// Channel array starts at byte 80; each channel is 64 bytes (packed).
	off := 80
	for i := 0; i < hle.MP2KMaxSoundChannels; i++ {
		c := &si.Channels[i]
		c.Status = b[off+0]
		c.Type = b[off+1]
		c.VolumeR = b[off+2]
		c.VolumeL = b[off+3]
		c.EnvelopeAttack = b[off+4]
		c.EnvelopeDecay = b[off+5]
		c.EnvelopeSustain = b[off+6]
		c.EnvelopeRelease = b[off+7]
		c.Unknown0 = b[off+8]
		c.EnvelopeVolume = b[off+9]
		c.EnvelopeVolumeR = b[off+10]
		c.EnvelopeVolumeL = b[off+11]
		c.EchoVolume = b[off+12]
		c.EchoLength = b[off+13]
		copy(c.Unknown1[:], b[off+14:off+32])
		c.Frequency = binary.LittleEndian.Uint32(b[off+32:])
		c.WaveAddress = binary.LittleEndian.Uint32(b[off+36:])
		for j := 0; j < 6; j++ {
			c.Unknown2[j] = binary.LittleEndian.Uint32(b[off+40+j*4:])
		}
		off += 64
	}
	return si
}

// ---------------------------------------------------------------------
// IO stub — routes DMA / IRQ / unmapped IO. Per-register dispatch matches
// the byte-offset addressing the upstream IO.cc uses.
// ---------------------------------------------------------------------

type stubDevice struct {
	irq    *irq.IRQ
	dma    *dma.DMA
	timer  *timer.Timer
	keypad *keypad.KeyPad
	bus    *bus.Bus
	cpu    *arm.ARM7TDMI

	// Serial communication shadow registers. The CPU sees zeros for most of
	// SIODATA; SIOCNT/RCNT preserve whatever was written.
	siocnt uint16
	rcnt   [2]uint8

	// mGBA-style debug logging registers (0x04FFF600..04FFF780). Optional
	// but harmless — many homebrew test ROMs use them.
	mgbaLogEnable uint16
	mgbaLogMsg    [0x100]byte
}

// Reset ⇄ the stubDevice slice of Bus::Reset that owns SIO + mGBA log.
func (s *stubDevice) Reset() {
	s.siocnt = 0
	s.rcnt[0] = 0
	s.rcnt[1] = 0
	s.mgbaLogEnable = 0
	for i := range s.mgbaLogMsg {
		s.mgbaLogMsg[i] = 0
	}
}

// timerChanOffset returns (channel, offset within channel) for a timer IO
// address, or (-1, 0) if the address isn't a timer register. Layout per
// upstream IO.cc: each channel is 4 bytes at 0x04000100 + 4*n.
func timerChanOffset(addr uint32) (int, int) {
	if addr < 0x04000100 || addr > 0x0400010F {
		return -1, 0
	}
	rel := addr - 0x04000100
	return int(rel / 4), int(rel % 4)
}

// dmaChanOffset returns the channel index and per-channel register offset
// for an IO address, or -1 if it isn't a DMA register.
//
// DMA layout (12 bytes per channel, channels 0..3):
//   0x040000B0 + 12*n .. 0x040000BB + 12*n
//
// In the upstream code Read(chanID, offset) takes offsets 0..11.
func dmaChanOffset(addr uint32) (int, int) {
	if addr < 0x040000B0 || addr > 0x040000DF {
		return -1, 0
	}
	rel := addr - 0x040000B0
	chanID := int(rel / 12)
	return chanID, int(rel % 12)
}

func (s *stubDevice) IORead8(addr uint32) (uint8, bool) {
	if cid, off := dmaChanOffset(addr); cid >= 0 {
		return s.dma.Read(cid, off), true
	}
	if cid, off := timerChanOffset(addr); cid >= 0 {
		return s.timer.ReadByte(cid, off), true
	}
	switch addr {
	case 0x04000128: // SIOCNT low
		return uint8(s.siocnt), true
	case 0x04000129: // SIOCNT high
		return uint8(s.siocnt >> 8), true
	case 0x04000130:
		return s.keypad.ReadInputByte(0), true
	case 0x04000131:
		return s.keypad.ReadInputByte(1), true
	case 0x04000132:
		return s.keypad.ReadControlByte(0), true
	case 0x04000133:
		return s.keypad.ReadControlByte(1), true
	case 0x04000134:
		return s.rcnt[0], true
	case 0x04000135:
		return s.rcnt[1], true
	case 0x04000200, 0x04000201, 0x04000202, 0x04000203, 0x04000208:
		return s.irq.ReadByte(int(addr - 0x04000200)), true
	case 0x04000204:
		return s.bus.ReadWaitcnt0(), true
	case 0x04000205:
		return s.bus.ReadWaitcnt1(), true
	case 0x04000206, 0x04000207:
		return 0, true
	case 0x04000300:
		return s.bus.Postflg, true
	case 0x04000301:
		return 0, true
	case 0x04FFF600: // MGBA_LOG_ENABLE low
		return uint8(s.mgbaLogEnable), true
	case 0x04FFF601:
		return uint8(s.mgbaLogEnable >> 8), true
	}
	if addr >= 0x04000000 && addr < 0x04000400 {
		return 0, true
	}
	return 0, false
}

func (s *stubDevice) IORead16(addr uint32) (uint16, bool) {
	if cid, off := dmaChanOffset(addr); cid >= 0 {
		lo := s.dma.Read(cid, off)
		hi := s.dma.Read(cid, off+1)
		return uint16(lo) | uint16(hi)<<8, true
	}
	if cid, off := timerChanOffset(addr); cid >= 0 {
		return s.timer.ReadHalf(cid, off), true
	}
	switch addr {
	case 0x04000128:
		return s.siocnt, true
	case 0x04000130:
		return uint16(s.keypad.ReadInputByte(0)) | uint16(s.keypad.ReadInputByte(1))<<8, true
	case 0x04000132:
		return uint16(s.keypad.ReadControlByte(0)) | uint16(s.keypad.ReadControlByte(1))<<8, true
	case 0x04000134:
		return uint16(s.rcnt[0]) | uint16(s.rcnt[1])<<8, true
	case 0x04000200:
		return s.irq.ReadHalf(0), true
	case 0x04000202:
		return s.irq.ReadHalf(2), true
	case 0x04000204:
		return uint16(s.bus.ReadWaitcnt0()) | uint16(s.bus.ReadWaitcnt1())<<8, true
	case 0x04000208:
		return s.irq.ReadHalf(4), true
	case 0x04FFF600:
		return s.mgbaLogEnable, true
	}
	if addr >= 0x04000000 && addr < 0x04000400 {
		return 0, true
	}
	return 0, false
}

func (s *stubDevice) IORead32(addr uint32) (uint32, bool) {
	lo, ok := s.IORead16(addr)
	if !ok {
		return 0, false
	}
	hi, _ := s.IORead16(addr + 2)
	return uint32(lo) | uint32(hi)<<16, true
}

func (s *stubDevice) IOWrite8(addr uint32, v uint8) bool {
	if cid, off := dmaChanOffset(addr); cid >= 0 {
		s.dma.Write(cid, off, v)
		return true
	}
	if cid, off := timerChanOffset(addr); cid >= 0 {
		s.timer.WriteByte(cid, off, v)
		return true
	}
	switch addr {
	case 0x04000128:
		// SIOCNT low byte — recompose and route through WriteHalf so the
		// transfer-start side effect runs the same way.
		s.writeSIOCNT((s.siocnt & 0xFF00) | uint16(v))
		return true
	case 0x04000129:
		s.writeSIOCNT((s.siocnt & 0x00FF) | (uint16(v) << 8))
		return true
	case 0x04000132:
		s.keypad.WriteControlByte(0, v)
		return true
	case 0x04000133:
		s.keypad.WriteControlByte(1, v)
		return true
	case 0x04000134:
		s.rcnt[0] = v
		return true
	case 0x04000135:
		s.rcnt[1] = v
		return true
	case 0x04000200, 0x04000201, 0x04000202, 0x04000203, 0x04000208:
		s.irq.WriteByte(int(addr-0x04000200), v)
		return true
	case 0x04000204:
		s.bus.WriteWaitcnt0(v)
		return true
	case 0x04000205:
		s.bus.WriteWaitcnt1(v)
		return true
	case 0x04000300:
		// POSTFLG — write-once, BIOS-only (r15 <= 0x3FFF).
		if s.cpu.State.Reg[15] <= 0x3FFF {
			s.bus.Postflg |= v & 1
		}
		return true
	case 0x04000301:
		// HALTCNT ⇄ bus/io.cc case HALTCNT. BIOS-only (r15 ≤ 0x3FFF).
		// Upstream's bit-7-set "Stop" path is commented out — it leaves
		// haltcnt unchanged. Only Halt enters HaltControl::Halt, and
		// it calls bus->Step(1) immediately.
		if s.cpu.State.Reg[15] <= 0x3FFF {
			if v&0x80 == 0 {
				s.bus.Haltcnt = bus.HaltControlHalt
				s.bus.Step(1)
			}
		}
		return true
	}
	// mGBA debug string buffer 0x04FFF600..0x04FFF6FF (offset 0x100 long
	// region for the message body); the enable + send half-write live at
	// 0x04FFF600/0x04FFF700.
	if addr >= 0x04FFF600 && addr < 0x04FFF700 {
		s.mgbaLogMsg[addr&0xFF] = v
		return true
	}
	if addr >= 0x04000000 && addr < 0x04000400 {
		return true
	}
	return false
}

// writeSIOCNT ⇄ io.cc WriteHalf SIOCNT path (transfer-start side effect).
// Bit 7 going 0->1 begins a transfer; the cycle count depends on
// internal clock (bit 1) and transfer length (bit 12). When the
// scheduled SIO_transfer_done event fires, bit 7 is cleared and (if
// IRQ-enabled via bit 14) a Serial IRQ is raised.
func (s *stubDevice) writeSIOCNT(v uint16) {
	const startBit = 0x80
	wasStarted := s.siocnt&startBit != 0
	// Preserve only bit 7 from the old value; everything else from new.
	s.siocnt = (s.siocnt & startBit) | (v &^ startBit)
	if !wasStarted && v&startBit != 0 {
		// Mark transfer in progress.
		s.siocnt |= startBit
		// Cycle count = table[(bit 1) | (bit 12 << 1)] (clock × length).
		table := [4]int64{512, 64, 2048, 256}
		idx := ((int64(s.siocnt) >> 1) & 1) | ((int64(s.siocnt) >> 11) & 2)
		cycles := table[idx]
		s.bus.Scheduler.AddClass(cycles, scheduler.EventClassSIOTransferDone, 0, 0)
	}
}

// onSIOTransferDone ⇄ Bus::SIOTransferDone. Raises IRQ if enabled and
// clears the transfer-in-progress bit. Registered once at startup.
func (s *stubDevice) onSIOTransferDone() {
	if s.siocnt&0x4000 != 0 {
		s.irq.Raise(irq.SourceSerial, 0)
	}
	s.siocnt &^= 0x80
}

func (s *stubDevice) IOWrite16(addr uint32, v uint16) bool {
	if cid, off := dmaChanOffset(addr); cid >= 0 {
		s.dma.Write(cid, off, uint8(v))
		s.dma.Write(cid, off+1, uint8(v>>8))
		return true
	}
	if cid, off := timerChanOffset(addr); cid >= 0 {
		s.timer.WriteHalf(cid, off, v)
		return true
	}
	switch addr {
	case 0x04000128:
		s.writeSIOCNT(v)
		return true
	case 0x04000132:
		s.keypad.WriteControlHalf(v)
		return true
	case 0x04000134:
		s.rcnt[0] = uint8(v)
		s.rcnt[1] = uint8(v >> 8)
		return true
	case 0x04000200:
		s.irq.WriteHalf(0, v)
		return true
	case 0x04000202:
		s.irq.WriteHalf(2, v)
		return true
	case 0x04000204:
		s.bus.WriteWaitcnt0(uint8(v))
		s.bus.WriteWaitcnt1(uint8(v >> 8))
		return true
	case 0x04000208:
		// IME — upstream writes the low byte directly here even on
		// half-word access (only bit 0 is meaningful).
		s.irq.WriteByte(4, uint8(v))
		return true
	case 0x04FFF700:
		// MGBA_LOG_SEND — bit 8 set + log enable = flush message.
		if s.mgbaLogEnable == 0x1DEA && v&0x100 != 0 {
			// trim trailing NULs
			end := 0
			for end < len(s.mgbaLogMsg) && s.mgbaLogMsg[end] != 0 {
				end++
			}
			fmt.Printf("mGBA log: %s\n", s.mgbaLogMsg[:end])
			for i := range s.mgbaLogMsg {
				s.mgbaLogMsg[i] = 0
			}
		}
		return true
	case 0x04FFF780:
		// MGBA_LOG_ENABLE — magic 0xC0DE enables logging.
		if v == 0xC0DE {
			s.mgbaLogEnable = 0x1DEA
		}
		return true
	}
	if addr >= 0x04FFF600 && addr < 0x04FFF700 {
		s.mgbaLogMsg[addr&0xFF] = uint8(v)
		s.mgbaLogMsg[(addr+1)&0xFF] = uint8(v >> 8)
		return true
	}
	if addr >= 0x04000000 && addr < 0x04000400 {
		return true
	}
	return false
}

func (s *stubDevice) IOWrite32(addr uint32, v uint32) bool {
	if !s.IOWrite16(addr, uint16(v)) {
		return false
	}
	s.IOWrite16(addr+2, uint16(v>>16))
	return true
}
