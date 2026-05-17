// handler16.go ⇄ src/nba/src/arm/handlers/handler16.inl
//
// Thumb instruction handlers. Same approach as handler32.go: template
// parameters are decoded at runtime, but the body matches upstream exactly.
package arm

import "github.com/mnmlyw/nanogoadvance/internal/bus"

// ThumbDataOp ⇄ ARM7TDMI::ThumbDataOp.
type ThumbDataOp int

const (
	T_AND ThumbDataOp = 0
	T_EOR ThumbDataOp = 1
	T_LSL ThumbDataOp = 2
	T_LSR ThumbDataOp = 3
	T_ASR ThumbDataOp = 4
	T_ADC ThumbDataOp = 5
	T_SBC ThumbDataOp = 6
	T_ROR ThumbDataOp = 7
	T_TST ThumbDataOp = 8
	T_NEG ThumbDataOp = 9
	T_CMP ThumbDataOp = 10
	T_CMN ThumbDataOp = 11
	T_ORR ThumbDataOp = 12
	T_MUL ThumbDataOp = 13
	T_BIC ThumbDataOp = 14
	T_MVN ThumbDataOp = 15
)

// Thumb_MoveShiftedRegister — THUMB.1.
func Thumb_MoveShiftedRegister(a *ARM7TDMI, instruction uint16) {
	op := int((instruction >> 11) & 3)
	imm := uint8((instruction >> 6) & 0x1F) // u8 in upstream
	dst := int(instruction & 7)
	src := int((instruction >> 3) & 7)
	carry := uint32(0)
	if a.State.CPSR.C() {
		carry = 1
	}
	result := a.State.Reg[src]
	a.DoShift(op, &result, imm, &carry, true)
	a.State.CPSR.SetC(carry)
	if result == 0 {
		a.State.CPSR.SetZ(1)
	} else {
		a.State.CPSR.SetZ(0)
	}
	a.State.CPSR.SetN(result >> 31)
	a.State.Reg[dst] = result
	a.Pipe.Access = bus.AccessCode | bus.AccessSequential
	a.State.Reg[15] += 2
}

// Thumb_AddSub — THUMB.2.
func Thumb_AddSub(a *ARM7TDMI, instruction uint16) {
	immediate := instruction&(1<<10) != 0
	subtract := instruction&(1<<9) != 0
	field3 := uint32((instruction >> 6) & 7)
	dst := int(instruction & 7)
	src := int((instruction >> 3) & 7)
	var operand uint32
	if immediate {
		operand = field3
	} else {
		operand = a.State.Reg[field3]
	}
	if subtract {
		a.State.Reg[dst] = a.SUB(a.State.Reg[src], operand, true)
	} else {
		a.State.Reg[dst] = a.ADD(a.State.Reg[src], operand, true)
	}
	a.Pipe.Access = bus.AccessCode | bus.AccessSequential
	a.State.Reg[15] += 2
}

// Thumb_Op3 — THUMB.3.
func Thumb_Op3(a *ARM7TDMI, instruction uint16) {
	op := int((instruction >> 11) & 3)
	dst := int((instruction >> 8) & 7)
	imm := uint32(instruction & 0xFF)
	switch op {
	case 0:
		a.State.Reg[dst] = imm
		a.State.CPSR.SetN(0)
		if imm == 0 {
			a.State.CPSR.SetZ(1)
		} else {
			a.State.CPSR.SetZ(0)
		}
	case 1:
		a.SUB(a.State.Reg[dst], imm, true)
	case 2:
		a.State.Reg[dst] = a.ADD(a.State.Reg[dst], imm, true)
	case 3:
		a.State.Reg[dst] = a.SUB(a.State.Reg[dst], imm, true)
	}
	a.Pipe.Access = bus.AccessCode | bus.AccessSequential
	a.State.Reg[15] += 2
}

// Thumb_ALU — THUMB.4.
func Thumb_ALU(a *ARM7TDMI, instruction uint16) {
	op := ThumbDataOp((instruction >> 6) & 0xF)
	dst := int(instruction & 7)
	src := int((instruction >> 3) & 7)

	a.Pipe.Access = bus.AccessCode | bus.AccessSequential
	a.State.Reg[15] += 2

	switch op {
	case T_AND:
		a.State.Reg[dst] &= a.State.Reg[src]
		a.SetZeroAndSignFlag(a.State.Reg[dst])
	case T_EOR:
		a.State.Reg[dst] ^= a.State.Reg[src]
		a.SetZeroAndSignFlag(a.State.Reg[dst])
	case T_LSL:
		shift := a.State.Reg[src]
		a.Bus.Idle()
		a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
		carry := uint32(0)
		if a.State.CPSR.C() {
			carry = 1
		}
		v := a.State.Reg[dst]
		a.LSL(&v, uint8(shift), &carry) // u8 in upstream
		a.State.Reg[dst] = v
		a.SetZeroAndSignFlag(a.State.Reg[dst])
		a.State.CPSR.SetC(carry)
	case T_LSR:
		shift := a.State.Reg[src]
		a.Bus.Idle()
		a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
		carry := uint32(0)
		if a.State.CPSR.C() {
			carry = 1
		}
		v := a.State.Reg[dst]
		a.LSR(&v, uint8(shift), &carry, false)
		a.State.Reg[dst] = v
		a.SetZeroAndSignFlag(a.State.Reg[dst])
		a.State.CPSR.SetC(carry)
	case T_ASR:
		shift := a.State.Reg[src]
		a.Bus.Idle()
		a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
		carry := uint32(0)
		if a.State.CPSR.C() {
			carry = 1
		}
		v := a.State.Reg[dst]
		a.ASR(&v, uint8(shift), &carry, false)
		a.State.Reg[dst] = v
		a.SetZeroAndSignFlag(a.State.Reg[dst])
		a.State.CPSR.SetC(carry)
	case T_ADC:
		a.State.Reg[dst] = a.ADC(a.State.Reg[dst], a.State.Reg[src], true)
	case T_SBC:
		a.State.Reg[dst] = a.SBC(a.State.Reg[dst], a.State.Reg[src], true)
	case T_ROR:
		shift := a.State.Reg[src]
		a.Bus.Idle()
		a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
		carry := uint32(0)
		if a.State.CPSR.C() {
			carry = 1
		}
		v := a.State.Reg[dst]
		a.ROR(&v, uint8(shift), &carry, false)
		a.State.Reg[dst] = v
		a.SetZeroAndSignFlag(a.State.Reg[dst])
		a.State.CPSR.SetC(carry)
	case T_TST:
		a.SetZeroAndSignFlag(a.State.Reg[dst] & a.State.Reg[src])
	case T_NEG:
		a.State.Reg[dst] = a.SUB(0, a.State.Reg[src], true)
	case T_CMP:
		a.SUB(a.State.Reg[dst], a.State.Reg[src], true)
	case T_CMN:
		a.ADD(a.State.Reg[dst], a.State.Reg[src], true)
	case T_ORR:
		a.State.Reg[dst] |= a.State.Reg[src]
		a.SetZeroAndSignFlag(a.State.Reg[dst])
	case T_MUL:
		lhs := a.State.Reg[src]
		rhs := a.State.Reg[dst]
		full := a.TickMultiply(rhs, true)
		a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
		a.State.Reg[dst] = lhs * rhs
		a.SetZeroAndSignFlag(a.State.Reg[dst])
		if full {
			a.State.CPSR.SetC(a.MultiplyCarrySimple(rhs))
		} else {
			a.State.CPSR.SetC(a.MultiplyCarryLo(lhs, rhs, 0))
		}
	case T_BIC:
		a.State.Reg[dst] &^= a.State.Reg[src]
		a.SetZeroAndSignFlag(a.State.Reg[dst])
	case T_MVN:
		a.State.Reg[dst] = ^a.State.Reg[src]
		a.SetZeroAndSignFlag(a.State.Reg[dst])
	}
}

// Thumb_HighRegisterOps_BX — THUMB.5.
func Thumb_HighRegisterOps_BX(a *ARM7TDMI, instruction uint16) {
	op := int((instruction >> 8) & 3)
	high1 := instruction&(1<<7) != 0
	high2 := instruction&(1<<6) != 0
	dst := int(instruction & 7)
	src := int((instruction >> 3) & 7)
	if high1 {
		dst |= 8
	}
	if high2 {
		src |= 8
	}
	operand := a.State.Reg[src]
	if src == 15 {
		operand &= ^uint32(1)
	}
	if op == 3 {
		if operand&1 != 0 {
			a.State.Reg[15] = operand & ^uint32(1)
			a.ReloadPipeline16()
		} else {
			a.State.CPSR.SetThumb(0)
			a.State.Reg[15] = operand
			a.ReloadPipeline32()
		}
	} else if op == 1 {
		a.SUB(a.State.Reg[dst], operand, true)
		a.Pipe.Access = bus.AccessCode | bus.AccessSequential
		a.State.Reg[15] += 2
	} else {
		if op == 0 {
			a.State.Reg[dst] += operand
		}
		if op == 2 {
			a.State.Reg[dst] = operand
		}
		if dst == 15 {
			a.State.Reg[15] &= ^uint32(1)
			a.ReloadPipeline16()
		} else {
			a.Pipe.Access = bus.AccessCode | bus.AccessSequential
			a.State.Reg[15] += 2
		}
	}
}

// Thumb_LoadStoreRelativePC — THUMB.6.
func Thumb_LoadStoreRelativePC(a *ARM7TDMI, instruction uint16) {
	dst := int((instruction >> 8) & 7)
	offset := uint32(instruction & 0xFF)
	address := (a.State.Reg[15] & ^uint32(2)) + (offset << 2)
	a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
	a.State.Reg[15] += 2
	a.State.Reg[dst] = a.ReadWord(address, bus.AccessNonsequential)
	a.Bus.Idle()
}

// Thumb_LoadStoreOffsetReg — THUMB.7.
func Thumb_LoadStoreOffsetReg(a *ARM7TDMI, instruction uint16) {
	op := int((instruction >> 10) & 3)
	off := int((instruction >> 6) & 7)
	dst := int(instruction & 7)
	base := int((instruction >> 3) & 7)
	address := a.State.Reg[base] + a.State.Reg[off]
	a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
	a.State.Reg[15] += 2
	switch op {
	case 0:
		a.WriteWord(address, a.State.Reg[dst], bus.AccessNonsequential)
	case 1:
		a.WriteByte(address, uint8(a.State.Reg[dst]), bus.AccessNonsequential)
	case 2:
		a.State.Reg[dst] = a.ReadWordRotate(address, bus.AccessNonsequential)
		a.Bus.Idle()
	case 3:
		a.State.Reg[dst] = a.ReadByte(address, bus.AccessNonsequential)
		a.Bus.Idle()
	}
}

// Thumb_LoadStoreSigned — THUMB.8.
func Thumb_LoadStoreSigned(a *ARM7TDMI, instruction uint16) {
	op := int((instruction >> 10) & 3)
	off := int((instruction >> 6) & 7)
	dst := int(instruction & 7)
	base := int((instruction >> 3) & 7)
	address := a.State.Reg[base] + a.State.Reg[off]
	a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
	a.State.Reg[15] += 2
	switch op {
	case 0:
		a.WriteHalf(address, uint16(a.State.Reg[dst]), bus.AccessNonsequential)
	case 1:
		a.State.Reg[dst] = a.ReadByteSigned(address, bus.AccessNonsequential)
		a.Bus.Idle()
	case 2:
		a.State.Reg[dst] = a.ReadHalfRotate(address, bus.AccessNonsequential)
		a.Bus.Idle()
	case 3:
		a.State.Reg[dst] = a.ReadHalfSigned(address, bus.AccessNonsequential)
		a.Bus.Idle()
	}
}

// Thumb_LoadStoreOffsetImm — THUMB.9.
func Thumb_LoadStoreOffsetImm(a *ARM7TDMI, instruction uint16) {
	op := int((instruction >> 11) & 3)
	imm := uint32((instruction >> 6) & 0x1F)
	dst := int(instruction & 7)
	base := int((instruction >> 3) & 7)
	a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
	a.State.Reg[15] += 2
	switch op {
	case 0:
		a.WriteWord(a.State.Reg[base]+imm*4, a.State.Reg[dst], bus.AccessNonsequential)
	case 1:
		a.State.Reg[dst] = a.ReadWordRotate(a.State.Reg[base]+imm*4, bus.AccessNonsequential)
		a.Bus.Idle()
	case 2:
		a.WriteByte(a.State.Reg[base]+imm, uint8(a.State.Reg[dst]), bus.AccessNonsequential)
	case 3:
		a.State.Reg[dst] = a.ReadByte(a.State.Reg[base]+imm, bus.AccessNonsequential)
		a.Bus.Idle()
	}
}

// Thumb_LoadStoreHword — THUMB.10.
func Thumb_LoadStoreHword(a *ARM7TDMI, instruction uint16) {
	load := instruction&(1<<11) != 0
	imm := uint32((instruction >> 6) & 0x1F)
	dst := int(instruction & 7)
	base := int((instruction >> 3) & 7)
	address := a.State.Reg[base] + imm*2
	a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
	a.State.Reg[15] += 2
	if load {
		a.State.Reg[dst] = a.ReadHalfRotate(address, bus.AccessNonsequential)
		a.Bus.Idle()
	} else {
		a.WriteHalf(address, uint16(a.State.Reg[dst]), bus.AccessNonsequential)
	}
}

// Thumb_LoadStoreRelativeToSP — THUMB.11.
func Thumb_LoadStoreRelativeToSP(a *ARM7TDMI, instruction uint16) {
	load := instruction&(1<<11) != 0
	dst := int((instruction >> 8) & 7)
	offset := uint32(instruction & 0xFF)
	address := a.State.Reg[13] + offset*4
	a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
	a.State.Reg[15] += 2
	if load {
		a.State.Reg[dst] = a.ReadWordRotate(address, bus.AccessNonsequential)
		a.Bus.Idle()
	} else {
		a.WriteWord(address, a.State.Reg[dst], bus.AccessNonsequential)
	}
}

// Thumb_LoadAddress — THUMB.12.
func Thumb_LoadAddress(a *ARM7TDMI, instruction uint16) {
	stackptr := instruction&(1<<11) != 0
	dst := int((instruction >> 8) & 7)
	offset := uint32(instruction&0xFF) << 2
	if stackptr {
		a.State.Reg[dst] = a.State.Reg[13] + offset
	} else {
		a.State.Reg[dst] = (a.State.Reg[15] & ^uint32(2)) + offset
	}
	a.Pipe.Access = bus.AccessCode | bus.AccessSequential
	a.State.Reg[15] += 2
}

// Thumb_AddOffsetToSP — THUMB.13.
func Thumb_AddOffsetToSP(a *ARM7TDMI, instruction uint16) {
	sub := instruction&(1<<7) != 0
	offset := uint32(instruction&0x7F) * 4
	if sub {
		a.State.Reg[13] -= offset
	} else {
		a.State.Reg[13] += offset
	}
	a.Pipe.Access = bus.AccessCode | bus.AccessSequential
	a.State.Reg[15] += 2
}

// Thumb_PushPop — THUMB.14.
func Thumb_PushPop(a *ARM7TDMI, instruction uint16) {
	pop := instruction&(1<<11) != 0
	rbit := instruction&(1<<8) != 0
	list := int(instruction & 0xFF)

	a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
	a.State.Reg[15] += 2

	if list == 0 && !rbit {
		if pop {
			a.State.Reg[15] = a.ReadWord(a.State.Reg[13], bus.AccessNonsequential)
			a.ReloadPipeline16()
			a.State.Reg[13] += 0x40
		} else {
			a.State.Reg[13] -= 0x40
			a.WriteWord(a.State.Reg[13], a.State.Reg[15], bus.AccessNonsequential)
		}
		return
	}

	address := a.State.Reg[13]
	accessType := bus.AccessNonsequential

	if pop {
		for reg := 0; reg <= 7; reg++ {
			if list&(1<<reg) != 0 {
				a.State.Reg[reg] = a.ReadWord(address, accessType)
				accessType = bus.AccessSequential
				address += 4
			}
		}
		if rbit {
			a.State.Reg[15] = a.ReadWord(address, accessType) & ^uint32(1)
			a.State.Reg[13] = address + 4
			a.Bus.Idle()
			a.ReloadPipeline16()
			return
		}
		a.Bus.Idle()
		a.State.Reg[13] = address
	} else {
		for reg := 0; reg <= 7; reg++ {
			if list&(1<<reg) != 0 {
				address -= 4
			}
		}
		if rbit {
			address -= 4
		}
		a.State.Reg[13] = address
		for reg := 0; reg <= 7; reg++ {
			if list&(1<<reg) != 0 {
				a.WriteWord(address, a.State.Reg[reg], accessType)
				accessType = bus.AccessSequential
				address += 4
			}
		}
		if rbit {
			a.WriteWord(address, a.State.Reg[14], accessType)
		}
	}
}

// Thumb_LoadStoreMultiple — THUMB.15.
func Thumb_LoadStoreMultiple(a *ARM7TDMI, instruction uint16) {
	load := instruction&(1<<11) != 0
	base := int((instruction >> 8) & 7)
	list := int(instruction & 0xFF)

	a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
	a.State.Reg[15] += 2

	if list == 0 {
		if load {
			a.State.Reg[15] = a.ReadWord(a.State.Reg[base], bus.AccessNonsequential)
			a.ReloadPipeline16()
		} else {
			a.WriteWord(a.State.Reg[base], a.State.Reg[15], bus.AccessNonsequential)
		}
		a.State.Reg[base] += 0x40
		return
	}

	if load {
		address := a.State.Reg[base]
		accessType := bus.AccessNonsequential
		for i := 0; i <= 7; i++ {
			if list&(1<<i) != 0 {
				a.State.Reg[i] = a.ReadWord(address, accessType)
				accessType = bus.AccessSequential
				address += 4
			}
		}
		a.Bus.Idle()
		if list&(1<<base) == 0 {
			a.State.Reg[base] = address
		}
	} else {
		count := 0
		first := 0
		for reg := 7; reg >= 0; reg-- {
			if list&(1<<reg) != 0 {
				count++
				first = reg
			}
		}
		address := a.State.Reg[base]
		baseNew := address + uint32(count)*4
		a.WriteWord(address, a.State.Reg[first], bus.AccessNonsequential)
		a.State.Reg[base] = baseNew
		address += 4
		for reg := first + 1; reg <= 7; reg++ {
			if list&(1<<reg) != 0 {
				a.WriteWord(address, a.State.Reg[reg], bus.AccessSequential)
				address += 4
			}
		}
	}
}

// Thumb_ConditionalBranch — THUMB.16.
func Thumb_ConditionalBranch(a *ARM7TDMI, instruction uint16) {
	cond := Condition((instruction >> 8) & 0xF)
	if a.CheckCondition(cond) {
		imm := uint32(instruction & 0xFF)
		if imm&0x80 != 0 {
			imm |= 0xFFFFFF00
		}
		a.State.Reg[15] += imm * 2
		a.ReloadPipeline16()
	} else {
		a.Pipe.Access = bus.AccessCode | bus.AccessSequential
		a.State.Reg[15] += 2
	}
}

// Thumb_SWI — THUMB.17.
func Thumb_SWI(a *ARM7TDMI, instruction uint16) {
	a.State.SPSR[BANK_SVC].V = a.State.CPSR.V
	a.SwitchMode(MODE_SVC)
	a.State.CPSR.SetThumb(0)
	a.State.CPSR.SetMaskIRQ(1)
	a.State.Reg[14] = a.State.Reg[15] - 2
	a.State.Reg[15] = 0x08
	a.ReloadPipeline32()
}

// Thumb_UnconditionalBranch — THUMB.18.
func Thumb_UnconditionalBranch(a *ARM7TDMI, instruction uint16) {
	imm := uint32(instruction&0x3FF) * 2
	if instruction&0x400 != 0 {
		imm |= 0xFFFFF800
	}
	a.State.Reg[15] += imm
	a.ReloadPipeline16()
}

// Thumb_LongBranchLink — THUMB.19.
func Thumb_LongBranchLink(a *ARM7TDMI, instruction uint16) {
	second := instruction&(1<<11) != 0
	imm := uint32(instruction & 0x7FF)
	if !second {
		imm <<= 12
		if imm&0x400000 != 0 {
			imm |= 0xFF800000
		}
		a.State.Reg[14] = a.State.Reg[15] + imm
		a.Pipe.Access = bus.AccessCode | bus.AccessSequential
		a.State.Reg[15] += 2
	} else {
		temp := a.State.Reg[15] - 2
		a.State.Reg[15] = (a.State.Reg[14] + imm*2) & ^uint32(1)
		a.State.Reg[14] = temp | 1
		a.ReloadPipeline16()
	}
}

func Thumb_Undefined(a *ARM7TDMI, instruction uint16) {}
