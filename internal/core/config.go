// config.go ⇄ src/nba/include/nba/config.hh — runtime configuration for
// the emulator core (skip-BIOS, audio HLE options, etc.).
package core

// Interpolation ⇄ Config::Audio::Interpolation.
type Interpolation int

const (
	InterpolationCosine  Interpolation = 0
	InterpolationCubic   Interpolation = 1
	InterpolationSinc64  Interpolation = 2
	InterpolationSinc128 Interpolation = 3
	InterpolationSinc256 Interpolation = 4
)

// BackupTypeConfig ⇄ Config::BackupType — separate from internal/rom's
// BackupType so callers can plumb config without importing rom.
type BackupTypeConfig int

const (
	BackupConfigDetect BackupTypeConfig = iota
	BackupConfigNone
	BackupConfigSRAM
	BackupConfigFlash64
	BackupConfigFlash128
	BackupConfigEEPROM4
	BackupConfigEEPROM64
	BackupConfigEEPROMDetect
)

// Config ⇄ struct Config. Tweakable knobs that the platform layer (or a
// test harness) passes to the Core.
type Config struct {
	SkipBIOS bool

	Audio AudioConfig
}

type AudioConfig struct {
	Interpolation     Interpolation
	Volume            int // 0..100
	MP2KHLEEnable     bool
	MP2KHLECubic      bool
	MP2KHLEForceReverb bool
}

// DefaultConfig returns the upstream-equivalent default config (cubic
// interpolation, MP2K HLE disabled, full volume).
func DefaultConfig() Config {
	return Config{
		Audio: AudioConfig{
			Interpolation:     InterpolationCubic,
			Volume:            100,
			MP2KHLEEnable:     false,
			MP2KHLECubic:      true,
			MP2KHLEForceReverb: true,
		},
	}
}

// ApplyConfig consumes a Config and wires its options into the core.
// Should be called after LoadROM (so MP2K's SearchSoundMainRAM finds
// SoundMain) but before Reset/Run.
func (c *Core) ApplyConfig(cfg Config) {
	c.config = cfg
	if cfg.Audio.MP2KHLEEnable {
		c.EnableMP2KHLE(cfg.Audio.MP2KHLECubic, cfg.Audio.MP2KHLEForceReverb)
	}
}

// Config returns a copy of the active configuration. Frontends inspect
// this to pick the matching resampler / video filter at startup.
func (c *Core) Config() Config { return c.config }
