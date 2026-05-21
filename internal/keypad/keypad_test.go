// Headless unit tests for the keypad. The interrupt path requires
// a wired IRQ controller and scheduler — both are pure Go so no
// fixtures needed.
package keypad

import (
	"testing"

	"github.com/mnmlyw/nanogoadvance/internal/irq"
	"github.com/mnmlyw/nanogoadvance/internal/scheduler"
)

// stubCPU is a no-op irq.CPULine — the keypad tests only care about
// whether the IRQ controller would assert, not whether the CPU
// receives it.
type stubCPU struct{}

func (stubCPU) SetIRQLine(bool) {}

func newKeypad() *KeyPad {
	s := scheduler.New()
	s.Reset()
	irqc := irq.New(stubCPU{}, s)
	irqc.Reset()
	k := New(s, irqc)
	return k
}

func TestResetActiveLow(t *testing.T) {
	k := newKeypad()
	// Active-low: all bits set on reset = all keys released.
	// KEYINPUT low byte should read 0xFF, high byte should read 0x03 (top 2 bits of 10).
	if got := k.ReadInputByte(0); got != 0xFF {
		t.Errorf("ReadInputByte(0) = %#x, want 0xFF", got)
	}
	if got := k.ReadInputByte(1); got != 0x03 {
		t.Errorf("ReadInputByte(1) = %#x, want 0x03", got)
	}
}

func TestSetKeyStatusClearsBit(t *testing.T) {
	k := newKeypad()
	k.SetKeyStatus(KeyA, true) // pressed → bit 0 clears
	if got := k.ReadInputByte(0); got&1 != 0 {
		t.Errorf("after KeyA press: bit 0 should be 0, got byte %#x", got)
	}
	k.SetKeyStatus(KeyA, false)
	if got := k.ReadInputByte(0); got&1 == 0 {
		t.Errorf("after KeyA release: bit 0 should be 1, got byte %#x", got)
	}
}

func TestKEYCNTRoundTrip(t *testing.T) {
	k := newKeypad()
	// Write mask 0x015A, interrupt enable on, mode AND.
	k.WriteControlByte(0, 0x5A)
	k.WriteControlByte(1, 0x01|64|128) // bits 8 of mask + interrupt enable + AND mode
	if got := k.ReadControlByte(0); got != 0x5A {
		t.Errorf("control[0] = %#x, want 0x5A", got)
	}
	if got := k.ReadControlByte(1); got != (0x01 | 64 | 128) {
		t.Errorf("control[1] = %#x, want 0xC1", got)
	}
}

func TestIRQModeOR(t *testing.T) {
	s := scheduler.New()
	s.Reset()
	irqc := irq.New(stubCPU{}, s)
	irqc.Reset()
	k := New(s, irqc)
	// Enable keypad IRQ in IE.
	irqc.WriteHalf(0, 1<<12)
	// Master enable IME.
	irqc.WriteByte(4, 1)
	// Drain the +1 scheduler events that IRQ.WriteByte/WriteHalf
	// schedule for register commit.
	s.Advance(2)

	// Mode OR (default), mask = KeyA bit.
	k.WriteControlHalf((1 << KeyA) | 0x4000) // mask=A, interrupt enabled
	if irqc.ShouldUnhaltCPU() {
		t.Fatalf("IRQ asserted before any key press")
	}
	k.SetKeyStatus(KeyA, true)
	// Raise schedules a +1 onWriteIO event which then schedules a
	// +1 EventClassIRQUpdateIEAndIF that sets irqAvailable. Advance
	// past both.
	s.Advance(3)
	if !irqc.ShouldUnhaltCPU() {
		t.Errorf("IRQ should fire on A press under mode OR")
	}
}

func TestIRQModeAND(t *testing.T) {
	s := scheduler.New()
	s.Reset()
	irqc := irq.New(stubCPU{}, s)
	irqc.Reset()
	k := New(s, irqc)
	irqc.WriteHalf(0, 1<<12)
	irqc.WriteByte(4, 1)
	s.Advance(2)

	// Mode AND, mask = A + B (must press BOTH).
	k.WriteControlHalf((1 << KeyA) | (1 << KeyB) | 0x4000 | 0x8000)
	k.SetKeyStatus(KeyA, true)
	s.Advance(3)
	if irqc.ShouldUnhaltCPU() {
		t.Fatalf("IRQ asserted with only A pressed under mode AND")
	}
	k.SetKeyStatus(KeyB, true)
	s.Advance(3)
	if !irqc.ShouldUnhaltCPU() {
		t.Errorf("IRQ should fire with both A and B pressed under mode AND")
	}
}
