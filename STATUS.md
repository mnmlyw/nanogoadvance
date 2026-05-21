# Status

Snapshot of where the port stands. Update as things change.

## Pokemon Emerald scanline flicker — resolved (upstream-faithful)

Real-game regression originally caught by `tests/pokemon_flicker_test.go`:
211 frames out of 3000 in the Birch intro tripped the X-Y-X signature
(scanline flips for one frame, then snaps back). Cluster around y≈40,
60–80, earliest at frame 568 with a 32-line stride.

**Root cause:** scheduler `Now()` semantics inside event dispatch
(`internal/scheduler/scheduler.go`). Upstream `Scheduler::Step`
(`scheduler.hh:245-252`) sets `timestamp_now = event->timestamp`
BEFORE each callback fires; the port previously bumped `s.now` to the
run target up front, so per-cycle callbacks (`DrawBackground`,
`DrawSprite`, etc.) computed cycle deltas against post-bump time and
over-advanced the BG/sprite engines. Over many scanlines this leaked
into the per-pixel affine counter — exactly one extra `BGPA` per
affected scanline, which the pixel-diff confirmed.

**Fix:** commit `e7e1eb6` aligned `Step`/`Advance` with upstream.
Dropped flicker 211 → 18 frames (91% reduction).

**The remaining 18 frames are upstream-faithful** — byte-identical to
NanoBoyAdvance's output for the same scripted input, cross-verified
via `tools/nba-headless/`. They're genuine single-frame pixel
oscillations in the Birch intro animation that exist in upstream NBA
too. `tests/upstream_baseline_test.go` is the authoritative parity
check (Levenshtein distance against a 3000-frame upstream baseline).
`tests/pokemon_flicker_test.go` is informational only — keeps the
X-Y-X count as a tuning signal.

## Limitations

- **Only jsmolka test ROMs + the synthetic PPU/save ROMs are
  CI-gated**: `arm.gba`, `thumb.gba`, `memory.gba`, `nes.gba`, plus
  `bios.gba`, `flash{64,128}.gba`, `sram.gba`, etc. live in
  `tests/testdata/`. The Pokemon Emerald baseline test is local-only
  (auto-skips when the ROM/BIOS aren't on disk).
- **Subsystem unit-test coverage is thin**: DMA, Timer, IRQ, Keypad,
  GPIO/RTC, MP2K HLE are exercised only via Pokemon Emerald and the
  jsmolka traces. Direct unit tests would catch regressions those
  traces don't hit.
- **`Layout()` doesn't handle window resize**: the Ebiten frontend
  returns a fixed 3x logical size, so dragging the window corner
  outer-scales the result instead of recomputing logical dimensions.
- **mGBA cross-reference is scanline-accurate, not cycle-accurate**:
  good for visual ground truth, can't do cycle-level diffing against
  NanoBoyAdvance-style traces. (`reference_mgba_source` memo.)

## Development process

This repo is a **line-by-line port** of NanoBoyAdvance, not a clean-room
reimplementation. Concretely:

1. **Map upstream → Go file**. `README.md` keeps the canonical Go-file ↔
   upstream-C++-file table. New code goes in the Go file that mirrors
   the upstream module; if there's no mapping yet, add a row to the
   table first.
2. **Translate, don't paraphrase**. Each Go function is the C++
   function, translated. Comments cite the upstream file (`⇄ upstream
   background.cc:144`). When you'd otherwise hypothesize about a bug,
   diff against upstream first — that's the primary debugging tool, not
   intuition. (`feedback_bug_lookup_upstream` memo.)
3. **Correctness gates are real ROMs**. `go test ./tests/...` runs
   jsmolka's `arm.gba`/`thumb.gba`/`memory.gba`/`nes.gba` etc.
   headlessly and checks the r12 pass signal. `go vet` and unit tests
   can be green while the CPU is still broken — only the ROM tests
   matter. New subsystems land alongside a ROM that exercises them.
4. **Build the harness before the feature**. For non-trivial
   subsystems: test harness first, line-by-line translation second,
   run-after-each-piece third. No best-effort skeletons.
   (`feedback_porting_approach` memo.)
5. **Real-game regressions get a headless reproducer**.
   `pokemon_flicker_test.go` is the template: drive the game with a
   scripted input stream, hash every scanline of every frame, and let
   pattern-matching (X-Y-X for single-frame flicker) flag the bug
   without a human watching the screen. Future visual bugs should reuse
   this scaffold (PNG ring-buffer dump, scanline hashing) rather than
   inventing a new one.

