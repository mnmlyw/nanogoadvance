// Package scheduler — port of src/nba/include/nba/scheduler.hh.
//
// Discrete-event scheduler: peripherals enqueue events at exact future
// timestamps and the CPU runs in chunks bounded by the next pending event.
//
// Two flavours of event coexist:
//   - Closure events: Add(delay, callback) returns an EventID. Convenient
//     for one-off callbacks; NOT survivable across save/load because Go
//     closures aren't serialisable.
//   - Class events: AddClass(delay, class[, userData]) dispatches through
//     a callback table registered with Register(class, callback). Class
//     events ARE survivable across save/load and mirror upstream's
//     Scheduler::EventClass system 1:1.
package scheduler

import "container/heap"

// EventID is the stable handle returned by Add()/AddClass(). Renamed to
// UID for class events to match upstream nomenclature.
type EventID = uint64

// Callback ⇄ Scheduler::EventMethod<T> — closure flavour. Receives the
// `cycles late` count (0 when the event fires on time).
type Callback func(late int64)

// EventClass ⇄ Scheduler::EventClass enum. Adding a new class? Append to
// the const list AND bump EventClassCount.
type EventClass uint16

const (
	// Sentinel — used as the "nothing scheduled" marker. Never fires.
	EventClassEndOfQueue EventClass = iota

	// ARM
	EventClassARMLDMUsermodeConflict

	// PPU
	EventClassPPUHDrawVDraw
	EventClassPPUHBlankVDraw
	EventClassPPUHDrawVBlank
	EventClassPPUHBlankVBlank
	EventClassPPUBeginSpriteFetch
	EventClassPPUUpdateVCountFlag
	EventClassPPUVideoDMA
	EventClassPPULatchDISPCNT
	EventClassPPUHBlankIRQ
	EventClassPPUVBlankIRQ
	EventClassPPUVCountIRQ

	// APU
	EventClassAPUMixer
	EventClassAPUSequencer
	EventClassAPUPSG1Generate
	EventClassAPUPSG2Generate
	EventClassAPUPSG3Generate
	EventClassAPUPSG4Generate

	// IRQ controller
	EventClassIRQWriteIO
	EventClassIRQUpdateIEAndIF
	EventClassIRQUpdateIRQLine

	// Timers
	EventClassTMOverflow
	EventClassTMWriteReload
	EventClassTMWriteControl

	// DMA
	EventClassDMAActivated

	// Backup memory
	EventClassEEPROMReady

	// Serial
	EventClassSIOTransferDone

	// Bookkeeping — keep this last.
	EventClassCount
)

// ClassCallback ⇄ the callbacks[] entry in upstream. Receives the
// user_data the event was scheduled with.
type ClassCallback func(userData uint64)

type event struct {
	id        EventID // stable UID (monotonic at creation)
	timestamp int64
	priority  int    // 0 (high) .. 3 (low) — tiebreaker on equal timestamp
	key       uint64 // (timestamp << 2) | priority — match upstream
	cb        Callback
	class     EventClass // EventClassEndOfQueue means "closure-only event"
	userData  uint64
	cancelled bool
	index     int
}

type pq []*event

func (p pq) Len() int { return len(p) }
func (p pq) Less(i, j int) bool { return p[i].key < p[j].key }
func (p pq) Swap(i, j int) { p[i], p[j] = p[j], p[i]; p[i].index = i; p[j].index = j }
func (p *pq) Push(x any)   { e := x.(*event); e.index = len(*p); *p = append(*p, e) }
func (p *pq) Pop() any     { old := *p; n := len(old); e := old[n-1]; *p = old[:n-1]; return e }

type Scheduler struct {
	now       int64
	queue     pq
	nextID    EventID
	callbacks [EventClassCount]ClassCallback
}

func New() *Scheduler {
	s := &Scheduler{nextID: 1}
	// Panic on unhandled class — upstream does the same.
	for i := range s.callbacks {
		s.callbacks[i] = func(uint64) { panic("scheduler: unhandled event class") }
	}
	return s
}

func (s *Scheduler) Now() int64 { return s.now }

// TimestampTarget ⇄ Scheduler::GetTimestampTarget — timestamp of the
// next pending event at the top of the heap. Used by the halt loop to
// step from `now` to the next event in one call instead of 1-cycle
// chunks (matches upstream's bus.Step(GetRemainingCycleCount()) usage).
//
// Lazily pops cancelled events sitting at the top; upstream cancels
// in-place too but its Cancel() also fixes up the heap, which Go's
// container/heap can't do without an extra index. Skipping them here
// keeps RemainingCycleCount honest.
func (s *Scheduler) TimestampTarget() int64 {
	for len(s.queue) > 0 && s.queue[0].cancelled {
		heap.Pop(&s.queue)
	}
	if len(s.queue) == 0 {
		return 1<<62 - 1
	}
	return s.queue[0].timestamp
}

// RemainingCycleCount ⇄ Scheduler::GetRemainingCycleCount — cycles
// from `now` until the next event fires. Always >= 0 because Drain
// runs every Advance and the EndOfQueue sentinel sits at int64-max.
func (s *Scheduler) RemainingCycleCount() int64 {
	return s.TimestampTarget() - s.now
}

// Reset clears the queue and zeros the clock. Used by save-state load.
// Registered callbacks are preserved. ⇄ Scheduler::Reset — also adds an
// EndOfQueue sentinel at u64::max so the heap is never empty (matches
// upstream's save-state event_count and prevents an empty-heap edge case
// in Drain/Step).
func (s *Scheduler) Reset() {
	s.now = 0
	s.queue = s.queue[:0]
	s.nextID = 1
	// EndOfQueue sentinel: timestamp = max (so it never fires), class =
	// EndOfQueue (so the dispatcher would panic if it ever did).
	heap.Push(&s.queue, &event{
		id:        s.nextID,
		timestamp: 1<<62 - 1, // safe "infinite" within int64
		key:       (uint64(1)<<62 - 1) << 2,
		class:     EventClassEndOfQueue,
	})
	s.nextID++
}

// SetTimestampNow forces the global clock to a specific value. Used by
// save-state load (after Reset).
func (s *Scheduler) SetTimestampNow(t int64) { s.now = t }

// Advance pushes the global clock forward and fires due events.
//
// ⇄ Scheduler::AddCycles (scheduler.hh:130-134) — fire events with
// timestamp ≤ target, setting `now` to each event's timestamp BEFORE
// its callback runs so Sync() / GetTimestampNow() inside the callback
// see the event-time, not the post-bump target. Bump to target after.
func (s *Scheduler) Advance(cycles int64) {
	target := s.now + cycles
	s.Step(target)
	s.now = target
}

// Step ⇄ Scheduler::Step (scheduler.hh:245-252). Drains all events
// scheduled at or before `target`, advancing `s.now` to each event's
// timestamp as it fires.
func (s *Scheduler) Step(target int64) {
	for s.queue.Len() > 0 {
		top := s.queue[0]
		if top.cancelled {
			heap.Pop(&s.queue)
			continue
		}
		if top.timestamp > target {
			return
		}
		heap.Pop(&s.queue)
		late := target - top.timestamp
		s.now = top.timestamp
		if top.class != EventClassEndOfQueue {
			s.callbacks[top.class](top.userData)
		} else if top.cb != nil {
			top.cb(late)
		}
	}
}

// Add ⇄ Scheduler::Add(delay, closure). Closure-based event — NOT saved
// in CopyState (closures can't be serialised).
func (s *Scheduler) Add(delay int64, cb Callback) EventID {
	id := s.nextID
	s.nextID++
	ts := s.now + delay
	e := &event{
		id:        id,
		timestamp: ts,
		key:       (uint64(ts) << 2),
		cb:        cb,
		class:     EventClassEndOfQueue,
	}
	heap.Push(&s.queue, e)
	return id
}

// Register ⇄ Scheduler::Register. Binds an EventClass to a callback so
// that future AddClass calls fire it.
func (s *Scheduler) Register(class EventClass, cb ClassCallback) {
	s.callbacks[class] = cb
}

// AddClass ⇄ Scheduler::Add(delay, event_class, priority, user_data).
// Class events survive save/load round trip.
func (s *Scheduler) AddClass(delay int64, class EventClass, priority int, userData uint64) EventID {
	id := s.nextID
	s.nextID++
	ts := s.now + delay
	e := &event{
		id:        id,
		timestamp: ts,
		priority:  priority,
		key:       (uint64(ts) << 2) | uint64(priority&3),
		class:     class,
		userData:  userData,
	}
	heap.Push(&s.queue, e)
	return id
}

func (s *Scheduler) Cancel(id EventID) {
	for _, e := range s.queue {
		if e.id == id {
			e.cancelled = true
			return
		}
	}
}

// PendingClassEvents returns a snapshot of every live class event in the
// queue. Used by save state. Closure events are skipped because they
// can't be serialised.
func (s *Scheduler) PendingClassEvents() []ClassEventSnapshot {
	out := make([]ClassEventSnapshot, 0, len(s.queue))
	for _, e := range s.queue {
		if e.cancelled || e.class == EventClassEndOfQueue {
			continue
		}
		out = append(out, ClassEventSnapshot{
			Key:        e.key,
			UID:        e.id,
			UserData:   e.userData,
			EventClass: uint16(e.class),
		})
	}
	return out
}

// RestoreClassEvents re-adds each snapshot to the queue, preserving the
// original UID. Caller must Reset() + SetTimestampNow() first.
func (s *Scheduler) RestoreClassEvents(events []ClassEventSnapshot, nextUID uint64) {
	for _, ev := range events {
		ts := int64(ev.Key >> 2)
		priority := int(ev.Key & 3)
		e := &event{
			id:        ev.UID,
			timestamp: ts,
			priority:  priority,
			key:       ev.Key,
			class:     EventClass(ev.EventClass),
			userData:  ev.UserData,
		}
		heap.Push(&s.queue, e)
		if ev.UID >= s.nextID {
			s.nextID = ev.UID + 1
		}
	}
	if nextUID > s.nextID {
		s.nextID = nextUID
	}
}

// NextUID exposes the monotonic UID counter for save state.
func (s *Scheduler) NextUID() uint64 { return s.nextID }

// ClassEventSnapshot ⇄ SaveState::Scheduler::Event — a single
// serialisable record for a queued class event.
type ClassEventSnapshot struct {
	Key        uint64
	UID        uint64
	UserData   uint64
	EventClass uint16
}
