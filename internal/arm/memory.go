// memory.go ⇄ src/nba/src/arm/handlers/memory.inl
//
// Thin wrappers around bus reads with the GBA's halfword/word rotation and
// signed-load quirks.
package arm

import "github.com/mnmlyw/nanogoadvance/internal/bus"

func (a *ARM7TDMI) ReadByte(addr uint32, access bus.Access) uint32 {
	return uint32(a.Bus.ReadByte(addr, access))
}

func (a *ARM7TDMI) ReadHalf(addr uint32, access bus.Access) uint32 {
	return uint32(a.Bus.ReadHalf(addr, access))
}

func (a *ARM7TDMI) ReadWord(addr uint32, access bus.Access) uint32 {
	return a.Bus.ReadWord(addr, access)
}

func (a *ARM7TDMI) ReadByteSigned(addr uint32, access bus.Access) uint32 {
	v := uint32(a.Bus.ReadByte(addr, access))
	if v&0x80 != 0 {
		v |= 0xFFFFFF00
	}
	return v
}

func (a *ARM7TDMI) ReadHalfRotate(addr uint32, access bus.Access) uint32 {
	v := uint32(a.Bus.ReadHalf(addr, access))
	if addr&1 != 0 {
		v = (v >> 8) | (v << 24)
	}
	return v
}

func (a *ARM7TDMI) ReadHalfSigned(addr uint32, access bus.Access) uint32 {
	if addr&1 != 0 {
		v := uint32(a.Bus.ReadByte(addr, access))
		if v&0x80 != 0 {
			v |= 0xFFFFFF00
		}
		return v
	}
	v := uint32(a.Bus.ReadHalf(addr, access))
	if v&0x8000 != 0 {
		v |= 0xFFFF0000
	}
	return v
}

func (a *ARM7TDMI) ReadWordRotate(addr uint32, access bus.Access) uint32 {
	v := a.Bus.ReadWord(addr, access)
	shift := (addr & 3) * 8
	if shift == 0 {
		return v
	}
	return (v >> shift) | (v << (32 - shift))
}

func (a *ARM7TDMI) WriteByte(addr uint32, v uint8, access bus.Access) {
	a.Bus.WriteByte(addr, v, access)
}
func (a *ARM7TDMI) WriteHalf(addr uint32, v uint16, access bus.Access) {
	a.Bus.WriteHalf(addr, v, access)
}
func (a *ARM7TDMI) WriteWord(addr uint32, v uint32, access bus.Access) {
	a.Bus.WriteWord(addr, v, access)
}
