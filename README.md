# nanogoadvance

A line-by-line Go port of [NanoBoyAdvance](https://github.com/nba-emu/NanoBoyAdvance),
a cycle-accurate Game Boy Advance emulator. The frontend is built on
[ebitengine](https://ebitengine.org/).

Each Go file maps to a specific upstream C++ source. The port is not a
reimplementation — translation is the unit of work, and the file table
below is the canonical mapping.

## Quickstart

```sh
go build ./cmd/nanogoadvance
./nanogoadvance -bios path/to/gba_bios.bin path/to/game.gba
```

Without `-bios`, a skip-BIOS startup pre-initializes SP banks and jumps
to the cart entry. That works for the jsmolka test ROMs but most retail
games crash on the first BIOS SWI, so for real games supply a BIOS.

Saves persist to a `.sav` next to the ROM. Pass `-mp2k-hle` for the MP2K
audio HLE (recommended for most retail games). `-load-state path` restores
a save state at startup.

For a headless dry-run (no window, prints CPU state after N frames):

```sh
go run ./cmd/smoketest -frames 600 path/to/game.gba
```

## Testing

Correctness is gated by real GBA test ROMs run headlessly — `go vet`
and unit tests can be green while the CPU is still broken, so only the
ROM tests count.

```sh
go test ./tests/...
```

The harness loads each ROM in `tests/testdata/`, runs until it parks
on the `b .` idle sentinel, and reads `r12` (jsmolka's convention:
`0` = all sub-tests passed, `N` = first failing sub-test). Currently
gated: `arm.gba`, `thumb.gba`, `memory.gba`, `nes.gba`, plus
`bios.gba`, `flash{64,128}.gba`, `sram.gba`, `shades.gba`, `stripes.gba`,
`hello.gba`, `none.gba`, `unsafe.gba`.

Real-game regressions get headless reproducers driven by hashing
scanlines (e.g. `tests/pokemon_flicker_test.go`). Those auto-skip if
the required ROM/BIOS isn't on disk, so CI runs only the jsmolka
suite.

## Status

See [STATUS.md](STATUS.md) for the current open issue (Pokemon Emerald
scanline flicker), known limitations, and the development process.

## File mapping

| Go file | Upstream C++ file |
| --- | --- |
| `internal/arm/state.go` | `src/nba/src/arm/state.hh` |
| `internal/arm/arm7tdmi.go` | `src/nba/src/arm/arm7tdmi.hh` |
| `internal/arm/arithmetic.go` | `src/nba/src/arm/handlers/arithmetic.inl` |
| `internal/arm/memory.go` | `src/nba/src/arm/handlers/memory.inl` |
| `internal/arm/handler32.go` | `src/nba/src/arm/handlers/handler32.inl` |
| `internal/arm/handler16.go` | `src/nba/src/arm/handlers/handler16.inl` |
| `internal/arm/tables.go` | `src/nba/src/arm/tablegen/{gen_arm,gen_thumb}.hh` |
| `internal/arm/serialization.go` | `src/nba/src/arm/serialization.cc` |
| `internal/bus/bus.go` | `src/nba/src/bus/bus.{hh,cc}` + `bus/timing.cc` + `bus/io.cc` (WAITCNT/HALTCNT) |
| `internal/bus/serialization.go` | `src/nba/src/bus/serialization.cc` |
| `internal/bus/io_addr.go` | `src/nba/src/bus/io.hh` |
| `internal/scheduler/scheduler.go` | `src/nba/include/nba/scheduler.hh` |
| `internal/apu/apu.go` + `channels.go` | `src/nba/src/hw/apu/apu.{cc,hh}` + `channel/*` |
| `internal/apu/serialization.go` | `src/nba/src/hw/apu/serialization.cc` |
| `internal/apu/hle/mp2k.go` | `src/nba/src/hw/apu/hle/mp2k.{hh,cc}` |
| `internal/dma/dma.go` | `src/nba/src/hw/dma/dma.{hh,cc}` |
| `internal/dma/serialization.go` | `src/nba/src/hw/dma/serialization.cc` |
| `internal/irq/irq.go` | `src/nba/src/hw/irq/irq.{hh,cc}` |
| `internal/irq/serialization.go` | `src/nba/src/hw/irq/serialization.cc` |
| `internal/keypad/keypad.go` | `src/nba/src/hw/keypad/keypad.{hh,cc}` |
| `internal/keypad/serialization.go` | `src/nba/src/hw/keypad/serialization.cc` |
| `internal/ppu/{ppu,registers,background,sprite,merge,window}.go` | `src/nba/src/hw/ppu/*` |
| `internal/ppu/serialization.go` | `src/nba/src/hw/ppu/serialization.cc` |
| `internal/timer/timer.go` | `src/nba/src/hw/timer/timer.{hh,cc}` |
| `internal/timer/serialization.go` | `src/nba/src/hw/timer/serialization.cc` |
| `internal/backup/backup.go` | `src/nba/src/hw/rom/backup/{sram,flash,eeprom}.cc` |
| `internal/backup/backup_file.go` | `src/nba/include/nba/rom/backup/backup_file.hh` |
| `internal/backup/serialization.go` | `src/nba/src/hw/rom/backup/serialization.cc` |
| `internal/gpio/gpio.go` | `src/nba/src/hw/rom/gpio/{gpio,rtc,solar_sensor}.cc` |
| `internal/gpio/serialization.go` | `src/nba/src/hw/rom/gpio/serialization.cc` |
| `internal/rom/rom.go` | `src/nba/include/nba/rom/{rom,header}.hh` (subset) |
| `internal/rom/header.go` | `src/nba/include/nba/rom/header.hh` |
| `internal/rom/game_db.go` | `src/platform/core/src/game_db.cc` |
| `internal/savestate/savestate.go` | `src/nba/include/nba/save_state.hh` |
| `internal/dsp/{stream,stereo,ring_buffer,resampler}.go` | `src/nba/include/nba/common/dsp/*` |
| `internal/common/crc32.go` | `src/nba/include/nba/common/crc32.hh` |
| `internal/device/device.go` | `src/nba/include/nba/device/{audio,video}_device.hh` |
| `internal/core/core.go` | `src/nba/src/core.{hh,cc}` |
| `internal/core/config.go` | `src/nba/include/nba/config.hh` |
| `internal/core/serialization.go` | `src/nba/src/serialization.cc` |
| `internal/platform/frontend.go` | `src/platform/*` (Go-native, ebitengine) |

## Credit

All emulation behavior is owed to NanoBoyAdvance and its author
([fleroviux](https://github.com/fleroviux)). This is a port, not
original work.

## License

The upstream NanoBoyAdvance is GPL-3.0; this port inherits the same
license obligations for any redistributed code derived from it.
