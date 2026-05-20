// Package ppu — port of src/nba/src/hw/ppu/. This file implements the IO
// dispatch + scanline scheduler. The rendering pipeline (BG layers,
// sprites, blending, windows) is a future task — current behavior renders
// nothing visible, but DISPSTAT VBlank/HBlank/VCount timing is faithful so
// the m_vsync macro in jsmolka's tests completes correctly.
package ppu

import (
	"github.com/mnmlyw/nanogoadvance/internal/dma"
	"github.com/mnmlyw/nanogoadvance/internal/irq"
	"github.com/mnmlyw/nanogoadvance/internal/scheduler"
)

const (
	ScreenWidth   = 240
	ScreenHeight  = 160
	cyclesPerDot  = 4
	dotsPerLine   = 308
	linesPerFrame = 228

	// HBlank flag is set at line cycle 1007 on real hardware, NOT at
	// the end of the visible region (cycle 960). The 47-cycle offset
	// matches PPU pipeline latency between the last drawn dot and the
	// H-blank assertion. ⇄ upstream BeginHDrawVDraw scheduling.
	cyclesPerLineHDraw  = 1007
	cyclesPerLineHBlank = dotsPerLine*cyclesPerDot - cyclesPerLineHDraw // 225
)

// Window mirrors PPU::window in ppu.hh — per-cycle window flag state +
// per-pixel mask buffer used by the compositor to gate layers.
type Window struct {
	VFlag             [2]bool
	HFlag             [2]bool
	Buffer            [240][2]bool
	TimestampLastSync int64
	Cycle             uint32
}

type PPU struct {
	sched *scheduler.Scheduler
	irq   *irq.IRQ
	dma   *dma.DMA

	// VRAMBGLatch ⇄ PPU::vram_bg_latch — last 16-bit value the BG
	// pipeline read from BG-VRAM. Returned for reads past the BG/OBJ
	// boundary.
	VRAMBGLatch uint16

	// DMA3VideoTransferRunning ⇄ PPU::dma3_video_transfer_running.
	DMA3VideoTransferRunning bool

	// References to the bus's VRAM/PRAM/OAM arrays (upstream stores these
	// in the PPU; we keep ownership on the bus and pass slices in for now).
	VRAM    []uint8
	Palette []uint8
	OAM     []uint8

	// Typed registers — ported from registers.{hh,cc}.
	DISPCNT  DisplayControl
	DISPSTAT DisplayStatus
	BGCNT    [4]BackgroundControl
	BGHOFS   [4]uint16
	BGVOFS   [4]uint16
	BG2X     ReferencePoint
	BG2Y     ReferencePoint
	BG3X     ReferencePoint
	BG3Y     ReferencePoint
	BGPA, BGPB, BGPC, BGPD [2]int16
	WIN0H, WIN1H WindowRange
	WIN0V, WIN1V WindowRange
	WININ, WINOUT WindowLayerSelect
	MOSAIC   Mosaic
	BLDCNT   BlendControl
	BLDALPHA struct{ EVA, EVB int }
	BLDY     int

	VCOUNT uint16
	GREENSWAP uint16

	Window Window
	Merge  Merge
	BG     Background
	Sprite Sprite

	// DISPCNTLatch ⇄ mmio.dispcnt_latch[3] in upstream: a 3-deep latch
	// queue advanced by LatchDISPCNT every 40 cycles.
	DISPCNTLatch [3]uint16

	// Output framebuffer (double-buffered, matching upstream's `output[2]`).
	Output [2][240 * 160]uint32
	Frame  int
}

// winv / winh accessors keep the rendering pipeline closer to upstream's
// `mmio.winv[i]`/`mmio.winh[i]` indexed access.
func (p *PPU) winv(i int) *WindowRange {
	if i == 0 {
		return &p.WIN0V
	}
	return &p.WIN1V
}

func (p *PPU) winh(i int) *WindowRange {
	if i == 0 {
		return &p.WIN0H
	}
	return &p.WIN1H
}

func New(s *scheduler.Scheduler, irqc *irq.IRQ, d *dma.DMA) *PPU {
	p := &PPU{sched: s, irq: irqc, dma: d}
	p.BGCNT = [4]BackgroundControl{{ID: 0}, {ID: 1}, {ID: 2}, {ID: 3}}
	p.MOSAIC.Reset()
	// Pre-seed the DISPCNT latch queue so layer-enable AND checks pass
	// before the scheduler has had a chance to advance the latch.
	p.DISPCNTLatch[0] = 0xFFFF
	p.DISPCNTLatch[1] = 0xFFFF
	p.DISPCNTLatch[2] = 0xFFFF
	// Register class callbacks so scanline events survive save state.
	s.Register(scheduler.EventClassPPUHBlankVDraw, func(uint64) { p.onHBlankStart(0) })
	s.Register(scheduler.EventClassPPUHBlankVBlank, func(uint64) { p.onHBlankStart(0) })
	s.Register(scheduler.EventClassPPUHDrawVDraw, func(uint64) { p.onLineEnd(0) })
	s.Register(scheduler.EventClassPPUHDrawVBlank, func(uint64) { p.onLineEnd(0) })
	s.Register(scheduler.EventClassPPULatchDISPCNT, func(uint64) { p.LatchDISPCNT() })
	s.Register(scheduler.EventClassPPUVideoDMA, func(uint64) {
		if p.dma != nil {
			p.dma.Request(dma.OccasionVideo)
		}
	})
	// BeginSpriteDrawing ⇄ PPU::BeginSpriteDrawing (ppu.cc:206). Fires
	// every 1232 cycles, independent of the HBlank/HDraw line state machine.
	// At each fire: flush the current line's sprite pixels (vcount<160),
	// swap rd/wr buffers (vcount==227 || vcount<160), and re-init the
	// engine for the next line (vcount != 159).
	s.Register(scheduler.EventClassPPUBeginSpriteFetch, func(uint64) {
		vcount := uint32(p.VCOUNT)
		if vcount < ScreenHeight {
			p.DrawSprite()
		}
		if vcount == 227 || vcount < ScreenHeight {
			p.SwapSpriteBuffers()
			if vcount != 159 {
				p.InitSprite()
			}
		}
		p.sched.AddClass(1232, scheduler.EventClassPPUBeginSpriteFetch, 0, 0)
	})
	// Per-scanline IRQs fire 1 cycle after the corresponding flag is
	// set (matches upstream BeginHBlankVDraw / BeginHDrawVDraw / etc).
	s.Register(scheduler.EventClassPPUHBlankIRQ, func(uint64) {
		if p.irq != nil {
			p.irq.Raise(irq.SourceHBlank, 0)
		}
	})
	s.Register(scheduler.EventClassPPUVBlankIRQ, func(uint64) {
		if p.irq != nil {
			p.irq.Raise(irq.SourceVBlank, 0)
		}
	})
	s.Register(scheduler.EventClassPPUVCountIRQ, func(uint64) {
		if p.irq != nil {
			p.irq.Raise(irq.SourceVCount, 0)
		}
	})
	s.Register(scheduler.EventClassPPUUpdateVCountFlag, func(uint64) {
		// UpdateVerticalCounterFlag schedules the IRQ itself on rising edge.
		p.UpdateVerticalCounterFlag()
	})
	// Scheduling happens in Reset() so a Core reset (which clears the
	// queue) re-engages PPU events. Mirrors upstream's PPU::Reset.
	p.Reset()
	return p
}

// Reset ⇄ PPU::Reset (ppu.cc). Faithfully reproduces the post-boot state
// measured on real hardware (VCOUNT=225, mid-VBlank, identity BG2/3
// matrices) and re-schedules the first scanline events.
func (p *PPU) Reset() {
	// The PPU does not own VRAM/PRAM/OAM in this port — bus.Reset() does
	// — but we still need to zero the slice views the PPU holds.
	for i := range p.VRAM {
		p.VRAM[i] = 0
	}
	for i := range p.Palette {
		p.Palette[i] = 0
	}
	for i := range p.OAM {
		p.OAM[i] = 0
	}

	p.DISPCNT = DisplayControl{}
	p.DISPSTAT = DisplayStatus{}
	p.GREENSWAP = 0
	p.BGCNT = [4]BackgroundControl{{ID: 0}, {ID: 1}, {ID: 2}, {ID: 3}}
	p.BGHOFS = [4]uint16{}
	p.BGVOFS = [4]uint16{}
	p.BG2X = ReferencePoint{}
	p.BG2Y = ReferencePoint{}
	p.BG3X = ReferencePoint{}
	p.BG3Y = ReferencePoint{}
	// Identity matrix scaling — BG2PA/BG3PA = BG2PD/BG3PD = 0x100.
	p.BGPA = [2]int16{0x100, 0x100}
	p.BGPB = [2]int16{}
	p.BGPC = [2]int16{}
	p.BGPD = [2]int16{0x100, 0x100}
	p.WIN0H = WindowRange{}
	p.WIN1H = WindowRange{}
	p.WIN0V = WindowRange{}
	p.WIN1V = WindowRange{}
	p.WININ = WindowLayerSelect{}
	p.WINOUT = WindowLayerSelect{}
	p.MOSAIC.Reset()
	p.BLDCNT = BlendControl{}
	p.BLDALPHA = struct{ EVA, EVB int }{}
	p.BLDY = 0
	p.VRAMBGLatch = 0
	p.DMA3VideoTransferRunning = false
	p.DISPCNTLatch[0] = 0
	p.DISPCNTLatch[1] = 0
	p.DISPCNTLatch[2] = 0
	for i := range p.Output {
		for j := range p.Output[i] {
			p.Output[i][j] = 0
		}
	}
	p.Frame = 0

	// VCOUNT=225, DISPSTAT[VBlank|HBlank]=1 — measured post-reset state
	// (3DS GBA mode, credit: Lady Starbreeze).
	p.VCOUNT = 225
	p.DISPSTAT.VBlankFlag = 1
	p.DISPSTAT.HBlankFlag = 1
	// Pre-stamp the merge/bg/sprite/window cycles to "done" so a Sync()
	// call before the first scanline has nothing to do. (Upstream's
	// equivalent: bg = {}, sprite = {}, merge = {} default-zero with
	// timestamp_last_sync = 0 — but their step loops check
	// `cycle >= cycle_limit` first, so empty state bails out.)
	now := p.sched.Now()
	p.BG.TimestampLastSync = now
	p.BG.Cycle = 1232
	p.BG.TimestampVRAMAccess = -1 // sentinel: matches upstream's u64 ~0
	p.Sprite.TimestampLastSync = now
	p.Sprite.Cycle = p.Sprite.LatchCycleLimit
	p.Sprite.TimestampVRAMAccess = -1
	p.Sprite.TimestampOAMAccess = -1
	// Sprite double-buffer init — upstream ppu.cc:94-95.
	p.Sprite.BufferRD = 0
	p.Sprite.BufferWR = 1
	p.Merge.TimestampLastSync = now
	p.Merge.Cycle = 1006
	p.Merge.TimestampPRAMAccess = 0
	p.Window.TimestampLastSync = now
	p.Window.Cycle = 1024
	// Post-reset state targets the first HDrawVBlank event at 226 cycles
	// (1232 cycle line - 1006, matching upstream's measured value).
	p.sched.AddClass(226, scheduler.EventClassPPUHDrawVBlank, 0, 0)
	// Sprite engine runs from its own event loop (upstream comment).
	p.sched.AddClass(266, scheduler.EventClassPPUBeginSpriteFetch, 0, 0)
}

// Sync ⇄ PPU::Sync. Brings every per-cycle state machine up to the
// current scheduler timestamp so the per-cycle access flags
// (TimestampPRAMAccess / TimestampVRAMAccess / TimestampOAMAccess) are
// fresh for the Bus contention loop. Order matters: DrawWindow runs
// before DrawMerge so the merge compositor reads up-to-date window
// flags (matches upstream PPU::Sync sequence).
func (p *PPU) Sync() {
	p.DrawBackground()
	p.DrawSprite()
	p.DrawWindow()
	p.DrawMerge()
}

// DidAccessPRAM ⇄ PPU::DidAccessPRAM — true if the PPU touched palette
// RAM this cycle (used by the Bus contention loop for u8/u16 PRAM
// accesses).
func (p *PPU) DidAccessPRAM() bool {
	return p.sched.Now() == p.Merge.TimestampPRAMAccess+1
}

// DidAccessVRAM_BG ⇄ PPU::DidAccessVRAM_BG.
func (p *PPU) DidAccessVRAM_BG() bool {
	return p.sched.Now() == p.BG.TimestampVRAMAccess
}

// DidAccessVRAM_OBJ ⇄ PPU::DidAccessVRAM_OBJ.
func (p *PPU) DidAccessVRAM_OBJ() bool {
	return p.sched.Now() == p.Sprite.TimestampVRAMAccess+1
}

// DidAccessOAM ⇄ PPU::DidAccessOAM.
func (p *PPU) DidAccessOAM() bool {
	return p.sched.Now() == p.Sprite.TimestampOAMAccess+1
}

// UpdateVerticalCounterFlag ⇄ PPU::UpdateVerticalCounterFlag. Sets the
// VCount-match flag; on rising edge AND with VCount IRQ enabled,
// schedules the VCount IRQ at +1 cycle (matches upstream — otherwise the
// IRQ would refire while the flag stays set).
func (p *PPU) UpdateVerticalCounterFlag() {
	var newFlag int
	if int(p.VCOUNT) == p.DISPSTAT.VCountSetting {
		newFlag = 1
	}
	if p.DISPSTAT.VCountIRQEnable != 0 && p.DISPSTAT.VCountFlag == 0 && newFlag == 1 {
		// priority=1 matches upstream ppu.cc:230 — ensures the
		// VCount IRQ event fires after other +1-cycle events at the
		// same timestamp (upstream's `// @todo: why is it necessary
		// to set the event priority here?` comment).
		p.sched.AddClass(1, scheduler.EventClassPPUVCountIRQ, 1, 0)
	}
	p.DISPSTAT.VCountFlag = newFlag
}

func (p *PPU) onHBlankStart(late int64) {
	p.DISPSTAT.HBlankFlag = 1
	// Order ⇄ upstream BeginHBlankVDraw (ppu.cc:138-146):
	// (1) DMA request, then (2) HBlank IRQ schedule, then (3) HDraw schedule.
	// Order affects scheduler-queue insertion of equal-timestamp events.
	if p.dma != nil && p.VCOUNT < ScreenHeight {
		p.dma.Request(dma.OccasionHBlank)
	}
	if p.DISPSTAT.HBlankIRQEnable != 0 {
		p.sched.AddClass(1, scheduler.EventClassPPUHBlankIRQ, 0, 0)
	}
	var cls scheduler.EventClass
	if p.VCOUNT < ScreenHeight {
		cls = scheduler.EventClassPPUHDrawVDraw
	} else {
		cls = scheduler.EventClassPPUHDrawVBlank
	}
	p.sched.AddClass(cyclesPerLineHBlank-late, cls, 0, 0)
}

func (p *PPU) onLineEnd(late int64) {
	// Flush draw passes for the scanline. Order must be
	// Background → Window → Merge (upstream ppu.cc:107-109) — Merge reads
	// Window.Buffer per pixel, so Window must finish before Merge runs.
	if p.VCOUNT < ScreenHeight {
		p.DrawBackground()
		p.DrawWindow()
		p.DrawMerge()
	} else {
		// VBlank: upstream BeginHDrawVBlank (ppu.cc:153) flushes only
		// DrawWindow — BG/Merge already terminated at line 159.
		p.DrawWindow()
	}

	// Schedule order mirrors upstream BeginHDrawVDraw (ppu.cc:103-135) for
	// the HDraw transitions (pre-VCOUNT 0..159) and BeginHDrawVBlank
	// (ppu.cc:149-187) for VBlank transitions. Upstream schedules
	// update_vcount_flag and latch_dispcnt BEFORE vcount++, so they go in
	// the heap before the +1 vblank_irq / +1232 hblank events.
	preVCOUNT := p.VCOUNT
	hdrawTransition := preVCOUNT < ScreenHeight
	p.sched.AddClass(1, scheduler.EventClassPPUUpdateVCountFlag, 0, 0)
	if hdrawTransition || preVCOUNT >= 224 {
		// BeginHDrawVDraw unconditional latch (pre 0..159) +
		// BeginHDrawVBlank pre>=224 latch (pre 224..227).
		p.sched.AddClass(40, scheduler.EventClassPPULatchDISPCNT, 0, 0)
	}
	if preVCOUNT == 162 && p.dma != nil {
		// Re-latch DMA3 video transfer state once per frame. Upstream
		// (ppu.cc:159-166) checks pre-increment vcount == 162; checking
		// post-increment would latch one scanline early.
		p.DMA3VideoTransferRunning = p.dma.HasVideoTransferDMA()
	}
	p.DISPSTAT.HBlankFlag = 0
	p.VCOUNT++
	if p.VCOUNT == linesPerFrame {
		p.VCOUNT = 0
		// End of frame: the buffer we just finished writing to is now
		// presentable. Swap so the next frame writes into the OTHER slot
		// and a consumer can pick up Output[Frame^1] as the latest frame.
		p.Frame ^= 1
	}
	p.UpdateVideoTransferDMA()
	switch p.VCOUNT {
	case ScreenHeight:
		// Upstream order in BeginHDrawVDraw (ppu.cc:119-126):
		// hblank_vblank(+1007) → RequestVblankDMA → vblank_flag=1 →
		// vblank_irq(+1) if enabled. We schedule the next HBlank below; do
		// the DMA + IRQ here in upstream's order.
		if p.dma != nil {
			p.dma.Request(dma.OccasionVBlank)
		}
		p.DISPSTAT.VBlankFlag = 1
		if p.DISPSTAT.VBlankIRQEnable != 0 {
			p.sched.AddClass(1, scheduler.EventClassPPUVBlankIRQ, 0, 0)
		}
	case 227:
		// Hardware quirk: VBlankFlag clears one line before VBlank
		// period ends (upstream ppu.cc:184-186 — when vcount
		// post-increments to 227, dispstat.vblank_flag = 0).
		p.DISPSTAT.VBlankFlag = 0
	}
	// Latch new window V flags and background state at the start of the
	// next scanline — matches BeginHDrawVDraw in upstream.
	if p.VCOUNT < ScreenHeight {
		p.InitBackground()
		p.InitMerge()
	}
	p.InitWindow()
	var nextCls scheduler.EventClass
	if p.VCOUNT < ScreenHeight {
		nextCls = scheduler.EventClassPPUHBlankVDraw
	} else {
		nextCls = scheduler.EventClassPPUHBlankVBlank
	}
	p.sched.AddClass(cyclesPerLineHDraw-late, nextCls, 0, 0)
}

// ---------------------------------------------------------------------
// IO dispatch
// ---------------------------------------------------------------------

// UpdateVideoTransferDMA ⇄ PPU::UpdateVideoTransferDMA (ppu.cc).
// Drives DMA3 video-timing transfers: stops at VCOUNT 162, schedules
// a video DMA 3 cycles into HBlank during VCOUNT 2..161.
func (p *PPU) UpdateVideoTransferDMA() {
	if !p.DMA3VideoTransferRunning {
		return
	}
	vcount := int(p.VCOUNT)
	switch {
	case vcount == 162:
		// Upstream ppu.cc:240-241 — call Stop but leave the flag
		// alone; the next frame's vcount==162 re-evaluation will
		// reset it from HasVideoTransferDMA().
		if p.dma != nil {
			p.dma.StopVideoTransferDMA()
		}
	case vcount >= 2 && vcount < 162:
		if p.dma != nil {
			p.sched.AddClass(3, scheduler.EventClassPPUVideoDMA, 0, 0)
		}
	}
}

func (p *PPU) IORead16(addr uint32) (uint16, bool) {
	switch addr {
	case 0x04000000:
		return p.DISPCNT.ReadHalf(), true
	case 0x04000002:
		return p.GREENSWAP, true
	case 0x04000004:
		return p.DISPSTAT.ReadHalf(), true
	case 0x04000006:
		return p.VCOUNT, true
	case 0x04000048: // WININ
		return p.WININ.ReadHalf(), true
	case 0x0400004A: // WINOUT
		return p.WINOUT.ReadHalf(), true
	case 0x04000050: // BLDCNT
		return p.BLDCNT.ReadHalf(), true
	case 0x04000052: // BLDALPHA
		return uint16(p.BLDALPHA.EVA&0x1F) | uint16(p.BLDALPHA.EVB&0x1F)<<8, true
	}
	if addr >= 0x04000008 && addr < 0x04000010 {
		return p.BGCNT[(addr-0x04000008)/2].ReadHalf(), true
	}
	if addr >= 0x04000060 {
		return 0, false
	}
	if addr >= 0x04000000 && addr < 0x04000060 {
		// Write-only registers (BG offsets, affine matrix, BGX/BGY,
		// WIN0H/WIN1H/WIN0V/WIN1V, MOSAIC, BLDY) — upstream's io.cc
		// returns 0 for the matching byte cases too.
		return 0, true
	}
	return 0, false
}

func (p *PPU) IOWrite16(addr uint32, v uint16) bool {
	switch addr {
	case 0x04000000:
		p.DISPCNT.WriteHalf(v)
		return true
	case 0x04000002:
		p.GREENSWAP = v
		return true
	case 0x04000004:
		p.DISPSTAT.WriteHalf(v)
		p.UpdateVerticalCounterFlag()
		return true
	case 0x04000006:
		// VCOUNT is read-only on hardware.
		return true
	}
	if addr >= 0x04000008 && addr < 0x04000010 {
		p.BGCNT[(addr-0x04000008)/2].WriteHalf(v)
		return true
	}
	if addr >= 0x04000010 && addr < 0x04000020 {
		idx := (addr - 0x04000010) / 4
		if addr&2 == 0 {
			p.BGHOFS[idx] = v & 0x1FF
		} else {
			p.BGVOFS[idx] = v & 0x1FF
		}
		return true
	}
	if addr >= 0x04000020 && addr < 0x04000040 {
		// BG2 affine block at 0x20..0x2F, BG3 affine block at 0x30..0x3F.
		// PA/PB/PC/PD = 16-bit fixed-point matrix entries (0x100 = 1.0).
		// BG2X/BG2Y / BG3X/BG3Y = 32-bit signed 28-bit-extended ref points.
		switch addr {
		case 0x04000020:
			p.BGPA[0] = int16(v)
		case 0x04000022:
			p.BGPB[0] = int16(v)
		case 0x04000024:
			p.BGPC[0] = int16(v)
		case 0x04000026:
			p.BGPD[0] = int16(v)
		case 0x04000028:
			p.BG2X.Write(0, uint8(v))
			p.BG2X.Write(1, uint8(v>>8))
		case 0x0400002A:
			p.BG2X.Write(2, uint8(v))
			p.BG2X.Write(3, uint8(v>>8))
		case 0x0400002C:
			p.BG2Y.Write(0, uint8(v))
			p.BG2Y.Write(1, uint8(v>>8))
		case 0x0400002E:
			p.BG2Y.Write(2, uint8(v))
			p.BG2Y.Write(3, uint8(v>>8))
		case 0x04000030:
			p.BGPA[1] = int16(v)
		case 0x04000032:
			p.BGPB[1] = int16(v)
		case 0x04000034:
			p.BGPC[1] = int16(v)
		case 0x04000036:
			p.BGPD[1] = int16(v)
		case 0x04000038:
			p.BG3X.Write(0, uint8(v))
			p.BG3X.Write(1, uint8(v>>8))
		case 0x0400003A:
			p.BG3X.Write(2, uint8(v))
			p.BG3X.Write(3, uint8(v>>8))
		case 0x0400003C:
			p.BG3Y.Write(0, uint8(v))
			p.BG3Y.Write(1, uint8(v>>8))
		case 0x0400003E:
			p.BG3Y.Write(2, uint8(v))
			p.BG3Y.Write(3, uint8(v>>8))
		}
		return true
	}
	if addr >= 0x04000040 && addr < 0x04000060 {
		// windows / mosaic / blend — accepted; some fields decoded below.
		switch addr {
		case 0x04000040:
			p.WIN0H.WriteHalf(v)
		case 0x04000042:
			p.WIN1H.WriteHalf(v)
		case 0x04000044:
			p.WIN0V.WriteHalf(v)
		case 0x04000046:
			p.WIN1V.WriteHalf(v)
		case 0x04000048:
			p.WININ.WriteHalf(v)
		case 0x0400004A:
			p.WINOUT.WriteHalf(v)
		case 0x0400004C:
			p.MOSAIC.Write(0, uint8(v))
			p.MOSAIC.Write(1, uint8(v>>8))
		case 0x04000050:
			p.BLDCNT.WriteHalf(v)
		case 0x04000052:
			p.BLDALPHA.EVA = int(v & 0x1F)
			p.BLDALPHA.EVB = int((v >> 8) & 0x1F)
		case 0x04000054:
			p.BLDY = int(v & 0x1F)
		}
		return true
	}
	return false
}

func (p *PPU) IORead8(addr uint32) (uint8, bool) {
	v, ok := p.IORead16(addr &^ 1)
	if !ok {
		return 0, false
	}
	return uint8(v >> (8 * (addr & 1))), true
}
// IOWrite8 ⇄ Bus::Hardware::WriteByte for the PPU IO range. Most PPU
// registers are write-only, so we can't read-modify-write through
// IORead16 (which returns 0). Each address is handled directly the same
// way upstream's io.cc does.
func (p *PPU) IOWrite8(addr uint32, v uint8) bool {
	switch addr {
	case 0x04000000:
		p.DISPCNT.Write(0, v)
		return true
	case 0x04000001:
		p.DISPCNT.Write(1, v)
		return true
	case 0x04000002:
		p.GREENSWAP = (p.GREENSWAP & 0xFF00) | uint16(v&1)
		return true
	case 0x04000003:
		return true
	case 0x04000004:
		p.DISPSTAT.Write(0, v)
		p.UpdateVerticalCounterFlag()
		return true
	case 0x04000005:
		p.DISPSTAT.Write(1, v)
		p.UpdateVerticalCounterFlag()
		return true
	case 0x04000006, 0x04000007:
		return true // VCOUNT is read-only
	}
	// BG0..3 CNT (4 hwords from 0x08): byte-level dispatch.
	if addr >= 0x04000008 && addr < 0x04000010 {
		id := (addr - 0x04000008) / 2
		p.BGCNT[id].Write(int(addr&1), v)
		return true
	}
	// BG0..3 HOFS/VOFS at 0x10..0x1F: byte-level — upstream writes the
	// low byte directly and ORs bit 8 from byte 1 (1-bit field).
	if addr >= 0x04000010 && addr < 0x04000020 {
		id := (addr - 0x04000010) / 4
		isVOFS := addr&2 != 0
		field := &p.BGHOFS[id]
		if isVOFS {
			field = &p.BGVOFS[id]
		}
		if addr&1 == 0 {
			*field = (*field & 0xFF00) | uint16(v)
		} else {
			*field = (*field & 0x00FF) | (uint16(v&1) << 8)
		}
		return true
	}
	// Affine block 0x20..0x3F.
	if addr >= 0x04000020 && addr < 0x04000040 {
		switch {
		case addr == 0x04000020:
			p.BGPA[0] = (p.BGPA[0] & ^int16(0xFF)) | int16(v)
		case addr == 0x04000021:
			p.BGPA[0] = (p.BGPA[0] & 0xFF) | int16(v)<<8
		case addr == 0x04000022:
			p.BGPB[0] = (p.BGPB[0] & ^int16(0xFF)) | int16(v)
		case addr == 0x04000023:
			p.BGPB[0] = (p.BGPB[0] & 0xFF) | int16(v)<<8
		case addr == 0x04000024:
			p.BGPC[0] = (p.BGPC[0] & ^int16(0xFF)) | int16(v)
		case addr == 0x04000025:
			p.BGPC[0] = (p.BGPC[0] & 0xFF) | int16(v)<<8
		case addr == 0x04000026:
			p.BGPD[0] = (p.BGPD[0] & ^int16(0xFF)) | int16(v)
		case addr == 0x04000027:
			p.BGPD[0] = (p.BGPD[0] & 0xFF) | int16(v)<<8
		case addr >= 0x04000028 && addr <= 0x0400002B:
			p.BG2X.Write(int(addr-0x04000028), v)
		case addr >= 0x0400002C && addr <= 0x0400002F:
			p.BG2Y.Write(int(addr-0x0400002C), v)
		case addr == 0x04000030:
			p.BGPA[1] = (p.BGPA[1] & ^int16(0xFF)) | int16(v)
		case addr == 0x04000031:
			p.BGPA[1] = (p.BGPA[1] & 0xFF) | int16(v)<<8
		case addr == 0x04000032:
			p.BGPB[1] = (p.BGPB[1] & ^int16(0xFF)) | int16(v)
		case addr == 0x04000033:
			p.BGPB[1] = (p.BGPB[1] & 0xFF) | int16(v)<<8
		case addr == 0x04000034:
			p.BGPC[1] = (p.BGPC[1] & ^int16(0xFF)) | int16(v)
		case addr == 0x04000035:
			p.BGPC[1] = (p.BGPC[1] & 0xFF) | int16(v)<<8
		case addr == 0x04000036:
			p.BGPD[1] = (p.BGPD[1] & ^int16(0xFF)) | int16(v)
		case addr == 0x04000037:
			p.BGPD[1] = (p.BGPD[1] & 0xFF) | int16(v)<<8
		case addr >= 0x04000038 && addr <= 0x0400003B:
			p.BG3X.Write(int(addr-0x04000038), v)
		case addr >= 0x0400003C && addr <= 0x0400003F:
			p.BG3Y.Write(int(addr-0x0400003C), v)
		}
		return true
	}
	// Window / mosaic / blend block 0x40..0x5F.
	if addr >= 0x04000040 && addr < 0x04000060 {
		switch addr {
		case 0x04000040:
			p.WIN0H.Write(0, v)
		case 0x04000041:
			p.WIN0H.Write(1, v)
		case 0x04000042:
			p.WIN1H.Write(0, v)
		case 0x04000043:
			p.WIN1H.Write(1, v)
		case 0x04000044:
			p.WIN0V.Write(0, v)
		case 0x04000045:
			p.WIN0V.Write(1, v)
		case 0x04000046:
			p.WIN1V.Write(0, v)
		case 0x04000047:
			p.WIN1V.Write(1, v)
		case 0x04000048:
			p.WININ.Write(0, v)
		case 0x04000049:
			p.WININ.Write(1, v)
		case 0x0400004A:
			p.WINOUT.Write(0, v)
		case 0x0400004B:
			p.WINOUT.Write(1, v)
		case 0x0400004C:
			p.MOSAIC.Write(0, v)
		case 0x0400004D:
			p.MOSAIC.Write(1, v)
		case 0x04000050:
			p.BLDCNT.Write(0, v)
		case 0x04000051:
			p.BLDCNT.Write(1, v)
		case 0x04000052:
			p.BLDALPHA.EVA = int(v & 0x1F)
		case 0x04000053:
			p.BLDALPHA.EVB = int(v & 0x1F)
		case 0x04000054:
			p.BLDY = int(v & 0x1F)
		}
		return true
	}
	return false
}
func (p *PPU) IORead32(addr uint32) (uint32, bool) {
	lo, ok := p.IORead16(addr)
	if !ok {
		return 0, false
	}
	hi, _ := p.IORead16(addr + 2)
	return uint32(lo) | uint32(hi)<<16, true
}
func (p *PPU) IOWrite32(addr uint32, v uint32) bool {
	if !p.IOWrite16(addr, uint16(v)) {
		return false
	}
	p.IOWrite16(addr+2, uint16(v>>16))
	return true
}
