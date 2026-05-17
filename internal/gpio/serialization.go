// serialization.go ⇄ src/nba/src/hw/rom/gpio/serialization.cc
package gpio

import "github.com/mnmlyw/nanogoadvance/internal/savestate"

// DeviceState ⇄ the per-device interface in nba::GPIODevice: each device
// type (RTC, SolarSensor) must implement LoadState/CopyState.
type DeviceState interface {
	LoadState(s *savestate.SaveState)
	CopyState(s *savestate.SaveState)
}

func (g *GPIO) LoadState(s *savestate.SaveState) {
	g.AllowReads = s.GPIO.AllowReads
	g.rdMask = s.GPIO.RDMask
	g.wrMask = (^s.GPIO.RDMask) & 15
	g.PortData = s.GPIO.PortData
	for _, d := range g.Devices {
		if ds, ok := d.(DeviceState); ok {
			ds.LoadState(s)
		}
		d.SetPortDirections(int(g.wrMask))
	}
}

func (g *GPIO) CopyState(s *savestate.SaveState) {
	s.GPIO.AllowReads = g.AllowReads
	s.GPIO.RDMask = g.rdMask
	s.GPIO.PortData = g.PortData
	for _, d := range g.Devices {
		if ds, ok := d.(DeviceState); ok {
			ds.CopyState(s)
		}
	}
}

func (r *RTC) LoadState(s *savestate.SaveState) {
	r.currentBit = int(s.GPIO.RTC.CurrentBit)
	r.currentByte = int(s.GPIO.RTC.CurrentByte)
	r.reg = int(s.GPIO.RTC.Reg)
	r.data = s.GPIO.RTC.Data
	r.state = rtcState(s.GPIO.RTC.State)
	r.control.unknown1 = s.GPIO.RTC.Control.Unknown1
	r.control.perMinuteIRQ = s.GPIO.RTC.Control.PerMinuteIRQ
	r.control.unknown2 = s.GPIO.RTC.Control.Unknown2
	r.control.mode24h = s.GPIO.RTC.Control.Mode24h
	r.control.poweroff = s.GPIO.RTC.Control.Poweroff
	for i := range 7 {
		r.buffer[i] = s.GPIO.RTC.Buffer[i]
	}
}

func (r *RTC) CopyState(s *savestate.SaveState) {
	s.GPIO.RTC.CurrentBit = uint8(r.currentBit)
	s.GPIO.RTC.CurrentByte = uint8(r.currentByte)
	s.GPIO.RTC.Reg = uint8(r.reg)
	s.GPIO.RTC.Data = r.data
	s.GPIO.RTC.State = uint8(r.state)
	s.GPIO.RTC.Control.Unknown1 = r.control.unknown1
	s.GPIO.RTC.Control.PerMinuteIRQ = r.control.perMinuteIRQ
	s.GPIO.RTC.Control.Unknown2 = r.control.unknown2
	s.GPIO.RTC.Control.Mode24h = r.control.mode24h
	s.GPIO.RTC.Control.Poweroff = r.control.poweroff
	for i := range 7 {
		s.GPIO.RTC.Buffer[i] = r.buffer[i]
	}
}

func (s *SolarSensor) LoadState(st *savestate.SaveState) {
	s.oldClk = st.GPIO.SolarSensor.OldCLK
	s.counter = st.GPIO.SolarSensor.Counter
}

func (s *SolarSensor) CopyState(st *savestate.SaveState) {
	st.GPIO.SolarSensor.OldCLK = s.oldClk
	st.GPIO.SolarSensor.Counter = s.counter
}
