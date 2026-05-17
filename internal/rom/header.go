// header.go ⇄ src/nba/include/nba/rom/header.hh — GBA cart header layout.
//
// For details see http://problemkaputt.de/gbatek.htm#gbacartridgeheader.
// Total size is 192 bytes; the cart ROM begins at offset 0xC0 after a
// fixed Nintendo logo + 12-char title.
package rom

import "encoding/binary"

// Header ⇄ struct Header (192 bytes packed).
type Header struct {
	Entrypoint   uint32   // 0x00 — first instruction (branch into cart)
	NintendoLogo [156]byte // 0x04..0xA0 — compressed Nintendo logo bitmap
	Game         struct {
		Title [12]byte // 0xA0..0xAC — game title
		Code  [4]byte  // 0xAC..0xB0 — 4-char game code (game_db key)
		Maker [2]byte  // 0xB0..0xB2 — maker code
	}
	Fixed96H   byte    // 0xB2 — always 0x96
	UnitCode   byte    // 0xB3
	DeviceType byte    // 0xB4
	Reserved   [7]byte // 0xB5..0xBC
	Version    byte    // 0xBC
	Checksum   byte    // 0xBD
	Reserved2  [2]byte // 0xBE..0xC0

	// Multiboot header — relevant only for booted-via-link-cable carts.
	MB struct {
		RAMEntrypoint uint32   // 0xC0
		BootMode      byte     // 0xC4
		SlaveID       byte     // 0xC5
		Unused        [26]byte // 0xC6..0xE0
		JoyEntrypoint uint32   // 0xE0
	}
}

// ParseHeader decodes a cart's first 0xE4 bytes into a Header struct.
// Returns false if the buffer is too short.
func ParseHeader(data []byte) (Header, bool) {
	if len(data) < 0xE4 {
		return Header{}, false
	}
	var h Header
	h.Entrypoint = binary.LittleEndian.Uint32(data[0:])
	copy(h.NintendoLogo[:], data[4:4+156])
	copy(h.Game.Title[:], data[0xA0:0xAC])
	copy(h.Game.Code[:], data[0xAC:0xB0])
	copy(h.Game.Maker[:], data[0xB0:0xB2])
	h.Fixed96H = data[0xB2]
	h.UnitCode = data[0xB3]
	h.DeviceType = data[0xB4]
	copy(h.Reserved[:], data[0xB5:0xBC])
	h.Version = data[0xBC]
	h.Checksum = data[0xBD]
	copy(h.Reserved2[:], data[0xBE:0xC0])
	h.MB.RAMEntrypoint = binary.LittleEndian.Uint32(data[0xC0:])
	h.MB.BootMode = data[0xC4]
	h.MB.SlaveID = data[0xC5]
	copy(h.MB.Unused[:], data[0xC6:0xE0])
	h.MB.JoyEntrypoint = binary.LittleEndian.Uint32(data[0xE0:])
	return h, true
}
