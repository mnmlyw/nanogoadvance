// serialization.go ⇄ src/nba/src/hw/keypad/serialization.cc
package keypad

import "github.com/mnmlyw/nanogoadvance/internal/savestate"

func (k *KeyPad) LoadState(s *savestate.SaveState) {
	keycnt := s.KEYCNT
	k.control.mask = keycnt & 0x3FF
	k.control.interrupt = keycnt&0x4000 != 0
	k.control.mode = Mode((keycnt >> 15) & 1)
}

func (k *KeyPad) CopyState(s *savestate.SaveState) {
	v := k.control.mask
	if k.control.interrupt {
		v |= 0x4000
	}
	v |= uint16(k.control.mode) << 15
	s.KEYCNT = v
}
