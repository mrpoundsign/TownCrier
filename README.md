# TownCrier

**TownCrier** is an open-source Windows and Linux desktop app that reads livestream chat and stream alerts aloud using local neural text-to-speech. Audio routes directly to any selected playback device—such as a virtual audio cable, PipeWire/PulseAudio sink, or dedicated monitor output—so it can be captured cleanly as its own audio track in OBS Studio.

Everything runs offline on the local PC with no cloud subscriptions, character limits, or API keys.

---

## Features

### Multi-Platform Stream Support
- **Twitch**: Connects to chat immediately with just a channel name. Signing in with a Twitch account adds channel alerts (Subscriptions, Resubs, Gift Subs, Bits, Raids, and Follows) and live moderation sync.
- **YouTube Live**: Reads live chat, Super Chats, Super Stickers, and New Member alerts using a channel `@handle`, channel URL, or live video URL—no Google Cloud API setup required.
- **TikTok Live**: Connects to any live `@username` room to read chat, follows, shares, and gifts. Rapid gift combos are grouped together and announced once when the streak finishes.
- **Kick**: Reads live chat, subscriptions, gifted subscriptions, and moderation events by channel name.
- **Show or Hide Platforms**: Toggle individual streaming services on or off in **Settings** to keep only active platform tabs visible.

### Local Voice Packs & Hardware Presets
- **Five Offline Voice Models**: Load one voice pack or run several at the same time to mix and match voices:
  - **Supertonic (Default)**: Fast, expressive voices (10 voices) with a lightweight memory footprint.
  - **Kokoro-82M**: Natural US and UK English voices (11 voices), with optional DirectML GPU acceleration on Windows.
  - **Kitten Mini & Kitten Nano**: Ultra-compact 8-voice packs built for minimal RAM usage on single-PC gaming and streaming setups.
  - **Piper LibriTTS-High**: A 904-speaker voice catalog for giving every regular chatter a unique voice.
- **Hardware Presets & Impact Meters**: Choose a one-click preset (*Ultra-Light*, *Fast & Lean*, *Studio Quality*, *Crowd Mode*, or *Full Arsenal*) or pick individual voice packs manually. Color-coded stacked bars show how each selected pack contributes to RAM, CPU, and GPU usage.
- **Automatic Voice Pack Downloads**: Voice packs download and install automatically on first launch or whenever a new pack is enabled in **Settings**.

### Voice Assignment & Chat Rules
- **Per-Chatter & Per-Alert Voices**: Assign specific voices to individual viewers from the **Dashboard** or **Viewers** tab, pick dedicated voices for stream alerts, or set a default voice per platform.
- **Configurable Reading Rules**: Choose whether to announce chatter names (`"User says..."`), read or strip emotes, or speak the platform name (`"On Twitch..."`), configured globally in **Settings** or overridden per platform.

### Stream Safety & Queue Control
- **Instant Panic Mute**: Click **PANIC MUTE**, press `Esc` inside the app, or use the global hotkey (`Ctrl+Shift+M` by default on Windows, customizable in **Settings**) to cut audio output immediately and clear the speech queue.
- **Live Moderation Sync**: Deleting a message or timing out/banning a user on Twitch or Kick automatically removes their queued messages and stops playback mid-sentence if their message is currently being read.
- **Smart Priority Queue**:
  - Subscriptions, Bits, Super Chats, Gifts, Follows, and Raids jump ahead of regular chat and never expire in the queue.
  - During fast chat bursts, regular chat messages waiting longer than 30 seconds are skipped so TTS stays current with the stream.
  - When the queue backs up past 5 messages, speech automatically speeds up slightly (`1.3x`) until caught up.
- **Bot & Word Filtering**: Built-in filtering skips known stream bots (`Nightbot`, `StreamElements`, `Fossabot`, `Moobot`, `Sery_Bot`, and others) with per-viewer allow/ignore overrides, and masks custom blocked words or phrases.

---

## Getting Started

1. **Download and Run**:
   - Grab `TownCrier.exe` (Windows) or `towncrier` (Linux) from [Releases](https://github.com/mrpoundsign/TownCrier/releases) and launch it.
   - On first run, the startup window downloads the default voice pack automatically before opening the main dashboard.
2. **Pick an Audio Output Device**:
   - Open the **Settings** tab and choose the playback device where TTS audio should go (for example, a virtual audio cable added as an Audio Output Capture source in OBS).
3. **Connect a Stream**:
   - Open the **Twitch**, **YouTube**, **TikTok**, or **Kick** tab, enter a channel name or handle, and toggle **Enable**.
   - Use the **Dashboard** tab to watch the live queue, pause or skip messages, and assign custom voices to active chatters.

---

## License

TownCrier is licensed under the **GNU Affero General Public License v3.0 (AGPL-3.0)**. See [`LICENSE`](LICENSE) for the full license text and [`THIRDPARTY_NOTICES.md`](THIRDPARTY_NOTICES.md) for third-party library and model attributions.
