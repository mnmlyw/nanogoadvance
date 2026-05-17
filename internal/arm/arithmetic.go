// arithmetic.go ⇄ src/nba/src/arm/handlers/arithmetic.inl
//
// Direct port: SetZeroAndSignFlag, ADD/ADC/SUB/SBC, DoShift / LSL / LSR /
// ASR / ROR, plus the multiply-cycle and multiply-carry helpers (the latter
// reproduce the booth-encoded carry behavior of the ARM7TDMI).
package arm

import "github.com/mnmlyw/nanogoadvance/internal/bus"

func (a *ARM7TDMI) SetZeroAndSignFlag(value uint32) {
	a.State.CPSR.SetN(value >> 31)
	if value == 0 {
		a.State.CPSR.SetZ(1)
	} else {
		a.State.CPSR.SetZ(0)
	}
}

// TickMultiply mirrors the upstream template. `signed` corresponds to the
// `is_signed` template parameter (default true).
func (a *ARM7TDMI) TickMultiply(multiplier uint32, signed_ bool) bool {
	mask := uint32(0xFFFFFF00)
	a.Bus.Idle()
	for {
		m := multiplier & mask
		if m == 0 {
			break
		}
		if signed_ && m == mask {
			break
		}
		mask <<= 8
		a.Bus.Idle()
	}
	return mask == 0
}

func (a *ARM7TDMI) MultiplyCarrySimple(multiplier uint32) uint32 {
	if (multiplier >> 30) == 2 {
		return 1
	}
	return 0
}

func (a *ARM7TDMI) MultiplyCarryLo(multiplicand, multiplier, accum uint32) uint32 {
	multiplicand |= 1
	booth := uint32(int32(multiplier<<31) >> 31)
	carry := multiplicand * booth
	sum := carry + accum
	shift := 29
	for {
		for range 4 {
			nextBooth := uint32(int32(multiplier<<uint32(shift)) >> uint32(shift))
			factor := nextBooth - booth
			booth = nextBooth
			addend := multiplicand * factor
			accum ^= carry ^ addend
			sum += addend
			carry = sum - accum
			shift -= 2
		}
		if booth == multiplier {
			break
		}
	}
	return carry >> 31
}

func (a *ARM7TDMI) MultiplyCarryHi(multiplicand, multiplier, accumHi uint32, signExtend bool) uint32 {
	if signExtend {
		multiplicand = uint32(int32(multiplicand) >> 6)
		multiplier = uint32(int32(multiplier) >> 26)
	} else {
		multiplicand >>= 6
		multiplier >>= 26
	}
	multiplicand |= 1
	carry := (^accumHi) & 0x20000000
	accum := accumHi - 0x08000000
	booth0 := uint32(int32(multiplier<<27) >> 27)
	booth1 := uint32(int32(multiplier<<29) >> 29)
	booth2 := uint32(int32(multiplier<<31) >> 31)
	factor0 := multiplier - booth0
	factor1 := booth0 - booth1
	factor2 := booth1 - booth2
	addend := multiplicand * factor2
	accum -= addend & 0x10000000
	addend = multiplicand * factor1
	accum -= addend & 0x40000000
	sum := accum + (addend & 0x20000000)
	accum -= carry
	addend = multiplicand * factor0
	sum += addend & 0x40000000
	return (sum ^ accum) >> 31
}

func (a *ARM7TDMI) ADD(op1, op2 uint32, setFlags bool) uint32 {
	result := op1 + op2
	if setFlags {
		a.SetZeroAndSignFlag(result)
		if result < op1 {
			a.State.CPSR.SetC(1)
		} else {
			a.State.CPSR.SetC(0)
		}
		a.State.CPSR.SetV(((^(op1 ^ op2)) & (op2 ^ result)) >> 31)
	}
	return result
}

func (a *ARM7TDMI) ADC(op1, op2 uint32, setFlags bool) uint32 {
	c := uint32(0)
	if a.State.CPSR.C() {
		c = 1
	}
	if setFlags {
		result64 := uint64(op1) + uint64(op2) + uint64(c)
		result32 := uint32(result64)
		a.SetZeroAndSignFlag(result32)
		a.State.CPSR.SetC(uint32(result64 >> 32))
		a.State.CPSR.SetV(((^(op1 ^ op2)) & (op2 ^ result32)) >> 31)
		return result32
	}
	return op1 + op2 + c
}

func (a *ARM7TDMI) SUB(op1, op2 uint32, setFlags bool) uint32 {
	result := op1 - op2
	if setFlags {
		a.SetZeroAndSignFlag(result)
		if op1 >= op2 {
			a.State.CPSR.SetC(1)
		} else {
			a.State.CPSR.SetC(0)
		}
		a.State.CPSR.SetV(((op1 ^ op2) & (op1 ^ result)) >> 31)
	}
	return result
}

func (a *ARM7TDMI) SBC(op1, op2 uint32, setFlags bool) uint32 {
	op3 := uint32(0)
	if !a.State.CPSR.C() {
		op3 = 1
	}
	result := op1 - op2 - op3
	if setFlags {
		a.SetZeroAndSignFlag(result)
		if uint64(op1) >= uint64(op2)+uint64(op3) {
			a.State.CPSR.SetC(1)
		} else {
			a.State.CPSR.SetC(0)
		}
		a.State.CPSR.SetV(((op1 ^ op2) & (op1 ^ result)) >> 31)
	}
	return result
}

// DoShift mirrors the C++ `void DoShift(int, u32&, u8, int&, bool)`. The
// u8 parameter type is load-bearing: callers in the register-shift path pass
// a full 32-bit register value and rely on the implicit u32→u8 truncation
// to use only the low byte. Keep this signature in sync with upstream.
func (a *ARM7TDMI) DoShift(opcode int, operand *uint32, amount uint8, carry *uint32, immediate bool) {
	switch opcode {
	case 0:
		a.LSL(operand, amount, carry)
	case 1:
		a.LSR(operand, amount, carry, immediate)
	case 2:
		a.ASR(operand, amount, carry, immediate)
	case 3:
		a.ROR(operand, amount, carry, immediate)
	}
}

func (a *ARM7TDMI) LSL(operand *uint32, amount uint8, carry *uint32) {
	adj := min(int(amount), 33)
	result := uint32(uint64(*operand) << adj)
	if adj != 0 {
		*carry = uint32(uint64(*operand)<<(adj-1)) >> 31
	}
	*operand = result
}

func (a *ARM7TDMI) LSR(operand *uint32, amount uint8, carry *uint32, immediate bool) {
	if immediate && amount == 0 {
		amount = 32
	}
	adj := min(int(amount), 33)
	result := uint32(uint64(*operand) >> adj)
	if adj != 0 {
		*carry = uint32((uint64(*operand) >> (adj - 1)) & 1)
	}
	*operand = result
}

func (a *ARM7TDMI) ASR(operand *uint32, amount uint8, carry *uint32, immediate bool) {
	if immediate && amount == 0 {
		amount = 32
	}
	adj := min(int(amount), 33)
	result := uint32(int64(int32(*operand)) >> adj)
	if adj != 0 {
		*carry = uint32((int64(int32(*operand)) >> (adj - 1)) & 1)
	}
	*operand = result
}

func (a *ARM7TDMI) ROR(operand *uint32, amount uint8, carry *uint32, immediate bool) {
	if immediate && amount == 0 {
		lsb := *operand & 1
		*operand = (*operand >> 1) | (*carry << 31)
		*carry = lsb
		return
	}
	if amount == 0 {
		return
	}
	adj := amount & 31
	*operand = (*operand >> adj) | (*operand << ((32 - adj) & 31))
	*carry = *operand >> 31
}

// Compile-time use of bus pkg so the import isn't elided when other files
// don't reference it.
var _ = bus.AccessNonsequential
