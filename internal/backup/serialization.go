// serialization.go ⇄ src/nba/src/hw/rom/backup/serialization.cc
package backup

import "github.com/mnmlyw/nanogoadvance/internal/savestate"

func (s *SRAM) LoadState(st *savestate.SaveState) {
	for i := 0; i < s.file.Size(); i++ {
		s.file.memory[i] = st.Backup.Data[i]
	}
	if s.file.AutoUpdate {
		s.file.Update(0, s.file.Size())
	}
}

func (s *SRAM) CopyState(st *savestate.SaveState) {
	for i := 0; i < s.file.Size(); i++ {
		st.Backup.Data[i] = s.file.memory[i]
	}
}

func (f *FLASH) LoadState(st *savestate.SaveState) {
	f.currentBank = int(st.Backup.FLASH.CurrentBank)
	f.phase = int(st.Backup.FLASH.Phase)
	f.enableChipID = st.Backup.FLASH.EnableChipID
	f.enableErase = st.Backup.FLASH.EnableErase
	f.enableWrite = st.Backup.FLASH.EnableWrite
	f.enableSelect = st.Backup.FLASH.EnableSelect

	for i := 0; i < f.file.Size(); i++ {
		f.file.memory[i] = st.Backup.Data[i]
	}
	if f.file.AutoUpdate {
		f.file.Update(0, f.file.Size())
	}
}

func (f *FLASH) CopyState(st *savestate.SaveState) {
	st.Backup.FLASH.CurrentBank = uint8(f.currentBank)
	st.Backup.FLASH.Phase = uint8(f.phase)
	st.Backup.FLASH.EnableChipID = f.enableChipID
	st.Backup.FLASH.EnableErase = f.enableErase
	st.Backup.FLASH.EnableWrite = f.enableWrite
	st.Backup.FLASH.EnableSelect = f.enableSelect

	for i := 0; i < f.file.Size(); i++ {
		st.Backup.Data[i] = f.file.memory[i]
	}
}

func (e *EEPROM) LoadState(st *savestate.SaveState) {
	e.state = int(st.Backup.EEPROM.State)
	e.address = int(st.Backup.EEPROM.Address)
	e.serialBuffer = st.Backup.EEPROM.SerialBuffer
	e.transmittedBits = int(st.Backup.EEPROM.TransmittedBits)

	for i := 0; i < e.file.Size(); i++ {
		e.file.memory[i] = st.Backup.Data[i]
	}
	if e.file.AutoUpdate {
		e.file.Update(0, e.file.Size())
	}
}

func (e *EEPROM) CopyState(st *savestate.SaveState) {
	st.Backup.EEPROM.State = uint16(e.state)
	st.Backup.EEPROM.Address = uint16(e.address)
	st.Backup.EEPROM.SerialBuffer = e.serialBuffer
	st.Backup.EEPROM.TransmittedBits = uint8(e.transmittedBits)

	for i := 0; i < e.file.Size(); i++ {
		st.Backup.Data[i] = e.file.memory[i]
	}
}
