# TownCrier

**TownCrier** is an open-source Windows desktop application (`Go` + `Fyne`) that reads livestream chat messages and stream events aloud using 100% free, local neural Text-to-Speech (**Kokoro-82M**) routed through a streamer-selected WASAPI audio device into OBS Studio.

---

## Features

### Multi-Platform Stream Connectors
- **Twitch**: Zero-credential read-only IRC chat (`irc.chat.twitch.tv`) plus optional OAuth Device Code authentication and EventSub WebSocket support for rich channel alerts (Subscriptions, Resubs, Gift Subs, Bits/Cheers, Raids, Follows) and live moderation sync.
- **YouTube Live**: Zero-credential InnerTube live chat polling supporting `@handle`, channel URL, or live video URL/ID, including Super Chats, Super Stickers, and New Sponsor events.
- **TikTok Live**: Zero-credential WebCast room connection with binary protobuf decoding (`WebcastChatMessage`, `WebcastSocialMessage`, `WebcastGiftMessage`) and sliding-window gift streak debouncing so rapid gift combos are announced once when the streak finishes.
- **Kick**: Zero-credential Pusher WebSocket chat and event reader with emote tag parsing (`[emote:id:name]`), subscriptions, gifted subscriptions, and live moderation sync.
- **Toggleable Services**: Enable or disable individual streaming platforms in **Settings** to dynamically show/hide their tabs and load/unload their background connectors.

### Local Neural TTS (Kokoro-82M)
- **Zero Cloud Costs or API Keys**: Runs completely offline on your machine via [`sherpa-onnx`](https://github.com/k2-fsa/sherpa-onnx).
- **DirectML GPU Acceleration & CPU Fallback**: Uses the FP32 model (`model.onnx`) on DirectML-capable GPUs by default, automatically falling back to the INT8 quantized model (`model.int8.onnx`) on multi-threaded CPU SIMD.
- **Automatic First-Launch Provisioning**: On first startup, TownCrier automatically downloads, SHA-256 verifies, and extracts both the FP32 and INT8 Kokoro-82M model archives in parallel before opening the main window.
- **11 Built-In Voices**: Includes US and UK English male and female voices (`af_bella`, `af_nicole`, `af_sarah`, `af_sky`, `am_adam`, `am_michael`, `bf_emma`, `bf_isabella`, `bm_george`, `bm_lewis`, and the default `af` blend) with live voice previewing.
- **Frequency-Gated Audio Cache**: Automatically caches repeated phrases and alert templates to disk (`%LOCALAPPDATA%\com.github.mrpoundsign.towncrier\cache\audio\`) while ignoring one-off chat messages to keep RAM usage near zero.

### Streamer Safety & Queue Intelligence
- **Instant Emergency Panic Mute (`<50ms`)**: Hit the UI **PANIC MUTE** button, press `Esc` (when focused), or trigger the global system-wide hotkey (`Ctrl+Shift+M` by default, customizable in Settings) to immediately zero out the audio buffer in `<50ms` and flush the speech queue.
- **Real-Time Moderation Sync**: Automatically removes messages from the speech queue—or aborts audio mid-sentence—when a message is deleted or a user is timed out/banned on Twitch or Kick.
- **Two-Tier Priority Queue & Staleness Protection**:
  - **Tier 0 (Paid & Stream Events)**: Subscriptions, Bits, Super Chats, Gifts, Follows, and Raids take priority and are never dropped for staleness.
  - **Tier 1 (Regular Chat)**: Chat messages older than 30 seconds during heavy chat bursts are automatically dropped so TTS never falls minutes behind live action.
  - **Fast Catchup Mode**: Automatically accelerates speech playback (`1.3x` default) when the queue depth exceeds 5 pending messages.
- **Profanity Blocklist & Bot Filtering**:
  - Masks configured blocked words/phrases prior to synthesis.
  - Built-in matcher filters out known stream bots (`Nightbot`, `StreamElements`, `Fossabot`, `Moobot`, `Sery_Bot`, etc.) with per-viewer allow/ignore overrides.

### Flexible Voice Routing & Rules
- **Global Chat Rules with Per-Platform Overrides**: Configure whether to speak chatter names (`"User says..."`), speak or strip platform emotes, and announce platform prefixes (`"[Twitch]"`), globally or overridden per platform.
- **Hierarchical Voice Selection**:
  `Viewer Custom Voice` → `Event Alert Voice` → `Platform Override Voice` → `Global Default Voice`.

---

## Quick Start (Windows)

1. **Launch `TownCrier.exe`**:
   - Download the portable single-file `TownCrier.exe` from Releases (or build it via `./build.sh`).
   - On first launch, the startup window will automatically download and extract the Kokoro-82M neural TTS models (~403 MB total) into `%APPDATA%\com.github.mrpoundsign.towncrier\models\kokoro-en`.
2. **Select Your Audio Output Device**:
   - Open the **Settings** tab and choose your target WASAPI playback device (for example, a virtual audio cable or dedicated monitor device routed into OBS Studio as an Audio Output Capture source).
3. **Connect Your Stream**:
   - Go to the **Twitch**, **YouTube**, **TikTok**, or **Kick** tab, enter your channel name/handle, and toggle **Enable**.
   - Use the **Dashboard** tab to monitor the live speech queue, pause/resume or skip items, and assign custom voices to active chatters with one click.

### Command-Line Flags
```powershell
TownCrier.exe [-twitch <channel>] [-debug]
```
- `-twitch <channel>`: Pre-configures and enables the specified Twitch channel on startup.
- `-debug`: Writes timestamped diagnostic and panic logs to `towncrier-debug.log`.

---

## Building from Source (WSL Cross-Compilation)

TownCrier is cross-compiled from WSL Ubuntu to Windows `amd64` using MinGW-w64 (`x86_64-w64-mingw32-gcc`) and packaged into a self-extracting single-file portable executable (`build/bin/TownCrier.exe`).

### Prerequisites (WSL Ubuntu)
- **Go 1.27+**
- **MinGW-w64 C/C++ toolchain**: `sudo apt install gcc-mingw-w64-x86-64 binutils-mingw-w64-x86-64`
- **Linux GUI headers (for running headless Fyne unit tests in WSL)**: `sudo apt install libgl1-mesa-dev xorg-dev libasound2-dev`
- **golangci-lint v2**: `golangci-lint-v2` in `PATH`

### Build Commands
```bash
# Run lint, unit tests, cross-compile Windows executable, and package release zip into dist/
./build.sh

# Cross-compile standalone build/bin/TownCrier.exe only
./build.sh win

# Run go fix --diff ./... and golangci-lint-v2 run ./...
./build.sh lint

# Run unit test suite with 60s timeout
./build.sh test

# Package dist/TownCrier-v<VERSION>-windows-amd64.zip
./build.sh package

# Clean build/ and dist/ artifacts
./build.sh clean
```

---

## Repository Structure

```text
TownCrier/
├── assets/               # Application icons and bundled media
├── build/                # Local build output and temporary binaries (gitignored)
│   └── bin/              # Cross-compiled executables (TownCrier.exe) and runtime DLLs
├── cmd/
│   ├── launcher/         # Single-file portable executable self-extracting launcher
│   ├── spike/            # Technical spike & audio engine validation CLI
│   └── towncrier/        # Main desktop application GUI entry point (main.go)
├── internal/
│   ├── app/              # Application lifecycle, session coordination, and wiring
│   ├── audio/            # Audio device enumeration (WASAPI), mixing, gain, emergency abort
│   ├── bots/             # Known-bot matcher, embedded bot list, ignore/allow overrides
│   ├── chat/             # Unified event model, emote stripping, phonetic filtering, voice router
│   ├── config/           # Configuration schema, global rules, platform overrides, atomic persistence
│   ├── logging/          # Crash recovery and debug file logging
│   ├── platform/         # Platform connectors (Twitch, YouTube, TikTok, Kick)
│   ├── queue/            # Priority speech queue with 30s staleness TTL and fast catchup
│   ├── secrets/          # OS Keyring (Windows Credential Manager) wrapper with memory fallback
│   ├── tts/              # Offline neural TTS engine, parallel model downloader, audio cache
│   └── ui/               # Fyne GUI components (dashboard, voices, viewers, settings, platform tabs)
├── build.sh              # WSL cross-compilation and quality-gate pipeline script
├── FyneApp.toml          # Fyne application metadata
├── LICENSE               # GNU Affero General Public License v3.0
└── THIRDPARTY_NOTICES.md # Third-party open-source licenses and attributions
```

---

## License

TownCrier is licensed under the **GNU Affero General Public License v3.0 (AGPL-3.0)**. See [`LICENSE`](LICENSE) for the full license text and [`THIRDPARTY_NOTICES.md`](THIRDPARTY_NOTICES.md) for third-party library and model attributions.
