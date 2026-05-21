// Headless harness around NanoBoyAdvance's nba + platform-core libraries.
//
// Usage:
//   nba-headless --rom game.gba [--bios bios.bin] --out DIR
//                [--frames N] [--interval F] [--skip-frames K]
//                [--audio WAV] [--audio-rate HZ]
//
// Drives core->RunForOneFrame() in a tight loop with no window. Every
// `interval` frames after the initial `skip-frames`, writes the most
// recent framebuffer to OUT/nba_tNNNN.png. With --audio, also captures
// stereo s16 audio samples to WAV.

#define STB_IMAGE_WRITE_IMPLEMENTATION
#include "stb_image_write.h"

#include <nba/core.hh>
#include <nba/device/audio_device.hh>
#include <platform/loader/bios.hh>
#include <platform/loader/rom.hh>

#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <filesystem>
#include <fstream>
#include <memory>
#include <string>
#include <vector>

namespace fs = std::filesystem;

namespace {

constexpr int kWidth  = 240;
constexpr int kHeight = 160;

struct CaptureVideo : nba::VideoDevice {
  std::uint32_t latest[kWidth * kHeight] = {};
  std::uint64_t frame_count = 0;

  void Draw(std::uint32_t* buffer) final {
    std::memcpy(latest, buffer, sizeof(latest));
    ++frame_count;
  }
};

// Captures NBA's resampled audio output. NBA's APU registers a callback
// via Open() that we drive by calling Pump(samples). Each call asks the
// APU for `samples` stereo s16 frames and appends them to `out`.
struct RecordingAudio : nba::AudioDevice {
  Callback callback = nullptr;
  void* userdata = nullptr;
  int sample_rate;
  std::vector<std::int16_t> out;

  explicit RecordingAudio(int rate) : sample_rate(rate) {}

  bool Open(void* ud, Callback cb) final {
    userdata = ud;
    callback = cb;
    return true;
  }
  void Close() final { callback = nullptr; }
  void Reset() final {}
  auto GetSampleRate() -> int final { return sample_rate; }
  void SetPause(bool) final {}

  void Pump(int frames) {
    if (!callback) return;
    const std::size_t old = out.size();
    out.resize(old + frames * 2);
    callback(userdata, out.data() + old, frames * 2 * sizeof(std::int16_t));
  }
};

void DumpPng(fs::path const& out_dir, std::uint64_t index, std::uint32_t const* argb) {
  std::vector<std::uint8_t> rgba(kWidth * kHeight * 4);
  for (int i = 0; i < kWidth * kHeight; ++i) {
    std::uint32_t p = argb[i];
    rgba[i * 4 + 0] = static_cast<std::uint8_t>((p >> 16) & 0xFF);
    rgba[i * 4 + 1] = static_cast<std::uint8_t>((p >> 8) & 0xFF);
    rgba[i * 4 + 2] = static_cast<std::uint8_t>(p & 0xFF);
    rgba[i * 4 + 3] = 0xFF;
  }
  char name[64];
  std::snprintf(name, sizeof(name), "nba_t%04llu.png", static_cast<unsigned long long>(index));
  fs::path out = out_dir / name;
  if (!stbi_write_png(out.string().c_str(), kWidth, kHeight, 4, rgba.data(), kWidth * 4)) {
    std::fprintf(stderr, "failed to write %s\n", out.string().c_str());
  }
}

// Minimal RIFF/WAVE writer: stereo 16-bit PCM.
void WriteWav(fs::path const& path, std::vector<std::int16_t> const& pcm, int rate) {
  std::ofstream f(path, std::ios::binary);
  if (!f) {
    std::fprintf(stderr, "failed to open %s\n", path.string().c_str());
    return;
  }
  const std::uint32_t data_bytes = static_cast<std::uint32_t>(pcm.size() * sizeof(std::int16_t));
  const std::uint32_t riff_size  = 36 + data_bytes;
  const std::uint16_t channels   = 2;
  const std::uint16_t bits       = 16;
  const std::uint32_t byte_rate  = rate * channels * (bits / 8);
  const std::uint16_t block_align = channels * (bits / 8);
  auto write_u32 = [&](std::uint32_t v) { f.write(reinterpret_cast<const char*>(&v), 4); };
  auto write_u16 = [&](std::uint16_t v) { f.write(reinterpret_cast<const char*>(&v), 2); };
  f.write("RIFF", 4); write_u32(riff_size); f.write("WAVE", 4);
  f.write("fmt ", 4); write_u32(16); write_u16(1); write_u16(channels);
  write_u32(static_cast<std::uint32_t>(rate)); write_u32(byte_rate);
  write_u16(block_align); write_u16(bits);
  f.write("data", 4); write_u32(data_bytes);
  f.write(reinterpret_cast<const char*>(pcm.data()), data_bytes);
}

// FNV-1a 64-bit over the 240*160 ARGB pixels of one framebuffer. Must
// stay byte-identical to the per-frame hash in
// tests/upstream_baseline_test.go.
std::uint64_t HashFrame(std::uint32_t const* fb) {
  std::uint64_t h = 1469598103934665603ULL;
  for (int i = 0; i < kWidth * kHeight; ++i) {
    h ^= static_cast<std::uint64_t>(fb[i]);
    h *= 1099511628211ULL;
  }
  return h;
}

[[noreturn]] void Usage() {
  std::fprintf(stderr,
    "usage: nba-headless --rom PATH [--bios PATH] [--out DIR]\n"
    "                    [--frames N] [--interval F] [--skip-frames K]\n"
    "                    [--audio WAV] [--audio-rate HZ]\n"
    "                    [--script-emerald]   replay the Birch-intro\n"
    "                                          START/A press script from\n"
    "                                          tests/pokemon_flicker_test.go\n"
    "                    [--hash-out PATH]    write per-frame FNV-1a hashes\n"
    "                                          (binary: 'NBAHASHv1\\0' + u32\n"
    "                                          frame_count + u64*frame_count)\n");
  std::exit(2);
}

// EmeraldScript mirrors tests/pokemon_flicker_test.go: from frame 60 to
// 1500, every 30 frames press START (held 4 frames) then 12 frames later
// press A (held 4 frames). Drives both emulators to the same point in
// the Birch intro for frame-by-frame comparison.
struct EmeraldEvent {
  int frame;
  nba::Key key;
  bool press;
};

std::vector<EmeraldEvent> EmeraldScript() {
  std::vector<EmeraldEvent> s;
  for (int f = 60; f < 1500; f += 30) {
    s.push_back({f,      nba::Key::Start, true});
    s.push_back({f + 4,  nba::Key::Start, false});
    s.push_back({f + 12, nba::Key::A,     true});
    s.push_back({f + 16, nba::Key::A,     false});
  }
  return s;
}

} // namespace

int main(int argc, char** argv) {
  std::string rom_path, bios_path, out_dir, audio_path, hash_out;
  int frames = 600;
  int interval = 60;
  int skip_frames = 0;
  int audio_rate = 32768;
  bool script_emerald = false;

  for (int i = 1; i < argc; ++i) {
    std::string a = argv[i];
    auto next = [&]() -> std::string {
      if (i + 1 >= argc) Usage();
      return argv[++i];
    };
    if (a == "--rom") rom_path = next();
    else if (a == "--bios") bios_path = next();
    else if (a == "--out") out_dir = next();
    else if (a == "--frames") frames = std::stoi(next());
    else if (a == "--interval") interval = std::stoi(next());
    else if (a == "--skip-frames") skip_frames = std::stoi(next());
    else if (a == "--audio") audio_path = next();
    else if (a == "--audio-rate") audio_rate = std::stoi(next());
    else if (a == "--script-emerald") script_emerald = true;
    else if (a == "--hash-out") hash_out = next();
    else { std::fprintf(stderr, "unknown arg: %s\n", a.c_str()); Usage(); }
  }
  if (rom_path.empty()) Usage();
  if (out_dir.empty() && hash_out.empty() && audio_path.empty()) {
    std::fprintf(stderr, "--out, --hash-out, or --audio required\n");
    Usage();
  }

  if (!out_dir.empty()) fs::create_directories(out_dir);

  auto config = std::make_shared<nba::Config>();
  auto capture = std::make_shared<CaptureVideo>();
  config->video_dev = capture;
  std::shared_ptr<RecordingAudio> rec_audio;
  if (!audio_path.empty()) {
    rec_audio = std::make_shared<RecordingAudio>(audio_rate);
    config->audio_dev = rec_audio;
  } else {
    config->audio_dev = std::make_shared<nba::NullAudioDevice>();
  }
  config->skip_bios = bios_path.empty();

  auto core = nba::CreateCore(config);

  if (!bios_path.empty()) {
    auto r = nba::BIOSLoader::Load(core, bios_path);
    if (r != nba::BIOSLoader::Result::Success) {
      std::fprintf(stderr, "bios load failed (%d)\n", static_cast<int>(r));
      return 1;
    }
  }

  auto r = nba::ROMLoader::Load(core, rom_path);
  if (r != nba::ROMLoader::Result::Success) {
    std::fprintf(stderr, "rom load failed (%d)\n", static_cast<int>(r));
    return 1;
  }

  core->Reset();

  // GBA refresh ≈ 59.7275 Hz. Use a phase accumulator so we pull the
  // right number of samples per frame on average and the WAV ends up at
  // the requested sample_rate.
  const double samples_per_frame = static_cast<double>(audio_rate) / 59.7275;
  double phase = 0.0;

  auto script = script_emerald ? EmeraldScript() : std::vector<EmeraldEvent>{};
  std::size_t script_idx = 0;

  std::vector<std::uint64_t> hashes;
  if (!hash_out.empty()) hashes.reserve(frames);

  std::uint64_t shot_index = 0;
  for (int f = 0; f < frames; ++f) {
    // Apply scripted input before the frame, matching the Go test's
    // order (events whose frame ≤ f fire BEFORE the next RunFrame).
    while (script_idx < script.size() && script[script_idx].frame <= f) {
      core->SetKeyStatus(script[script_idx].key, script[script_idx].press);
      ++script_idx;
    }
    core->RunForOneFrame();

    if (!hash_out.empty()) hashes.push_back(HashFrame(capture->latest));

    if (rec_audio) {
      phase += samples_per_frame;
      int frames_to_pump = static_cast<int>(phase);
      phase -= frames_to_pump;
      if (frames_to_pump > 0) rec_audio->Pump(frames_to_pump);
    }

    if (out_dir.empty()) continue;
    if (f < skip_frames) continue;
    if ((f - skip_frames) % interval == 0) {
      DumpPng(out_dir, shot_index, capture->latest);
      std::fprintf(stdout, "frame %d -> nba_t%04llu.png\n", f, static_cast<unsigned long long>(shot_index));
      std::fflush(stdout);
      ++shot_index;
    }
  }

  if (!hash_out.empty()) {
    std::ofstream hf(hash_out, std::ios::binary);
    if (!hf) {
      std::fprintf(stderr, "failed to open %s\n", hash_out.c_str());
      return 1;
    }
    hf.write("NBAHASHv1", 9);
    char nul = 0; hf.write(&nul, 1);
    std::uint32_t n = static_cast<std::uint32_t>(hashes.size());
    hf.write(reinterpret_cast<const char*>(&n), 4);
    hf.write(reinterpret_cast<const char*>(hashes.data()), n * sizeof(std::uint64_t));
    std::fprintf(stdout, "wrote %u frame hashes to %s\n", n, hash_out.c_str());
  }

  if (rec_audio && !audio_path.empty()) {
    WriteWav(audio_path, rec_audio->out, audio_rate);
    std::fprintf(stdout, "wrote %zu stereo samples to %s\n", rec_audio->out.size() / 2, audio_path.c_str());
  }
  return 0;
}
