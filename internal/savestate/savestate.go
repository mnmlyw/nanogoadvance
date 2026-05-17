// Package savestate — port of src/nba/include/nba/save_state.hh.
//
// One Go struct that mirrors nba::SaveState, including every nested
// substructure. Field names use the upstream's underscore_case (renamed to
// Go-style CamelCase only for capitalization). Sizes are deliberately
// preserved so a serialized blob can be wire-compatible with upstream
// (subject to padding differences between C++ struct packing and Go's).
package savestate

const (
	MagicNumber    uint32 = 0x5353424E // "NBSS"
	CurrentVersion uint32 = 10
)

// SaveState ⇄ nba::SaveState.
type SaveState struct {
	Magic     uint32
	Version   uint32
	Timestamp uint64

	ARM ARMState
	Bus BusState
	IRQ IRQState
	PPU PPUState
	APU APUState

	Timer [4]TimerState

	DMA DMAState

	ROMAddressLatch uint32

	Backup BackupState
	GPIO   GPIOState

	KEYCNT uint16

	Scheduler SchedulerState
}

// ----- ARM -----

type ARMState struct {
	Regs    ARMRegisterFile
	Pipe    ARMPipeline
	IRQLine bool
}

type ARMRegisterFile struct {
	GPR  [16]uint32
	Bank [7][7]uint32
	CPSR uint32
	SPSR [7]uint32
}

type ARMPipeline struct {
	Access uint8
	Opcode [2]uint32
}

// ----- Bus -----

type BusState struct {
	Memory                    BusMemory
	IO                        BusIO
	Prefetch                  BusPrefetch
	LastAccess                int32
	ParallelInternalCPUCycles int32
	PrefetchBufferWasDisabled bool
}

type BusMemory struct {
	WRAM  [0x40000]uint8
	IRAM  [0x8000]uint8
	PRAM  [0x400]uint8
	OAM   [0x400]uint8
	VRAM  [0x18000]uint8
	Latch BusMemoryLatch
}

type BusMemoryLatch struct {
	BIOS uint32
}

type BusIO struct {
	Waitcnt WaitstateControl
	Haltcnt uint8
	RCNT    [2]uint8
	Postflg uint8
}

type WaitstateControl struct {
	SRAM     uint8
	WS0      [2]uint8
	WS1      [2]uint8
	WS2      [2]uint8
	PHI      uint8
	Prefetch bool
}

type BusPrefetch struct {
	Active      bool
	HeadAddress uint32
	LastAddress uint32
	Count       uint8
	Countdown   uint8
	Thumb       bool
}

// ----- IRQ -----

type IRQState struct {
	PendingIME   uint8
	PendingIE    uint16
	PendingIF    uint16
	RegIME       uint8
	RegIE        uint16
	RegIF        uint16
	IRQAvailable bool
}

// ----- PPU -----

type PPUState struct {
	IO                       PPUIO
	BGX                      [2]ReferencePoint
	BGY                      [2]ReferencePoint
	VRAMBGLatch              uint16
	DMA3VideoTransferRunning bool
}

type PPUIO struct {
	DISPCNT   uint16
	GREENSWAP uint16
	DISPSTAT  uint16
	VCOUNT    uint16
	BGCNT     [4]uint16
	BGHOFS    [4]uint16
	BGVOFS    [4]uint16
	BGPA      [2]uint16
	BGPB      [2]uint16
	BGPC      [2]uint16
	BGPD      [2]uint16
	BGX       [2]uint32
	BGY       [2]uint32
	WINH      [2]uint16
	WINV      [2]uint16
	WININ     uint16
	WINOUT    uint16
	MOSAIC    uint16
	BLDCNT    uint16
	BLDALPHA  uint16
	BLDY      uint16
}

type ReferencePoint struct {
	Current int32
	Written bool
}

// ----- APU -----

type APUState struct {
	IO            APUIO
	FIFO          [2]FIFOState
	ResolutionOld uint8
}

type APUIO struct {
	Quad      [2]QuadChannelState
	Wave      WaveChannelState
	Noise     NoiseChannelState
	SOUNDCNT  uint32
	SOUNDBIAS uint16
}

type PSGBase struct {
	Enabled  bool
	Step     uint8
	Length   LengthState
	Envelope EnvelopeState
	Sweep    SweepState
	EventUID uint64
}

type LengthState struct {
	Enabled bool
	Counter uint8
}

type EnvelopeState struct {
	Active        bool
	Direction     uint8
	InitialVolume uint8
	CurrentVolume uint8
	Divider       uint8
	Step          uint8
}

type SweepState struct {
	Active      bool
	Direction   uint8
	Reserved    uint16
	CurrentFreq uint16
	ShadowFreq  uint16
	Divider     uint8
	Shift       uint8
	Step        uint8
}

type QuadChannelState struct {
	PSGBase
	DACEnable bool
	Phase     uint8
	WaveDuty  uint8
	Sample    int8
}

type WaveChannelState struct {
	PSGBase
	Playing     bool
	ForceVolume bool
	Phase       uint8
	Volume      uint8
	Frequency   uint16
	Dimension   uint8
	WaveBank    uint8
	WaveRAM     [2][16]uint8
}

type NoiseChannelState struct {
	PSGBase
	DACEnable      bool
	FrequencyShift uint8
	FrequencyRatio uint8
	Width          uint8
}

type FIFOState struct {
	Data    [7]uint32
	Pending uint32
	Count   uint8
	Pipe    FIFOPipe
}

type FIFOPipe struct {
	Word uint32
	Size uint8
}

// ----- Timer -----

type TimerState struct {
	Counter  uint16
	Reload   uint16
	Control  uint16
	Pending  TimerPending
	EventUID uint64
}

type TimerPending struct {
	Reload  uint16
	Control uint16
}

// ----- DMA -----

type DMAState struct {
	Channels    [4]DMAChannel
	HBlankSet   uint8
	VBlankSet   uint8
	VideoSet    uint8
	RunnableSet uint8
	Latch       uint32
}

type DMAChannel struct {
	DstAddress uint32
	SrcAddress uint32
	Length     uint16
	Control    uint16
	Latch      DMALatch
	IsFIFODMA  bool
	EventUID   uint64
}

type DMALatch struct {
	Length     uint32
	DstAddress uint32
	SrcAddress uint32
	Bus        uint32
}

// ----- Backup -----

type BackupState struct {
	Data   [131072]uint8
	FLASH  FLASHState
	EEPROM EEPROMState
}

type FLASHState struct {
	CurrentBank   uint8
	Phase         uint8
	EnableChipID  bool
	EnableErase   bool
	EnableWrite   bool
	EnableSelect  bool
}

type EEPROMState struct {
	State           uint16
	Address         uint16
	SerialBuffer    uint64
	TransmittedBits uint8
}

// ----- GPIO -----

type GPIOState struct {
	RTC         RTCState
	SolarSensor SolarSensorState

	AllowReads bool
	RDMask     uint8
	PortData   uint8
}

type RTCState struct {
	CurrentBit  uint8
	CurrentByte uint8
	Reg         uint8
	Data        uint8
	Buffer      [7]uint8
	Port        RTCPort
	State       uint8
	Control     RTCControl
}

type RTCPort struct {
	SCK uint8
	SIO uint8
	CS  uint8
}

type RTCControl struct {
	Unknown1     bool
	PerMinuteIRQ bool
	Unknown2     bool
	Mode24h      bool
	Poweroff     bool
}

type SolarSensorState struct {
	OldCLK  bool
	Counter uint8
}

// ----- Scheduler -----

type SchedulerState struct {
	Events     [64]SchedEvent
	EventCount uint8
	NextUID    uint64
}

type SchedEvent struct {
	Key        uint64
	UID        uint64
	UserData   uint64
	EventClass uint16
}
