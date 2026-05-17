// serialization.go ⇄ src/nba/src/hw/timer/serialization.cc
package timer

import "github.com/mnmlyw/nanogoadvance/internal/savestate"

func (t *Timer) LoadState(s *savestate.SaveState) {
	for i := range 4 {
		reload := s.Timer[i].Reload
		control := s.Timer[i].Control

		t.channels[i].reload = reload
		t.channels[i].counter = uint32(s.Timer[i].Counter)

		t.channels[i].control.frequency = int(control & 3)
		t.channels[i].control.cascade = control&4 != 0
		t.channels[i].control.interrupt = control&64 != 0
		t.channels[i].control.enable = control&128 != 0

		t.channels[i].shift = ticksShift[t.channels[i].control.frequency]
		t.channels[i].mask = ticksMask[t.channels[i].control.frequency]

		t.channels[i].running = false

		t.channels[i].pending.reload = s.Timer[i].Pending.Reload
		t.channels[i].pending.control = s.Timer[i].Pending.Control

		// Restore the in-flight overflow event UID. Upstream sets
		// running=false even when an event is queued — the channel
		// re-arms (running=true) the next time OnOverflow fires.
		t.channels[i].eventOverflow = s.Timer[i].EventUID
		t.channels[i].hasEvent = s.Timer[i].EventUID != 0
	}
}

func (t *Timer) CopyState(s *savestate.SaveState) {
	for i := range 4 {
		s.Timer[i].Counter = t.readCounter(&t.channels[i])
		s.Timer[i].Reload = t.channels[i].reload
		s.Timer[i].Control = t.readControl(&t.channels[i])
		s.Timer[i].Pending.Reload = t.channels[i].pending.reload
		s.Timer[i].Pending.Control = t.channels[i].pending.control
		if t.channels[i].hasEvent {
			s.Timer[i].EventUID = t.channels[i].eventOverflow
		} else {
			s.Timer[i].EventUID = 0
		}
	}
}
