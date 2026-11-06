# TownCrier

**TownCrier** is an open-source Windows and Linux desktop application that reads livestream chat and stream alerts aloud using local neural text-to-speech (TTS), or serves as a lightweight, centralized multi-platform chat aggregator and event logger.

Audio routes directly to any selected playback device—such as a virtual audio cable, PipeWire/PulseAudio sink, or dedicated monitor output—allowing OBS Studio to capture TTS on an isolated audio track. Everything runs completely offline on your PC with zero cloud subscriptions, API keys, or character limits.

<p align="center">
  <img src="assets/screenshots/dashboard.png" alt="TownCrier Dashboard" width="800">
</p>

---

## Features

### Modular Application Features & Smart TTS Output Modes
- **Independent TTS & Logging Toggles**: Toggle **Text-to-Speech (TTS)** and **Event Logging** independently in **Settings → General & Audio**.
- **TTS Output Modes & Auto-AFK Detection**: Switch between **Always On**, **Auto-AFK**, and **Always Off** from the Dashboard header, Settings, or global hotkey (`Ctrl+Shift+T`). In **Auto-AFK** mode, TownCrier monitors keyboard/mouse inactivity (`GetLastInputInfo`) so chat is read aloud while you step away or play on a controller, and automatically mutes & flushes pending speech the instant you touch your keyboard or mouse.
- **Inactive TTS Alert Sounds**: Optionally play a built-in harmonic chime or custom `.wav` / `.mp3` alert sound (with 3-second cooldown protection) whenever messages arrive while TTS speech is inactive.
- **Elgato Stream Deck Plugin & Control API**: Built-in local WebSocket (binary Protocol Buffers & JSON) and HTTP REST control server (`127.0.0.1:8765`), plus a 1-click installer for the native **Elgato Stream Deck** plugin (`com.github.mrpoundsign.towncrier.sdPlugin`) with 5 hardware actions (TTS Output Mode, Panic Mute, Queue Control, Custom Announcement, and Master Volume with Stream Deck + rotary dial support).
- **Unified Setup Installer & Self-Updater**: Standalone `TownCrier-Setup.exe` wizard with optional voice model pre-downloading and SHA-256 verified GitHub Release self-updates.
- **Ultra-Fast Launch (<100ms) & Near-Zero RAM**: When TTS is disabled, TownCrier skips ONNX runtime initialization and model downloads entirely, running as a lean stream monitor and event logger with ~35–45 MB baseline RAM.
- **Dynamic UI Removal**: Disabling features cleanly removes non-applicable navigation tabs, settings sub-tabs, volume sliders, panic buttons, and queue controls rather than simply greying them out.
- **Full Configuration Retention**: All voice assignments, audio devices, hardware presets, speech templates, platform overrides, and retention rules are retained in `config.json` when features are toggled.

<p align="center">
  <img src="assets/screenshots/general-audio-features.png" alt="TownCrier Application Features and Settings" width="750">
</p>

### Multi-Platform Stream Support
- **Twitch**: Connect immediately with just a channel name via IRC, or connect via OAuth for comprehensive EventSub alerts (Subscriptions, Resubs, Gift Subs, Bits, Raids, Follows, Watch Streaks, Sub Upgrades, Channel Point Redemptions, Bits Badge Milestones, Charity Donations, and Announcements) with IRC/EventSub message deduplication and live moderation sync.
- **YouTube Live**: Read live chat, Super Chats, Super Stickers (rendered inline), New Members, Incoming Raids / Live Redirects, Gift Membership redemptions, and YouTube Giving Charity donations using a channel `@handle`, channel URL, or live video URL—no Google Cloud API keys required.
- **TikTok Live**: Connect to any live `@username` to read chat, follows, stream shares, Super Fan subscriptions/resubs, and gifts (rendered inline). Rapid gift combos are automatically grouped and announced when finished.
- **Kick**: Read live chat, subscriptions, multi-recipient gifted subscriptions, channel reward redemptions, and moderation actions by channel name.
- **Platform Connection Badges**: Real-time vector indicator badges in the top application header display live connection and streaming status for each service.
- **Show or Hide Platforms**: Toggle individual streaming services in **Settings** to keep only active platform tabs visible.

### Local Neural Voice Packs & Hardware Presets
- **Five Offline Voice Models**: Load a single lightweight model or run multiple voice engines simultaneously:
  - **Supertonic (Default)**: Ultra-fast 66M-parameter flow-matching model (10 voices) with near-instant speech start (~0.2s) and light CPU usage.
  - **Kokoro-82M**: Natural voices with all 34 US & UK English voices, featuring optional DirectML GPU acceleration on Windows (~0.3s speech start).
  - **Kokoro International**: Optional multilingual pack adding 25 international voices (Japanese, Mandarin, Spanish, French, Hindi, Italian, and Brazilian Portuguese) sharing the Kokoro engine at 0 MB extra RAM.
  - **Kitten Mini & Kitten Nano**: Ultra-compact distilled voice packs (8 voices each) designed for minimal RAM on single-PC streaming rigs.
  - **Piper LibriTTS-High**: Massive 904-speaker voice catalog for giving every regular chatter a distinct voice.
- **Hardware Presets & Stacked Impact Meters**: Choose a one-click preset (*Ultra-Low Resource*, *Fast & Lean*, *Studio Quality*, *Crowd Mode*, or *Full Arsenal*) or customize voice packs manually. Color-coded stacked bars display real-world process RAM, CPU math, and GPU acceleration usage.
- **Modal Download Progress Dialog**: Seamless in-app downloads with full-window modal interaction locking, determinate progress bars (`Model X of Y`, downloaded MB / total MB, percentage), safe cancellation, and automatic multi-engine hot-reloading.

<p align="center">
  <img src="assets/screenshots/tts-hardware-presets.png" alt="TownCrier Voice Packs and Hardware Presets" width="750">
</p>

### Chat Logging, Stream Sessions & Live Overlay Pop-Outs
- **Centralized Live Logs**: High-performance unified live tail feed aggregating Twitch, YouTube Live, Kick, and TikTok into a single chronological stream.
- **Rich Inline Emotes, Stickers & Badges**: Native inline rendering of Twitch emotes, YouTube custom emotes and Super Stickers, Kick emotes, and animated TikTok gifts (with automatic rapid combo grouping). Chatter badges (Broadcaster, Mod, VIP, Subscriber, Founder, Fan Club) appear alongside usernames.
- **Independent Chat Scaling**: Custom zoom and font scaling for the chat feed with instant one-click presets (**80%**, **100%**, **125%**, **150%**) and a smooth percentage slider to match any monitor DPI or streaming setup.
- **Streamlined Pop-Out Windows**: Launch distraction-free floating windows stripped of toolbars and filter panels—displaying only the live event feed and sticky session totals. Perfect for placing on a secondary monitor or window-capturing directly in OBS Studio.
- **Broadcast Stream Sessions**: Automatic session tracking for live broadcasts with session start/end timestamps, elapsed `HH:MM:SS` duration counters, and sticky summary metrics (total message volume, alert counts, and unique chatter counts).
- **Session-Bound Filtering**: Instantly filter live logs to the current broadcast session or isolate specific elapsed time ranges.

<p align="center">
  <img src="assets/screenshots/live-popout.png" alt="TownCrier Streamlined Live Log Pop-Out Window" width="700">
</p>

### Search History & Local SQLite Database
- **Instant FTS5 Trigram Full-Text Search**: Embedded local SQLite database with full-text search indexing across millions of chat messages and stream events with instant, typo-tolerant search results.
- **Multi-Session Selection & Date Ranges**: Filter historical chat records by calendar date ranges, multi-session broadcast selections, specific streaming services, event kinds (chat, subscriptions, raids, bits/super chats), or specific usernames.
- **Automated Retention Management**: Configurable local retention policies with automatic pruning by age (e.g. 30, 60, 90 days) or max database row counts to preserve disk space while retaining essential stream records.
- **Zero Cloud Storage**: Chat logs, sessions, and search indexes remain strictly on your local PC in `%LOCALAPPDATA%\com.github.mrpoundsign.towncrier\` (Windows) or `~/.local/share/towncrier/` (Linux).

### In-Log Streamer Moderation & Safety Controls
- **Inline Moderation Actions**: Moderate your stream directly from the chat feed with one-click message deletion and user timeout/ban for Twitch and YouTube Live (via secure OAuth 2.0 PKCE).
- **Local TTS Mute**: Instantly silence irritating or spammy chatters from text-to-speech with a single click—without having to ban or timeout the viewer on the stream platform.
- **Twitch Held-for-Review AutoMod Queue**: Review AutoMod held messages in a dedicated real-time queue with one-click approval or denial before messages reach the stream.
- **Instant Emergency Panic Stop**: Click **EMERGENCY STOP**, press `Esc` inside the app, or trigger the global hotkey (`Ctrl+Shift+M` default on Windows) to immediately zero audio output buffers in under 50µs and flush all pending speech.
- **Live Moderation Sync**: Message deletions or timeouts/bans on Twitch and Kick immediately purge queued speech items and halt audio playback mid-sentence.
- **Bot & Profanity Filtering**: Built-in one-click profanity filter toggle ("Block common profanity & slurs"), custom blocked words/phrases, and known stream bot filtering (`Nightbot`, `StreamElements`, `Fossabot`, `Moobot`, `Sery_Bot`, etc.) with per-viewer allow/ignore overrides.

<p align="center">
  <img src="assets/screenshots/template-help-modal.png" alt="TownCrier Stream Alert Template Variables Modal" width="700">
</p>

---

## Getting Started

1. **Download and Launch**:
   - Download `TownCrier.exe` (Windows) or `towncrier` (Linux) from [Releases](https://github.com/mrpoundsign/TownCrier/releases).
   - On first launch, TownCrier sets up the default voice pack automatically before opening the Dashboard.
2. **Configure Audio Output**:
   - Open **Settings → General & Audio** and select your playback device (e.g., a virtual audio cable configured as an Audio Output Capture source in OBS).
3. **Connect Your Stream**:
   - In **Settings**, open the **Twitch**, **YouTube**, **TikTok**, or **Kick** sub-tab, enter your channel username or handle, and click **Enable**.
   - Watch the platform connection badge in the top header illuminate when connected.
4. **Monitor & Manage**:
   - Use the **Dashboard** to monitor active speech, skip or pause messages, and assign custom voices to regular chatters.
   - Use **Logs → Live Logs** to pop out real-time chat overlay windows or review past broadcasts in **Logs → Search History**.

---

## License & Legal

TownCrier is licensed under the **GNU Affero General Public License v3.0 (AGPL-3.0)**. See [`LICENSE`](LICENSE) for the full license text and [`THIRDPARTY_NOTICES.md`](THIRDPARTY_NOTICES.md) for third-party library and model attributions.

- [Privacy Policy](https://mrpoundsign.github.io/TownCrier/privacy.html)
- [Terms of Service](https://mrpoundsign.github.io/TownCrier/terms.html)
