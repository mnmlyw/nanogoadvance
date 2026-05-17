// Package rom — port of the bits of src/nba/include/nba/rom/* and the ROM
// loader in src/platform/core/src/loader/rom.cc that the emulator core
// actually needs at runtime: backup-type magic-string detection.
package rom

import "bytes"

// BackupType mirrors nba's BackupType enum.
type BackupType int

const (
	BackupNone     BackupType = iota
	BackupDetect
	BackupSRAM
	BackupFlash64
	BackupFlash128
	BackupEEPROM4
	BackupEEPROM64
	BackupEEPROMDetect
)

// DetectBackupType scans the ROM for one of the standard SDK signature
// strings — replicated verbatim from rom.cc.
var detectSignatures = []struct {
	tag  string
	kind BackupType
}{
	{"EEPROM_V", BackupEEPROMDetect},
	{"SRAM_V", BackupSRAM},
	{"SRAM_F_V", BackupSRAM},
	{"FLASH_V", BackupFlash64},
	{"FLASH512_V", BackupFlash64},
	{"FLASH1M_V", BackupFlash128},
}

func DetectBackupType(data []byte) BackupType {
	// Upstream scans aligned 4-byte boundaries — we match that.
	for i := 0; i+1 < len(data); i += 4 {
		for _, sig := range detectSignatures {
			if i+len(sig.tag) <= len(data) && bytes.Equal(data[i:i+len(sig.tag)], []byte(sig.tag)) {
				return sig.kind
			}
		}
	}
	return BackupDetect
}
