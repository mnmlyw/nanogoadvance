// serialization.go ⇄ src/nba/src/hw/ppu/serialization.cc
package ppu

import "github.com/mnmlyw/nanogoadvance/internal/savestate"

func (p *PPU) bgRef(id int) (*ReferencePoint, *ReferencePoint) {
	if id == 0 {
		return &p.BG2X, &p.BG2Y
	}
	return &p.BG3X, &p.BG3Y
}

func (p *PPU) LoadState(s *savestate.SaveState) {
	io := &s.PPU.IO

	// VCOUNT must be restored before DISPSTAT so loading DISPSTAT doesn't
	// trip a phantom V-count IRQ (upstream comment).
	p.VCOUNT = io.VCOUNT

	p.DISPCNT.WriteHalf(io.DISPCNT)
	p.GREENSWAP = io.GREENSWAP
	p.DISPSTAT.WriteHalf(io.DISPSTAT)

	for id := 0; id < 4; id++ {
		p.BGCNT[id].WriteHalf(io.BGCNT[id])
		p.BGHOFS[id] = io.BGHOFS[id]
		p.BGVOFS[id] = io.BGVOFS[id]
	}

	for id := 0; id < 2; id++ {
		p.BGPA[id] = int16(io.BGPA[id])
		p.BGPB[id] = int16(io.BGPB[id])
		p.BGPC[id] = int16(io.BGPC[id])
		p.BGPD[id] = int16(io.BGPD[id])

		bgx, bgy := p.bgRef(id)
		bgx.Initial = int32(io.BGX[id])
		bgy.Initial = int32(io.BGY[id])
		bgx.Current = s.PPU.BGX[id].Current
		bgx.Written = s.PPU.BGX[id].Written
		bgy.Current = s.PPU.BGY[id].Current
		bgy.Written = s.PPU.BGY[id].Written

		winH, winV := p.winRefs(id)
		winH.WriteHalf(io.WINH[id])
		winV.WriteHalf(io.WINV[id])
	}

	p.WININ.WriteHalf(io.WININ)
	p.WINOUT.WriteHalf(io.WINOUT)

	p.MOSAIC.BG.SizeX = int((io.MOSAIC >> 0) & 15)
	p.MOSAIC.BG.SizeY = int((io.MOSAIC >> 4) & 15)
	p.MOSAIC.OBJ.SizeX = int((io.MOSAIC >> 8) & 15)
	p.MOSAIC.OBJ.SizeY = int(io.MOSAIC >> 12)

	p.BLDCNT.WriteHalf(io.BLDCNT)
	p.BLDALPHA.EVA = int(io.BLDALPHA & 31)
	p.BLDALPHA.EVB = int((io.BLDALPHA >> 8) & 31)
	p.BLDY = int(io.BLDY & 31)

	// Bus owns palette/oam/vram in the port; the per-byte arrays live in
	// the savestate.Bus.Memory section and are restored by Bus.LoadState.

	p.VRAMBGLatch = s.PPU.VRAMBGLatch
	p.DMA3VideoTransferRunning = s.PPU.DMA3VideoTransferRunning
}

func (p *PPU) winRefs(id int) (*WindowRange, *WindowRange) {
	if id == 0 {
		return &p.WIN0H, &p.WIN0V
	}
	return &p.WIN1H, &p.WIN1V
}

func (p *PPU) CopyState(s *savestate.SaveState) {
	io := &s.PPU.IO

	io.DISPCNT = p.DISPCNT.ReadHalf()
	io.GREENSWAP = p.GREENSWAP
	io.DISPSTAT = p.DISPSTAT.ReadHalf()
	io.VCOUNT = p.VCOUNT

	for id := 0; id < 4; id++ {
		io.BGCNT[id] = p.BGCNT[id].ReadHalf()
		io.BGHOFS[id] = p.BGHOFS[id]
		io.BGVOFS[id] = p.BGVOFS[id]
	}

	for id := 0; id < 2; id++ {
		io.BGPA[id] = uint16(p.BGPA[id])
		io.BGPB[id] = uint16(p.BGPB[id])
		io.BGPC[id] = uint16(p.BGPC[id])
		io.BGPD[id] = uint16(p.BGPD[id])

		bgx, bgy := p.bgRef(id)
		io.BGX[id] = uint32(bgx.Initial)
		io.BGY[id] = uint32(bgy.Initial)
		s.PPU.BGX[id].Current = bgx.Current
		s.PPU.BGX[id].Written = bgx.Written
		s.PPU.BGY[id].Current = bgy.Current
		s.PPU.BGY[id].Written = bgy.Written

		winH, winV := p.winRefs(id)
		io.WINH[id] = winH.ReadHalf()
		io.WINV[id] = winV.ReadHalf()
	}

	io.WININ = p.WININ.ReadHalf()
	io.WINOUT = p.WINOUT.ReadHalf()

	io.MOSAIC = uint16(p.MOSAIC.BG.SizeX)<<0 |
		uint16(p.MOSAIC.BG.SizeY)<<4 |
		uint16(p.MOSAIC.OBJ.SizeX)<<8 |
		uint16(p.MOSAIC.OBJ.SizeY)<<12

	io.BLDCNT = p.BLDCNT.ReadHalf()
	io.BLDALPHA = uint16(p.BLDALPHA.EVA) | (uint16(p.BLDALPHA.EVB) << 8)
	io.BLDY = uint16(p.BLDY)

	s.PPU.VRAMBGLatch = p.VRAMBGLatch
	s.PPU.DMA3VideoTransferRunning = p.DMA3VideoTransferRunning
}
