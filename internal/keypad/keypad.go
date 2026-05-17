// Package keypad — port of src/nba/src/hw/keypad/keypad.{hh,cc}.
package keypad

import (
	"github.com/mnmlyw/nanogoadvance/internal/irq"
	"github.com/mnmlyw/nanogoadvance/internal/scheduler"
)

// Key — values match nba::Key in nba/core.hh.
type Key int

const (
	KeyA      Key = 0
	KeyB      Key = 1
	KeySelect Key = 2
	KeyStart  Key = 3
	KeyRight  Key = 4
	KeyLeft   Key = 5
	KeyUp     Key = 6
	KeyDown   Key = 7
	KeyR      Key = 8
	KeyL      Key = 9
)

// Mode mirrors KeyControl::Mode.
type Mode int

const (
	ModeLogicalOR  Mode = 0
	ModeLogicalAND Mode = 1
)

type KeyPad struct {
	scheduler *scheduler.Scheduler
	irq       *irq.IRQ

	input struct {
		value uint16 // active-low: bit set = button released
	}
	control struct {
		mask      uint16
		interrupt bool
		mode      Mode
	}
}

func New(s *scheduler.Scheduler, irqc *irq.IRQ) *KeyPad {
	k := &KeyPad{scheduler: s, irq: irqc}
	k.Reset()
	return k
}

func (k *KeyPad) Reset() {
	k.input.value = 0x3FF
	k.control.mask = 0
	k.control.interrupt = false
	k.control.mode = ModeLogicalOR
}

func (k *KeyPad) SetKeyStatus(key Key, pressed bool) {
	bit := uint16(1) << key
	if pressed {
		k.input.value &^= bit
	} else {
		k.input.value |= bit
	}
	k.updateIRQ()
}

func (k *KeyPad) updateIRQ() {
	if !k.control.interrupt {
		return
	}
	notInput := ^k.input.value & 0x3FF
	if k.control.mode == ModeLogicalAND {
		if k.control.mask == notInput {
			k.irq.Raise(irq.SourceKeypad, 0)
		}
	} else if k.control.mask&notInput != 0 {
		k.irq.Raise(irq.SourceKeypad, 0)
	}
}

// KEYINPUT @ 0x04000130, KEYCNT @ 0x04000132 — exposed via IORead/Write.

func (k *KeyPad) ReadInputByte(offset int) uint8 {
	switch offset {
	case 0:
		return uint8(k.input.value)
	case 1:
		return uint8(k.input.value >> 8)
	}
	return 0
}

func (k *KeyPad) ReadControlByte(offset int) uint8 {
	switch offset {
	case 0:
		return uint8(k.control.mask)
	case 1:
		v := uint8((k.control.mask >> 8) & 3)
		if k.control.interrupt {
			v |= 64
		}
		v |= uint8(k.control.mode) << 7
		return v
	}
	return 0
}

func (k *KeyPad) WriteControlByte(offset int, value uint8) {
	switch offset {
	case 0:
		k.control.mask = (k.control.mask & 0xFF00) | uint16(value)
	case 1:
		k.control.mask = (k.control.mask & 0x00FF) | (uint16(value)&3)<<8
		k.control.interrupt = value&64 != 0
		k.control.mode = Mode(value >> 7)
	}
	k.updateIRQ()
}

func (k *KeyPad) WriteControlHalf(value uint16) {
	k.control.mask = value & 0x03FF
	k.control.interrupt = value&0x4000 != 0
	k.control.mode = Mode(value >> 15)
	k.updateIRQ()
}
