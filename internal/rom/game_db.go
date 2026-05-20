// game_db.go ⇄ src/platform/core/src/game_db.cc + game_db.hh
//
// Per-game backup type + GPIO config table, adapted from VBA-M's vba-over.ini
// the same way upstream does.
package rom

// GPIODeviceType ⇄ enum class GPIODeviceType in game_db.hh.
type GPIODeviceType int

const (
	GPIONone        GPIODeviceType = 0
	GPIORTC         GPIODeviceType = 1
	GPIOSolarSensor GPIODeviceType = 2
)

// GameInfo ⇄ struct GameInfo.
type GameInfo struct {
	BackupType BackupType
	GPIO       GPIODeviceType
	Mirror     bool
}

// GameDB ⇄ g_game_db. Keys are 4-char game codes (header bytes 0xAC..0xAF).
var GameDB = map[string]GameInfo{
	"ALFP": {BackupEEPROM64, GPIONone, false},                  // Dragon Ball Z - The Legacy of Goku II (Europe)
	"ALGP": {BackupEEPROM64, GPIONone, false},                  // Dragon Ball Z - The Legacy of Goku (Europe)
	"AROP": {BackupEEPROM64, GPIONone, false},                  // Rocky (Europe)
	"AR8e": {BackupEEPROM64, GPIONone, false},                  // Rocky (USA)
	"AXVE": {BackupFlash128, GPIORTC, false},                   // Pokemon Ruby (USA, Europe)
	"AXPE": {BackupFlash128, GPIORTC, false},                   // Pokemon Sapphire (USA, Europe)
	"AX4P": {BackupFlash128, GPIONone, false},                  // Super Mario Advance 4 (Europe)
	"A2YE": {BackupNone, GPIONone, false},                      // Top Gun - Combat Zones (USA)
	"BDBP": {BackupEEPROM64, GPIONone, false},                  // Dragon Ball Z - Taiketsu (Europe)
	"BM5P": {BackupFlash64, GPIONone, false},                   // Mario vs. Donkey Kong (Europe)
	"BPEE": {BackupFlash128, GPIORTC, false},                   // Pokemon Emerald (USA, Europe)
	"BY6P": {BackupSRAM, GPIONone, false},                      // Yu-Gi-Oh! Ultimate Masters 2006 (Europe)
	"B24E": {BackupFlash128, GPIONone, false},                  // Pokemon Mystery Dungeon - Red Rescue Team (USA, Australia)
	"FADE": {BackupEEPROM4, GPIONone, true},                    // Classic NES - Castlevania
	"FBME": {BackupEEPROM4, GPIONone, true},                    // Classic NES - Bomberman
	"FDKE": {BackupEEPROM4, GPIONone, true},                    // Classic NES - Donkey Kong
	"FDME": {BackupEEPROM4, GPIONone, true},                    // Classic NES - Dr. Mario
	"FEBE": {BackupEEPROM64, GPIONone, true},                   // Classic NES - Excitebike
	"FICE": {BackupEEPROM4, GPIONone, true},                    // Classic NES - Ice Climber
	"FLBE": {BackupEEPROM64, GPIONone, true},                   // Classic NES - Zelda II
	"FMRE": {BackupEEPROM4, GPIONone, true},                    // Classic NES - Metroid
	"FP7E": {BackupEEPROM4, GPIONone, true},                    // Classic NES - Pac-Man
	"FSME": {BackupEEPROM4, GPIONone, true},                    // Classic NES - Super Mario Bros.
	"FXVE": {BackupEEPROM4, GPIONone, true},                    // Classic NES - Xevious
	"FZLE": {BackupEEPROM64, GPIONone, true},                   // Classic NES - Legend of Zelda
	"KYGP": {BackupEEPROM64, GPIONone, false},                  // Yoshi's Universal Gravitation (Europe)
	"U3IP": {BackupDetect, GPIORTC | GPIOSolarSensor, false},   // Boktai (Europe)
	"U32P": {BackupDetect, GPIORTC | GPIOSolarSensor, false},   // Boktai 2 (Europe)
	"AGFE": {BackupFlash64, GPIORTC, false},                    // Golden Sun - The Lost Age (USA)
	"AGSE": {BackupFlash64, GPIORTC, false},                    // Golden Sun (USA)
	"ALFE": {BackupEEPROM64, GPIONone, false},                  // DBZ Legacy of Goku II (USA)
	"ALGE": {BackupEEPROM64, GPIONone, false},                  // DBZ Legacy of Goku (USA)
	"AX4E": {BackupFlash128, GPIONone, false},                  // Super Mario Advance 4 v1.1 (USA)
	"BDBE": {BackupEEPROM64, GPIONone, false},                  // DBZ Taiketsu (USA)
	"BG3E": {BackupEEPROM64, GPIONone, false},                  // DBZ Buu's Fury (USA)
	"BLFE": {BackupEEPROM64, GPIONone, false},                  // 2 Games in 1 - DBZ I&II (USA)
	"BPRE": {BackupFlash128, GPIONone, false},                  // Pokemon FireRed (USA, Europe)
	"BPGE": {BackupFlash128, GPIONone, false},                  // Pokemon LeafGreen (USA, Europe)
	"BT4E": {BackupEEPROM64, GPIONone, false},                  // DBGT Transformation (USA)
	"BUFE": {BackupEEPROM64, GPIONone, false},                  // 2 in 1 - DBZ Buu's Fury + GT (USA)
	"BYGE": {BackupSRAM, GPIONone, false},                      // Yu-Gi-Oh! GX - Duel Academy (USA)
	"KYGE": {BackupEEPROM64, GPIONone, false},                  // Yoshi - Topsy-Turvy (USA)
	"PSAE": {BackupFlash128, GPIONone, false},                  // e-Reader (USA)
	"U3IE": {BackupDetect, GPIORTC | GPIOSolarSensor, false},   // Boktai (USA)
	"U32E": {BackupDetect, GPIORTC | GPIOSolarSensor, false},   // Boktai 2 (USA)
	"ALFJ": {BackupEEPROM64, GPIONone, false},                  // DBZ Legacy of Goku II Intl (Japan)
	"AXPJ": {BackupFlash128, GPIORTC, false},                   // Pokemon Sapphire (Japan)
	"AXVJ": {BackupFlash128, GPIORTC, false},                   // Pokemon Ruby (Japan)
	"AX4J": {BackupFlash128, GPIONone, false},                  // Super Mario Advance 4 (Japan)
	"BFTJ": {BackupFlash128, GPIONone, false},                  // F-Zero Climax (Japan)
	"BGWJ": {BackupFlash128, GPIONone, false},                  // Game Boy Wars Advance 1+2 (Japan)
	"BKAJ": {BackupFlash128, GPIORTC, false},                   // Sennen Kazoku (Japan)
	"BPEJ": {BackupFlash128, GPIORTC, false},                   // Pokemon Emerald (Japan)
	"BPGJ": {BackupFlash128, GPIONone, false},                  // Pokemon LeafGreen (Japan)
	"BPRJ": {BackupFlash128, GPIONone, false},                  // Pokemon FireRed (Japan)
	"BDKJ": {BackupEEPROM64, GPIONone, false},                  // Digi Communication 2 (Japan)
	"BR4J": {BackupDetect, GPIORTC, false},                     // Rockman EXE 4.5 (Japan)
	"FSRJ": {BackupEEPROM64, GPIONone, true},                   // Famicom Mini Dai-2-ji Super Robot Taisen
	"FGZJ": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini Z Gundam
	"FMBJ": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini 01 - Super Mario Bros.
	"FCLJ": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini 12 - Clu Clu Land
	"FBFJ": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini 13 - Balloon Fight
	"FWCJ": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini 14 - Wrecking Crew
	"FDMJ": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini 15 - Dr. Mario
	"FDDJ": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini 16 - Dig Dug
	"FTBJ": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini 17 - Adventure Island
	"FMKJ": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini 18 - Makaimura
	"FTWJ": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini 19 - Twin Bee
	"FGGJ": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini 20 - Ganbare Goemon
	"FM2J": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini 21 - Super Mario Bros. 2
	"FNMJ": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini 22 - Nazo no Murasame Jou
	"FMRJ": {BackupEEPROM64, GPIONone, true},                   // Famicom Mini 23 - Metroid
	"FPTJ": {BackupEEPROM64, GPIONone, true},                   // Famicom Mini 24 - Palthena
	"FLBJ": {BackupEEPROM64, GPIONone, true},                   // Famicom Mini 25 - Zelda 2
	"FFMJ": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini 26
	"FTKJ": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini 27
	"FTUJ": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini 28
	"FADJ": {BackupEEPROM4, GPIONone, true},                    // Famicom Mini 29 - Akumajou Dracula
	"FSDJ": {BackupEEPROM64, GPIONone, true},                   // Famicom Mini 30 - SD Gundam
	"KHPJ": {BackupEEPROM64, GPIONone, false},                  // Koro Koro Puzzle Happy Panechu! (Japan)
	"KYGJ": {BackupEEPROM64, GPIONone, false},                  // Yoshi no Banyuuinryoku (Japan)
	"PSAJ": {BackupFlash128, GPIONone, false},                  // Card e-Reader+ (Japan)
	"U3IJ": {BackupDetect, GPIORTC | GPIOSolarSensor, false},   // Bokura no Taiyou (Japan)
	"U32J": {BackupDetect, GPIORTC | GPIOSolarSensor, false},   // Zoku Bokura no Taiyou (Japan)
	"U33J": {BackupDetect, GPIORTC | GPIOSolarSensor, false},   // Shin Bokura no Taiyou (Japan)
	"AXPF": {BackupFlash128, GPIORTC, false},                   // Pokemon Saphir (France)
	"AXVF": {BackupFlash128, GPIORTC, false},                   // Pokemon Rubis (France)
	"BPEF": {BackupFlash128, GPIORTC, false},                   // Pokemon Emeraude (France)
	"BPGF": {BackupFlash128, GPIONone, false},                  // Pokemon Vert Feuille (France)
	"BPRF": {BackupFlash128, GPIONone, false},                  // Pokemon Rouge Feu (France)
	"AXPI": {BackupFlash128, GPIORTC, false},                   // Pokemon Zaffiro (Italy)
	"AXVI": {BackupFlash128, GPIORTC, false},                   // Pokemon Rubino (Italy)
	"BPEI": {BackupFlash128, GPIORTC, false},                   // Pokemon Smeraldo (Italy)
	"BPGI": {BackupFlash128, GPIONone, false},                  // Pokemon Verde Foglia (Italy)
	"BPRI": {BackupFlash128, GPIONone, false},                  // Pokemon Rosso Fuoco (Italy)
	"AXPD": {BackupFlash128, GPIORTC, false},                   // Pokemon Saphir (Germany)
	"AXVD": {BackupFlash128, GPIORTC, false},                   // Pokemon Rubin (Germany)
	"BPED": {BackupFlash128, GPIORTC, false},                   // Pokemon Smaragd (Germany)
	"BPGD": {BackupFlash128, GPIONone, false},                  // Pokemon Blattgruene (Germany)
	"BPRD": {BackupFlash128, GPIONone, false},                  // Pokemon Feuerrote (Germany)
	"AXPS": {BackupFlash128, GPIORTC, false},                   // Pokemon Zafiro (Spain)
	"AXVS": {BackupFlash128, GPIORTC, false},                   // Pokemon Rubi (Spain)
	"BPES": {BackupFlash128, GPIORTC, false},                   // Pokemon Esmeralda (Spain)
	"BPGS": {BackupFlash128, GPIONone, false},                  // Pokemon Verde Hoja (Spain)
	"BPRS": {BackupFlash128, GPIONone, false},                  // Pokemon Rojo Fuego (Spain)
	"A9DP": {BackupEEPROM4, GPIONone, false},                   // DOOM II
	"AAOJ": {BackupEEPROM4, GPIONone, false},                   // Acrobat Kid (Japan)
	"BGDP": {BackupEEPROM4, GPIONone, false},                   // Baldur's Gate Dark Alliance (Europe)
	"BGDE": {BackupEEPROM4, GPIONone, false},                   // Baldur's Gate Dark Alliance (USA)
	"BJBE": {BackupEEPROM4, GPIONone, false},                   // 007 Everything or Nothing (USA/Europe)
	"BJBJ": {BackupEEPROM4, GPIONone, false},                   // 007 Everything or Nothing (Japan)
	"ALUP": {BackupEEPROM4, GPIONone, false},                   // Super Monkey Ball Jr. (Europe)
	"ALUE": {BackupEEPROM4, GPIONone, false},                   // Super Monkey Ball Jr. (USA)
	"BL8E": {BackupEEPROM4, GPIONone, false},                   // Tomb Raider - Legend
}

// LookupGameDB returns the GameInfo for a cart, or a zero value (BackupDetect,
// GPIONone, mirror=false) when no match exists. The 4-char game code lives
// at offset 0xAC of the cart header.
func LookupGameDB(data []byte) GameInfo {
	if len(data) < 0xB0 {
		return GameInfo{BackupType: BackupDetect}
	}
	// Direct string([]byte) in a map index elides the allocation in Go.
	if info, ok := GameDB[string(data[0xAC:0xB0])]; ok {
		return info
	}
	return GameInfo{BackupType: BackupDetect}
}
