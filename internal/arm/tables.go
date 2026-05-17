// tables.go ⇄ src/nba/src/arm/tablegen/{tablegen.cc, gen_arm.hh, gen_thumb.hh}
//
// At init time we walk every possible decode hash and bind the matching
// handler — identical logic to the C++ TableGen, but using Go's runtime
// init function instead of constexpr.
package arm

var (
	sConditionLUT [256]bool
	sOpcodeLUT16  [1024]Handler16
	sOpcodeLUT32  [4096]Handler32
)

func init() {
	buildConditionLUT()
	buildARMLUT()
	buildThumbLUT()
}

// buildConditionLUT ⇄ TableGen::GenerateConditionTable.
func buildConditionLUT() {
	for flags := 0; flags < 16; flags++ {
		n := flags&8 != 0
		z := flags&4 != 0
		c := flags&2 != 0
		v := flags&1 != 0
		sConditionLUT[(int(COND_EQ)<<4)|flags] = z
		sConditionLUT[(int(COND_NE)<<4)|flags] = !z
		sConditionLUT[(int(COND_CS)<<4)|flags] = c
		sConditionLUT[(int(COND_CC)<<4)|flags] = !c
		sConditionLUT[(int(COND_MI)<<4)|flags] = n
		sConditionLUT[(int(COND_PL)<<4)|flags] = !n
		sConditionLUT[(int(COND_VS)<<4)|flags] = v
		sConditionLUT[(int(COND_VC)<<4)|flags] = !v
		sConditionLUT[(int(COND_HI)<<4)|flags] = c && !z
		sConditionLUT[(int(COND_LS)<<4)|flags] = !c || z
		sConditionLUT[(int(COND_GE)<<4)|flags] = n == v
		sConditionLUT[(int(COND_LT)<<4)|flags] = n != v
		sConditionLUT[(int(COND_GT)<<4)|flags] = !(z || (n != v))
		sConditionLUT[(int(COND_LE)<<4)|flags] = z || (n != v)
		sConditionLUT[(int(COND_AL)<<4)|flags] = true
		sConditionLUT[(int(COND_NV)<<4)|flags] = false
	}
}

// buildARMLUT mirrors gen_arm.hh. Hash = bits[27:20]<<4 | bits[7:4].
// We reconstruct a representative instruction word from each hash and run
// the same case analysis upstream uses.
func buildARMLUT() {
	for hash := 0; hash < 4096; hash++ {
		instruction := uint32(((hash & 0xFF0) << 16) | ((hash & 0xF) << 4))
		sOpcodeLUT32[hash] = generateHandlerARM(instruction)
	}
}

func generateHandlerARM(instruction uint32) Handler32 {
	opcode := instruction & 0x0FFFFFFF

	switch opcode >> 26 {
	case 0b00:
		if opcode&(1<<25) != 0 {
			// Data processing / PSR transfer (immediate)
			setFlags := instruction&(1<<20) != 0
			op := (instruction >> 21) & 0xF
			if !setFlags && op >= 0b1000 && op <= 0b1011 {
				return ARM_StatusTransfer
			}
			return ARM_DataProcessing
		}
		if opcode&0xFF000F0 == 0x1200010 {
			return ARM_BranchAndExchange
		}
		if opcode&0x10000F0 == 0x0000090 {
			// Multiply / multiply long
			if opcode&(1<<23) != 0 {
				return ARM_MultiplyLong
			}
			return ARM_Multiply
		}
		if opcode&0x10000F0 == 0x1000090 {
			return ARM_SingleDataSwap
		}
		if opcode&0xF0 == 0xB0 || opcode&0xD0 == 0xD0 {
			return ARM_HalfwordSignedTransfer
		}
		// Data processing / PSR transfer (register)
		setFlags := instruction&(1<<20) != 0
		op := (instruction >> 21) & 0xF
		if !setFlags && op >= 0b1000 && op <= 0b1011 {
			return ARM_StatusTransfer
		}
		return ARM_DataProcessing

	case 0b01:
		if opcode&0x2000010 == 0x2000010 {
			return ARM_Undefined
		}
		return ARM_SingleDataTransfer

	case 0b10:
		if opcode&(1<<25) != 0 {
			return ARM_BranchAndLink
		}
		return ARM_BlockDataTransfer

	case 0b11:
		if opcode&(1<<25) != 0 {
			if opcode&(1<<24) != 0 {
				return ARM_SWI
			}
		}
	}
	return ARM_Undefined
}

// buildThumbLUT mirrors gen_thumb.hh. Hash = bits[15:6] of the 16-bit op.
func buildThumbLUT() {
	for hash := 0; hash < 1024; hash++ {
		instruction := uint16(hash << 6)
		sOpcodeLUT16[hash] = generateHandlerThumb(instruction)
	}
}

func generateHandlerThumb(instruction uint16) Handler16 {
	switch {
	case instruction&0xF800 < 0x1800:
		return Thumb_MoveShiftedRegister
	case instruction&0xF800 == 0x1800:
		return Thumb_AddSub
	case instruction&0xE000 == 0x2000:
		return Thumb_Op3
	case instruction&0xFC00 == 0x4000:
		return Thumb_ALU
	case instruction&0xFC00 == 0x4400:
		return Thumb_HighRegisterOps_BX
	case instruction&0xF800 == 0x4800:
		return Thumb_LoadStoreRelativePC
	case instruction&0xF200 == 0x5000:
		return Thumb_LoadStoreOffsetReg
	case instruction&0xF200 == 0x5200:
		return Thumb_LoadStoreSigned
	case instruction&0xE000 == 0x6000:
		return Thumb_LoadStoreOffsetImm
	case instruction&0xF000 == 0x8000:
		return Thumb_LoadStoreHword
	case instruction&0xF000 == 0x9000:
		return Thumb_LoadStoreRelativeToSP
	case instruction&0xF000 == 0xA000:
		return Thumb_LoadAddress
	case instruction&0xFF00 == 0xB000:
		return Thumb_AddOffsetToSP
	case instruction&0xF600 == 0xB400:
		return Thumb_PushPop
	case instruction&0xF000 == 0xC000:
		return Thumb_LoadStoreMultiple
	case instruction&0xFF00 < 0xDF00:
		return Thumb_ConditionalBranch
	case instruction&0xFF00 == 0xDF00:
		return Thumb_SWI
	case instruction&0xF800 == 0xE000:
		return Thumb_UnconditionalBranch
	case instruction&0xF000 == 0xF000:
		return Thumb_LongBranchLink
	}
	return Thumb_Undefined
}
