// serialization.go ⇄ src/nba/src/bus/serialization.cc
package bus

import "github.com/mnmlyw/nanogoadvance/internal/savestate"

// LoadState ⇄ Bus::LoadState. memory.rom backup state is restored
// separately by the core (since the ROM struct is not in this package).
func (b *Bus) LoadState(s *savestate.SaveState) {
	b.EWRAM = s.Bus.Memory.WRAM
	b.IWRAM = s.Bus.Memory.IRAM
	b.Palette = s.Bus.Memory.PRAM
	b.OAM = s.Bus.Memory.OAM
	b.VRAM = s.Bus.Memory.VRAM
	b.BIOSLatch = s.Bus.Memory.Latch.BIOS

	b.Waitcnt.SRAM = s.Bus.IO.Waitcnt.SRAM
	for i := 0; i < 2; i++ {
		b.Waitcnt.WS0[i] = s.Bus.IO.Waitcnt.WS0[i]
		b.Waitcnt.WS1[i] = s.Bus.IO.Waitcnt.WS1[i]
		b.Waitcnt.WS2[i] = s.Bus.IO.Waitcnt.WS2[i]
	}
	b.Waitcnt.PHI = s.Bus.IO.Waitcnt.PHI
	b.Waitcnt.Prefetch = s.Bus.IO.Waitcnt.Prefetch
	b.UpdateWaitStateTable()

	b.Haltcnt = HaltControl(s.Bus.IO.Haltcnt)
	// RCNT lives in core.stubDevice — restored by Core.LoadState.
	b.Postflg = s.Bus.IO.Postflg
	b.PrefetchBufferWasDisabled = s.Bus.PrefetchBufferWasDisabled

	b.prefetch.active = s.Bus.Prefetch.Active
	b.prefetch.headAddress = s.Bus.Prefetch.HeadAddress
	b.prefetch.lastAddress = s.Bus.Prefetch.LastAddress
	b.prefetch.count = int(s.Bus.Prefetch.Count)
	b.prefetch.countdown = int64(s.Bus.Prefetch.Countdown)
	b.prefetch.thumb = s.Bus.Prefetch.Thumb
	if b.prefetch.thumb {
		b.prefetch.opcodeWidth = 2
		b.prefetch.capacity = 8
		b.prefetch.duty = b.wait16[1][b.prefetch.lastAddress>>24]
	} else {
		b.prefetch.opcodeWidth = 4
		b.prefetch.capacity = 4
		b.prefetch.duty = b.wait32[1][b.prefetch.lastAddress>>24]
	}

	b.LastAccess = Access(s.Bus.LastAccess)
	b.ParallelInternalCPUCycleLimit = int64(s.Bus.ParallelInternalCPUCycles)

	// ROM address latch ⇄ SaveState.ROMAddressLatch (lives at the root
	// of upstream's SaveState, not the bus sub-struct; we restore it
	// here because the bus owns the field in this port).
	b.ROMAddressLatch = s.ROMAddressLatch
}

// CopyState ⇄ Bus::CopyState.
func (b *Bus) CopyState(s *savestate.SaveState) {
	s.Bus.Memory.WRAM = b.EWRAM
	s.Bus.Memory.IRAM = b.IWRAM
	s.Bus.Memory.PRAM = b.Palette
	s.Bus.Memory.OAM = b.OAM
	s.Bus.Memory.VRAM = b.VRAM
	s.Bus.Memory.Latch.BIOS = b.BIOSLatch

	s.Bus.IO.Waitcnt.SRAM = b.Waitcnt.SRAM
	for i := 0; i < 2; i++ {
		s.Bus.IO.Waitcnt.WS0[i] = b.Waitcnt.WS0[i]
		s.Bus.IO.Waitcnt.WS1[i] = b.Waitcnt.WS1[i]
		s.Bus.IO.Waitcnt.WS2[i] = b.Waitcnt.WS2[i]
	}
	s.Bus.IO.Waitcnt.PHI = b.Waitcnt.PHI
	s.Bus.IO.Waitcnt.Prefetch = b.Waitcnt.Prefetch

	s.Bus.IO.Haltcnt = uint8(b.Haltcnt)
	// RCNT lives in core.stubDevice — captured by Core.CopyState.
	s.Bus.IO.Postflg = b.Postflg
	s.Bus.PrefetchBufferWasDisabled = b.PrefetchBufferWasDisabled

	s.Bus.Prefetch.Active = b.prefetch.active
	s.Bus.Prefetch.HeadAddress = b.prefetch.headAddress
	s.Bus.Prefetch.LastAddress = b.prefetch.lastAddress
	s.Bus.Prefetch.Count = uint8(b.prefetch.count)
	s.Bus.Prefetch.Countdown = uint8(b.prefetch.countdown)
	s.Bus.Prefetch.Thumb = b.prefetch.thumb

	s.Bus.LastAccess = int32(b.LastAccess)
	s.Bus.ParallelInternalCPUCycles = int32(b.ParallelInternalCPUCycleLimit)
	s.ROMAddressLatch = b.ROMAddressLatch
}
