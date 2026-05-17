// Command nanogoadvance is the entry point.
//
//	nanogoadvance [-bios path/to/gba_bios.bin] path/to/game.gba
//
// Without -bios, the core uses a "skip BIOS" startup that pre-initializes
// SP banks and jumps to the cart entry — works for the jsmolka test ROMs
// but most retail games will crash on the first BIOS SWI.
//
// Save data is persisted to a .sav file alongside the ROM by default
// (use -save '' to disable).
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/mnmlyw/nanogoadvance/internal/core"
	"github.com/mnmlyw/nanogoadvance/internal/platform"
)

func main() {
	biosPath := flag.String("bios", "", "GBA BIOS image (skip-BIOS init if empty)")
	savePath := flag.String("save", "", "save file path (defaults to ROM with .sav extension)")
	mp2kHLE := flag.Bool("mp2k-hle", false, "enable MP2K audio HLE (recommended for most retail games)")
	loadState := flag.String("load-state", "", "load a save state from this path at startup")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: nanogoadvance [-bios path] [-save path] game.gba")
		os.Exit(2)
	}

	c := core.New()
	if *biosPath != "" {
		data, err := os.ReadFile(*biosPath)
		if err != nil {
			log.Fatalf("bios: %v", err)
		}
		c.LoadBIOS(data)
	}

	romPath := flag.Arg(0)
	rom, err := os.ReadFile(romPath)
	if err != nil {
		log.Fatalf("rom: %v", err)
	}

	// Derive the .sav path the same way upstream's ROMLoader does:
	// replace the extension on the ROM path.
	resolvedSave := *savePath
	if resolvedSave == "" && romPath != "" {
		if ext := strings.LastIndex(romPath, "."); ext > 0 {
			resolvedSave = romPath[:ext] + ".sav"
		} else {
			resolvedSave = romPath + ".sav"
		}
	}
	c.LoadROMWithSave(rom, resolvedSave)
	cfg := core.DefaultConfig()
	cfg.Audio.MP2KHLEEnable = *mp2kHLE
	c.ApplyConfig(cfg)
	c.Reset()
	if *loadState != "" {
		if err := c.LoadStateFromFile(*loadState); err != nil {
			log.Fatalf("load-state %s: %v", *loadState, err)
		}
	}

	if err := platform.New(c).Run(); err != nil {
		log.Fatalf("frontend: %v", err)
	}
}
