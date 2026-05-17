// Package irq — port of src/nba/src/hw/irq/irq.{hh,cc}.
//
// Upstream models IE/IF/IME with a one-cycle write latency and a two-cycle
// CPU-line-update latency, driven by scheduler events. We port that faithfully:
// writes update `pending_*`, then a scheduled OnWriteIO commits them and
// schedules further events for the actual CPU IRQ line transitions.
package irq

import "github.com/mnmlyw/nanogoadvance/internal/scheduler"

// Source enumerates the IRQ source classes. The Raise() method takes a
// Source and (for Timer/DMA) a channel index.
type Source int

const (
	SourceVBlank Source = iota
	SourceHBlank
	SourceVCount
	SourceTimer
	SourceSerial
	SourceDMA
	SourceKeypad
	SourceROM
)

// CPULine is the slice of the CPU that IRQ touches. nba sets cpu.IRQLine()
// directly (it returns a `bool&`); we use a setter.
type CPULine interface {
	SetIRQLine(asserted bool)
}

// Registers in the I/O space.
const (
	regIE  = 0
	regIF  = 2
	regIME = 4
)

type IRQ struct {
	pendingIME int
	pendingIE  uint16
	pendingIF  uint16

	regIME int
	regIE  uint16
	regIF  uint16

	cpu       CPULine
	scheduler *scheduler.Scheduler
	irqLine   bool

	// irqAvailable mirrors upstream — exposed via ShouldUnhaltCPU.
	irqAvailable bool
}

func New(cpu CPULine, s *scheduler.Scheduler) *IRQ {
	q := &IRQ{cpu: cpu, scheduler: s}
	// Register class callbacks so IRQ events survive save state.
	s.Register(scheduler.EventClassIRQWriteIO, func(uint64) { q.onWriteIO() })
	s.Register(scheduler.EventClassIRQUpdateIEAndIF, func(userData uint64) {
		q.irqAvailable = userData != 0
	})
	s.Register(scheduler.EventClassIRQUpdateIRQLine, func(userData uint64) {
		q.cpu.SetIRQLine(userData != 0)
	})
	q.Reset()
	return q
}

func (q *IRQ) Reset() {
	q.pendingIME = 0
	q.pendingIE = 0
	q.pendingIF = 0
	q.regIME = 0
	q.regIE = 0
	q.regIF = 0
	q.irqLine = false
	q.cpu.SetIRQLine(false)
	q.irqAvailable = false
}

func (q *IRQ) ShouldUnhaltCPU() bool { return q.irqAvailable }

func (q *IRQ) ReadByte(offset int) uint8 {
	switch offset {
	case regIE | 0:
		return uint8(q.regIE)
	case regIE | 1:
		return uint8(q.regIE >> 8)
	case regIF | 0:
		return uint8(q.regIF)
	case regIF | 1:
		return uint8(q.regIF >> 8)
	case regIME:
		if q.regIME != 0 {
			return 1
		}
		return 0
	}
	return 0
}

func (q *IRQ) ReadHalf(offset int) uint16 {
	switch offset {
	case regIE:
		return q.regIE
	case regIF:
		return q.regIF
	case regIME:
		if q.regIME != 0 {
			return 1
		}
		return 0
	}
	return 0
}

func (q *IRQ) WriteByte(offset int, value uint8) {
	switch offset {
	case regIE | 0:
		q.pendingIE = (q.pendingIE & 0x3F00) | uint16(value)
	case regIE | 1:
		q.pendingIE = (q.pendingIE & 0x00FF) | (uint16(value)<<8)&0x3F00
	case regIF | 0:
		q.pendingIF &^= uint16(value)
	case regIF | 1:
		q.pendingIF &^= uint16(value) << 8
	case regIME:
		q.pendingIME = int(value & 1)
	}
	// priority=1: write-driven events fire AFTER source-driven Raise()
	// events at the same timestamp, so a same-cycle IRQ source isn't
	// masked by a register write.
	q.scheduler.AddClass(1, scheduler.EventClassIRQWriteIO, 1, 0)
}

func (q *IRQ) WriteHalf(offset int, value uint16) {
	switch offset {
	case regIE:
		q.pendingIE = value & 0x3FFF
	case regIF:
		q.pendingIF &^= value
	case regIME:
		q.pendingIME = int(value & 1)
	}
	q.scheduler.AddClass(1, scheduler.EventClassIRQWriteIO, 1, 0)
}

func (q *IRQ) Raise(source Source, channel int) {
	switch source {
	case SourceVBlank:
		q.pendingIF |= 1
	case SourceHBlank:
		q.pendingIF |= 2
	case SourceVCount:
		q.pendingIF |= 4
	case SourceTimer:
		q.pendingIF |= 8 << channel
	case SourceSerial:
		q.pendingIF |= 128
	case SourceDMA:
		q.pendingIF |= 256 << channel
	case SourceKeypad:
		q.pendingIF |= 4096
	case SourceROM:
		q.pendingIF |= 8192
	}
	q.scheduler.AddClass(1, scheduler.EventClassIRQWriteIO, 0, 0)
}

func (q *IRQ) onWriteIO() {
	q.regIME = q.pendingIME
	q.regIE = q.pendingIE
	q.regIF = q.pendingIF

	irqAvailableNew := (q.regIE & q.regIF) != 0
	if q.irqAvailable != irqAvailableNew {
		var ud uint64
		if irqAvailableNew {
			ud = 1
		}
		q.scheduler.AddClass(1, scheduler.EventClassIRQUpdateIEAndIF, 0, ud)
	}

	irqLineNew := q.regIME != 0 && irqAvailableNew
	if q.irqLine != irqLineNew {
		var ud uint64
		if irqLineNew {
			ud = 1
		}
		q.scheduler.AddClass(2, scheduler.EventClassIRQUpdateIRQLine, 0, ud)
		q.irqLine = irqLineNew
	}
}
