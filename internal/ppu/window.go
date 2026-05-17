// window.go ⇄ src/nba/src/hw/ppu/window.cc
//
// Per-cycle window flag state machine. InitWindow latches V-flags at the
// start of each scanline (when vcount equals winv.min/max). DrawWindow is
// driven from the scanline event handler — every 4 cycles advances to the
// next pixel column and updates the H-flag against winh.min/max.
package ppu

// InitWindow ⇄ PPU::InitWindow.
func (p *PPU) InitWindow() {
	vcount := int(p.VCOUNT)
	for i := range 2 {
		winv := p.winv(i)
		if vcount == winv.Min {
			p.Window.VFlag[i] = true
		}
		if vcount == winv.Max {
			p.Window.VFlag[i] = false
		}
	}
	p.Window.TimestampLastSync = p.sched.Now()
	p.Window.Cycle = 0
}

// DrawWindow ⇄ PPU::DrawWindow.
func (p *PPU) DrawWindow() {
	now := p.sched.Now()
	cycles := int(now - p.Window.TimestampLastSync)
	if cycles == 0 || p.Window.Cycle >= 1024 {
		return
	}
	for range cycles {
		if (p.Window.Cycle & 3) == 0 {
			x := p.Window.Cycle >> 2
			for j := range 2 {
				winh := p.winh(j)
				if int(x) == winh.Min {
					p.Window.HFlag[j] = true
				}
				if int(x) == winh.Max {
					p.Window.HFlag[j] = false
				}
				if x < 240 {
					p.Window.Buffer[x][j] = p.Window.HFlag[j] && p.Window.VFlag[j]
				}
			}
		}
		p.Window.Cycle++
		if p.Window.Cycle == 1024 {
			break
		}
	}
	p.Window.TimestampLastSync = now
}
