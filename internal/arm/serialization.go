// serialization.go ⇄ src/nba/src/arm/serialization.cc
package arm

import (
	"github.com/mnmlyw/nanogoadvance/internal/bus"
	"github.com/mnmlyw/nanogoadvance/internal/savestate"
)

// LoadState ⇄ ARM7TDMI::LoadState.
func (a *ARM7TDMI) LoadState(s *savestate.SaveState) {
	for i := 0; i < 16; i++ {
		a.State.Reg[i] = s.ARM.Regs.GPR[i]
	}
	for i := 0; i < int(BANK_COUNT); i++ {
		for j := 0; j < 7; j++ {
			a.State.Bank[i][j] = s.ARM.Regs.Bank[i][j]
		}
		a.State.SPSR[i].V = s.ARM.Regs.SPSR[i]
	}
	a.State.CPSR.V = s.ARM.Regs.CPSR

	bank := GetRegisterBankByMode(a.State.CPSR.Mode())
	if bank != BANK_NONE {
		a.pSpsr = &a.State.SPSR[bank]
	} else {
		a.pSpsr = &a.State.CPSR
	}

	a.Pipe.Access = bus.Access(s.ARM.Pipe.Access)
	for i := 0; i < 2; i++ {
		a.Pipe.Opcode[i] = s.ARM.Pipe.Opcode[i]
	}
	a.IrqLine = s.ARM.IRQLine

	// Upstream TODO — these flags aren't yet serialized.
	a.ldmUsermode = false
	a.cpuModeInvalid = false
	a.latchIrqDisable = a.State.CPSR.MaskIRQ()
}

// CopyState ⇄ ARM7TDMI::CopyState.
func (a *ARM7TDMI) CopyState(s *savestate.SaveState) {
	for i := 0; i < 16; i++ {
		s.ARM.Regs.GPR[i] = a.State.Reg[i]
	}
	for i := 0; i < int(BANK_COUNT); i++ {
		for j := 0; j < 7; j++ {
			s.ARM.Regs.Bank[i][j] = a.State.Bank[i][j]
		}
		s.ARM.Regs.SPSR[i] = a.State.SPSR[i].V
	}
	s.ARM.Regs.CPSR = a.State.CPSR.V
	s.ARM.Pipe.Access = uint8(a.Pipe.Access)
	for i := 0; i < 2; i++ {
		s.ARM.Pipe.Opcode[i] = a.Pipe.Opcode[i]
	}
	s.ARM.IRQLine = a.IrqLine
}

