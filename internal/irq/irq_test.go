package irq

import (
	"testing"

	"github.com/mnmlyw/nanogoadvance/internal/scheduler"
)

type stubCPU struct{ asserted bool }

func (s *stubCPU) SetIRQLine(b bool) { s.asserted = b }

func newIRQ() (*IRQ, *scheduler.Scheduler, *stubCPU) {
	s := scheduler.New()
	s.Reset()
	cpu := &stubCPU{}
	q := New(cpu, s)
	q.Reset()
	return q, s, cpu
}

// TestRaiseSetsCPULineAfterPipeline — Raise sets pendingIF, then a +1
// scheduler event commits IE/IF, then a +1 event sets the CPU IRQ line.
// 2-cycle pipeline minimum from Raise → SetIRQLine(true).
func TestRaiseSetsCPULineAfterPipeline(t *testing.T) {
	q, s, cpu := newIRQ()
	// Enable VBlank IRQ in IE + master enable IME.
	q.WriteHalf(0, 1)  // IE = 0x0001
	q.WriteByte(4, 1)  // IME = 1
	s.Advance(2)       // Drain the +1 onWriteIO + +1 IRQLine update.

	if cpu.asserted {
		t.Fatalf("CPU IRQ line asserted before any source raised")
	}
	q.Raise(SourceVBlank, 0)
	s.Advance(4) // generous — covers Raise → onWriteIO → IRQLine
	if !cpu.asserted {
		t.Errorf("CPU IRQ line should be asserted after VBlank raise + 4 cycles")
	}
}

// TestWriteIFClearsBitsViaW1C — writing 1 to IF clears matching bits
// (upstream's write-1-to-clear semantics).
func TestWriteIFClearsBitsViaW1C(t *testing.T) {
	q, s, _ := newIRQ()
	q.WriteHalf(0, 1) // IE = 0x0001
	q.WriteByte(4, 1) // IME = 1
	s.Advance(2)

	q.Raise(SourceVBlank, 0)
	s.Advance(4)
	if q.regIF&1 == 0 {
		t.Fatalf("IF VBlank bit not set after Raise; IF=%#x", q.regIF)
	}

	// Write 1 to bit 0 of IF — should clear it (W1C semantics).
	q.WriteHalf(regIF, 1)
	s.Advance(2)
	if q.regIF&1 != 0 {
		t.Errorf("IF VBlank bit should clear on W1C; IF=%#x", q.regIF)
	}
}

// TestIMEMaskBlocksCPULine — even with IE&IF != 0, IME=0 keeps the
// CPU IRQ line low. Setting IME=1 must immediately re-evaluate.
func TestIMEMaskBlocksCPULine(t *testing.T) {
	q, s, cpu := newIRQ()
	q.WriteHalf(0, 1) // IE = 0x0001
	// IME stays 0 (default).
	s.Advance(2)

	q.Raise(SourceVBlank, 0)
	s.Advance(4)
	if cpu.asserted {
		t.Errorf("CPU IRQ line should NOT assert while IME=0")
	}

	q.WriteByte(4, 1) // IME = 1
	s.Advance(4)
	if !cpu.asserted {
		t.Errorf("CPU IRQ line should assert once IME=1 with pending IE&IF")
	}
}
