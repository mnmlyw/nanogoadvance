// handler32.go ⇄ src/nba/src/arm/handlers/handler32.inl
//
// Each handler decodes its template-parameter bits at runtime (Go has no
// templates), but the dispatch logic, register file accesses, pipeline
// updates, and r15 adjustments mirror upstream line-for-line.
package arm

import (
	"github.com/mnmlyw/nanogoadvance/internal/bus"
	"github.com/mnmlyw/nanogoadvance/internal/scheduler"
)

// DataOp ⇄ ARM7TDMI::DataOp.
type DataOp int

const (
	OP_AND DataOp = 0
	OP_EOR DataOp = 1
	OP_SUB DataOp = 2
	OP_RSB DataOp = 3
	OP_ADD DataOp = 4
	OP_ADC DataOp = 5
	OP_SBC DataOp = 6
	OP_RSC DataOp = 7
	OP_TST DataOp = 8
	OP_TEQ DataOp = 9
	OP_CMP DataOp = 10
	OP_CMN DataOp = 11
	OP_ORR DataOp = 12
	OP_MOV DataOp = 13
	OP_BIC DataOp = 14
	OP_MVN DataOp = 15
)

// ARM_DataProcessing ⇄ template ARM_DataProcessing<...>.
func ARM_DataProcessing(a *ARM7TDMI, instruction uint32) {
	immediate := instruction&(1<<25) != 0
	opcode := DataOp((instruction >> 21) & 0xF)
	setFlags := instruction&(1<<20) != 0
	field4 := (instruction >> 4) & 0xF

	shiftType := int((field4 >> 1) & 3)
	shiftImm := (^field4)&1 != 0

	regDst := int((instruction >> 12) & 0xF)
	regOp1 := int((instruction >> 16) & 0xF)
	regOp2 := int(instruction & 0xF)

	carry := uint32(0)
	if a.State.CPSR.C() {
		carry = 1
	}
	var op1, op2 uint32

	a.Pipe.Access = bus.AccessCode | bus.AccessSequential

	if immediate {
		value := instruction & 0xFF
		shift := ((instruction >> 8) & 0xF) * 2
		if shift != 0 {
			carry = (value >> (shift - 1)) & 1
			op2 = (value >> shift) | (value << (32 - shift))
		} else {
			op2 = value
		}
		op1 = a.GetReg(regOp1)
	} else {
		var shift uint32
		if shiftImm {
			shift = (instruction >> 7) & 0x1F
		} else {
			shift = a.GetReg(int((instruction >> 8) & 0xF))
			a.State.Reg[15] += 4
			a.Bus.Idle()
			a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
		}
		op1 = a.GetReg(regOp1)
		op2 = a.GetReg(regOp2)
		a.DoShift(shiftType, &op2, uint8(shift), &carry, shiftImm) // u8 in upstream
	}

	var result uint32

	switch opcode {
	case OP_AND:
		result = op1 & op2
		if setFlags {
			a.SetZeroAndSignFlag(result)
			a.State.CPSR.SetC(carry)
		}
		a.SetReg(regDst, result)
	case OP_EOR:
		result = op1 ^ op2
		if setFlags {
			a.SetZeroAndSignFlag(result)
			a.State.CPSR.SetC(carry)
		}
		a.SetReg(regDst, result)
	case OP_SUB:
		a.SetReg(regDst, a.SUB(op1, op2, setFlags))
	case OP_RSB:
		a.SetReg(regDst, a.SUB(op2, op1, setFlags))
	case OP_ADD:
		a.SetReg(regDst, a.ADD(op1, op2, setFlags))
	case OP_ADC:
		a.SetReg(regDst, a.ADC(op1, op2, setFlags))
	case OP_SBC:
		a.SetReg(regDst, a.SBC(op1, op2, setFlags))
	case OP_RSC:
		a.SetReg(regDst, a.SBC(op2, op1, setFlags))
	case OP_TST:
		a.SetZeroAndSignFlag(op1 & op2)
		a.State.CPSR.SetC(carry)
	case OP_TEQ:
		a.SetZeroAndSignFlag(op1 ^ op2)
		a.State.CPSR.SetC(carry)
	case OP_CMP:
		a.SUB(op1, op2, true)
	case OP_CMN:
		a.ADD(op1, op2, true)
	case OP_ORR:
		result = op1 | op2
		if setFlags {
			a.SetZeroAndSignFlag(result)
			a.State.CPSR.SetC(carry)
		}
		a.SetReg(regDst, result)
	case OP_MOV:
		if setFlags {
			a.SetZeroAndSignFlag(op2)
			a.State.CPSR.SetC(carry)
		}
		a.SetReg(regDst, op2)
	case OP_BIC:
		result = op1 &^ op2
		if setFlags {
			a.SetZeroAndSignFlag(result)
			a.State.CPSR.SetC(carry)
		}
		a.SetReg(regDst, result)
	case OP_MVN:
		result = ^op2
		if setFlags {
			a.SetZeroAndSignFlag(result)
			a.State.CPSR.SetC(carry)
		}
		a.SetReg(regDst, result)
	}

	isTestOp := opcode == OP_TST || opcode == OP_TEQ || opcode == OP_CMP || opcode == OP_CMN

	if regDst == 15 {
		if setFlags {
			spsr := a.GetSPSR()
			a.SwitchMode(spsr.Mode())
			a.State.CPSR.V = spsr.V
		}
		if !isTestOp {
			if a.State.CPSR.Thumb() {
				a.ReloadPipeline16()
			} else {
				a.ReloadPipeline32()
			}
		} else if immediate || shiftImm {
			a.State.Reg[15] += 4
		}
	} else if immediate || shiftImm {
		a.State.Reg[15] += 4
	}
}

// ARM_StatusTransfer ⇄ template ARM_StatusTransfer<immediate, use_spsr, to_status>.
func ARM_StatusTransfer(a *ARM7TDMI, instruction uint32) {
	immediate := instruction&(1<<25) != 0
	useSPSR := instruction&(1<<22) != 0
	toStatus := instruction&(1<<21) != 0

	if toStatus {
		var op uint32
		var mask uint32
		if instruction&(1<<16) != 0 {
			mask |= 0x000000FF
		}
		if instruction&(1<<17) != 0 {
			mask |= 0x0000FF00
		}
		if instruction&(1<<18) != 0 {
			mask |= 0x00FF0000
		}
		if instruction&(1<<19) != 0 {
			mask |= 0xFF000000
		}
		if immediate {
			value := instruction & 0xFF
			shift := ((instruction >> 8) & 0xF) * 2
			op = (value >> shift) | (value << (32 - shift))
		} else {
			op = a.GetReg(int(instruction & 0xF))
		}
		if !useSPSR {
			if a.State.CPSR.Mode() == MODE_USR {
				mask &= 0xFF000000
			}
			if mask&0xFF != 0 {
				op |= 0x00000010
				a.SwitchMode(Mode(op & 0x1F))
			}
			a.State.CPSR.V = (a.State.CPSR.V &^ mask) | (op & mask)
		} else if a.pSpsr != &a.State.CPSR && !a.cpuModeInvalid {
			a.pSpsr.V = (a.GetSPSR().V &^ mask) | (op & mask)
		}
	} else {
		dst := int((instruction >> 12) & 0xF)
		if useSPSR {
			a.SetReg(dst, a.GetSPSR().V)
		} else {
			a.SetReg(dst, a.State.CPSR.V)
		}
	}
	a.Pipe.Access = bus.AccessCode | bus.AccessSequential
	a.State.Reg[15] += 4
}

// ARM_Multiply ⇄ template ARM_Multiply<accumulate, set_flags>.
func ARM_Multiply(a *ARM7TDMI, instruction uint32) {
	accumulate := instruction&(1<<21) != 0
	setFlags := instruction&(1<<20) != 0

	op1 := int(instruction & 0xF)
	op2 := int((instruction >> 8) & 0xF)
	op3 := int((instruction >> 12) & 0xF)
	dst := int((instruction >> 16) & 0xF)

	a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
	a.State.Reg[15] += 4

	lhs := a.GetReg(op1)
	rhs := a.GetReg(op2)
	result := lhs * rhs

	full := a.TickMultiply(rhs, true)

	accum := uint32(0)
	if accumulate {
		accum = a.GetReg(op3)
		result += accum
		a.Bus.Idle()
	}

	if setFlags {
		a.SetZeroAndSignFlag(result)
		if full {
			a.State.CPSR.SetC(a.MultiplyCarrySimple(rhs))
		} else {
			a.State.CPSR.SetC(a.MultiplyCarryLo(lhs, rhs, accum))
		}
	}

	a.SetReg(dst, result)
	if dst == 15 {
		a.ReloadPipeline32()
	}
}

// ARM_MultiplyLong ⇄ template ARM_MultiplyLong<sign_extend, accumulate, set_flags>.
func ARM_MultiplyLong(a *ARM7TDMI, instruction uint32) {
	signExtend := instruction&(1<<22) != 0
	accumulate := instruction&(1<<21) != 0
	setFlags := instruction&(1<<20) != 0

	op1 := int(instruction & 0xF)
	op2 := int((instruction >> 8) & 0xF)
	dstLo := int((instruction >> 12) & 0xF)
	dstHi := int((instruction >> 16) & 0xF)

	a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
	a.State.Reg[15] += 4

	lhs := a.GetReg(op1)
	rhs := a.GetReg(op2)

	var result uint64
	if signExtend {
		result = uint64(int64(int32(lhs)) * int64(int32(rhs)))
	} else {
		result = uint64(lhs) * uint64(rhs)
	}

	full := a.TickMultiply(rhs, signExtend)
	a.Bus.Idle()

	var accumLo, accumHi uint32
	if accumulate {
		accumLo = a.GetReg(dstLo)
		accumHi = a.GetReg(dstHi)
		value := uint64(accumHi)<<32 | uint64(accumLo)
		result += value
		a.Bus.Idle()
	}

	resultHi := uint32(result >> 32)

	if setFlags {
		a.State.CPSR.SetN(resultHi >> 31)
		if result == 0 {
			a.State.CPSR.SetZ(1)
		} else {
			a.State.CPSR.SetZ(0)
		}
		if full {
			a.State.CPSR.SetC(a.MultiplyCarryHi(lhs, rhs, accumHi, signExtend))
		} else {
			a.State.CPSR.SetC(a.MultiplyCarryLo(lhs, rhs, accumLo))
		}
	}

	a.SetReg(dstLo, uint32(result))
	a.SetReg(dstHi, resultHi)
	if dstLo == 15 || dstHi == 15 {
		a.ReloadPipeline32()
	}
}

// ARM_SingleDataSwap ⇄ template ARM_SingleDataSwap<byte>.
func ARM_SingleDataSwap(a *ARM7TDMI, instruction uint32) {
	byteOp := instruction&(1<<22) != 0
	src := int(instruction & 0xF)
	dst := int((instruction >> 12) & 0xF)
	base := int((instruction >> 16) & 0xF)

	var tmp uint32
	a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
	a.State.Reg[15] += 4

	if byteOp {
		tmp = a.ReadByte(a.GetReg(base), bus.AccessNonsequential)
		a.WriteByte(a.GetReg(base), uint8(a.GetReg(src)), bus.AccessNonsequential|bus.AccessLock)
	} else {
		tmp = a.ReadWordRotate(a.GetReg(base), bus.AccessNonsequential)
		a.WriteWord(a.GetReg(base), a.GetReg(src), bus.AccessNonsequential|bus.AccessLock)
	}
	a.Bus.Idle()
	a.SetReg(dst, tmp)
	if dst == 15 {
		a.ReloadPipeline32()
	}
}

// ARM_BranchAndExchange.
func ARM_BranchAndExchange(a *ARM7TDMI, instruction uint32) {
	address := a.GetReg(int(instruction & 0xF))
	if address&1 != 0 {
		a.State.Reg[15] = address & ^uint32(1)
		a.State.CPSR.SetThumb(1)
		a.ReloadPipeline16()
	} else {
		a.State.Reg[15] = address
		a.ReloadPipeline32()
	}
}

// ARM_HalfwordSignedTransfer ⇄ template <pre, add, immediate, writeback, load, opcode>.
func ARM_HalfwordSignedTransfer(a *ARM7TDMI, instruction uint32) {
	pre := instruction&(1<<24) != 0
	add := instruction&(1<<23) != 0
	immediate := instruction&(1<<22) != 0
	writeback := instruction&(1<<21) != 0
	load := instruction&(1<<20) != 0
	opcode := int((instruction >> 5) & 3)

	dst := int((instruction >> 12) & 0xF)
	base := int((instruction >> 16) & 0xF)

	var offset uint32
	address := a.GetReg(base)

	if immediate {
		offset = (instruction & 0xF) | ((instruction >> 4) & 0xF0)
	} else {
		offset = a.GetReg(int(instruction & 0xF))
	}

	a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
	a.State.Reg[15] += 4

	if !add {
		offset = ^offset + 1
	}
	if pre {
		address += offset
	}

	switch opcode {
	case 0:
	case 1:
		if load {
			value := a.ReadHalfRotate(address, bus.AccessNonsequential)
			if writeback || !pre {
				a.SetReg(base, a.GetReg(base)+offset)
			}
			a.Bus.Idle()
			a.SetReg(dst, value)
		} else {
			a.WriteHalf(address, uint16(a.GetReg(dst)), bus.AccessNonsequential)
			if writeback || !pre {
				a.SetReg(base, a.GetReg(base)+offset)
			}
		}
	case 2:
		if load {
			value := a.ReadByteSigned(address, bus.AccessNonsequential)
			if writeback || !pre {
				a.SetReg(base, a.GetReg(base)+offset)
			}
			a.Bus.Idle()
			a.SetReg(dst, value)
		} else {
			a.Bus.Idle()
			if writeback || !pre {
				a.SetReg(base, a.GetReg(base)+offset)
			}
			a.Bus.Idle()
		}
	case 3:
		if load {
			value := a.ReadHalfSigned(address, bus.AccessNonsequential)
			if writeback || !pre {
				a.SetReg(base, a.GetReg(base)+offset)
			}
			a.Bus.Idle()
			a.SetReg(dst, value)
		} else {
			a.Bus.Idle()
			if writeback || !pre {
				a.SetReg(base, a.GetReg(base)+offset)
			}
		}
	}

	if load && dst == 15 {
		a.ReloadPipeline32()
	}
}

// ARM_BranchAndLink ⇄ template <link>.
func ARM_BranchAndLink(a *ARM7TDMI, instruction uint32) {
	link := instruction&(1<<24) != 0
	offset := instruction & 0xFFFFFF
	if offset&0x800000 != 0 {
		offset |= 0xFF000000
	}
	if link {
		a.SetReg(14, a.State.Reg[15]-4)
	}
	a.State.Reg[15] += offset * 4
	a.ReloadPipeline32()
}

// ARM_SingleDataTransfer ⇄ template <immediate, pre, add, byte, writeback, load>.
func ARM_SingleDataTransfer(a *ARM7TDMI, instruction uint32) {
	// Upstream maps: bit 25 = !immediate (register-offset when set).
	immediate := instruction&(1<<25) == 0
	pre := instruction&(1<<24) != 0
	add := instruction&(1<<23) != 0
	byteOp := instruction&(1<<22) != 0
	writeback := instruction&(1<<21) != 0
	load := instruction&(1<<20) != 0

	var offset uint32
	dst := int((instruction >> 12) & 0xF)
	base := int((instruction >> 16) & 0xF)
	address := a.GetReg(base)

	if immediate {
		offset = instruction & 0xFFF
	} else {
		carry := uint32(0)
		if a.State.CPSR.C() {
			carry = 1
		}
		opcode := int((instruction >> 5) & 3)
		amount := uint8((instruction >> 7) & 0x1F)
		offset = a.GetReg(int(instruction & 0xF))
		a.DoShift(opcode, &offset, amount, &carry, true)
	}

	a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
	a.State.Reg[15] += 4

	if !add {
		offset = ^offset + 1
	}
	if pre {
		address += offset
	}

	if load {
		var value uint32
		if byteOp {
			value = a.ReadByte(address, bus.AccessNonsequential)
		} else {
			value = a.ReadWordRotate(address, bus.AccessNonsequential)
		}
		if writeback || !pre {
			a.SetReg(base, a.GetReg(base)+offset)
		}
		a.Bus.Idle()
		a.SetReg(dst, value)
	} else {
		if byteOp {
			a.WriteByte(address, uint8(a.GetReg(dst)), bus.AccessNonsequential)
		} else {
			a.WriteWord(address, a.GetReg(dst), bus.AccessNonsequential)
		}
		if writeback || !pre {
			a.SetReg(base, a.GetReg(base)+offset)
		}
	}
	if load && dst == 15 {
		a.ReloadPipeline32()
	}
}

// ARM_BlockDataTransfer ⇄ template <pre, add, user_mode, writeback, load>.
func ARM_BlockDataTransfer(a *ARM7TDMI, instruction uint32) {
	pre0 := instruction&(1<<24) != 0
	add := instruction&(1<<23) != 0
	userMode := instruction&(1<<22) != 0
	writeback := instruction&(1<<21) != 0
	load := instruction&(1<<20) != 0

	base := int((instruction >> 16) & 0xF)
	list := int(instruction & 0xFFFF)

	mode := a.State.CPSR.Mode()
	transferPC := list&(1<<15) != 0
	first := 0
	bytes := 0
	pre := pre0

	address := a.GetReg(base)

	if list != 0 {
		for i := 15; i >= 0; i-- {
			if list&(1<<i) == 0 {
				continue
			}
			first = i
			bytes += 4
		}
	} else {
		list = 1 << 15
		first = 15
		transferPC = true
		bytes = 64
	}

	switchMode := userMode && (!load || !transferPC) && mode != MODE_USR && mode != MODE_SYS
	if switchMode {
		a.SwitchMode(MODE_USR)
	}

	baseNew := address
	if !add {
		pre = !pre
		address -= uint32(bytes)
		baseNew -= uint32(bytes)
	} else {
		baseNew += uint32(bytes)
	}

	accessType := bus.AccessNonsequential
	a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
	a.State.Reg[15] += 4

	for i := first; i < 16; i++ {
		if list&(1<<i) == 0 {
			continue
		}
		if pre {
			address += 4
		}
		if load {
			value := a.ReadWord(address, accessType)
			if writeback && i == first {
				a.SetReg(base, baseNew)
			}
			a.SetReg(i, value)
		} else {
			a.WriteWord(address, a.GetReg(i), accessType)
			if writeback && i == first {
				a.SetReg(base, baseNew)
			}
		}
		if !pre {
			address += 4
		}
		accessType = bus.AccessSequential
	}

	if load {
		a.Bus.Idle()
		if switchMode {
			a.ldmUsermode = true
			// 2-cycle window where user-bank and original-bank reads
			// alias each other ⇄ Scheduler::EventClass::ARM_ldm_usermode_conflict.
			a.Bus.Scheduler.AddClass(2, scheduler.EventClassARMLDMUsermodeConflict, 0, 0)
		}
		if transferPC {
			if userMode {
				spsr := a.GetSPSR()
				a.SwitchMode(spsr.Mode())
				a.State.CPSR.V = spsr.V
			}
			if a.State.CPSR.Thumb() {
				a.ReloadPipeline16()
			} else {
				a.ReloadPipeline32()
			}
		}
	}

	if switchMode {
		a.SwitchMode(mode)
	}
}

// ARM_Undefined.
func ARM_Undefined(a *ARM7TDMI, instruction uint32) {
	a.State.SPSR[BANK_UND].V = a.State.CPSR.V
	a.SwitchMode(MODE_UND)
	a.State.CPSR.SetMaskIRQ(1)
	a.SetReg(14, a.State.Reg[15]-4)
	a.State.Reg[15] = 0x04
	a.ReloadPipeline32()
}

// ARM_SWI.
func ARM_SWI(a *ARM7TDMI, instruction uint32) {
	a.State.SPSR[BANK_SVC].V = a.State.CPSR.V
	a.SwitchMode(MODE_SVC)
	a.State.CPSR.SetMaskIRQ(1)
	a.SetReg(14, a.State.Reg[15]-4)
	a.State.Reg[15] = 0x08
	a.ReloadPipeline32()
}
