// sprite.go ⇄ src/nba/src/hw/ppu/sprite.cc
//
// OBJ-layer state machine: OAM scan, per-cycle drawer state, dual buffer
// for the pixel pipeline.
package ppu

// SpritePixel encoding (matches the bitfield layout in sprite.Pixel).
// Bit layout: color[7:0], priority[9:8], alpha[10], window[11], mosaic[12].

func makeSpritePixel(color uint32, priority uint32, alpha, window, mosaic bool) SpritePixel {
	d := color & 0xFF
	d |= (priority & 3) << 16 // Priority() reads from bit 16 in merge.go
	if window {
		d |= 1 << 18
	}
	if alpha {
		d |= 1 << 19
	}
	if mosaic {
		d |= 1 << 20
	}
	return SpritePixel{Data: d}
}

// OBJ modes.
const (
	OBJNormal     = 0
	OBJSemi       = 1
	OBJWindow     = 2
	OBJProhibited = 3
)

// Sprite drawer state (per-OBJ working set).
type drawerState struct {
	Width           int
	Height          int
	Mode            int
	Mosaic          bool
	Affine          bool
	DrawX           int
	RemainingPixels int
	Matrix          [4]int16
	TileNumber      uint32
	Priority        uint32
	Palette         uint32
	FlipH           bool
	Is256           bool
	TextureX        int
	TextureY        int
}

// Sprite ⇄ PPU::Sprite.
type Sprite struct {
	TimestampInit       int64
	TimestampLastSync   int64
	TimestampVRAMAccess int64
	TimestampOAMAccess  int64
	Cycle               uint32
	VCount              uint32
	MosaicY             int

	OAMFetch struct {
		Index         uint32
		Step          int
		Wait          int
		PendingWait   int
		DelayWait     bool
		InitialLocalX int
		InitialLocalY int
		MatrixAddress uint32
	}

	Drawing bool

	DrawerState [2]drawerState
	StateRD     int
	StateWR     int

	Buffer    [2][240]SpritePixel
	BufferRD  int
	BufferWR  int

	LatchCycleLimit uint32
}

var spriteSize = [4][4][2]int{
	{{8, 8}, {16, 16}, {32, 32}, {64, 64}},   // Square
	{{16, 8}, {32, 8}, {32, 16}, {64, 32}},   // Horizontal
	{{8, 16}, {8, 32}, {16, 32}, {32, 64}},   // Vertical
	{{8, 8}, {8, 8}, {8, 8}, {8, 8}},          // Prohibited
}

// InitSprite ⇄ PPU::InitSprite.
func (p *PPU) InitSprite() {
	now := p.sched.Now()
	p.Sprite.TimestampInit = now
	p.Sprite.TimestampLastSync = now
	p.Sprite.Cycle = 0
	p.Sprite.VCount = (uint32(p.VCOUNT) + 1) % 228
	p.Sprite.MosaicY = p.MOSAIC.OBJ.CounterY
	p.Sprite.OAMFetch.Index = 0
	p.Sprite.OAMFetch.Step = 0
	p.Sprite.OAMFetch.Wait = 0
	p.Sprite.OAMFetch.DelayWait = false
	p.Sprite.Drawing = false
	p.Sprite.StateRD = 0
	p.Sprite.StateWR = 1
	if p.DISPCNT.HBlankOAMAccess != 0 {
		p.Sprite.LatchCycleLimit = 964
	} else {
		p.Sprite.LatchCycleLimit = 1232
	}
	for i := range p.Sprite.Buffer[p.Sprite.BufferWR] {
		p.Sprite.Buffer[p.Sprite.BufferWR][i] = SpritePixel{}
	}
}

// DrawSprite ⇄ PPU::DrawSprite.
func (p *PPU) DrawSprite() {
	now := p.sched.Now()
	cycles := int(now - p.Sprite.TimestampLastSync)
	if cycles == 0 || p.Sprite.Cycle >= p.Sprite.LatchCycleLimit {
		return
	}
	p.drawSpriteImpl(cycles)
	p.Sprite.TimestampLastSync = now
}

func (p *PPU) drawSpriteImpl(cycles int) {
	cycleLimit := p.Sprite.LatchCycleLimit
	for i := 0; i < cycles; i++ {
		cycle := p.Sprite.Cycle
		if p.DISPCNT.Enable[EnableOBJ] != 0 && (cycle&1) == 0 {
			p.drawSpriteFetchVRAM(cycle)
			p.drawSpriteFetchOAM(cycle)
		}
		if cycle == 1192 {
			if p.Sprite.VCount < 159 {
				p.MOSAIC.OBJ.CounterY++
				if p.MOSAIC.OBJ.CounterY == p.MOSAIC.OBJ.SizeY {
					p.MOSAIC.OBJ.CounterY = 0
				} else {
					p.MOSAIC.OBJ.CounterY &= 15
				}
			} else {
				p.MOSAIC.OBJ.CounterY = 0
			}
		}
		p.Sprite.Cycle++
		if p.Sprite.Cycle == cycleLimit {
			break
		}
	}
}

func (p *PPU) fetchOAM_u32(cycle uint32, addr uint32) uint32 {
	p.Sprite.TimestampOAMAccess = p.Sprite.TimestampInit + int64(cycle)
	addr &= 0x3FC
	return uint32(p.OAM[addr]) | uint32(p.OAM[addr+1])<<8 |
		uint32(p.OAM[addr+2])<<16 | uint32(p.OAM[addr+3])<<24
}

func (p *PPU) fetchOAM_u16(cycle uint32, addr uint32) uint16 {
	p.Sprite.TimestampOAMAccess = p.Sprite.TimestampInit + int64(cycle)
	addr &= 0x3FE
	return uint16(p.OAM[addr]) | uint16(p.OAM[addr+1])<<8
}

func (p *PPU) fetchVRAM_OBJ_u8(cycle uint32, addr uint32) uint8 {
	addr &= 0x1FFFF
	if addr >= 0x18000 {
		addr &= 0x17FFF
	}
	if addr >= p.spriteVRAMBoundary() {
		p.Sprite.TimestampVRAMAccess = p.Sprite.TimestampInit + int64(cycle)
		return p.VRAM[addr]
	}
	return 0
}

func (p *PPU) fetchVRAM_OBJ_u16(cycle uint32, addr uint32) uint16 {
	addr &= 0x1FFFF
	if addr >= 0x18000 {
		addr &= 0x17FFF
	}
	if addr >= p.spriteVRAMBoundary() {
		p.Sprite.TimestampVRAMAccess = p.Sprite.TimestampInit + int64(cycle)
		return uint16(p.VRAM[addr]) | uint16(p.VRAM[addr+1])<<8
	}
	return 0
}

func (p *PPU) submitOAM() {
	p.Sprite.StateRD ^= 1
	p.Sprite.StateWR ^= 1
	p.Sprite.Drawing = true
	p.Sprite.OAMFetch.Index++
	p.Sprite.OAMFetch.Step = 0
	p.Sprite.OAMFetch.Wait = p.Sprite.OAMFetch.PendingWait
	p.Sprite.OAMFetch.DelayWait = true
}

// drawSpriteFetchOAM ⇄ PPU::DrawSpriteFetchOAM.
func (p *PPU) drawSpriteFetchOAM(cycle uint32) {
	fetch := &p.Sprite.OAMFetch
	if fetch.Wait > 0 && !fetch.DelayWait {
		fetch.Wait--
		return
	}
	fetch.DelayWait = false
	ds := &p.Sprite.DrawerState[p.Sprite.StateWR]

	switch fetch.Step {
	case 0:
		if fetch.Index == 128 {
			fetch.Step = 6
			return
		}
		attr01 := p.fetchOAM_u32(cycle, fetch.Index*8)
		active := false
		if attr01&0x300 != 0x200 {
			mode := int((attr01 >> 10) & 3)
			if mode != OBJProhibited {
				x := int32((attr01 >> 16) & 0x1FF)
				y := int32(attr01 & 0xFF)
				if x >= 240 {
					x -= 512
				}
				shape := (attr01 >> 14) & 3
				size := attr01 >> 30
				width := spriteSize[shape][size][0]
				height := spriteSize[shape][size][1]
				halfWidth := width >> 1
				halfHeight := height >> 1
				affine := attr01&0x100 != 0
				if affine {
					doubleSize := attr01&0x200 != 0
					if doubleSize {
						halfWidth *= 2
						halfHeight *= 2
					}
				}
				vcount := int(p.Sprite.VCount)
				yMax := (int(y) + halfHeight*2) & 255
				if (vcount >= int(y) || yMax < int(y)) && vcount < yMax {
					mosaic := attr01&(1<<12) != 0 && mode != OBJWindow
					ds.Width = width
					ds.Height = height
					ds.Mode = mode
					ds.Mosaic = mosaic
					ds.Affine = affine
					ds.DrawX = int(x)
					ds.RemainingPixels = halfWidth << 1
					ds.Is256 = (attr01>>13)&1 != 0
					localY := (vcount - int(y)) & 255
					if mosaic {
						localY -= p.Sprite.MosaicY
						if localY < 0 {
							localY = 0
						}
					}
					if !affine {
						flipV := attr01&(1<<29) != 0
						ds.FlipH = attr01&(1<<28) != 0
						ds.TextureX = 0
						ds.TextureY = localY
						if flipV {
							ds.TextureY ^= height - 1
						}
						fetch.PendingWait = halfWidth - 2
					} else {
						fetch.InitialLocalX = -halfWidth
						fetch.InitialLocalY = localY - halfHeight
						fetch.PendingWait = halfWidth*2 - 1
						fetch.MatrixAddress = ((attr01 >> 25) & 31) * 32 + 6
					}
					active = true
					if x < 0 {
						clip := int(-x)
						if !affine {
							clip &= ^1
						}
						ds.DrawX += clip
						ds.RemainingPixels -= clip
						if affine {
							fetch.PendingWait -= clip
							fetch.InitialLocalX += clip
						} else {
							fetch.PendingWait -= clip >> 1
							ds.TextureX += clip
						}
						if ds.RemainingPixels <= 0 {
							active = false
						}
					}
				}
			}
		}
		if active {
			fetch.Step = 1
		} else {
			fetch.Index++
		}

	case 1:
		attr2 := p.fetchOAM_u16(cycle, fetch.Index*8+4)
		ds.TileNumber = uint32(attr2 & 0x3FF)
		ds.Priority = uint32((attr2 >> 10) & 3)
		ds.Palette = uint32(attr2 >> 12)
		if ds.Affine {
			fetch.Step = 2
		} else {
			p.submitOAM()
		}

	case 2, 3, 4, 5:
		raw := p.fetchOAM_u16(cycle, fetch.MatrixAddress)
		ds.Matrix[fetch.Step-2] = int16(raw)
		fetch.MatrixAddress += 8
		fetch.Step++
		if fetch.Step == 6 {
			x0 := fetch.InitialLocalX
			y0 := fetch.InitialLocalY
			ds.TextureX = int(ds.Matrix[0])*x0 + int(ds.Matrix[1])*y0 + (ds.Width << 7)
			ds.TextureY = int(ds.Matrix[2])*x0 + int(ds.Matrix[3])*y0 + (ds.Height << 7)
			p.submitOAM()
		}
	}
}

func (p *PPU) calcTileNumber4BPP(baseTile uint32, blockX, blockY, width int) uint32 {
	if p.DISPCNT.OAMMapping1D != 0 {
		return (baseTile + uint32(blockY)*(uint32(width)>>3) + uint32(blockX)) & 0x3FF
	}
	return ((baseTile + uint32(blockY)<<5) & 0x3E0) | ((baseTile + uint32(blockX)) & 0x1F)
}

func (p *PPU) calcTileNumber8BPP(baseTile uint32, blockX, blockY, width int) uint32 {
	if p.DISPCNT.OAMMapping1D != 0 {
		return (baseTile + uint32(blockY)*(uint32(width)>>2) + uint32(blockX)<<1) & 0x3FF
	}
	return ((baseTile + uint32(blockY)<<5) & 0x3E0) | (((baseTile &^ 1) + uint32(blockX)<<1) & 0x1F)
}

func (p *PPU) plotSpritePixel(x int, color uint32, ds *drawerState) {
	if x < 0 || x >= 240 {
		return
	}
	pixel := &p.Sprite.Buffer[p.Sprite.BufferWR][x]
	opaque := color != 0
	mode := ds.Mode
	priority := ds.Priority

	pColor := pixel.Color()
	pPri := pixel.Priority()

	if mode == OBJWindow && opaque {
		pixel.Data |= 1 << 18 // set window bit
	} else if priority < pPri || pColor == 0 {
		c, _, a, w, m := pColor, pPri, pixel.Alpha(), pixel.Window(), pixel.Mosaic()
		_ = a
		if opaque {
			c = color
			a = (mode == OBJSemi)
		}
		m = ds.Mosaic
		*pixel = makeSpritePixel(c, priority, a, w, m)
	}
}

// drawSpriteFetchVRAM ⇄ PPU::DrawSpriteFetchVRAM.
func (p *PPU) drawSpriteFetchVRAM(cycle uint32) {
	if !p.Sprite.Drawing {
		return
	}
	ds := &p.Sprite.DrawerState[p.Sprite.StateRD]
	width := ds.Width
	height := ds.Height
	baseTile := ds.TileNumber

	if ds.Affine {
		if p.Sprite.OAMFetch.DelayWait {
			return
		}
		textureX := ds.TextureX >> 8
		textureY := ds.TextureY >> 8
		if textureX >= 0 && textureX < width && textureY >= 0 && textureY < height {
			tileX := textureX & 7
			tileY := textureY & 7
			blockX := textureX >> 3
			blockY := textureY >> 3
			var colorIndex uint32
			if ds.Is256 {
				tile := p.calcTileNumber8BPP(baseTile, blockX, blockY, width)
				addr := uint32(0x10000) + tile<<5 + uint32(tileY)<<3 + uint32(tileX)
				colorIndex = uint32(p.fetchVRAM_OBJ_u8(cycle, addr))
			} else {
				tile := p.calcTileNumber4BPP(baseTile, blockX, blockY, width)
				addr := uint32(0x10000) + tile<<5 + uint32(tileY)<<2 + uint32(tileX)>>1
				data := p.fetchVRAM_OBJ_u8(cycle, addr)
				if tileX&1 != 0 {
					colorIndex = uint32(data >> 4)
				} else {
					colorIndex = uint32(data & 15)
				}
				if colorIndex > 0 {
					colorIndex |= ds.Palette << 4
				}
			}
			p.plotSpritePixel(ds.DrawX, colorIndex, ds)
		}
		ds.DrawX++
		ds.TextureX += int(ds.Matrix[0])
		ds.TextureY += int(ds.Matrix[2])
		ds.RemainingPixels--
		if ds.RemainingPixels == 0 {
			p.Sprite.Drawing = false
		}
	} else {
		flipH := ds.FlipH
		textureX := ds.TextureX
		if flipH {
			textureX ^= width - 1
		}
		textureY := ds.TextureY
		tileX := textureX & 7 & ^1
		tileY := textureY & 7
		blockX := textureX >> 3
		blockY := textureY >> 3

		var palette uint32
		var colorIndices [2]uint32
		if ds.Is256 {
			tile := p.calcTileNumber8BPP(baseTile, blockX, blockY, width)
			addr := uint32(0x10000) + tile<<5 + uint32(tileY)<<3 + uint32(tileX)
			data := p.fetchVRAM_OBJ_u16(cycle, addr)
			if flipH {
				colorIndices[0] = uint32(data >> 8)
				colorIndices[1] = uint32(data & 0xFF)
			} else {
				colorIndices[0] = uint32(data & 0xFF)
				colorIndices[1] = uint32(data >> 8)
			}
			palette = 0
		} else {
			tile := p.calcTileNumber4BPP(baseTile, blockX, blockY, width)
			addr := uint32(0x10000) + tile<<5 + uint32(tileY)<<2 + uint32(tileX)>>1
			data := p.fetchVRAM_OBJ_u8(cycle, addr)
			if flipH {
				colorIndices[0] = uint32(data >> 4)
				colorIndices[1] = uint32(data & 15)
			} else {
				colorIndices[0] = uint32(data & 15)
				colorIndices[1] = uint32(data >> 4)
			}
			palette = ds.Palette << 4
		}

		for i := 0; i < 2; i++ {
			colorIndex := colorIndices[i]
			if colorIndex > 0 {
				colorIndex |= palette
			}
			p.plotSpritePixel(ds.DrawX, colorIndex, ds)
			ds.DrawX++
		}
		ds.TextureX += 2
		ds.RemainingPixels -= 2
		if ds.RemainingPixels == 0 {
			p.Sprite.Drawing = false
		}
	}
}

// SwapSpriteBuffers ⇄ std::swap(sprite.buffer_rd, sprite.buffer_wr) in
// BeginSpriteDrawing.
func (p *PPU) SwapSpriteBuffers() {
	p.Sprite.BufferRD, p.Sprite.BufferWR = p.Sprite.BufferWR, p.Sprite.BufferRD
}
