// Package arm — port of src/nba/src/arm/.
//
// state.go ⇄ src/nba/src/arm/state.hh
package arm

// Mode is the low 5 bits of CPSR.
type Mode uint32

const (
	MODE_USR Mode = 0x10
	MODE_FIQ Mode = 0x11
	MODE_IRQ Mode = 0x12
	MODE_SVC Mode = 0x13
	MODE_ABT Mode = 0x17
	MODE_UND Mode = 0x1B
	MODE_SYS Mode = 0x1F
)

// Bank slots (mode → bank index). nba::core::arm::Bank.
type Bank int

const (
	BANK_NONE Bank = 0
	BANK_FIQ  Bank = 1
	BANK_SVC  Bank = 2
	BANK_ABT  Bank = 3
	BANK_IRQ  Bank = 4
	BANK_UND  Bank = 5
	BANK_INVALID Bank = 6
	BANK_COUNT   = 7
)

// Condition codes — nba::core::arm::Condition.
type Condition uint32

const (
	COND_EQ Condition = 0
	COND_NE Condition = 1
	COND_CS Condition = 2
	COND_CC Condition = 3
	COND_MI Condition = 4
	COND_PL Condition = 5
	COND_VS Condition = 6
	COND_VC Condition = 7
	COND_HI Condition = 8
	COND_LS Condition = 9
	COND_GE Condition = 10
	COND_LT Condition = 11
	COND_GT Condition = 12
	COND_LE Condition = 13
	COND_AL Condition = 14
	COND_NV Condition = 15
)

// StatusRegister mirrors the union in state.hh — we expose flag accessors
// rather than a bitfield struct since Go has no bit fields.
type StatusRegister struct{ V uint32 }

func (s StatusRegister) Mode() Mode    { return Mode(s.V & 0x1F) }
func (s StatusRegister) Thumb() bool   { return s.V&(1<<5) != 0 }
func (s StatusRegister) MaskFIQ() bool { return s.V&(1<<6) != 0 }
func (s StatusRegister) MaskIRQ() bool { return s.V&(1<<7) != 0 }
func (s StatusRegister) Q() bool       { return s.V&(1<<27) != 0 }
func (s StatusRegister) V_() bool      { return s.V&(1<<28) != 0 }
func (s StatusRegister) C() bool       { return s.V&(1<<29) != 0 }
func (s StatusRegister) Z() bool       { return s.V&(1<<30) != 0 }
func (s StatusRegister) N() bool       { return s.V&(1<<31) != 0 }

// CPSR/SPSR field setters — these match the assignments in the upstream
// `f.x = y` syntax. They preserve other bits.
func (s *StatusRegister) SetMode(m Mode) { s.V = (s.V &^ 0x1F) | (uint32(m) & 0x1F) }
func (s *StatusRegister) SetThumb(v uint32) {
	if v != 0 {
		s.V |= 1 << 5
	} else {
		s.V &^= 1 << 5
	}
}
func (s *StatusRegister) SetMaskFIQ(v uint32) {
	if v != 0 {
		s.V |= 1 << 6
	} else {
		s.V &^= 1 << 6
	}
}
func (s *StatusRegister) SetMaskIRQ(v uint32) {
	if v != 0 {
		s.V |= 1 << 7
	} else {
		s.V &^= 1 << 7
	}
}
func (s *StatusRegister) SetV(v uint32) {
	if v != 0 {
		s.V |= 1 << 28
	} else {
		s.V &^= 1 << 28
	}
}
func (s *StatusRegister) SetC(v uint32) {
	if v != 0 {
		s.V |= 1 << 29
	} else {
		s.V &^= 1 << 29
	}
}
func (s *StatusRegister) SetZ(v uint32) {
	if v != 0 {
		s.V |= 1 << 30
	} else {
		s.V &^= 1 << 30
	}
}
func (s *StatusRegister) SetN(v uint32) {
	if v != 0 {
		s.V |= 1 << 31
	} else {
		s.V &^= 1 << 31
	}
}

// RegisterFile mirrors nba::core::arm::RegisterFile.
//
// `Reg[0..15]` are the active registers; `Bank[bank][0..6]` holds the banked
// r8..r14 per mode (BANK_NONE stores user/sys r8..r14, BANK_FIQ stores
// r8_fiq..r14_fiq; other modes only use slots [5]=r13 and [6]=r14).
type RegisterFile struct {
	Reg  [16]uint32
	Bank [BANK_COUNT][7]uint32
	CPSR StatusRegister
	SPSR [BANK_COUNT]StatusRegister
}

func (r *RegisterFile) Reset() {
	for i := range r.Reg {
		r.Reg[i] = 0
	}
	for i := range r.Bank {
		for j := range r.Bank[i] {
			r.Bank[i][j] = 0
		}
		r.SPSR[i].V = 0
	}
	r.CPSR.V = uint32(MODE_SVC)
	r.CPSR.SetMaskIRQ(1)
	r.CPSR.SetMaskFIQ(1)
}
