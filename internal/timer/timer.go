// Package timer — port of src/nba/src/hw/timer/timer.{hh,cc}.
//
// Preserves the "pending" register write model (writes don't take effect
// immediately — they are committed by scheduler events one cycle later),
// the prescaler-aligned start, and the cascade-on-overflow chain.
package timer

import (
	"github.com/mnmlyw/nanogoadvance/internal/irq"
	"github.com/mnmlyw/nanogoadvance/internal/scheduler"
)

// APU is the small interface Timer needs from the APU (FIFO drain on
// timer 0/1 overflow). Optional — nil means no APU is attached.
type APU interface {
	OnTimerOverflow(timerID int, times int)
}

const (
	regCNT_L = 0
	regCNT_H = 2
)

var (
	ticksShift = [4]int{0, 6, 8, 10}
	ticksMask  = [4]int{0, 0x3F, 0xFF, 0x3FF}
)

type pending struct {
	reload  uint16
	control uint16
}

type control struct {
	frequency int
	cascade   bool
	interrupt bool
	enable    bool
}

type channel struct {
	id      int
	reload  uint16
	counter uint32
	pending pending
	control control

	running          bool
	shift            int
	mask             int
	timestampStarted int64

	eventOverflow scheduler.EventID
	hasEvent      bool
}

type Timer struct {
	scheduler *scheduler.Scheduler
	irq       *irq.IRQ
	apu       APU
	channels  [4]channel
}

func New(s *scheduler.Scheduler, irqc *irq.IRQ, apu APU) *Timer {
	t := &Timer{scheduler: s, irq: irqc, apu: apu}
	// Register class callback for timer overflow events so they survive
	// save-state round trip. userData carries the channel index.
	s.Register(scheduler.EventClassTMOverflow, func(userData uint64) {
		t.onOverflow(userData)
	})
	s.Register(scheduler.EventClassTMWriteReload, func(userData uint64) {
		t.onReloadWritten(userData)
	})
	s.Register(scheduler.EventClassTMWriteControl, func(userData uint64) {
		t.onControlWritten(userData)
	})
	t.Reset()
	return t
}

func (t *Timer) Reset() {
	for i := range t.channels {
		t.channels[i] = channel{id: i}
	}
}

func (t *Timer) ReadByte(chanID, offset int) uint8 {
	ch := &t.channels[chanID]
	switch offset {
	case regCNT_L | 0:
		return uint8(t.readCounter(ch))
	case regCNT_L | 1:
		return uint8(t.readCounter(ch) >> 8)
	case regCNT_H:
		return uint8(t.readControl(ch))
	}
	return 0
}

func (t *Timer) ReadHalf(chanID, offset int) uint16 {
	ch := &t.channels[chanID]
	switch offset {
	case regCNT_L:
		return t.readCounter(ch)
	case regCNT_H:
		return t.readControl(ch)
	}
	return 0
}

func (t *Timer) ReadWord(chanID int) uint32 {
	ch := &t.channels[chanID]
	return (uint32(t.readControl(ch)) << 16) | uint32(t.readCounter(ch))
}

func (t *Timer) WriteByte(chanID, offset int, value uint8) {
	ch := &t.channels[chanID]
	switch offset {
	case regCNT_L | 0:
		t.writeReload(ch, (ch.pending.reload&0xFF00)|uint16(value))
	case regCNT_L | 1:
		t.writeReload(ch, (ch.pending.reload&0x00FF)|(uint16(value)<<8))
	case regCNT_H:
		t.writeControl(ch, uint16(value))
	}
}

func (t *Timer) WriteHalf(chanID, offset int, value uint16) {
	ch := &t.channels[chanID]
	switch offset {
	case regCNT_L:
		t.writeReload(ch, value)
	case regCNT_H:
		t.writeControl(ch, value)
	}
}

func (t *Timer) WriteWord(chanID int, value uint32) {
	ch := &t.channels[chanID]
	t.writeReload(ch, uint16(value))
	t.writeControl(ch, uint16(value>>16))
}

func (t *Timer) readCounter(ch *channel) uint16 {
	counter := ch.counter
	if ch.running {
		counter += t.getCounterDeltaSinceLastUpdate(ch)
	}
	return uint16(counter)
}

func (t *Timer) writeReload(ch *channel, value uint16) {
	ch.pending.reload = value
	id := ch.id
	// priority=1: write-reload fires after overflow (pri 0) at same ts.
	t.scheduler.AddClass(1, scheduler.EventClassTMWriteReload, 1, uint64(id))
}

func (t *Timer) readControl(ch *channel) uint16 {
	var v uint16 = uint16(ch.control.frequency)
	if ch.control.cascade {
		v |= 4
	}
	if ch.control.interrupt {
		v |= 64
	}
	if ch.control.enable {
		v |= 128
	}
	return v
}

func (t *Timer) writeControl(ch *channel, value uint16) {
	ch.pending.control = value
	id := ch.id
	// priority=2: write-control fires last among same-ts timer events.
	t.scheduler.AddClass(1, scheduler.EventClassTMWriteControl, 2, uint64(id))
}

func (t *Timer) onReloadWritten(chanID uint64) {
	t.channels[chanID].reload = t.channels[chanID].pending.reload
}

func (t *Timer) onControlWritten(chanID uint64) {
	ch := &t.channels[chanID]
	enablePrevious := ch.control.enable
	value := ch.pending.control

	if ch.running {
		t.stopChannel(ch)
	}

	ch.control.frequency = int(value & 3)
	ch.control.interrupt = value&64 != 0
	ch.control.enable = value&128 != 0
	if ch.id != 0 {
		ch.control.cascade = value&4 != 0
	}

	ch.shift = ticksShift[ch.control.frequency]
	ch.mask = ticksMask[ch.control.frequency]

	if ch.control.enable {
		prescalerOffset := t.scheduler.Now() & int64(ch.mask)
		if enablePrevious {
			if !ch.control.cascade {
				t.startChannel(ch, prescalerOffset)
			}
		} else {
			if ch.control.cascade {
				ch.counter = uint32(ch.reload)
			} else if ch.counter == 0xFFFF && prescalerOffset == 0 {
				t.startChannel(ch, 0)
			} else {
				ch.counter = uint32(ch.reload)
				t.startChannel(ch, prescalerOffset-1)
			}
		}
	}
}

func (t *Timer) getCounterDeltaSinceLastUpdate(ch *channel) uint32 {
	return uint32((t.scheduler.Now() - ch.timestampStarted) >> ch.shift)
}

func (t *Timer) startChannel(ch *channel, cycleOffset int64) {
	cycles := int64(0x10000-int64(ch.counter))<<ch.shift - cycleOffset
	ch.running = true
	ch.timestampStarted = t.scheduler.Now() - cycleOffset
	id := ch.id
	ch.eventOverflow = t.scheduler.AddClass(cycles, scheduler.EventClassTMOverflow, 0, uint64(id))
	ch.hasEvent = true
}

func (t *Timer) stopChannel(ch *channel) {
	ch.counter += t.getCounterDeltaSinceLastUpdate(ch)
	if ch.counter >= 0x10000 {
		t.reloadCascadeAndRequestIRQ(ch)
	}
	if ch.hasEvent {
		t.scheduler.Cancel(ch.eventOverflow)
		ch.hasEvent = false
	}
	ch.running = false
}

func (t *Timer) reloadCascadeAndRequestIRQ(ch *channel) {
	ch.counter = uint32(ch.reload)
	if ch.control.interrupt {
		t.irq.Raise(irq.SourceTimer, ch.id)
	}
	if ch.id <= 1 && t.apu != nil {
		t.apu.OnTimerOverflow(ch.id, 1)
	}
	if ch.id != 3 {
		next := &t.channels[ch.id+1]
		if next.control.enable && next.control.cascade {
			next.counter++
			if next.counter == 0x10000 {
				t.reloadCascadeAndRequestIRQ(next)
			}
		}
	}
}

func (t *Timer) onOverflow(chanID uint64) {
	ch := &t.channels[chanID]
	t.reloadCascadeAndRequestIRQ(ch)
	t.startChannel(ch, 0)
}
