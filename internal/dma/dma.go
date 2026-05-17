// Package dma — port of src/nba/src/hw/dma/dma.{hh,cc}.
//
// Mirrors upstream channel layout, address-control modes, timing modes, the
// "runnable_set" bitset abstraction, channel scheduling on enable, and the
// active-channel preemption logic when a higher-priority channel becomes
// runnable mid-transfer.
package dma

import (
	"github.com/mnmlyw/nanogoadvance/internal/bus"
	"github.com/mnmlyw/nanogoadvance/internal/irq"
	"github.com/mnmlyw/nanogoadvance/internal/scheduler"
)

// eepromSizeHinter ⇄ the SetSizeHint slice of nba::EEPROM that DMA needs
// for auto-size detection. Mirrors EEPROM::SetSizeHint and the SIZE_4K /
// SIZE_64K constants.
type eepromSizeHinter interface {
	SetSizeHint(size int)
}

const (
	eepromSize4K  = 0
	eepromSize64K = 1
)

// Occasion mirrors DMA::Occasion. Request() inspects each enabled channel's
// `time` and schedules those that match.
type Occasion int

const (
	OccasionHBlank Occasion = iota
	OccasionVBlank
	OccasionVideo
	OccasionFIFO0
	OccasionFIFO1
)

const (
	regSAD   = 0
	regDAD   = 4
	regCNT_L = 8
	regCNT_H = 10
)

// Per-channel address-control and timing tables — direct ports of the
// static constexpr tables in dma.cc.
var (
	srcModify = [2][4]int32{
		{2, -2, 0, 0},
		{4, -4, 0, 0},
	}
	dstModify = [2][4]int32{
		{2, -2, 0, 2},
		{4, -4, 0, 4},
	}
	srcMask = [4]uint32{0x07FFFFFF, 0x0FFFFFFF, 0x0FFFFFFF, 0x0FFFFFFF}
	dstMask = [4]uint32{0x07FFFFFF, 0x07FFFFFF, 0x07FFFFFF, 0x0FFFFFFF}
	lenMask = [4]uint32{0x3FFF, 0x3FFF, 0x3FFF, 0xFFFF}
)

// Address-control modes — match Channel::Control.
const (
	ctlIncrement = 0
	ctlDecrement = 1
	ctlFixed     = 2
	ctlReload    = 3
)

// Timing modes — match Channel::Timing.
const (
	timingImmediate = 0
	timingVBlank    = 1
	timingHBlank    = 2
	timingSpecial   = 3
)

// Size — match Channel::Size.
const (
	sizeHalf = 0
	sizeWord = 1
)

// dmaFromBitset matches the `g_dma_from_bitset` lookup — highest-priority
// runnable DMA index, or -1.
var dmaFromBitset = [16]int{-1, 0, 1, 0, 2, 0, 1, 0, 3, 0, 1, 0, 2, 0, 1, 0}

type channelLatch struct {
	length   uint32
	dstAddr  uint32
	srcAddr  uint32
	busValue uint32
}

type channel struct {
	id        int
	enable    bool
	repeat    bool
	interrupt bool
	gamepak   bool

	length  uint16
	dstAddr uint32
	srcAddr uint32

	dstCntl int
	srcCntl int
	time    int
	size    int

	latch     channelLatch
	isFIFODMA bool

	// `event` mirrors upstream's `Scheduler::Event*` — we track the scheduler
	// EventID so it can be cancelled when the channel is disabled before its
	// 2-cycle activation delay elapses.
	eventID    scheduler.EventID
	hasEvent   bool
}

type DMA struct {
	bus       *bus.Bus
	irq       *irq.IRQ
	scheduler *scheduler.Scheduler

	channels [4]channel

	activeDMAID              int
	shouldReenterTransferLoop bool

	hblankSet  uint8 // bitset of 4 channels
	vblankSet  uint8
	videoSet   uint8
	runnableSet uint8

	latch uint32
}

func New(b *bus.Bus, irqc *irq.IRQ, s *scheduler.Scheduler) *DMA {
	d := &DMA{bus: b, irq: irqc, scheduler: s}
	// Register the DMA-activated class callback so the 2-cycle delay
	// between enable and start survives save state. userData carries the
	// channel ID.
	s.Register(scheduler.EventClassDMAActivated, func(userData uint64) {
		d.onActivated(userData)
	})
	d.Reset()
	return d
}

// RequestFIFO satisfies apu.DMARequester — translates FIFO 0/1 to the
// matching DMA occasion.
func (d *DMA) RequestFIFO(idx int) {
	if idx == 0 {
		d.Request(OccasionFIFO0)
	} else {
		d.Request(OccasionFIFO1)
	}
}

func (d *DMA) Reset() {
	d.activeDMAID = -1
	d.shouldReenterTransferLoop = false
	d.hblankSet = 0
	d.vblankSet = 0
	d.videoSet = 0
	d.runnableSet = 0
	for i := range d.channels {
		d.channels[i] = channel{id: i, dstCntl: ctlIncrement, srcCntl: ctlIncrement}
	}
}

func (d *DMA) IsRunning() bool { return d.runnableSet != 0 }

func (d *DMA) GetOpenBusValue() uint32 { return d.latch }

func (d *DMA) scheduleDMAs(bitset uint8) {
	for bitset != 0 {
		chanID := dmaFromBitset[bitset]
		bitset &^= 1 << chanID
		ch := chanID // capture
		id := d.scheduler.AddClass(2, scheduler.EventClassDMAActivated, 0, uint64(ch))
		d.channels[chanID].eventID = id
		d.channels[chanID].hasEvent = true
	}
}

func (d *DMA) onActivated(chanID uint64) {
	d.channels[chanID].hasEvent = false
	if d.runnableSet == 0 {
		d.activeDMAID = int(chanID)
	} else if int(chanID) < d.activeDMAID {
		d.activeDMAID = int(chanID)
		d.shouldReenterTransferLoop = true
	}
	d.runnableSet |= 1 << chanID
}

func (d *DMA) selectNextDMA() {
	d.activeDMAID = dmaFromBitset[d.runnableSet]
}

func (d *DMA) Request(occasion Occasion) {
	switch occasion {
	case OccasionHBlank:
		d.scheduleDMAs(d.hblankSet)
	case OccasionVBlank:
		d.scheduleDMAs(d.vblankSet)
	case OccasionVideo:
		d.scheduleDMAs(d.videoSet)
	case OccasionFIFO0:
		if d.channels[1].enable && d.channels[1].time == timingSpecial {
			d.scheduleDMAs(2)
		}
	case OccasionFIFO1:
		if d.channels[2].enable && d.channels[2].time == timingSpecial {
			d.scheduleDMAs(4)
		}
	}
}

func (d *DMA) StopVideoTransferDMA() {
	ch := &d.channels[3]
	if ch.enable {
		ch.enable = false
		d.onChannelWritten(ch, true)
	}
}

func (d *DMA) HasVideoTransferDMA() bool {
	return d.channels[3].enable && d.channels[3].time == timingSpecial
}

// Run ⇄ DMA::Run. Drains all runnable channels and returns the number of
// scheduler cycles consumed — used by Bus.Idle to gate the parallel CPU
// internal-cycle budget.
func (d *DMA) Run() int64 {
	t0 := d.scheduler.Now()
	d.bus.Step(1)
	for d.IsRunning() {
		d.runChannel()
	}
	d.bus.Step(1)
	return d.scheduler.Now() - t0
}

func (d *DMA) runChannel() {
	ch := &d.channels[d.activeDMAID]
	var dstMod, srcMod int32
	size := ch.size
	if ch.isFIFODMA {
		size = sizeWord
		dstMod = 0
	} else {
		dstMod = dstModify[size][ch.dstCntl]
	}
	srcMod = srcModify[size][ch.srcCntl]

	didAccessROM := false

	for ch.latch.length != 0 {
		if d.shouldReenterTransferLoop {
			d.shouldReenterTransferLoop = false
			return
		}
		srcAddr := ch.latch.srcAddr
		dstAddr := ch.latch.dstAddr

		accessSrc := bus.AccessSequential | bus.AccessDma
		accessDst := bus.AccessSequential | bus.AccessDma
		if !didAccessROM {
			if srcAddr >= 0x08000000 {
				accessSrc = bus.AccessNonsequential | bus.AccessDma
				didAccessROM = true
			} else if dstAddr >= 0x08000000 {
				accessDst = bus.AccessNonsequential | bus.AccessDma
				didAccessROM = true
			}
		}

		if size == sizeHalf {
			var value uint16
			if srcAddr >= 0x02000000 {
				value = d.bus.ReadHalf(srcAddr, accessSrc)
				ch.latch.busValue = (uint32(value) << 16) | uint32(value)
				d.latch = ch.latch.busValue
			} else {
				if dstAddr&2 != 0 {
					value = uint16(ch.latch.busValue >> 16)
				} else {
					value = uint16(ch.latch.busValue)
				}
				d.bus.Idle()
			}
			d.bus.WriteHalf(dstAddr, value, accessDst)
		} else {
			if srcAddr >= 0x02000000 {
				ch.latch.busValue = d.bus.ReadWord(srcAddr, accessSrc)
				d.latch = ch.latch.busValue
			} else {
				d.bus.Idle()
			}
			d.bus.WriteWord(dstAddr, ch.latch.busValue, accessDst)
		}

		ch.latch.srcAddr = uint32(int32(ch.latch.srcAddr) + srcMod)
		ch.latch.dstAddr = uint32(int32(ch.latch.dstAddr) + dstMod)
		ch.latch.length--
	}

	d.runnableSet &^= 1 << ch.id

	if ch.interrupt {
		d.irq.Raise(irq.SourceDMA, ch.id)
	}

	if ch.repeat && ch.time != timingImmediate {
		if ch.isFIFODMA {
			ch.latch.length = 4
		} else {
			ch.latch.length = uint32(ch.length) & lenMask[ch.id]
			if ch.latch.length == 0 {
				ch.latch.length = lenMask[ch.id] + 1
			}
		}
		if ch.dstCntl == ctlReload && !ch.isFIFODMA {
			var mask uint32
			if ch.size == sizeWord {
				mask = ^uint32(3)
			} else {
				mask = ^uint32(1)
			}
			ch.latch.dstAddr = ch.dstAddr & mask
		}
	} else {
		d.removeChannelFromDMASets(ch)
		ch.enable = false
	}

	d.selectNextDMA()
}

// Read returns one byte of a DMA channel's registers.
func (d *DMA) Read(chanID int, offset int) uint8 {
	ch := &d.channels[chanID]
	switch offset {
	case regCNT_H | 0:
		return uint8((ch.dstCntl << 5) | (ch.srcCntl << 7))
	case regCNT_H | 1:
		v := uint8(ch.srcCntl >> 1)
		v |= uint8(ch.size << 2)
		v |= uint8(ch.time << 4)
		if ch.repeat {
			v |= 2
		}
		if ch.gamepak {
			v |= 8
		}
		if ch.interrupt {
			v |= 64
		}
		if ch.enable {
			v |= 128
		}
		return v
	}
	return 0
}

// Write applies one byte to a DMA channel's registers and may schedule
// channel activation.
func (d *DMA) Write(chanID int, offset int, value uint8) {
	ch := &d.channels[chanID]
	switch offset {
	case regSAD | 0, regSAD | 1, regSAD | 2, regSAD | 3:
		shift := offset * 8
		ch.srcAddr &^= 0xFF << shift
		ch.srcAddr |= (uint32(value) << shift) & srcMask[chanID]
	case regDAD | 0, regDAD | 1, regDAD | 2, regDAD | 3:
		shift := (offset - 4) * 8
		ch.dstAddr &^= 0xFF << shift
		ch.dstAddr |= (uint32(value) << shift) & dstMask[chanID]
	case regCNT_L | 0:
		ch.length = (ch.length & 0xFF00) | uint16(value)
	case regCNT_L | 1:
		ch.length = (ch.length & 0x00FF) | (uint16(value) << 8)
	case regCNT_H | 0:
		ch.dstCntl = int((value >> 5) & 3)
		ch.srcCntl = (ch.srcCntl & 0b10) | int(value>>7)
	case regCNT_H | 1:
		enableOld := ch.enable
		ch.srcCntl = (ch.srcCntl & 0b01) | int((value&1)<<1)
		ch.size = int((value >> 2) & 1)
		ch.time = int((value >> 4) & 3)
		ch.repeat = value&2 != 0
		ch.gamepak = value&8 != 0 && chanID == 3
		ch.interrupt = value&64 != 0
		ch.enable = value&128 != 0
		d.onChannelWritten(ch, enableOld)
	}
}

func (d *DMA) onChannelWritten(ch *channel, enableOld bool) {
	enableNew := ch.enable
	d.removeChannelFromDMASets(ch)

	if enableNew {
		if !enableOld {
			ch.latch.dstAddr = ch.dstAddr
			ch.latch.srcAddr = ch.srcAddr

			if ch.time == timingSpecial && (ch.id == 1 || ch.id == 2) {
				ch.isFIFODMA = true
				ch.size = sizeWord
				ch.latch.length = 4
				ch.latch.srcAddr &^= 3
				ch.latch.dstAddr &^= 3
			} else {
				ch.isFIFODMA = false
				var mask uint32
				if ch.size == sizeWord {
					mask = ^uint32(3)
				} else {
					mask = ^uint32(1)
				}
				ch.latch.srcAddr &= mask
				ch.latch.dstAddr &= mask
				ch.latch.length = uint32(ch.length) & lenMask[ch.id]
				if ch.latch.length == 0 {
					ch.latch.length = lenMask[ch.id] + 1
				}

				if ch.time == timingImmediate {
					d.scheduleDMAs(1 << ch.id)
				} else {
					d.addChannelToDMASet(ch)
				}

				// EEPROM auto-size detection (dma.cc lines 371-384).
				// 6-bit address protocol → 9 or 73 word transfers (4K-bit);
				// 14-bit address protocol → 17 or 81 (64K-bit).
				if ch.dstAddr >= 0x0D000000 {
					length := int(ch.length)
					if hint, ok := d.bus.BackupEEPROM.(eepromSizeHinter); ok && d.bus.BackupEEPROM != nil {
						if length == 9 || length == 73 {
							hint.SetSizeHint(eepromSize4K)
						}
						if length == 17 || length == 81 {
							hint.SetSizeHint(eepromSize64K)
						}
					}
				}
			}
		} else {
			// Enable was already set; re-enrolment of the channel.
			if !ch.hasEvent {
				d.addChannelToDMASet(ch)
				if ch.id == d.activeDMAID {
					d.shouldReenterTransferLoop = true
				}
			}
		}
	} else {
		d.runnableSet &^= 1 << ch.id
		if ch.hasEvent {
			d.scheduler.Cancel(ch.eventID)
			ch.hasEvent = false
		}
		if ch.id == d.activeDMAID {
			d.shouldReenterTransferLoop = true
			d.selectNextDMA()
		}
	}
}

func (d *DMA) addChannelToDMASet(ch *channel) {
	switch ch.time {
	case timingHBlank:
		d.hblankSet |= 1 << ch.id
	case timingVBlank:
		d.vblankSet |= 1 << ch.id
	case timingSpecial:
		if ch.id == 3 {
			d.videoSet |= 1 << 3
		}
	}
}

func (d *DMA) removeChannelFromDMASets(ch *channel) {
	mask := uint8(1 << ch.id)
	d.hblankSet &^= mask
	d.vblankSet &^= mask
	d.videoSet &^= mask
}
