// serialization.go ⇄ src/nba/src/serialization.cc (top-level Core
// LoadState/CopyState orchestrator).
package core

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/mnmlyw/nanogoadvance/internal/savestate"
	"github.com/mnmlyw/nanogoadvance/internal/scheduler"
)

// stateful is the optional interface a Backup or GPIO satisfies if it
// supports save-state round-tripping.
type stateful interface {
	LoadState(s *savestate.SaveState)
	CopyState(s *savestate.SaveState)
}

// LoadState ⇄ Core::LoadState — restores every subsystem from a snapshot.
func (c *Core) LoadState(s *savestate.SaveState) {
	if s.Magic != savestate.MagicNumber {
		return
	}
	c.Sched.Reset()
	c.Sched.SetTimestampNow(int64(s.Timestamp))

	// Restore scheduled class events ⇄ Scheduler::LoadState. Re-adds each
	// event with its original UID so subsystem state (DMA/Timer/APU
	// event_uid fields) reconciles correctly.
	events := make([]scheduler.ClassEventSnapshot, 0, s.Scheduler.EventCount)
	for i := uint8(0); i < s.Scheduler.EventCount; i++ {
		e := &s.Scheduler.Events[i]
		// Skip the implicit EndOfQueue sentinel.
		if scheduler.EventClass(e.EventClass) == scheduler.EventClassEndOfQueue {
			continue
		}
		events = append(events, scheduler.ClassEventSnapshot{
			Key:        e.Key,
			UID:        e.UID,
			UserData:   e.UserData,
			EventClass: e.EventClass,
		})
	}
	c.Sched.RestoreClassEvents(events, s.Scheduler.NextUID)

	c.CPU.LoadState(s)
	c.Bus.LoadState(s)
	c.IRQ.LoadState(s)
	c.PPU.LoadState(s)
	c.APU.LoadState(s)
	c.Timer.LoadState(s)
	c.DMA.LoadState(s)
	c.Keypad.LoadState(s)

	if b, ok := c.Bus.BackupSRAM.(stateful); ok && b != nil {
		b.LoadState(s)
	}
	if b, ok := c.Bus.BackupEEPROM.(stateful); ok && b != nil {
		b.LoadState(s)
	}
	if g, ok := c.Bus.GPIO.(stateful); ok && g != nil {
		g.LoadState(s)
	}

	// RCNT lives in stubDevice in our port (vs Bus::Hardware upstream).
	c.stubIO.rcnt[0] = s.Bus.IO.RCNT[0]
	c.stubIO.rcnt[1] = s.Bus.IO.RCNT[1]
}

// CopyState ⇄ Core::CopyState — snapshots every subsystem.
func (c *Core) CopyState(s *savestate.SaveState) {
	s.Magic = savestate.MagicNumber
	s.Version = savestate.CurrentVersion
	s.Timestamp = uint64(c.Sched.Now())

	c.CPU.CopyState(s)
	c.Bus.CopyState(s)
	c.IRQ.CopyState(s)
	c.PPU.CopyState(s)
	c.APU.CopyState(s)
	c.Timer.CopyState(s)
	c.DMA.CopyState(s)
	c.Keypad.CopyState(s)

	if b, ok := c.Bus.BackupSRAM.(stateful); ok && b != nil {
		b.CopyState(s)
	}
	if b, ok := c.Bus.BackupEEPROM.(stateful); ok && b != nil {
		b.CopyState(s)
	}
	if g, ok := c.Bus.GPIO.(stateful); ok && g != nil {
		g.CopyState(s)
	}

	s.Bus.IO.RCNT[0] = c.stubIO.rcnt[0]
	s.Bus.IO.RCNT[1] = c.stubIO.rcnt[1]

	// Scheduler events ⇄ Scheduler::CopyState. Closure-based events are
	// silently dropped (not serialisable); only class-tagged events make
	// it into the save. Cap at 64 to match the upstream layout.
	pending := c.Sched.PendingClassEvents()
	count := min(uint8(len(pending)), 64)
	for i := uint8(0); i < count; i++ {
		s.Scheduler.Events[i] = savestate.SchedEvent{
			Key:        pending[i].Key,
			UID:        pending[i].UID,
			UserData:   pending[i].UserData,
			EventClass: pending[i].EventClass,
		}
	}
	s.Scheduler.EventCount = count
	s.Scheduler.NextUID = c.Sched.NextUID()
}

// SaveStateToFile writes a binary blob of the current SaveState to a path.
// Uses Go's encoding/binary in little-endian order. This isn't wire-
// compatible with upstream's C++ save files (struct padding differs) but
// is sufficient for in-process snapshots.
func (c *Core) SaveStateToFile(path string) error {
	var s savestate.SaveState
	c.CopyState(&s)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return binary.Write(f, binary.LittleEndian, &s)
}

// LoadStateFromFile reads a save state blob and applies it.
func (c *Core) LoadStateFromFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var s savestate.SaveState
	if err := binary.Read(f, binary.LittleEndian, &s); err != nil && err != io.EOF {
		return fmt.Errorf("LoadStateFromFile: %w", err)
	}
	c.LoadState(&s)
	return nil
}
