// Command smoketest runs a ROM headlessly for N frames and prints CPU state.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime/pprof"

	"github.com/mnmlyw/nanogoadvance/internal/core"
)

func main() {
	biosPath := flag.String("bios", "", "BIOS image")
	frames := flag.Int("frames", 600, "frames to run")
	cpuProfile := flag.String("cpuprofile", "", "write CPU profile to this path")
	flag.Parse()
	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			log.Fatalf("cpuprofile: %v", err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatalf("cpuprofile start: %v", err)
		}
		defer pprof.StopCPUProfile()
	}
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: smoketest [-bios path] [-frames N] game.gba")
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
	rom, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		log.Fatalf("rom: %v", err)
	}
	c.LoadROM(rom)
	c.Reset()

	defer func() {
		if r := recover(); r != nil {
			regs, cp := c.CPU.State.Reg, c.CPU.State.CPSR.V
			fmt.Printf("PANIC: %v\npc=%08x cpsr=%08x\n", r, regs[15], cp)
			os.Exit(1)
		}
	}()

	for i := 0; i < *frames; i++ {
		c.RunFrame()
	}
	regs, cp := c.CPU.State.Reg, c.CPU.State.CPSR.V
	fmt.Printf("ran %d frames OK\npc=%08x cpsr=%08x r0=%08x r1=%08x\nDISPCNT=%04x VCOUNT=%d\n",
		*frames, regs[15], cp, regs[0], regs[1],
		c.PPU.DISPCNT.Hword, c.PPU.VCOUNT)

	// Read the most recently *completed* frame (frontend convention).
	completed := c.PPU.Output[c.PPU.Frame^1]
	var nonzero int
	var hash uint64 = 1099511628211
	for _, p := range completed {
		if p&0x00FFFFFF != 0 {
			nonzero++
		}
		hash = (hash * 1099511628211) ^ uint64(p)
	}
	fmt.Printf("completed frame: nonzero=%d/%d hash=%016x  (Frame=%d)\n",
		nonzero, 240*160, hash, c.PPU.Frame)

	p := c.PPU
	fmt.Printf("BG0CNT=%04x BG1CNT=%04x BG2CNT=%04x BG3CNT=%04x\n",
		p.BGCNT[0].ReadHalf(), p.BGCNT[1].ReadHalf(), p.BGCNT[2].ReadHalf(), p.BGCNT[3].ReadHalf())
	fmt.Printf("WININ=%04x WINOUT=%04x BLDCNT=%04x EVA=%d EVB=%d EVY=%d\n",
		p.WININ.ReadHalf(), p.WINOUT.ReadHalf(), p.BLDCNT.ReadHalf(),
		p.BLDALPHA.EVA, p.BLDALPHA.EVB, p.BLDY)
	fmt.Printf("Palette[0..3] = %02x%02x %02x%02x\n",
		p.Palette[1], p.Palette[0], p.Palette[3], p.Palette[2])
	// Dump BG buffer middle row + non-zero counts per BG
	var bgNonZero [4]int
	for x := range 240 {
		for bg := range 4 {
			if p.BG.Buffer[x][bg] != 0 {
				bgNonZero[bg]++
			}
		}
	}
	fmt.Printf("BG.Buffer non-zero by layer (last scanline): %v\n", bgNonZero)
	fmt.Printf("DISPCNT.Enable=%v ForcedBlank=%d\n", p.DISPCNT.Enable, p.DISPCNT.ForcedBlank)
}
