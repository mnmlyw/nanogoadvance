# nanogoadvance

A line-by-line Go port of [NanoBoyAdvance](https://github.com/nba-emu/NanoBoyAdvance).

Each Go file maps to a specific upstream C++ source. Correctness is gated
by running real GBA test ROMs (jsmolka's `arm.gba`, `thumb.gba`, etc.)
headlessly and asserting their pass signal.

## Running the tests

```
go test ./tests/...
```

The test harness loads each ROM, runs until it reaches the `idle: b idle`
sentinel, then reads `r12` — the test ROM's convention for "0 = all pass,
N = test N failed".

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
| `internal/savestate/savestate.go` | `src/nba/include/nba/save_state.hh` |
| `internal/dsp/{stream,stereo,ring_buffer,resampler}.go` | `src/nba/include/nba/common/dsp/*` |
| `internal/common/crc32.go` | `src/nba/include/nba/common/crc32.hh` |
| `internal/device/device.go` | `src/nba/include/nba/device/{audio,video}_device.hh` |
| `internal/bus/io_addr.go` | `src/nba/src/bus/io.hh` |
| `internal/rom/header.go` | `src/nba/include/nba/rom/header.hh` |
| `internal/rom/game_db.go` | `src/platform/core/src/game_db.cc` |
| `internal/core/core.go` | `src/nba/src/core.{hh,cc}` |
| `internal/core/config.go` | `src/nba/include/nba/config.hh` |
| `internal/core/serialization.go` | `src/nba/src/serialization.cc` |
| `internal/platform/frontend.go` | `src/platform/*` (Go-native, ebitengine) |
