// Package device — port of src/nba/include/nba/device/{audio,video}_device.hh.
//
// Abstract sinks for emulator output: an AudioDevice hands int16 stereo
// samples to a callback; a VideoDevice receives an ARGB framebuffer per
// frame. The platform layer implements both against ebitengine.
package device

// AudioCallback ⇄ AudioDevice::Callback.
type AudioCallback func(userdata any, stream []int16)

// AudioDevice ⇄ struct AudioDevice.
type AudioDevice interface {
	Open(userdata any, cb AudioCallback) bool
	Close()
	Reset()
	SampleRate() int
	SetPause(paused bool)
}

// NullAudioDevice ⇄ struct NullAudioDevice. Drops all audio output.
type NullAudioDevice struct{}

func (NullAudioDevice) Open(any, AudioCallback) bool { return true }
func (NullAudioDevice) Close()                       {}
func (NullAudioDevice) Reset()                       {}
func (NullAudioDevice) SampleRate() int              { return 32768 }
func (NullAudioDevice) SetPause(bool)                {}

// VideoDevice ⇄ struct VideoDevice.
type VideoDevice interface {
	Draw(buffer []uint32)
}

// NullVideoDevice ⇄ struct NullVideoDevice. Drops all frame output.
type NullVideoDevice struct{}

func (NullVideoDevice) Draw([]uint32) {}
