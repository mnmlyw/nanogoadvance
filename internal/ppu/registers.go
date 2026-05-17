// registers.go ⇄ src/nba/src/hw/ppu/registers.{hh,cc}
//
// PPU IO-register decoders. Each register type owns its raw byte representation
// plus pre-decoded fields. WriteHalf delegates to two Write calls — matches
// upstream's `Write(0, (u8)value); Write(1, (u8)(value >> 8))` pattern.
package ppu

// DisplayControl ⇄ DisplayControl in registers.hh.
type DisplayControl struct {
	Hword           uint16
	Mode            int
	CGBMode         int
	Frame           int
	HBlankOAMAccess int
	OAMMapping1D    int
	ForcedBlank     int
	Enable          [8]int
}

func (d *DisplayControl) Reset() { d.Write(0, 0); d.Write(1, 0) }

func (d *DisplayControl) Read(address int) uint8 {
	switch address {
	case 0:
		return uint8(d.Hword)
	case 1:
		return uint8(d.Hword >> 8)
	}
	return 0
}

func (d *DisplayControl) Write(address int, value uint8) {
	switch address {
	case 0:
		d.Hword = (d.Hword & 0xFF00) | uint16(value)
		d.Mode = int(value & 7)
		d.CGBMode = int((value >> 3) & 1)
		d.Frame = int((value >> 4) & 1)
		d.HBlankOAMAccess = int((value >> 5) & 1)
		d.OAMMapping1D = int((value >> 6) & 1)
		d.ForcedBlank = int((value >> 7) & 1)
	case 1:
		d.Hword = (d.Hword & 0x00FF) | (uint16(value) << 8)
		for i := range 8 {
			d.Enable[i] = int((value >> i) & 1)
		}
	}
}

func (d *DisplayControl) ReadHalf() uint16   { return uint16(d.Read(0)) | uint16(d.Read(1))<<8 }
func (d *DisplayControl) WriteHalf(v uint16) { d.Write(0, uint8(v)); d.Write(1, uint8(v>>8)) }

// DisplayStatus ⇄ DisplayStatus. We omit the PPU back-reference and instead
// expose UpdateVerticalCounterFlag through the PPU when state changes.
type DisplayStatus struct {
	VBlankFlag      int
	HBlankFlag      int
	VCountFlag      int
	VBlankIRQEnable int
	HBlankIRQEnable int
	VCountIRQEnable int
	VCountSetting   int
}

func (d *DisplayStatus) Reset() { *d = DisplayStatus{} }

func (d *DisplayStatus) Read(address int) uint8 {
	switch address {
	case 0:
		return uint8(d.VBlankFlag) |
			uint8(d.HBlankFlag)<<1 |
			uint8(d.VCountFlag)<<2 |
			uint8(d.VBlankIRQEnable)<<3 |
			uint8(d.HBlankIRQEnable)<<4 |
			uint8(d.VCountIRQEnable)<<5
	case 1:
		return uint8(d.VCountSetting)
	}
	return 0
}

func (d *DisplayStatus) Write(address int, value uint8) {
	switch address {
	case 0:
		d.VBlankIRQEnable = int((value >> 3) & 1)
		d.HBlankIRQEnable = int((value >> 4) & 1)
		d.VCountIRQEnable = int((value >> 5) & 1)
	case 1:
		d.VCountSetting = int(value)
	}
}

func (d *DisplayStatus) ReadHalf() uint16   { return uint16(d.Read(0)) | uint16(d.Read(1))<<8 }
func (d *DisplayStatus) WriteHalf(v uint16) { d.Write(0, uint8(v)); d.Write(1, uint8(v>>8)) }

// BackgroundControl ⇄ BackgroundControl. ID is needed because BG0/1 ignore
// the wraparound bit (which BG2/3 honour).
type BackgroundControl struct {
	Priority     int
	TileBlock    int
	Unused       int
	MosaicEnable int
	FullPalette  int
	MapBlock     int
	Wraparound   int
	Size         int
	ID           int
}

func (b *BackgroundControl) Reset() { b.Write(0, 0); b.Write(1, 0) }

func (b *BackgroundControl) Read(address int) uint8 {
	switch address {
	case 0:
		return uint8(b.Priority) |
			uint8(b.TileBlock)<<2 |
			uint8(b.Unused)<<4 |
			uint8(b.MosaicEnable)<<6 |
			uint8(b.FullPalette)<<7
	case 1:
		return uint8(b.MapBlock) |
			uint8(b.Wraparound)<<5 |
			uint8(b.Size)<<6
	}
	return 0
}

func (b *BackgroundControl) Write(address int, value uint8) {
	switch address {
	case 0:
		b.Priority = int(value & 3)
		b.TileBlock = int((value >> 2) & 3)
		b.Unused = int((value >> 4) & 3)
		b.MosaicEnable = int((value >> 6) & 1)
		b.FullPalette = int(value >> 7)
	case 1:
		b.MapBlock = int(value & 0x1F)
		if b.ID >= 2 {
			b.Wraparound = int((value >> 5) & 1)
		}
		b.Size = int(value >> 6)
	}
}

func (b *BackgroundControl) ReadHalf() uint16   { return uint16(b.Read(0)) | uint16(b.Read(1))<<8 }
func (b *BackgroundControl) WriteHalf(v uint16) { b.Write(0, uint8(v)); b.Write(1, uint8(v>>8)) }

// ReferencePoint ⇄ BG affine reference point (BG2X, BG2Y, BG3X, BG3Y).
type ReferencePoint struct {
	Initial int32
	Current int32
	Written bool
}

func (r *ReferencePoint) Reset() { r.Initial = 0; r.Current = 0; r.Written = false }

func (r *ReferencePoint) Write(address int, value uint8) {
	switch address {
	case 0:
		r.Initial = (r.Initial & 0x0FFFFF00) | int32(value)
	case 1:
		r.Initial = (r.Initial & 0x0FFF00FF) | int32(value)<<8
	case 2:
		r.Initial = (r.Initial & 0x0F00FFFF) | int32(value)<<16
	case 3:
		r.Initial = (r.Initial & 0x00FFFFFF) | int32(value)<<24
	}
	if r.Initial&(1<<27) != 0 {
		r.Initial |= ^int32(0x0FFFFFFF)
	}
	r.Written = true
}

// BlendEffect ⇄ BlendControl::Effect.
type BlendEffect int

const (
	SFXNone     BlendEffect = 0
	SFXBlend    BlendEffect = 1
	SFXBrighten BlendEffect = 2
	SFXDarken   BlendEffect = 3
)

type BlendControl struct {
	SFX     BlendEffect
	Targets [2][6]int
}

func (b *BlendControl) Reset() { b.Write(0, 0); b.Write(1, 0) }

func (b *BlendControl) Read(address int) uint8 {
	v := uint8(0)
	switch address {
	case 0:
		for i := range 6 {
			v |= uint8(b.Targets[0][i]) << i
		}
		v |= uint8(b.SFX) << 6
	case 1:
		for i := range 6 {
			v |= uint8(b.Targets[1][i]) << i
		}
	}
	return v
}

func (b *BlendControl) Write(address int, value uint8) {
	switch address {
	case 0:
		for i := range 6 {
			b.Targets[0][i] = int((value >> i) & 1)
		}
		b.SFX = BlendEffect(value >> 6)
	case 1:
		for i := range 6 {
			b.Targets[1][i] = int((value >> i) & 1)
		}
	}
}

func (b *BlendControl) ReadHalf() uint16   { return uint16(b.Read(0)) | uint16(b.Read(1))<<8 }
func (b *BlendControl) WriteHalf(v uint16) { b.Write(0, uint8(v)); b.Write(1, uint8(v>>8)) }

// WindowRange ⇄ WindowRange.
type WindowRange struct {
	Min int
	Max int
}

func (w *WindowRange) Reset() { w.Min = 0; w.Max = 0 }

func (w *WindowRange) Write(address int, value uint8) {
	switch address {
	case 0:
		w.Max = int(value)
	case 1:
		w.Min = int(value)
	}
}

func (w *WindowRange) ReadHalf() uint16   { return uint16(w.Max) | uint16(w.Min)<<8 }
func (w *WindowRange) WriteHalf(v uint16) { w.Write(0, uint8(v)); w.Write(1, uint8(v>>8)) }

// WindowLayerSelect ⇄ WININ/WINOUT.
type WindowLayerSelect struct {
	Enable [2][6]int
}

func (w *WindowLayerSelect) Reset() { w.Write(0, 0); w.Write(1, 0) }

func (w *WindowLayerSelect) Read(address int) uint8 {
	v := uint8(0)
	for i := range 6 {
		v |= uint8(w.Enable[address][i]) << i
	}
	return v
}

func (w *WindowLayerSelect) Write(address int, value uint8) {
	for i := range 6 {
		w.Enable[address][i] = int((value >> i) & 1)
	}
}

func (w *WindowLayerSelect) ReadHalf() uint16   { return uint16(w.Read(0)) | uint16(w.Read(1))<<8 }
func (w *WindowLayerSelect) WriteHalf(v uint16) { w.Write(0, uint8(v)); w.Write(1, uint8(v>>8)) }

// Mosaic ⇄ Mosaic.
type Mosaic struct {
	BG  MosaicAxis
	OBJ MosaicAxis
}
type MosaicAxis struct {
	SizeX    int
	SizeY    int
	CounterY int
}

// Reset ⇄ Mosaic::Reset. Sets each axis size to 1 (no mosaic effect) and
// zeros the per-line counter.
func (m *Mosaic) Reset() {
	m.BG = MosaicAxis{SizeX: 1, SizeY: 1}
	m.OBJ = MosaicAxis{SizeX: 1, SizeY: 1}
}

// Write ⇄ Mosaic::Write. Stores `raw + 1` for each axis (range 1..16) —
// matches upstream's storage. CounterY is NOT reset on register write
// (upstream's Write doesn't touch _counter_y).
func (m *Mosaic) Write(address int, value uint8) {
	switch address {
	case 0:
		m.BG.SizeX = int(value&15) + 1
		m.BG.SizeY = int(value>>4) + 1
	case 1:
		m.OBJ.SizeX = int(value&15) + 1
		m.OBJ.SizeY = int(value>>4) + 1
	}
}
