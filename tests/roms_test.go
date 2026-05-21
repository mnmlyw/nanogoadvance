// Package tests runs real GBA test ROMs against the emulator and asserts the
// pass signal each ROM exposes. These are the only correctness gates that
// matter — go vet / unit tests can be green while the CPU is still broken.
//
// Convention: jsmolka's test ROMs leave the result in r12 (0 = all sub-tests
// passed; non-zero = number of the first failing sub-test) and then sit in a
// `b .` idle loop. We run a generous frame budget, detect the idle loop, and
// snapshot the registers.
package tests

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/mnmlyw/nanogoadvance/internal/core"
)

//go:embed testdata/*.gba
var testROMs embed.FS

// runROM loads a test ROM, runs up to maxFrames frames, and returns the
// captured CPU state once the program is idling on `b .` (or once the frame
// budget is exhausted).
func runROM(t *testing.T, romName string, maxFrames int) ([16]uint32, uint32) {
	t.Helper()

	data, err := testROMs.ReadFile("testdata/" + romName)
	if err != nil {
		t.Fatalf("read %s: %v", romName, err)
	}

	c := core.New()
	c.LoadROM(data)
	c.Reset()

	prevPC := uint32(0xFFFFFFFF)
	stable := 0
	for i := range maxFrames {
		c.RunFrame()
		pc := c.CPU.State.Reg[15]
		if i > 30 && pc == prevPC {
			stable++
			if stable >= 3 {
				return c.CPU.State.Reg, c.CPU.State.CPSR.V
			}
		} else {
			stable = 0
		}
		prevPC = pc
	}
	return c.CPU.State.Reg, c.CPU.State.CPSR.V
}

func TestARMjsmolka(t *testing.T) {
	regs, cpsr := runROM(t, "arm.gba", 600)
	if regs[12] != 0 {
		t.Fatalf("arm.gba: first failing test = %d  (r12=%d, pc=%08x, cpsr=%08x)",
			regs[12], regs[12], regs[15], cpsr)
	}
}

func TestThumbJsmolka(t *testing.T) {
	regs, cpsr := runROM(t, "thumb.gba", 600)
	if regs[12] != 0 {
		t.Fatalf("thumb.gba: first failing test = %d  (r12=%d, pc=%08x, cpsr=%08x)",
			regs[12], regs[12], regs[15], cpsr)
	}
}

func TestMemoryJsmolka(t *testing.T) {
	regs, cpsr := runROM(t, "memory.gba", 600)
	if regs[12] != 0 {
		t.Fatalf("memory.gba: first failing test = %d  (r12=%d, pc=%08x, cpsr=%08x)",
			regs[12], regs[12], regs[15], cpsr)
	}
}

func TestNesJsmolka(t *testing.T) {
	regs, cpsr := runROM(t, "nes.gba", 1200)
	if regs[12] != 0 {
		t.Fatalf("nes.gba: first failing test = %d  (r12=%d, pc=%08x, cpsr=%08x)",
			regs[12], regs[12], regs[15], cpsr)
	}
}

func TestSaveSRAM(t *testing.T) {
	regs, cpsr := runROM(t, "sram.gba", 600)
	if regs[12] != 0 {
		t.Fatalf("sram.gba: first failing test = %d  (pc=%08x, cpsr=%08x)",
			regs[12], regs[15], cpsr)
	}
}

func TestSaveFlash64(t *testing.T) {
	regs, cpsr := runROM(t, "flash64.gba", 600)
	if regs[12] != 0 {
		t.Fatalf("flash64.gba: first failing test = %d  (pc=%08x, cpsr=%08x)",
			regs[12], regs[15], cpsr)
	}
}

func TestSaveFlash128(t *testing.T) {
	regs, cpsr := runROM(t, "flash128.gba", 600)
	if regs[12] != 0 {
		t.Fatalf("flash128.gba: first failing test = %d  (pc=%08x, cpsr=%08x)",
			regs[12], regs[15], cpsr)
	}
}

func TestSaveNone(t *testing.T) {
	regs, cpsr := runROM(t, "none.gba", 600)
	if regs[12] != 0 {
		t.Fatalf("none.gba: first failing test = %d  (pc=%08x, cpsr=%08x)",
			regs[12], regs[15], cpsr)
	}
}

func TestUnsafe(t *testing.T) {
	regs, cpsr := runROM(t, "unsafe.gba", 600)
	if regs[12] != 0 {
		t.Fatalf("unsafe.gba: first failing test = %d  (pc=%08x, cpsr=%08x)",
			regs[12], regs[15], cpsr)
	}
}

// TestSaveStateRoundTrip exercises CopyState/LoadState including the
// scheduler event survival logic added when porting Scheduler::EventClass.
// Runs arm.gba for 50 frames, snapshots, runs another instance from cold
// start, loads the snapshot, and verifies the two cores agree on
// CPU regs, all RAM regions, and the PPU output framebuffer after
// another N frames. The RAM/framebuffer checks catch per-subsystem
// CopyState/LoadState bugs that don't desync CPU regs but leave PPU,
// VRAM, OAM, or palette state behind.
func TestSaveStateRoundTrip(t *testing.T) {
	data, err := testROMs.ReadFile("testdata/arm.gba")
	if err != nil {
		t.Fatalf("read arm.gba: %v", err)
	}

	source := core.New()
	source.LoadROM(data)
	source.Reset()
	for range 50 {
		source.RunFrame()
	}

	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "snapshot.state")
	if err := source.SaveStateToFile(statePath); err != nil {
		t.Fatalf("SaveStateToFile: %v", err)
	}

	loaded := core.New()
	loaded.LoadROM(data)
	loaded.Reset()
	if err := loaded.LoadStateFromFile(statePath); err != nil {
		t.Fatalf("LoadStateFromFile: %v", err)
	}

	// Continue both cores for another 50 frames and assert they agree.
	for range 50 {
		source.RunFrame()
		loaded.RunFrame()
	}

	for i := range 16 {
		assertEq(t, source.CPU.State.Reg[i], loaded.CPU.State.Reg[i],
			"r%d", i)
	}
	assertEq(t, source.CPU.State.CPSR.V, loaded.CPU.State.CPSR.V, "CPSR")

	// Memory regions — any per-subsystem CopyState bug that leaves
	// stale bytes shows up here. Compared via SHA-256 hashes so a
	// failure log isn't a megabyte of bytes.
	assertEq(t, sha(source.Bus.EWRAM[:]), sha(loaded.Bus.EWRAM[:]), "EWRAM hash")
	assertEq(t, sha(source.Bus.IWRAM[:]), sha(loaded.Bus.IWRAM[:]), "IWRAM hash")
	assertEq(t, sha(source.Bus.Palette[:]), sha(loaded.Bus.Palette[:]), "Palette hash")
	assertEq(t, sha(source.Bus.VRAM[:]), sha(loaded.Bus.VRAM[:]), "VRAM hash")
	assertEq(t, sha(source.Bus.OAM[:]), sha(loaded.Bus.OAM[:]), "OAM hash")

	// PPU output frame — catches PPU-internal state desync (background
	// affine counters, sprite drawer slots, merge cycle position, etc.)
	// that don't reach RAM.
	sFrame := source.PPU.Output[source.PPU.Frame^1]
	lFrame := loaded.PPU.Output[loaded.PPU.Frame^1]
	assertEq(t, sha(framePixelsToBytes(sFrame[:])), sha(framePixelsToBytes(lFrame[:])), "PPU frame hash")
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func framePixelsToBytes(fb []uint32) []byte {
	out := make([]byte, len(fb)*4)
	for i, p := range fb {
		out[i*4+0] = uint8(p)
		out[i*4+1] = uint8(p >> 8)
		out[i*4+2] = uint8(p >> 16)
		out[i*4+3] = uint8(p >> 24)
	}
	return out
}

// assertEq compares any comparable state across the save/load divide.
// Uses the comparable type parameter so callers don't need format-string
// gymnastics for different value types.
func assertEq[T comparable](t *testing.T, got, want T, label string, args ...any) {
	t.Helper()
	if got != want {
		t.Fatalf(label+" diverged: source=%v loaded=%v", append(args, got, want)...)
	}
}

// PPU tests (hello/stripes/shades) don't set r12 — they're visual. They
// "pass" here as long as the ROM doesn't crash or hang on m_vsync.
func TestPPUHello(t *testing.T) {
	regs, _ := runROM(t, "hello.gba", 600)
	if regs[12] != 0 {
		t.Fatalf("hello.gba unexpectedly set r12=%d (pc=%08x)", regs[12], regs[15])
	}
}

func TestPPUStripes(t *testing.T) {
	regs, _ := runROM(t, "stripes.gba", 600)
	if regs[12] != 0 {
		t.Fatalf("stripes.gba unexpectedly set r12=%d (pc=%08x)", regs[12], regs[15])
	}
}

func TestPPUShades(t *testing.T) {
	regs, _ := runROM(t, "shades.gba", 600)
	if regs[12] != 0 {
		t.Fatalf("shades.gba unexpectedly set r12=%d (pc=%08x)", regs[12], regs[15])
	}
}
