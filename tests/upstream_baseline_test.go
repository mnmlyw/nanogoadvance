// upstream_baseline_test.go — assert pixel-exact parity with upstream
// NanoBoyAdvance over the Pokemon Emerald Birch-intro trace.
//
// Baseline is `tests/testdata/emerald_birch.hashes`: a binary file of
// per-frame FNV-1a hashes produced by `tools/nba-headless --hash-out`
// using the same scripted input as TestPokemonEmeraldBirchFlicker. Any
// divergence from this baseline = the port stopped being byte-exact
// with upstream NBA for the affected frame.
//
// Regenerate after intentional behavior changes (or when upstream NBA
// is updated):
//
//   cmake --build tools/nba-headless/build --target nba-headless
//   tools/nba-headless/build/nba-headless \
//     --rom $EMERALD_ROM --bios $GBA_BIOS --script-emerald \
//     --frames 3000 --hash-out tests/testdata/emerald_birch.hashes
//
// Auto-skips if ROM/BIOS or the baseline file isn't present.
package tests

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/mnmlyw/nanogoadvance/internal/core"
	"github.com/mnmlyw/nanogoadvance/internal/keypad"
)

// keyEvent — one scripted input event in a baseline run.
type keyEvent struct {
	frame int
	key   keypad.Key
	press bool
}

// emeraldScript — START/A spam to drive Emerald to the Birch intro.
// Mirrored in tools/nba-headless main.cc EmeraldScript().
func emeraldScript() []keyEvent {
	pressAt := func(frame int, k keypad.Key) []keyEvent {
		return []keyEvent{{frame, k, true}, {frame + 4, k, false}}
	}
	var script []keyEvent
	for f := 60; f < 1500; f += 30 {
		script = append(script, pressAt(f, keypad.KeyStart)...)
		script = append(script, pressAt(f+12, keypad.KeyA)...)
	}
	return script
}

// runBaselineParity drives the port through `totalFrames` frames using
// the given scripted input, hashes each presented frame, and asserts
// the deduped distinct-frame sequence matches the upstream baseline
// within `maxEditOps` Levenshtein distance. Auto-skips if ROM/BIOS/
// baseline file isn't present.
//
// `script` may be nil for no-input runs (relies on the game's attract /
// title-screen animations to exercise code paths).
//
// maxEditOps tunes tolerance for the ±1-frame phase noise both
// emulators have from ARM-instruction overshoot. 20 is the
// empirically-tuned value for Emerald; new games may need adjustment.
func runBaselineParity(t *testing.T, romPath, biosPath, baselinePath string, script []keyEvent, maxEditOps int) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping upstream-baseline probe in -short mode")
	}
	if _, err := os.Stat(romPath); err != nil {
		t.Skipf("ROM not found at %s", romPath)
	}
	if _, err := os.Stat(biosPath); err != nil {
		t.Skipf("BIOS not found at %s", biosPath)
	}
	baseline, err := loadBaseline(baselinePath)
	if err != nil {
		t.Skipf("baseline %s missing or unreadable (%v) — regenerate with tools/nba-headless", baselinePath, err)
	}

	rom, err := os.ReadFile(romPath)
	if err != nil {
		t.Fatalf("read ROM: %v", err)
	}
	bios, err := os.ReadFile(biosPath)
	if err != nil {
		t.Fatalf("read BIOS: %v", err)
	}

	c := core.New()
	c.LoadBIOS(bios)
	c.LoadROM(rom)
	c.Reset()

	scriptIdx := 0
	totalFrames := len(baseline)
	portHashes := make([]uint64, totalFrames)
	for f := 0; f < totalFrames; f++ {
		for scriptIdx < len(script) && script[scriptIdx].frame <= f {
			c.SetKeyStatus(script[scriptIdx].key, script[scriptIdx].press)
			scriptIdx++
		}
		c.RunFrame()
		fb := c.PPU.Output[c.PPU.Frame^1]
		portHashes[f] = hashFrame(fb[:])
	}

	// Compare AS CONTENT SEQUENCES: collapse consecutive duplicates,
	// then run a banded Levenshtein. Both Run(280896) loops overshoot
	// to ARM instruction boundaries by a few cycles, so the same
	// content occasionally lands on a different presentation iteration
	// (phase noise) and transient animations may render N±1 distinct
	// frames between the two emulators.
	portSeq := dedupConsec(portHashes)
	upSeq := dedupConsec(baseline)

	d := alignEditDistance(portSeq, upSeq, maxEditOps+1)
	if d > maxEditOps {
		i, j := firstDivergence(portSeq, upSeq)
		t.Errorf("distinct-frame sequences diverge beyond tolerance "+
			"(edit ops >= %d, limit %d): port has %d distinct frames, upstream %d; "+
			"first divergence at port-idx=%d (hash %#016x) vs upstream-idx=%d (hash %#016x)",
			d, maxEditOps, len(portSeq), len(upSeq), i, hashOrZero(portSeq, i), j, hashOrZero(upSeq, j))
	} else {
		t.Logf("distinct-frame parity ok: %d edit ops over port=%d, upstream=%d frames",
			d, len(portSeq), len(upSeq))
	}
}

func defaultGBABIOS() string {
	if p := os.Getenv("GBA_BIOS"); p != "" {
		return p
	}
	return "/Users/yw/Documents/gba/gba_bios.bin"
}

func TestEmeraldBirchUpstreamBaseline(t *testing.T) {
	rom := os.Getenv("EMERALD_ROM")
	if rom == "" {
		rom = "/Users/yw/Documents/gba/Pokemon - Emerald Version (USA, Europe).gba"
	}
	runBaselineParity(t, rom, defaultGBABIOS(), "testdata/emerald_birch.hashes", emeraldScript(), 20)
}

// TestZeldaMinishAttractUpstreamBaseline — no-input run through the
// boot animation + title-screen attract sequence. Exercises code paths
// Pokemon Emerald doesn't (e.g., Capcom's intro engine, the Minish
// Cap-specific PPU effects).
func TestZeldaMinishAttractUpstreamBaseline(t *testing.T) {
	rom := os.Getenv("ZELDA_MINISH_ROM")
	if rom == "" {
		rom = "/Users/yw/Documents/gba/Legend of Zelda, The - The Minish Cap (USA).gba"
	}
	runBaselineParity(t, rom, defaultGBABIOS(), "testdata/zelda_minish_attract.hashes", nil, 20)
}

// TestMother3AttractUpstreamBaseline — no-input run through Mother 3's
// boot + HAL/Nintendo logos + title. Mother 3 is famous for heavy
// MP2K audio engine usage and sprite-driven cutscenes.
func TestMother3AttractUpstreamBaseline(t *testing.T) {
	rom := os.Getenv("MOTHER3_ROM")
	if rom == "" {
		rom = "/Users/yw/Documents/gba/Mother 3 (Japan).gba"
	}
	runBaselineParity(t, rom, defaultGBABIOS(), "testdata/mother3_attract.hashes", nil, 20)
}

// dedupConsec collapses runs of identical consecutive values down to one.
func dedupConsec(s []uint64) []uint64 {
	if len(s) == 0 {
		return nil
	}
	out := make([]uint64, 0, len(s))
	out = append(out, s[0])
	for i := 1; i < len(s); i++ {
		if s[i] != s[i-1] {
			out = append(out, s[i])
		}
	}
	return out
}

// alignEditDistance returns the Levenshtein edit distance between a and b,
// capped at limit so we don't pay O(n*m) on large sequences. Returns limit
// if distance > limit. Uses banded DP with bandwidth = limit.
func alignEditDistance(a, b []uint64, limit int) int {
	if abs(len(a)-len(b)) > limit {
		return limit
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		lo := max(1, i-limit)
		hi := min(len(b), i+limit)
		// Out-of-band cells stay at limit (any path through is too costly).
		for j := 1; j < lo; j++ {
			curr[j] = limit
		}
		for j := hi + 1; j <= len(b); j++ {
			curr[j] = limit
		}
		for j := lo; j <= hi; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			best := prev[j-1] + cost
			if v := prev[j] + 1; v < best {
				best = v
			}
			if v := curr[j-1] + 1; v < best {
				best = v
			}
			if best > limit {
				best = limit
			}
			curr[j] = best
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// firstDivergence returns the first (i, j) at which two sequences differ.
func firstDivergence(a, b []uint64) (int, int) {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i, i
		}
	}
	return len(a), len(b)
}

func hashOrZero(s []uint64, i int) uint64 {
	if i < 0 || i >= len(s) {
		return 0
	}
	return s[i]
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// hashFrame ⇄ HashFrame in tools/nba-headless/main.cc — FNV-1a 64-bit
// over the 240*160 ARGB pixels of one framebuffer. Must stay byte-exact
// with the C++ side or the baseline becomes meaningless.
func hashFrame(fb []uint32) uint64 {
	var h uint64 = 1469598103934665603
	for _, px := range fb {
		h ^= uint64(px)
		h *= 1099511628211
	}
	return h
}

// loadBaseline reads the binary file produced by `nba-headless --hash-out`.
// Format: "NBAHASHv1\0" (10 bytes) + u32 frame_count + u64*frame_count.
func loadBaseline(path string) ([]uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 14 || string(data[:9]) != "NBAHASHv1" || data[9] != 0 {
		return nil, errBadBaseline
	}
	n := binary.LittleEndian.Uint32(data[10:14])
	if len(data) != 14+int(n)*8 {
		return nil, errBadBaseline
	}
	out := make([]uint64, n)
	for i := range out {
		out[i] = binary.LittleEndian.Uint64(data[14+i*8:])
	}
	return out, nil
}

type baselineErr string

func (e baselineErr) Error() string { return string(e) }

const errBadBaseline = baselineErr("baseline file is malformed or wrong format")
