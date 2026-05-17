// merge.go ⇄ src/nba/src/hw/ppu/merge.cc
//
// Pixel composition. Ports the layer constants, the RGB555 expansion, the
// three SFX color operations (Blend/Brighten/Darken), and the merge state
// machine init+draw. The DrawMergeImpl per-cycle body depends on background
// and sprite state machines that are still being ported; for now the
// pipeline runs with empty layer buffers, producing the backdrop color.
package ppu

// Layer constants ⇄ enum in ppu.hh.
const (
	LayerBG0 = 0
	LayerBG1 = 1
	LayerBG2 = 2
	LayerBG3 = 3
	LayerOBJ = 4
	LayerBD  = 5 // backdrop
	LayerSFX = 5 // alias used by window enable bitmap
)

// DISPCNT enable bit indices (0..7) — repeated from registers.go for
// readability. Layout is: BG0/BG1/BG2/BG3/OBJ/WIN0/WIN1/OBJWIN.
const (
	EnableBG0    = 0
	EnableBG1    = 1
	EnableBG2    = 2
	EnableBG3    = 3
	EnableOBJ    = 4
	EnableWIN0   = 5
	EnableWIN1   = 6
	EnableOBJWIN = 7
)

// Merge mirrors PPU::merge in ppu.hh.
type Merge struct {
	TimestampInit       int64
	TimestampLastSync   int64
	TimestampPRAMAccess int64
	Cycle               uint32

	MosaicX     [2]uint32
	ForcedBlank bool

	Layers [2]int
	Colors [2]uint32

	ForceAlphaBlend bool
	ColorL          uint16

	// SpritePixelLatch is a tiny struct holding a single OBJ-pixel snapshot
	// used by the cycle compositor's mosaic-x latching.
	SpritePixelLatch SpritePixel
}

// SpritePixel ⇄ Sprite::Pixel in ppu.hh.
type SpritePixel struct {
	Data uint32 // packed: color | priority | window | alpha | mosaic
}

func (p *SpritePixel) Color() uint32    { return p.Data & 0x1FF }
func (p *SpritePixel) Priority() uint32 { return (p.Data >> 16) & 3 }
func (p *SpritePixel) Window() bool     { return p.Data&(1<<18) != 0 }
func (p *SpritePixel) Alpha() bool      { return p.Data&(1<<19) != 0 }
func (p *SpritePixel) Mosaic() bool     { return p.Data&(1<<20) != 0 }

// RGB555 ⇄ static RGB555 in merge.cc.
func RGB555(c uint16) uint32 {
	r := uint32(c & 31)
	g := uint32((c >> 5) & 31)
	b := uint32((c >> 10) & 31)
	r = (r << 3) | (r >> 2)
	g = (g << 3) | (g >> 2)
	b = (b << 3) | (b >> 2)
	return 0xFF000000 | r<<16 | g<<8 | b
}

// Blend ⇄ PPU::Blend.
func Blend(colorA, colorB uint16, eva, evb int) uint16 {
	rA := int(colorA & 31)
	gA := int(((colorA >> 4) & 62) | (colorA >> 15))
	bA := int((colorA >> 10) & 31)
	rB := int(colorB & 31)
	gB := int(((colorB >> 4) & 62) | (colorB >> 15))
	bB := int((colorB >> 10) & 31)
	if eva > 16 {
		eva = 16
	}
	if evb > 16 {
		evb = 16
	}
	r := min((rA*eva+rB*evb+8)>>4, 31)
	g := min((gA*eva+gB*evb+8)>>4, 63)
	g >>= 1
	b := min((bA*eva+bB*evb+8)>>4, 31)
	return uint16(b<<10 | g<<5 | r)
}

// Brighten ⇄ PPU::Brighten.
func Brighten(color uint16, evy int) uint16 {
	if evy > 16 {
		evy = 16
	}
	r := int(color & 31)
	g := int(((color >> 4) & 62) | (color >> 15))
	b := int((color >> 10) & 31)
	r += ((31-r)*evy + 8) >> 4
	g += ((63-g)*evy + 8) >> 4
	b += ((31-b)*evy + 8) >> 4
	g >>= 1
	return uint16(b<<10 | g<<5 | r)
}

// Darken ⇄ PPU::Darken.
func Darken(color uint16, evy int) uint16 {
	if evy > 16 {
		evy = 16
	}
	r := int(color & 31)
	g := int(((color >> 4) & 62) | (color >> 15))
	b := int((color >> 10) & 31)
	r -= (r*evy + 7) >> 4
	g -= (g*evy + 7) >> 4
	b -= (b*evy + 7) >> 4
	g >>= 1
	return uint16(b<<10 | g<<5 | r)
}

// InitMerge ⇄ PPU::InitMerge.
func (p *PPU) InitMerge() {
	now := p.sched.Now()
	p.Merge.TimestampInit = now
	p.Merge.TimestampLastSync = now
	p.Merge.Cycle = 0
	p.Merge.MosaicX = [2]uint32{}
	p.Merge.ForcedBlank = false
	p.Merge.SpritePixelLatch.Data = 0
}

// DrawMerge ⇄ PPU::DrawMerge.
func (p *PPU) DrawMerge() {
	now := p.sched.Now()
	cycles := int(now - p.Merge.TimestampLastSync)
	if cycles == 0 || p.Merge.Cycle >= 1006 {
		return
	}
	p.drawMergeImpl(cycles)
	p.Merge.TimestampLastSync = now
}

func (p *PPU) forcedBlank() bool {
	return (p.DISPCNTLatch[0]|p.DISPCNT.Hword)&0x80 != 0
}

// k_min_max_bg ⇄ static constexpr table in merge.cc.
var minMaxBG = [8][2]int{
	{0, 3}, {0, 2}, {2, 3}, {2, 2}, {2, 2}, {2, 2}, {0, -1}, {0, -1},
}

func (p *PPU) fetchPRAM(cycle uint32, addr uint32) uint16 {
	addr &= 0x3FF
	// Mark this cycle as a PRAM access so the bus can detect CPU/PPU
	// contention (matches upstream's merge.timestamp_pram_access).
	p.Merge.TimestampPRAMAccess = p.Merge.TimestampInit + int64(cycle)
	return uint16(p.Palette[addr]) | uint16(p.Palette[addr+1])<<8
}

func (p *PPU) drawMergeImpl(cycles int) {
	mode := p.DISPCNT.Mode
	minBG, maxBG := minMaxBG[mode][0], minMaxBG[mode][1]
	latchedAndCurrent := p.DISPCNTLatch[0] & p.DISPCNT.Hword

	var bgList [4]int
	bgCount := 0
	for priority := 0; priority <= 3; priority++ {
		for id := minBG; id <= maxBG; id++ {
			if p.BGCNT[id].Priority == priority && latchedAndCurrent&(256<<id) != 0 {
				bgList[bgCount] = id
				bgCount++
			}
		}
	}

	enableOBJ := latchedAndCurrent&(256<<LayerOBJ) != 0
	enableWIN0 := p.DISPCNT.Enable[EnableWIN0] != 0
	enableWIN1 := p.DISPCNT.Enable[EnableWIN1] != 0
	enableOBJWIN := p.DISPCNT.Enable[EnableOBJWIN] != 0 && enableOBJ
	haveWindows := enableWIN0 || enableWIN1 || enableOBJWIN

	var winLayerEnable [6]int

	for range cycles {
		cycle := int(p.Merge.Cycle) - 46
		if cycle < 0 {
			p.Merge.Cycle++
			continue
		}
		x := uint(cycle) >> 2

		if haveWindows {
			switch {
			case enableWIN0 && p.Window.Buffer[x][0]:
				winLayerEnable = [6]int(p.WININ.Enable[0])
			case enableWIN1 && p.Window.Buffer[x][1]:
				winLayerEnable = [6]int(p.WININ.Enable[1])
			case enableOBJWIN && p.Sprite.Buffer[p.Sprite.BufferRD][x].Window():
				winLayerEnable = [6]int(p.WINOUT.Enable[1])
			default:
				winLayerEnable = [6]int(p.WINOUT.Enable[0])
			}
		}

		phase := cycle & 3
		if phase == 0 {
			p.Merge.ForcedBlank = p.forcedBlank()
			if !p.Merge.ForcedBlank {
				priorities := [2]uint32{3, 3}
				p.Merge.Layers[0] = LayerBD
				p.Merge.Layers[1] = LayerBD
				p.Merge.Colors[0] = 0
				p.Merge.Colors[1] = 0
				bgIdx := 0
				for j := range 2 {
					for bgIdx < bgCount {
						bgID := bgList[bgIdx]
						bgIdx++
						if !haveWindows || winLayerEnable[bgID] != 0 {
							bgcnt := &p.BGCNT[bgID]
							mx := x
							if bgcnt.MosaicEnable != 0 {
								mx = x - uint(p.Merge.MosaicX[0])
							}
							bgColor := p.BG.Buffer[mx][bgID]
							if bgColor != 0 {
								p.Merge.Layers[j] = bgID
								p.Merge.Colors[j] = bgColor
								priorities[j] = uint32(bgcnt.Priority)
								break
							}
						}
					}
				}

				p.Merge.ForceAlphaBlend = false
				var currentSprite SpritePixel
				if enableOBJ {
					currentSprite = p.Sprite.Buffer[p.Sprite.BufferRD][x]
				}
				latch := &p.Merge.SpritePixelLatch
				if !currentSprite.Mosaic() || !latch.Mosaic() ||
					currentSprite.Priority() < latch.Priority() || p.Merge.MosaicX[1] == 0 {
					*latch = currentSprite
				}

				if enableOBJ && (!haveWindows || winLayerEnable[LayerOBJ] != 0) {
					pixel := *latch
					if pixel.Color() != 0 {
						if pixel.Priority() <= priorities[0] {
							p.Merge.Layers[1] = p.Merge.Layers[0]
							p.Merge.Colors[1] = p.Merge.Colors[0]
							p.Merge.Layers[0] = LayerOBJ
							p.Merge.Colors[0] = pixel.Color() | 256
							p.Merge.ForceAlphaBlend = pixel.Alpha()
						} else if pixel.Priority() <= priorities[1] {
							p.Merge.Layers[1] = LayerOBJ
							p.Merge.Colors[1] = pixel.Color() | 256
						}
					}
				}

				if p.Merge.Colors[0]&0x80000000 == 0 {
					p.Merge.Colors[0] = uint32(p.fetchPRAM(p.Merge.Cycle, p.Merge.Colors[0]<<1))
				}
			} else {
				p.Merge.Colors[0] = 0x7FFF
			}
		} else if phase == 2 {
			if !p.Merge.ForcedBlank {
				haveSrc := p.BLDCNT.Targets[1][p.Merge.Layers[1]] != 0
				if p.Merge.ForceAlphaBlend && haveSrc {
					if p.Merge.Colors[1]&0x80000000 == 0 {
						p.Merge.Colors[1] = uint32(p.fetchPRAM(p.Merge.Cycle, p.Merge.Colors[1]<<1))
					}
					p.Merge.Colors[0] = uint32(Blend(uint16(p.Merge.Colors[0]), uint16(p.Merge.Colors[1]), p.BLDALPHA.EVA, p.BLDALPHA.EVB))
				} else if !haveWindows || winLayerEnable[LayerSFX] != 0 {
					haveDst := p.BLDCNT.Targets[0][p.Merge.Layers[0]] != 0
					switch p.BLDCNT.SFX {
					case SFXBlend:
						if haveDst && haveSrc {
							if p.Merge.Colors[1]&0x80000000 == 0 {
								p.Merge.Colors[1] = uint32(p.fetchPRAM(p.Merge.Cycle, p.Merge.Colors[1]<<1))
							}
							p.Merge.Colors[0] = uint32(Blend(uint16(p.Merge.Colors[0]), uint16(p.Merge.Colors[1]), p.BLDALPHA.EVA, p.BLDALPHA.EVB))
						}
					case SFXBrighten:
						if haveDst {
							p.Merge.Colors[0] = uint32(Brighten(uint16(p.Merge.Colors[0]), p.BLDY))
						}
					case SFXDarken:
						if haveDst {
							p.Merge.Colors[0] = uint32(Darken(uint16(p.Merge.Colors[0]), p.BLDY))
						}
					}
				}
			}

			if x&1 != 0 {
				colorL := p.Merge.ColorL
				colorR := uint16(p.Merge.Colors[0])
				if p.GREENSWAP&1 != 0 {
					mask := uint16(31 << 5)
					gL := colorL & mask
					gR := colorR & mask
					colorL = (colorL &^ mask) | gR
					colorR = (colorR &^ mask) | gL
				}
				out := &p.Output[p.Frame]
				idx := int(p.VCOUNT)*240 + int(x&^1)
				out[idx] = RGB555(colorL)
				out[idx+1] = RGB555(colorR)
			} else {
				p.Merge.ColorL = uint16(p.Merge.Colors[0])
			}

			p.Merge.MosaicX[0]++
			if int(p.Merge.MosaicX[0]) == p.MOSAIC.BG.SizeX {
				p.Merge.MosaicX[0] = 0
			}
			p.Merge.MosaicX[1]++
			if int(p.Merge.MosaicX[1]) == p.MOSAIC.OBJ.SizeX {
				p.Merge.MosaicX[1] = 0
			}
		}

		p.Merge.Cycle++
		if p.Merge.Cycle == 1006 {
			break
		}
	}
}

// LatchDISPCNT ⇄ PPU::LatchDISPCNT.
func (p *PPU) LatchDISPCNT() {
	p.DISPCNTLatch[0] = p.DISPCNTLatch[1]
	p.DISPCNTLatch[1] = p.DISPCNTLatch[2]
	p.DISPCNTLatch[2] = p.DISPCNT.Hword
}
