// pokemon_flicker_test.go — automated detection of the Birch-intro
// horizontal-line flicker. Requires an Emerald ROM and a GBA BIOS, so it
// auto-skips on machines without them (CI, contributors who don't own a
// dump). Set EMERALD_ROM / GBA_BIOS to override the well-known paths.
//
// Method: run N frames headlessly, hash every scanline of every frame,
// then look for the X-Y-X "flicker" signature — scanline `y` at frame `f`
// differs from `f-1` and `f+1`, AND `f-1`'s hash matches `f+1`'s. That
// matches "a line briefly turns wrong, then snaps back" but does NOT
// match genuine animation (which moves forward each frame).
package tests

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/mnmlyw/nanogoadvance/internal/core"
	"github.com/mnmlyw/nanogoadvance/internal/keypad"
)

func TestPokemonEmeraldBirchFlicker(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping flicker probe in -short mode")
	}

	romPath := os.Getenv("EMERALD_ROM")
	if romPath == "" {
		romPath = "/Users/yw/Documents/gba/Pokemon - Emerald Version (USA, Europe).gba"
	}
	biosPath := os.Getenv("GBA_BIOS")
	if biosPath == "" {
		biosPath = "/Users/yw/Documents/gba/gba_bios.bin"
	}
	if _, err := os.Stat(romPath); err != nil {
		t.Skipf("ROM not found at %s (set EMERALD_ROM)", romPath)
	}
	if _, err := os.Stat(biosPath); err != nil {
		t.Skipf("BIOS not found at %s (set GBA_BIOS)", biosPath)
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

	// Input script — drives Emerald to the Birch intro. The GameFreak / title
	// sequence is interactive; spamming START + A reliably reaches "New Game"
	// and Birch's "Hello, there!" speech within ~1500 frames on real cart.
	// Each entry is (frame, key, pressed).
	type keyEvent struct {
		frame int
		key   keypad.Key
		press bool
	}
	pressAt := func(frame int, k keypad.Key) []keyEvent {
		return []keyEvent{{frame, k, true}, {frame + 4, k, false}}
	}
	var script []keyEvent
	for f := 60; f < 1500; f += 30 {
		script = append(script, pressAt(f, keypad.KeyStart)...)
		script = append(script, pressAt(f+12, keypad.KeyA)...)
	}
	scriptIdx := 0

	const totalFrames = 3000 // ~50 s — reaches Birch intro after START/A spam.
	hashes := make([][160]uint64, totalFrames)
	// Keep a ring of the last 3 framebuffers so we can dump the X-Y-X
	// triplet when an anomaly is found.
	var fbRing [3][240 * 160]uint32
	dumpDir := os.Getenv("FLICKER_DUMP")
	if dumpDir != "" {
		_ = os.MkdirAll(dumpDir, 0o755)
	}
	dumpedClusters := 0
	const maxDumps = 10 // dump up to 10 clusters worth of triplets
	dumpAfter := 0
	if v := os.Getenv("FLICKER_DUMP_AFTER"); v != "" {
		fmt.Sscanf(v, "%d", &dumpAfter)
	}

	for f := range totalFrames {
		// Apply script events whose frame index has been reached.
		for scriptIdx < len(script) && script[scriptIdx].frame <= f {
			c.SetKeyStatus(script[scriptIdx].key, script[scriptIdx].press)
			scriptIdx++
		}
		c.RunFrame()
		fb := c.PPU.Output[c.PPU.Frame^1]
		// Hash scanlines.
		for y := range 160 {
			var h uint64 = 1469598103934665603
			row := fb[y*240 : y*240+240]
			for _, px := range row {
				h ^= uint64(px)
				h *= 1099511628211
			}
			hashes[f][y] = h
		}
		// Ring-buffer the framebuffer for later PNG dump.
		if dumpDir != "" {
			copy(fbRing[f%3][:], fb[:])
			// At frame f, we have frames [f-2, f-1, f] in the ring. Check
			// whether f-1 is a flicker frame; if so, dump the triplet.
			if dumpedClusters < maxDumps && f >= 2 && f-1 >= dumpAfter {
				mid := f - 1
				var bad []int
				for y := range 160 {
					if hashes[mid][y] != hashes[mid-1][y] && hashes[mid-1][y] == hashes[mid+1][y] {
						bad = append(bad, y)
					}
				}
				// Dump if at least 4 adjacent scanlines flickered, OR if any flicker
				// happened after the FLICKER_DUMP_AFTER threshold (caller wants to
				// inspect post-warmup single-scanline events too).
				if largestRunLen(bad) >= 4 || (dumpAfter > 0 && len(bad) > 0) {
					prefix := fmt.Sprintf("%s/flicker_frame%04d", dumpDir, mid)
					writePNG(prefix+"_before.png", fbRing[(f-2)%3][:])
					writePNG(prefix+"_bad.png", fbRing[(f-1)%3][:])
					writePNG(prefix+"_after.png", fbRing[f%3][:])
					// Pixel diff between bad and before, restricted to flicker scanlines.
					writeDiffPNG(prefix+"_diff.png", fbRing[(f-2)%3][:], fbRing[(f-1)%3][:], bad)
					t.Logf("dumped flicker triplet for frame %d → %s_{before,bad,after,diff}.png", mid, prefix)
					// Per-pixel detail for the first bad scanline.
					if len(bad) > 0 {
						y := bad[0]
						var details []string
						for x := range 240 {
							b := fbRing[(f-1)%3][y*240+x]
							pre := fbRing[(f-2)%3][y*240+x]
							if b != pre {
								details = append(details,
									fmt.Sprintf("x=%d %06x→%06x", x, pre&0xFFFFFF, b&0xFFFFFF))
							}
						}
						t.Logf("  frame %d scanline %d: %d pixel diffs: %s", mid, y, len(details), strings.Join(details, ", "))
					}
					dumpedClusters++
				}
			}
		}
	}

	// Detect X-Y-X single-frame flicker on each scanline.
	type flicker struct {
		frame int
		ys    []int
	}
	var flickers []flicker
	scanlineHits := make(map[int]int)
	for f := 1; f < totalFrames-1; f++ {
		var bad []int
		for y := range 160 {
			if hashes[f][y] != hashes[f-1][y] && hashes[f-1][y] == hashes[f+1][y] {
				bad = append(bad, y)
				scanlineHits[y]++
			}
		}
		if len(bad) > 0 {
			flickers = append(flickers, flicker{f, bad})
		}
	}

	if len(flickers) == 0 {
		t.Logf("no flicker detected across %d frames", totalFrames)
		return
	}

	// Summarize: top frames + top scanlines.
	t.Logf("detected %d frames with X-Y-X flicker over %d total", len(flickers), totalFrames)
	maxShow := min(30, len(flickers))
	for _, fl := range flickers[:maxShow] {
		t.Logf("  frame %4d: %d scanlines %s", fl.frame, len(fl.ys), rangeStr(fl.ys))
	}
	if len(flickers) > maxShow {
		t.Logf("  ... and %d more", len(flickers)-maxShow)
	}

	// Top 20 scanlines by hit count — clusters reveal the affected sprite/BG region.
	type yc struct {
		y, n int
	}
	var ranking []yc
	for y, n := range scanlineHits {
		ranking = append(ranking, yc{y, n})
	}
	sort.Slice(ranking, func(i, j int) bool { return ranking[i].n > ranking[j].n })
	t.Logf("top scanlines by flicker hits:")
	for i, r := range ranking {
		if i >= 20 {
			break
		}
		t.Logf("  y=%3d  %d hits", r.y, r.n)
	}

	t.Fatalf("flicker detected (%d frames affected)", len(flickers))
}

// rangeStr collapses a sorted-ascending int slice into "0-3,7,12-14" form.
func rangeStr(ys []int) string {
	if len(ys) == 0 {
		return "[]"
	}
	var parts []string
	start, end := ys[0], ys[0]
	for i := 1; i < len(ys); i++ {
		if ys[i] == end+1 {
			end = ys[i]
			continue
		}
		parts = append(parts, fmtRange(start, end))
		start, end = ys[i], ys[i]
	}
	parts = append(parts, fmtRange(start, end))
	return strings.Join(parts, ",")
}

func fmtRange(a, b int) string {
	if a == b {
		return fmt.Sprintf("%d", a)
	}
	return fmt.Sprintf("%d-%d", a, b)
}

// largestRunLen returns the length of the longest run of consecutive ints
// in a sorted-ascending slice.
func largestRunLen(ys []int) int {
	if len(ys) == 0 {
		return 0
	}
	best, cur := 1, 1
	for i := 1; i < len(ys); i++ {
		if ys[i] == ys[i-1]+1 {
			cur++
			if cur > best {
				best = cur
			}
		} else {
			cur = 1
		}
	}
	return best
}

// writePNG dumps a 240x160 BGRA framebuffer to a PNG file.
func writePNG(path string, fb []uint32) {
	img := image.NewNRGBA(image.Rect(0, 0, 240, 160))
	for y := range 160 {
		for x := range 240 {
			p := fb[y*240+x]
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(p >> 16), G: uint8(p >> 8), B: uint8(p), A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_ = png.Encode(f, img)
}

// writeDiffPNG dumps a difference map between two framebuffers; pixels that
// match are grayscaled (from the bad frame), pixels that differ are bright red.
// Only highlights diffs on `bad` scanlines.
func writeDiffPNG(path string, before, bad []uint32, badY []int) {
	yset := map[int]bool{}
	for _, y := range badY {
		yset[y] = true
	}
	img := image.NewNRGBA(image.Rect(0, 0, 240, 160))
	for y := range 160 {
		for x := range 240 {
			b := bad[y*240+x]
			pre := before[y*240+x]
			if yset[y] && b != pre {
				img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 0, B: 255, A: 255})
			} else {
				r, g, bl := uint8(b>>16), uint8(b>>8), uint8(b)
				gray := uint8((uint16(r) + uint16(g) + uint16(bl)) / 3)
				img.SetNRGBA(x, y, color.NRGBA{R: gray, G: gray, B: gray, A: 255})
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_ = png.Encode(f, img)
}
