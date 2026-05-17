// serialization.go ⇄ src/nba/src/hw/dma/serialization.cc
package dma

import "github.com/mnmlyw/nanogoadvance/internal/savestate"

func (d *DMA) LoadState(s *savestate.SaveState) {
	d.shouldReenterTransferLoop = false

	d.hblankSet = s.DMA.HBlankSet
	d.vblankSet = s.DMA.VBlankSet
	d.videoSet = s.DMA.VideoSet
	d.runnableSet = s.DMA.RunnableSet
	d.latch = s.DMA.Latch

	for i := 0; i < 4; i++ {
		src := &s.DMA.Channels[i]
		dst := &d.channels[i]
		control := src.Control

		dst.dstAddr = src.DstAddress
		dst.srcAddr = src.SrcAddress
		dst.length = src.Length

		dst.enable = control&32768 != 0
		dst.repeat = control&512 != 0
		dst.interrupt = control&16384 != 0
		dst.gamepak = control&2048 != 0
		dst.dstCntl = int((control >> 5) & 3)
		dst.srcCntl = int((control >> 7) & 3)
		dst.time = int((control >> 12) & 3)
		dst.size = int((control >> 10) & 1)

		dst.latch.dstAddr = src.Latch.DstAddress
		dst.latch.srcAddr = src.Latch.SrcAddress
		dst.latch.length = src.Latch.Length
		dst.latch.busValue = src.Latch.Bus

		dst.isFIFODMA = src.IsFIFODMA

		// Rebind the scheduler event UID. Core.LoadState already pushed
		// the EventClassDMAActivated event back into the queue with this
		// same UID via Sched.RestoreClassEvents, so this handle stays
		// live and Cancel/lookup paths work after restore. ⇄ upstream
		// channel.event = scheduler.GetEventByUID(channel.event_uid).
		dst.eventID = src.EventUID
		dst.hasEvent = src.EventUID != 0
	}
	d.selectNextDMA()
}

func (d *DMA) CopyState(s *savestate.SaveState) {
	s.DMA.HBlankSet = d.hblankSet
	s.DMA.VBlankSet = d.vblankSet
	s.DMA.VideoSet = d.videoSet
	s.DMA.RunnableSet = d.runnableSet
	s.DMA.Latch = d.latch

	for i := 0; i < 4; i++ {
		src := &d.channels[i]
		dst := &s.DMA.Channels[i]

		dst.DstAddress = src.dstAddr
		dst.SrcAddress = src.srcAddr
		dst.Length = src.length

		var control uint16
		control |= uint16(src.dstCntl) << 5
		control |= uint16(src.srcCntl) << 7
		if src.repeat {
			control |= 512
		}
		control |= uint16(src.size) << 10
		if src.gamepak {
			control |= 2048
		}
		control |= uint16(src.time) << 12
		if src.interrupt {
			control |= 16384
		}
		if src.enable {
			control |= 32768
		}
		dst.Control = control

		dst.Latch.DstAddress = src.latch.dstAddr
		dst.Latch.SrcAddress = src.latch.srcAddr
		dst.Latch.Length = src.latch.length
		dst.Latch.Bus = src.latch.busValue

		dst.IsFIFODMA = src.isFIFODMA
		dst.EventUID = uint64(src.eventID)
	}
}
