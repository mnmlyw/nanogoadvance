// arm7tdmi.go ⇄ src/nba/src/arm/arm7tdmi.hh
//
// Direct port of the ARM7TDMI struct: registers, mode switching, the
// fetch-decode-execute pipeline, IRQ entry, and the public Run() loop.
package arm

import (
	"github.com/mnmlyw/nanogoadvance/internal/bus"
	"github.com/mnmlyw/nanogoadvance/internal/scheduler"
)

type Handler16 func(a *ARM7TDMI, op uint16)
type Handler32 func(a *ARM7TDMI, op uint32)

// Pipeline mirrors nba's nested `struct Pipeline`.
type Pipeline struct {
	Access bus.Access
	Opcode [2]uint32
}

type ARM7TDMI struct {
	State RegisterFile
	Bus   *bus.Bus

	pSpsr           *StatusRegister // pointer to current SPSR (or to CPSR for USR/SYS)
	ldmUsermode     bool
	cpuModeInvalid  bool
	Pipe            Pipeline
	IrqLine         bool
	latchIrqDisable bool
}

func New(b *bus.Bus) *ARM7TDMI {
	a := &ARM7TDMI{Bus: b}
	// Register the ARM_ldm_usermode_conflict class so the LDM user-mode
	// bus conflict timer survives save/load round trip.
	b.Scheduler.Register(scheduler.EventClassARMLDMUsermodeConflict, func(uint64) {
		a.ldmUsermode = false
	})
	a.Reset()
	return a
}

// SetIRQLine satisfies the irq.CPULine interface. Upstream exposes
// `bool& IRQLine()` which callers assign; SetIRQLine is the Go equivalent.
func (a *ARM7TDMI) SetIRQLine(v bool) { a.IrqLine = v }

// Reset — matches upstream Reset(). Note: opcode[0..1] are pre-loaded with
// NV-conditional NOPs (0xF0000000), so the first two Run() calls fetch the
// real opcodes at PC=0 and PC=4 while executing the NOPs.
func (a *ARM7TDMI) Reset() {
	a.State.Reset()
	a.SwitchMode(a.State.CPSR.Mode())
	a.Pipe.Opcode[0] = 0xF0000000
	a.Pipe.Opcode[1] = 0xF0000000
	a.Pipe.Access = bus.AccessCode | bus.AccessNonsequential
	a.IrqLine = false
	a.latchIrqDisable = a.State.CPSR.MaskIRQ()
	a.ldmUsermode = false
	a.cpuModeInvalid = false
}

// GetRegisterBankByMode ⇄ nba::ARM7TDMI::GetRegisterBankByMode.
func GetRegisterBankByMode(m Mode) Bank {
	switch m {
	case MODE_USR, MODE_SYS:
		return BANK_NONE
	case MODE_FIQ:
		return BANK_FIQ
	case MODE_IRQ:
		return BANK_IRQ
	case MODE_SVC:
		return BANK_SVC
	case MODE_ABT:
		return BANK_ABT
	case MODE_UND:
		return BANK_UND
	}
	return BANK_INVALID
}

// SwitchMode ⇄ nba::ARM7TDMI::SwitchMode. Faithfully copies the banking dance.
func (a *ARM7TDMI) SwitchMode(newMode Mode) {
	oldBank := GetRegisterBankByMode(a.State.CPSR.Mode())
	newBank := GetRegisterBankByMode(newMode)

	a.State.CPSR.SetMode(newMode)

	if newBank != BANK_NONE {
		a.pSpsr = &a.State.SPSR[newBank]
	} else {
		// USR/SYS: SPSR reads return CPSR; writes are no-op (handled in MSR).
		a.pSpsr = &a.State.CPSR
	}

	if oldBank == newBank {
		return
	}

	if oldBank == BANK_FIQ {
		for i := range 5 {
			a.State.Bank[BANK_FIQ][i] = a.State.Reg[8+i]
		}
		for i := range 5 {
			a.State.Reg[8+i] = a.State.Bank[BANK_NONE][i]
		}
	} else if newBank == BANK_FIQ {
		for i := range 5 {
			a.State.Bank[BANK_NONE][i] = a.State.Reg[8+i]
		}
		for i := range 5 {
			a.State.Reg[8+i] = a.State.Bank[BANK_FIQ][i]
		}
	}

	a.State.Bank[oldBank][5] = a.State.Reg[13]
	a.State.Bank[oldBank][6] = a.State.Reg[14]

	if newBank != BANK_INVALID {
		a.State.Reg[13] = a.State.Bank[newBank][5]
		a.State.Reg[14] = a.State.Bank[newBank][6]
		a.cpuModeInvalid = false
	} else {
		for i := range 7 {
			a.State.Reg[8+i] = 0
		}
		a.State.SPSR[BANK_INVALID].V = 0
		a.cpuModeInvalid = true
	}
}

// GetReg ⇄ ARM7TDMI::GetReg (handles LDM-usermode conflict quirk).
func (a *ARM7TDMI) GetReg(id int) uint32 {
	result := a.State.Reg[id]
	if a.ldmUsermode && id >= 8 && id != 15 {
		result |= a.State.Bank[BANK_NONE][id-8]
	}
	return result
}

// SetReg ⇄ ARM7TDMI::SetReg.
func (a *ARM7TDMI) SetReg(id int, value uint32) {
	isBanked := id >= 8 && id != 15
	if a.ldmUsermode && isBanked {
		a.State.Bank[BANK_NONE][id-8] = value
	}
	if !a.cpuModeInvalid || !isBanked {
		a.State.Reg[id] = value
	}
}

// GetSPSR ⇄ ARM7TDMI::GetSPSR.
func (a *ARM7TDMI) GetSPSR() StatusRegister {
	v := a.pSpsr.V | 0x00000010
	if a.ldmUsermode {
		v |= a.State.CPSR.V
	}
	return StatusRegister{V: v}
}

// CheckCondition ⇄ ARM7TDMI::CheckCondition.
func (a *ARM7TDMI) CheckCondition(cond Condition) bool {
	if cond == COND_AL {
		return true
	}
	return sConditionLUT[(int(cond)<<4)|int(a.State.CPSR.V>>28)]
}

// ReloadPipeline16 / ReloadPipeline32 ⇄ ARM7TDMI::ReloadPipeline16/32.
func (a *ARM7TDMI) ReloadPipeline16() {
	a.Pipe.Opcode[0] = uint32(a.Bus.ReadHalf(a.State.Reg[15]+0, bus.AccessCode|bus.AccessNonsequential))
	a.Pipe.Opcode[1] = uint32(a.Bus.ReadHalf(a.State.Reg[15]+2, bus.AccessCode|bus.AccessSequential))
	a.Pipe.Access = bus.AccessCode | bus.AccessSequential
	a.State.Reg[15] += 4
	a.latchIrqDisable = a.State.CPSR.MaskIRQ()
}

func (a *ARM7TDMI) ReloadPipeline32() {
	a.Pipe.Opcode[0] = a.Bus.ReadWord(a.State.Reg[15]+0, bus.AccessCode|bus.AccessNonsequential)
	a.Pipe.Opcode[1] = a.Bus.ReadWord(a.State.Reg[15]+4, bus.AccessCode|bus.AccessSequential)
	a.Pipe.Access = bus.AccessCode | bus.AccessSequential
	a.State.Reg[15] += 8
	a.latchIrqDisable = a.State.CPSR.MaskIRQ()
}

// SignalIRQ ⇄ ARM7TDMI::SignalIRQ.
func (a *ARM7TDMI) SignalIRQ() {
	if a.latchIrqDisable {
		return
	}
	if a.State.CPSR.Thumb() {
		a.Bus.ReadHalf(a.State.Reg[15] & ^uint32(1), a.Pipe.Access)
	} else {
		a.Bus.ReadWord(a.State.Reg[15] & ^uint32(3), a.Pipe.Access)
	}
	a.State.SPSR[BANK_IRQ].V = a.State.CPSR.V
	a.SwitchMode(MODE_IRQ)
	a.State.CPSR.SetMaskIRQ(1)
	if a.State.CPSR.Thumb() {
		a.State.CPSR.SetThumb(0)
		a.SetReg(14, a.State.Reg[15])
	} else {
		a.SetReg(14, a.State.Reg[15]-4)
	}
	a.State.Reg[15] = 0x18
	a.ReloadPipeline32()
}

// Run ⇄ ARM7TDMI::Run — the heart of the interpreter.
func (a *ARM7TDMI) Run() {
	if a.IrqLine {
		a.SignalIRQ()
	}
	instruction := a.Pipe.Opcode[0]
	a.latchIrqDisable = a.State.CPSR.MaskIRQ()
	a.State.Reg[15] &^= 1

	if a.State.CPSR.Thumb() {
		a.Pipe.Opcode[0] = a.Pipe.Opcode[1]
		a.Pipe.Opcode[1] = uint32(a.Bus.ReadHalf(a.State.Reg[15], a.Pipe.Access))
		sOpcodeLUT16[instruction>>6](a, uint16(instruction))
	} else {
		a.Pipe.Opcode[0] = a.Pipe.Opcode[1]
		a.Pipe.Opcode[1] = a.Bus.ReadWord(a.State.Reg[15], a.Pipe.Access)
		if a.CheckCondition(Condition(instruction >> 28)) {
			hash := ((instruction >> 16) & 0xFF0) | ((instruction >> 4) & 0x00F)
			sOpcodeLUT32[hash](a, instruction)
		} else {
			a.Pipe.Access = bus.AccessCode | bus.AccessSequential
			a.State.Reg[15] += 4
		}
	}
}
