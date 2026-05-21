package timer

import (
	"testing"

	"github.com/mnmlyw/nanogoadvance/internal/irq"
	"github.com/mnmlyw/nanogoadvance/internal/scheduler"
)

type stubCPU struct{}

func (stubCPU) SetIRQLine(bool) {}

type stubAPU struct{ overflows []int }

func (s *stubAPU) OnTimerOverflow(timerID, times int) {
	for range times {
		s.overflows = append(s.overflows, timerID)
	}
}

func newTimer() (*Timer, *scheduler.Scheduler, *stubAPU) {
	s := scheduler.New()
	s.Reset()
	irqc := irq.New(stubCPU{}, s)
	irqc.Reset()
	apu := &stubAPU{}
	t := New(s, irqc, apu)
	t.Reset()
	return t, s, apu
}

// TestTimerEnableThenOverflow — enable timer 0 at 1:1 prescaler with
// reload 0xFFFE (wraps every 2 cycles). Advance enough cycles for
// multiple overflows; the APU.OnTimerOverflow hook fires for timer 0
// so we count overflow events through that.
func TestTimerEnableThenOverflow(t *testing.T) {
	tm, s, apu := newTimer()
	tm.WriteHalf(0, 0, 0xFFFE)
	tm.WriteHalf(0, 2, 0x0080) // enable, freq 0
	s.Advance(1)               // commit reload + control events
	s.Advance(20)              // enough for several overflows

	count := 0
	for _, id := range apu.overflows {
		if id == 0 {
			count++
		}
	}
	if count == 0 {
		t.Errorf("timer 0 didn't overflow at all over 20 cycles — APU hook never called")
	}
}

// TestTimerCascade — timer 1 in cascade mode with reload 0xFFFF: every
// timer 0 overflow advances timer 1 by 1, and after one tick timer 1
// itself overflows. Verify the cascaded overflow reaches APU.OnTimer
// for timer 1.
func TestTimerCascade(t *testing.T) {
	tm, s, apu := newTimer()
	// Timer 1: cascade + reload 0xFFFF + enable.
	tm.WriteHalf(1, 0, 0xFFFF)
	tm.WriteHalf(1, 2, 0x0080|0x0004)
	s.Advance(1)
	// Timer 0: reload 0xFFFE so it overflows quickly at 1:1.
	tm.WriteHalf(0, 0, 0xFFFE)
	tm.WriteHalf(0, 2, 0x0080)
	s.Advance(1)
	s.Advance(20)

	count1 := 0
	for _, id := range apu.overflows {
		if id == 1 {
			count1++
		}
	}
	if count1 == 0 {
		t.Errorf("timer 1 cascade never overflowed — cascade not wired")
	}
}
