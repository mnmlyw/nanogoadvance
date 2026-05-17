// Package gpio — port of src/nba/include/nba/rom/gpio/* and
// src/nba/src/hw/rom/gpio/*. Cart-side GPIO: 4 data lines + direction + control,
// with attached devices (RTC, solar sensor). Used by Pokémon Ruby/Sapphire/
// Emerald/FireRed/LeafGreen, Boktai, etc.
package gpio

import (
	"time"

	"github.com/mnmlyw/nanogoadvance/internal/irq"
)

// Device is the per-peripheral interface — matches nba::GPIODevice.
type Device interface {
	Reset()
	Read() int
	Write(value int)
	SetPortDirections(d int)
}

// Direction values match GPIODevice::PortDirection.
const (
	DirIn  = 0 // peripheral -> GBA
	DirOut = 1 // GBA -> peripheral
)

// baseDevice is the embeddable counterpart to nba's GPIODevice fields.
type baseDevice struct {
	portDirections int
}

func (b *baseDevice) SetPortDirections(d int) { b.portDirections = d }
func (b *baseDevice) GetPortDirection(pin int) int {
	return (b.portDirections >> pin) & 1
}

// Register values match the offsets in the cart-IO space (0x080000C4 etc.).
const (
	RegData      = 0xC4
	RegDirection = 0xC6
	RegControl   = 0xC8
)

type GPIO struct {
	AllowReads bool
	rdMask     uint8
	wrMask     uint8
	PortData   uint8
	Devices    []Device
}

func New() *GPIO {
	g := &GPIO{}
	g.Reset()
	return g
}

func (g *GPIO) Reset() {
	g.AllowReads = false
	g.PortData = 0
	g.rdMask = 0b1111
	g.wrMask = 0b0000
	for _, d := range g.Devices {
		d.Reset()
		d.SetPortDirections(0)
	}
}

func (g *GPIO) Attach(d Device) { g.Devices = append(g.Devices, d) }

// IsReadable ⇄ GPIO::IsReadable — gates whether cart-IO reads route to
// the GPIO chip or fall through to ROM data.
func (g *GPIO) IsReadable() bool { return g.AllowReads }

func (g *GPIO) Read(address uint32) uint8 {
	if !g.AllowReads {
		return 0
	}
	switch address {
	case RegData:
		value := 0
		for _, d := range g.Devices {
			value |= d.Read()
		}
		g.PortData &= g.wrMask
		g.PortData |= g.rdMask & uint8(value)
		return uint8(value)
	case RegDirection:
		return g.rdMask
	case RegControl:
		if g.AllowReads {
			return 1
		}
		return 0
	}
	return 0
}

func (g *GPIO) Write(address uint32, value uint8) {
	switch address {
	case RegData:
		g.PortData &= g.rdMask
		g.PortData |= g.wrMask & value
		for _, d := range g.Devices {
			d.Write(int(g.PortData))
		}
	case RegDirection:
		value &= 15
		g.rdMask = ^value & 15
		g.wrMask = value
		for _, d := range g.Devices {
			d.SetPortDirections(int(value))
		}
	case RegControl:
		g.AllowReads = value&1 != 0
	}
}

// ---------------------------------------------------------------------
// RTC — port of nba::RTC.
// ---------------------------------------------------------------------

// RTC port enum (also bit positions in the data line).
const (
	rtcPortSCK = 0
	rtcPortSIO = 1
	rtcPortCS  = 2
)

// RTC state machine.
type rtcState int

const (
	rtcStateCommand rtcState = iota
	rtcStateSending
	rtcStateReceiving
	rtcStateComplete
)

// RTC registers — what the command byte selects.
const (
	rtcRegForceReset = 0
	rtcRegDateTime   = 2
	rtcRegForceIRQ   = 3
	rtcRegControl    = 4
	rtcRegTime       = 6
	rtcRegFree       = 7
)

// rtcArgCount mirrors RTC::s_argument_count — how many bytes the reg takes.
var rtcArgCount = [8]int{0, 0, 7, 0, 1, 0, 3, 0}

type RTC struct {
	baseDevice
	currentBit  int
	currentByte int
	reg         int
	data        uint8
	buffer      [7]uint8

	port struct {
		sck int
		sio int
		cs  int
	}
	state rtcState

	control struct {
		unknown1     bool
		perMinuteIRQ bool
		unknown2     bool
		mode24h      bool
		poweroff     bool
	}

	irq *irq.IRQ
}

func NewRTC(irqc *irq.IRQ) *RTC {
	r := &RTC{irq: irqc}
	r.Reset()
	return r
}

func (r *RTC) Reset() {
	r.currentBit = 0
	r.currentByte = 0
	r.data = 0
	for i := range r.buffer {
		r.buffer[i] = 0
	}
	r.port.sck = 0
	r.port.sio = 0
	r.port.cs = 0
	r.state = rtcStateComplete
	r.control = struct {
		unknown1     bool
		perMinuteIRQ bool
		unknown2     bool
		mode24h      bool
		poweroff     bool
	}{}
	// Sennen Kazoku (J) refuses to boot unless 24h is enabled.
	r.control.mode24h = true
}

func (r *RTC) Read() int {
	return (r.port.sio & r.port.cs) << rtcPortSIO
}

func (r *RTC) Write(value int) {
	oldSCK := r.port.sck
	oldCS := r.port.cs

	if r.GetPortDirection(rtcPortCS) == DirOut {
		r.port.cs = (value >> rtcPortCS) & 1
	}
	if r.GetPortDirection(rtcPortSCK) == DirOut {
		r.port.sck = (value >> rtcPortSCK) & 1
	}
	if r.GetPortDirection(rtcPortSIO) == DirOut {
		r.port.sio = (value >> rtcPortSIO) & 1
	}

	if r.port.cs != 0 {
		// CS rising edge — start a new command.
		if oldCS == 0 {
			r.state = rtcStateCommand
			r.currentBit = 0
			r.currentByte = 0
			return
		}
		// SCK rising edge.
		if oldSCK == 0 && r.port.sck != 0 {
			switch r.state {
			case rtcStateCommand:
				r.receiveCommandSIO()
			case rtcStateReceiving:
				r.receiveBufferSIO()
			case rtcStateSending:
				r.transmitBufferSIO()
			}
		}
	}
}

func (r *RTC) readSIO() bool {
	r.data &^= 1 << r.currentBit
	r.data |= uint8(r.port.sio) << r.currentBit
	r.currentBit++
	if r.currentBit == 8 {
		r.currentBit = 0
		return true
	}
	return false
}

func (r *RTC) receiveCommandSIO() {
	if !r.readSIO() {
		return
	}
	// Check whether command is MSB-first or LSB-first.
	if (r.data >> 4) == 6 {
		// Reverse bit order.
		r.data = (r.data << 4) | (r.data >> 4)
		r.data = ((r.data & 0x33) << 2) | ((r.data & 0xCC) >> 2)
		r.data = ((r.data & 0x55) << 1) | ((r.data & 0xAA) >> 1)
	} else if (r.data & 15) != 6 {
		return
	}
	r.reg = int((r.data >> 4) & 7)
	r.currentBit = 0
	r.currentByte = 0
	if r.data&0x80 != 0 {
		r.readRegister()
		if rtcArgCount[r.reg] > 0 {
			r.state = rtcStateSending
		} else {
			r.state = rtcStateComplete
		}
	} else {
		if rtcArgCount[r.reg] > 0 {
			r.state = rtcStateReceiving
		} else {
			r.writeRegister()
			r.state = rtcStateComplete
		}
	}
}

func (r *RTC) receiveBufferSIO() {
	if r.currentByte < rtcArgCount[r.reg] && r.readSIO() {
		r.buffer[r.currentByte] = r.data
		r.currentByte++
		if r.currentByte == rtcArgCount[r.reg] {
			r.writeRegister()
			r.state = rtcStateComplete
		}
	}
}

func (r *RTC) transmitBufferSIO() {
	r.port.sio = int(r.buffer[r.currentByte] & 1)
	r.buffer[r.currentByte] >>= 1
	r.currentBit++
	if r.currentBit == 8 {
		r.currentBit = 0
		r.currentByte++
		if r.currentByte == rtcArgCount[r.reg] {
			r.state = rtcStateComplete
		}
	}
}

func decimalToBCD(x uint8) uint8 {
	y, e := uint8(0), uint8(1)
	for x > 0 {
		y += (x % 10) * e
		e *= 16
		x /= 10
	}
	return y
}

func (r *RTC) readRegister() {
	adjustHour := func(hour *int) {
		if !r.control.mode24h && *hour >= 12 {
			*hour = (*hour - 12) | 64
		}
	}
	switch r.reg {
	case rtcRegControl:
		v := uint8(0)
		if r.control.unknown1 {
			v |= 2
		}
		if r.control.perMinuteIRQ {
			v |= 8
		}
		if r.control.unknown2 {
			v |= 32
		}
		if r.control.mode24h {
			v |= 64
		}
		if r.control.poweroff {
			v |= 128
		}
		r.buffer[0] = v
	case rtcRegDateTime:
		now := time.Now()
		hour := now.Hour()
		adjustHour(&hour)
		r.buffer[0] = decimalToBCD(uint8(now.Year() - 2000))
		r.buffer[1] = decimalToBCD(uint8(now.Month()))
		r.buffer[2] = decimalToBCD(uint8(now.Day()))
		r.buffer[3] = decimalToBCD(uint8(now.Weekday()))
		r.buffer[4] = decimalToBCD(uint8(hour))
		r.buffer[5] = decimalToBCD(uint8(now.Minute()))
		r.buffer[6] = decimalToBCD(uint8(now.Second()))
	case rtcRegTime:
		now := time.Now()
		hour := now.Hour()
		adjustHour(&hour)
		r.buffer[0] = decimalToBCD(uint8(hour))
		r.buffer[1] = decimalToBCD(uint8(now.Minute()))
		r.buffer[2] = decimalToBCD(uint8(now.Second()))
	}
}

func (r *RTC) writeRegister() {
	switch r.reg {
	case rtcRegControl:
		r.control.unknown1 = r.buffer[0]&2 != 0
		r.control.perMinuteIRQ = r.buffer[0]&8 != 0
		r.control.unknown2 = r.buffer[0]&32 != 0
		r.control.mode24h = r.buffer[0]&64 != 0
	case rtcRegForceReset:
		r.control = struct {
			unknown1     bool
			perMinuteIRQ bool
			unknown2     bool
			mode24h      bool
			poweroff     bool
		}{}
	case rtcRegForceIRQ:
		r.irq.Raise(irq.SourceROM, 0)
	}
}

// ---------------------------------------------------------------------
// SolarSensor — port of nba::SolarSensor.
// ---------------------------------------------------------------------

const (
	solarPinCLK = 0
	solarPinRST = 1
	solarPinFLG = 3
)

type SolarSensor struct {
	baseDevice
	oldClk       bool
	counter      uint8
	currentLevel uint8
}

func NewSolarSensor() *SolarSensor {
	s := &SolarSensor{}
	s.Reset()
	return s
}

func (s *SolarSensor) Reset() {
	s.oldClk = false
	s.counter = 0
	s.SetLightLevel(0x60)
}

func (s *SolarSensor) Read() int {
	if s.counter > s.currentLevel {
		return 1 << solarPinFLG
	}
	return 0
}

func (s *SolarSensor) Write(value int) {
	clk := value&(1<<solarPinCLK) != 0 && s.GetPortDirection(solarPinCLK) == DirOut
	rst := value&(1<<solarPinRST) != 0 && s.GetPortDirection(solarPinRST) == DirOut
	if rst {
		s.counter = 0
	} else if s.oldClk && !clk {
		s.counter++
	}
	s.oldClk = clk
}

func (s *SolarSensor) SetLightLevel(level uint8) { s.currentLevel = 255 - level }
