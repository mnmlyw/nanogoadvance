# Status

Snapshot of where the port stands. Update as things change.

## Current issue — Pokemon Emerald scanline flicker

Real-game regression caught by `tests/pokemon_flicker_test.go`:

- 211 frames out of 3000 (`TestPokemonEmeraldBirchFlicker`) trip the X-Y-X
  signature: scanline `y` at frame `f` differs from `f-1` and `f+1`, AND
  `f-1` and `f+1` hash identically. That matches "a line flips wrong for one
  frame, then snaps back" — not animation.
- Hot scanlines cluster around y≈40, 60–80 (top of the visible field
  during the Birch intro on Emerald). Top offenders: y=40 (8 hits), y=68
  (8 hits), y=61, y=69, y=1 (7 hits each).
- Earliest cluster: frame 568, 5 evenly-spaced scanlines (1, 33, 65, 97,
  129 — exact 32-line stride suggests an affine BG row span or mosaic
  Y-counter boundary).
- Last fix tried (uncommitted in `internal/ppu/background.go`):
  `advanceAffineXY` now updates the per-scanline working copy
  `bgx.Current` / `bgy.Current` instead of the per-pixel `BG.Affine[id]`
  (which `InitBackground` re-seeds from `Current` at the start of every
  line, making any write to `Affine[id].X/Y` a no-op). This matches
  upstream `background.cc:144-149` (`bgx[id]._current += bgpb[id]`).
  The fix is correct upstream-wise but **does not eliminate the flicker**.

### Next debug step

Pull a triplet (`before`/`bad`/`after`) PNG dump via
`FLICKER_DUMP=/tmp/flicker go test ... -run Birch...`, pick the earliest
cluster (frame 568, 32-line stride), and diff per pixel. Cross-check the
affected scanline against mGBA frame output (clone at `/private/tmp/mgba`)
to confirm which frame is the wrong one. Diff the upstream
`DrawBackgroundImpl` / `InitBackground` / `advanceAffineXY` paths against
ours line-by-line — the 32-line stride strongly implies a mosaic or
affine-counter bug, not a CPU/timing issue.

## Limitations

- **PPU rendering is incomplete relative to upstream**: any visible bug
  in a real game is suspect until proven otherwise. The flicker above is
  the first one we're chasing; expect more once a wider game corpus is
  driven through the headless test harness.
- **Post-PPU filters not implemented**: NanoBoyAdvance applies LCD
  ghosting, color correction, etc. after the PPU. We don't — so
  pixel-level frame comparisons against NBA's screen capture won't match
  even when the PPU is byte-correct. (`project_upstream_pixel_diff` memo.)
- **`tests/pokemon_flicker_test.go` is local-only**: it needs an Emerald
  ROM at `~/Documents/gba/Pokemon - Emerald Version (USA, Europe).gba`
  and a GBA BIOS at `~/Documents/gba/gba_bios.bin`, or `EMERALD_ROM` /
  `GBA_BIOS` env overrides. It auto-skips on machines without them, so
  CI won't catch this regression yet.
- **Only jsmolka test ROMs are CI-gated**: `arm.gba`, `thumb.gba`,
  `memory.gba`, `nes.gba`, plus `bios.gba`, `flash{64,128}.gba`,
  `sram.gba`, etc. live in `tests/testdata/`. Anything outside those
  ROMs depends on local-only tests or eyeballing.
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

## Files currently dirty

- `internal/ppu/background.go` — affine-counter fix described above
  (uncommitted).
- `internal/ppu/ppu.go` — comment cleanup on the sprite double-buffer
  init (uncommitted, no behavior change).
- `tests/pokemon_flicker_test.go` — new headless flicker reproducer
  (untracked).
