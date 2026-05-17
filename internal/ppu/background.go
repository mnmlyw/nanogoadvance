// background.go ⇄ src/nba/src/hw/ppu/background.{cc,inl}
package ppu

import "encoding/binary"

// Background ⇄ PPU::Background.
type Background struct {
	TimestampInit       int64
	TimestampLastSync   int64
	TimestampVRAMAccess int64
	Cycle               uint32

	Text   [4]BackgroundText
	Affine [2]BackgroundAffine

	Buffer [240][4]uint32
}

type BackgroundText struct {
	Fetches int
	Tile    struct {
		Address uint32
		Palette uint32
		FlipX   bool
	}
	PISO struct {
		Data      uint16
		Remaining int
	}
}

type BackgroundAffine struct {
	X           int32
	Y           int32
	OutOfBounds bool
	TileAddress uint16
}

// FetchVRAM_BG ⇄ PPU::FetchVRAM_BG<T>. Returns 0 in forced-blank mode.
// Past the sprite VRAM boundary the BG circuitry returns its latched
// 16-bit value rather than fresh data (an actual hardware quirk; some
// games depend on it).
func (p *PPU) spriteVRAMBoundary() uint32 {
	// In bitmap modes (3/4/5) sprite VRAM starts at 0x14000; otherwise
	// 0x10000. Upstream pulls this from DISPCNT.bg_mode.
	switch p.DISPCNT.Mode {
	case 3, 4, 5:
		return 0x14000
	default:
		return 0x10000
	}
}

func (p *PPU) fetchVRAM_BG_u8(cycle uint32, addr uint32) uint8 {
	if p.forcedBlank() {
		return 0
	}
	addr &= 0x1FFFF
	if addr >= 0x18000 {
		addr &= 0x17FFF
	}
	if addr < p.spriteVRAMBoundary() {
		p.BG.TimestampVRAMAccess = p.BG.TimestampInit + int64(cycle)
		// Update the latch on the aligned halfword.
		p.VRAMBGLatch = binary.LittleEndian.Uint16(p.VRAM[addr&^1:])
		return p.VRAM[addr]
	}
	// Return the corresponding byte of the latch.
	return uint8(p.VRAMBGLatch >> ((addr & 1) << 3))
}

func (p *PPU) fetchVRAM_BG_u16(cycle uint32, addr uint32) uint16 {
	if p.forcedBlank() {
		return 0
	}
	addr &= 0x1FFFF
	if addr >= 0x18000 {
		addr &= 0x17FFF
	}
	if addr < p.spriteVRAMBoundary() {
		p.BG.TimestampVRAMAccess = p.BG.TimestampInit + int64(cycle)
		p.VRAMBGLatch = binary.LittleEndian.Uint16(p.VRAM[addr&^1:])
		return binary.LittleEndian.Uint16(p.VRAM[addr:])
	}
	return p.VRAMBGLatch
}

// InitBackground ⇄ PPU::InitBackground.
func (p *PPU) InitBackground() {
	now := p.sched.Now()
	p.BG.TimestampInit = now
	p.BG.TimestampLastSync = now
	p.BG.Cycle = 0
	for i := range p.BG.Text {
		p.BG.Text[i].Fetches = 0
	}
	firstScanline := p.VCOUNT == 0
	for id := range 2 {
		bgx, bgy := p.bgxRef(id), p.bgyRef(id)
		if bgx.Written || firstScanline {
			bgx.Current = bgx.Initial
			bgx.Written = false
		}
		if bgy.Written || firstScanline {
			bgy.Current = bgy.Initial
			bgy.Written = false
		}
		p.BG.Affine[id].X = bgx.Current
		p.BG.Affine[id].Y = bgy.Current
	}
}

func (p *PPU) DrawBackground() {
	now := p.sched.Now()
	cycles := int(now - p.BG.TimestampLastSync)
	if cycles == 0 || p.BG.Cycle >= 1232 {
		return
	}
	mode := p.DISPCNT.Mode
	// BG enable is gated by the AND of the latched DISPCNT (set 40 cycles
	// ago) and the current DISPCNT — matches upstream's
	// `latched_dispcnt_and_current_dispcnt`.
	latchAnd := p.DISPCNTLatch[0] & p.DISPCNT.Hword
	bgEnabled := func(id int) bool { return latchAnd&(256<<uint(id)) != 0 }
	for range cycles {
		cycle := uint32(1) + p.BG.Cycle
		// Text-mode BGs (mode 0/1).
		if mode <= 1 {
			id := int(cycle & 3)
			if (id <= 1 || mode == 0) && bgEnabled(id) {
				p.renderMode0BG(uint(id), uint(cycle))
			}
		}
		if cycle < 1007 {
			switch mode {
			case 1, 2:
				id := int(^(cycle >> 1) & 1) // 0: BG2, 1: BG3
				if (id == 0 || mode == 2) && bgEnabled(2+id) {
					p.renderMode2BG(uint(id), uint(cycle))
				}
			case 3:
				if bgEnabled(2) {
					p.renderMode3BG(uint(cycle))
				}
			case 4:
				if bgEnabled(2) {
					p.renderMode4BG(uint(cycle))
				}
			case 5:
				if bgEnabled(2) {
					p.renderMode5BG(uint(cycle))
				}
			}
		}
		// Per-line tail work — upstream does this at cycle 1232.
		if cycle == 1232 {
			// BG mosaic counter Y advance ⇄ DrawBackgroundImpl tail.
			if p.VCOUNT < 159 {
				p.MOSAIC.BG.CounterY++
				if p.MOSAIC.BG.CounterY == p.MOSAIC.BG.SizeY {
					p.MOSAIC.BG.CounterY = 0
				} else {
					p.MOSAIC.BG.CounterY &= 15
				}
			} else {
				p.MOSAIC.BG.CounterY = 0
			}
			p.advanceAffineXY(mode, latchAnd)
		}
		p.BG.Cycle++
		if p.BG.Cycle == 1232 {
			break
		}
	}
	p.BG.TimestampLastSync = now
}

func (p *PPU) advanceAffineXY(mode int, latchAnd uint16) {
	// "Do not update internal X/Y unless the latched BG enable bit is
	// set" — upstream comment. Gate on the same latched mask the
	// rendering uses.
	advance := func(id int) {
		bgID := 2 + id
		if latchAnd&(256<<uint(bgID)) == 0 {
			return
		}
		if p.BGCNT[bgID].MosaicEnable != 0 {
			if p.MOSAIC.BG.CounterY == 0 {
				p.BG.Affine[id].X += int32(p.MOSAIC.BG.SizeY) * int32(p.BGPB[id])
				p.BG.Affine[id].Y += int32(p.MOSAIC.BG.SizeY) * int32(p.BGPD[id])
			}
		} else {
			p.BG.Affine[id].X += int32(p.BGPB[id])
			p.BG.Affine[id].Y += int32(p.BGPD[id])
		}
	}
	if mode >= 1 && mode <= 5 {
		advance(0)
	}
	if mode == 2 {
		advance(1)
	}
}

// renderMode0BG ⇄ RenderMode0BG.
func (p *PPU) renderMode0BG(id, cycle uint) {
	bgcnt := &p.BGCNT[id]
	text := &p.BG.Text[id]

	if text.Fetches > 0 && text.PISO.Remaining == 0 {
		data := p.fetchVRAM_BG_u16(uint32(cycle), text.Tile.Address)
		if text.Tile.FlipX {
			data = (data >> 8) | (data << 8)
			if bgcnt.FullPalette == 0 {
				data = ((data & 0xF0F0) >> 4) | ((data & 0x0F0F) << 4)
			}
			text.Tile.Address -= 2
		} else {
			text.Tile.Address += 2
		}
		text.PISO.Data = data
		text.PISO.Remaining = 4
		text.Fetches--
	}

	var index uint
	screenX := int(cycle>>2) - 9

	if bgcnt.FullPalette != 0 {
		index = uint(text.PISO.Data & 0xFF)
		text.PISO.Data >>= 8
		text.PISO.Remaining -= 2
	} else {
		index = uint(text.PISO.Data & 0x0F)
		if index != 0 {
			index |= uint(text.Tile.Palette) << 4
		}
		text.PISO.Data >>= 4
		text.PISO.Remaining--
	}

	if screenX >= 0 && screenX < 240 {
		p.BG.Buffer[screenX][id] = uint32(index)
	}

	bghofs := uint(p.BGHOFS[id])
	bghofsDiv8 := bghofs >> 3
	bghofsMod8 := bghofs & 7
	step := (cycle >> 2) + bghofsMod8

	if cycle < 1007 && step >= 8 && (step&7) == 0 {
		tileBase := uint32(bgcnt.TileBlock) << 14
		mapBlock := uint(bgcnt.MapBlock)
		line := uint(p.VCOUNT) + uint(p.BGVOFS[id])
		if bgcnt.MosaicEnable != 0 {
			line -= uint(p.MOSAIC.BG.CounterY)
		}
		gridX := bghofsDiv8 + (step >> 3) - 1
		gridY := line >> 3
		tileY := line & 7
		screenX := (gridX >> 5) & 1
		screenY := (gridY >> 5) & 1
		switch bgcnt.Size {
		case 1:
			mapBlock += screenX
		case 2:
			mapBlock += screenY
		case 3:
			mapBlock += screenX + (screenY << 1)
		}
		address := (uint32(mapBlock) << 11) + (uint32(gridY&31) << 6) + (uint32(gridX&31) << 1)
		tile := p.fetchVRAM_BG_u16(uint32(cycle), address)
		if cycle < 1004 {
			number := uint(tile & 0x3FF)
			flipX := tile&(1<<10) != 0
			flipY := tile&(1<<11) != 0
			text.Tile.Palette = uint32(tile >> 12)
			text.Tile.FlipX = flipX
			realTileY := tileY
			if flipY {
				realTileY = 7 - tileY
			}
			if bgcnt.FullPalette != 0 {
				text.Tile.Address = tileBase + uint32(number)<<6 + uint32(realTileY)<<3
				if flipX {
					text.Tile.Address += 6
				}
				text.Fetches = 4
			} else {
				text.Tile.Address = tileBase + uint32(number)<<5 + uint32(realTileY)<<2
				if flipX {
					text.Tile.Address += 2
				}
				text.Fetches = 2
			}
			text.PISO.Remaining = 0
		}
	}
}

// renderMode2BG ⇄ RenderMode2BG (BG2/BG3 affine).
func (p *PPU) renderMode2BG(id, cycle uint) {
	bgcnt := &p.BGCNT[2+id]
	if cycle < 32 {
		return
	}
	if (cycle & 1) == 0 {
		logSize := bgcnt.Size
		size := int32(128 << logSize)
		mask := size - 1
		x := p.BG.Affine[id].X >> 8
		y := p.BG.Affine[id].Y >> 8
		p.BG.Affine[id].X += int32(p.BGPA[id])
		p.BG.Affine[id].Y += int32(p.BGPC[id])
		if bgcnt.Wraparound != 0 {
			x &= mask
			y &= mask
			p.BG.Affine[id].OutOfBounds = false
		} else {
			p.BG.Affine[id].OutOfBounds = ((x | y) & -size) != 0
		}
		address := uint16(bgcnt.MapBlock)<<11 + uint16((y>>3)<<(4+logSize)) + uint16(x>>3)
		tile := p.fetchVRAM_BG_u8(uint32(cycle), uint32(address))
		p.BG.Affine[id].TileAddress = uint16(bgcnt.TileBlock)<<14 + uint16(tile)<<6 + uint16((y&7)<<3) + uint16(x&7)
	} else {
		index := uint(p.fetchVRAM_BG_u8(uint32(cycle), uint32(p.BG.Affine[id].TileAddress)))
		if p.BG.Affine[id].OutOfBounds {
			index = 0
		}
		x := (cycle - 32) >> 2
		if x < 240 {
			p.BG.Buffer[x][2+id] = uint32(index)
		}
	}
}

// renderMode3BG ⇄ RenderMode3BG.
func (p *PPU) renderMode3BG(cycle uint) {
	if cycle < 32 || (cycle&3) != 3 {
		return
	}
	screenX := (cycle - 32) >> 2
	x := p.BG.Affine[0].X >> 8
	y := p.BG.Affine[0].Y >> 8
	address := (uint32(y)*240 + uint32(x)) * 2
	data := p.fetchVRAM_BG_u16(uint32(cycle), address&0x1FFFF)
	var color uint32
	if x >= 0 && x < 240 && y >= 0 && y < 160 {
		color = uint32(data) | 0x80000000
	}
	if screenX < 240 {
		p.BG.Buffer[screenX][2] = color
	}
	p.BG.Affine[0].X += int32(p.BGPA[0])
	p.BG.Affine[0].Y += int32(p.BGPC[0])
}

// renderMode4BG ⇄ RenderMode4BG.
func (p *PPU) renderMode4BG(cycle uint) {
	if cycle < 32 || (cycle&3) != 3 {
		return
	}
	screenX := (cycle - 32) >> 2
	x := p.BG.Affine[0].X >> 8
	y := p.BG.Affine[0].Y >> 8
	address := uint32(p.DISPCNT.Frame)*0xA000 + uint32(y)*240 + uint32(x)
	data := p.fetchVRAM_BG_u8(uint32(cycle), address&0x1FFFF)
	var index uint
	if x >= 0 && x < 240 && y >= 0 && y < 160 {
		index = uint(data)
	}
	if screenX < 240 {
		p.BG.Buffer[screenX][2] = uint32(index)
	}
	p.BG.Affine[0].X += int32(p.BGPA[0])
	p.BG.Affine[0].Y += int32(p.BGPC[0])
}

// renderMode5BG ⇄ RenderMode5BG.
func (p *PPU) renderMode5BG(cycle uint) {
	if cycle < 32 || (cycle&3) != 3 {
		return
	}
	screenX := (cycle - 32) >> 2
	x := p.BG.Affine[0].X >> 8
	y := p.BG.Affine[0].Y >> 8
	address := uint32(p.DISPCNT.Frame)*0xA000 + (uint32(y)*160+uint32(x))*2
	data := p.fetchVRAM_BG_u16(uint32(cycle), address&0x1FFFF)
	var color uint32
	if x >= 0 && x < 160 && y >= 0 && y < 128 {
		color = uint32(data) | 0x80000000
	}
	if screenX < 240 {
		p.BG.Buffer[screenX][2] = color
	}
	p.BG.Affine[0].X += int32(p.BGPA[0])
	p.BG.Affine[0].Y += int32(p.BGPC[0])
}

func (p *PPU) bgxRef(id int) *ReferencePoint {
	if id == 0 {
		return &p.BG2X
	}
	return &p.BG3X
}

func (p *PPU) bgyRef(id int) *ReferencePoint {
	if id == 0 {
		return &p.BG2Y
	}
	return &p.BG3Y
}
