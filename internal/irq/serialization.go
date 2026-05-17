// serialization.go ⇄ src/nba/src/hw/irq/serialization.cc
package irq

import "github.com/mnmlyw/nanogoadvance/internal/savestate"

func (q *IRQ) LoadState(s *savestate.SaveState) {
	q.pendingIME = int(s.IRQ.PendingIME)
	q.pendingIE = s.IRQ.PendingIE
	q.pendingIF = s.IRQ.PendingIF
	q.regIME = int(s.IRQ.RegIME)
	q.regIE = s.IRQ.RegIE
	q.regIF = s.IRQ.RegIF
	// irq_line is reconstructed from ime/ie/if (matches upstream
	// irq.cc:17). CPU's own irq_line field is restored independently
	// by ARM.LoadState; do NOT propagate here — any pending
	// UpdateIRQLine event in the scheduler will fire after restore
	// with the 2-cycle delay it had at save time.
	q.irqLine = q.regIME != 0 && (q.regIE&q.regIF) != 0
	q.irqAvailable = s.IRQ.IRQAvailable
}

func (q *IRQ) CopyState(s *savestate.SaveState) {
	s.IRQ.PendingIME = uint8(q.pendingIME)
	s.IRQ.PendingIE = q.pendingIE
	s.IRQ.PendingIF = q.pendingIF
	s.IRQ.RegIME = uint8(q.regIME)
	s.IRQ.RegIE = q.regIE
	s.IRQ.RegIF = q.regIF
	s.IRQ.IRQAvailable = q.irqAvailable
}
