# Changelog

## Unreleased

---

## v0.0.3 — 2026-10-18

### Added
- **Chat Logs Tab & Local Event History**: Added a top-level **Chat Logs** tab backed by a local SQLite database (`events.db`) that records chat messages, stream alerts, moderation events, filter outcomes, and speech playback status across Twitch, YouTube, TikTok, and Kick. Includes a compact **Live Tail** mode, a **Hide Bots** filter (enabled by default), date separator banners, hoverable status/badge/platform icons, and a collapsible search and filter drawer.
- **Custom Bot Usernames**: Added a custom bot list in **Settings → Rules & Safety → Bot Filters** so streamers can add custom bot accounts that are ignored by TTS and hidden when **Hide Bots** is active.
- **Inline Platform Emotes**: Renders custom Twitch, YouTube, and Kick emotes as inline `20×20` images in both the **Dashboard** recent messages feed and the **Chat Logs** table, with automatic memory and disk caching (`cache/emotes/`).
- **Configurable Log Retention**: Added customizable retention windows (`0–365 days`, defaulting to `30 days` for chat and `90 days` for stream alerts) in **Settings → Event Logs** with automatic background pruning and one-click manual pruning.
- **Linux Desktop Support**: Added single-file portable Linux (`amd64`) builds with PipeWire, PulseAudio, and ALSA audio output support and standard XDG config/cache paths (`~/.config/com.github.mrpoundsign.towncrier/`).
- **Multi-Model Voice Engine**: Run up to five local TTS voice packs at the same time—Supertonic (new default, 10 voices), Kokoro-82M (11 voices), Kitten Mini (8 voices), Kitten Nano (8 voices), and Piper LibriTTS-High (904 speakers)—and assign voices from any loaded pack to chatters, alerts, or platforms.
- **Hardware Presets & Resource Impact Meters**: Added one-click presets (*Ultra-Light*, *Fast & Lean*, *Studio Quality*, *Crowd Mode*, and *Full Arsenal*) in **Settings**, paired with color-coded stacked bars showing each model pack's share of RAM, CPU, and GPU usage.
- **Optional Kokoro GPU Acceleration**: Moved DirectML GPU acceleration to a dedicated sub-option under **Kokoro-82M**. The larger FP32 GPU model now downloads only when GPU acceleration is turned on.
- **In-Use Voice Pack Protection**: Unchecking a voice pack in **Settings** now checks whether any of its voices are assigned to chatters, alerts, or platform defaults and prevents unloading the pack while its voices are in use.
- **Idle Model Auto-Unload**: Added an **Idle Model Auto-Unload** setting (`0–60 min`) to automatically release TTS models from RAM during quiet periods and reload them on the next message.
- **Display & UI Scale Selector**: Added a **UI Scale** setting (`100%`, `115%`, `125%`, `150%`, or `Auto`) in **Settings → General & Audio** that resizes the interface immediately without restarting.

### Changed
- **Settings Sub-Tabs**: Organized **Settings** into four sub-tabs (*General & Audio*, *TTS & Hardware*, *Rules & Safety*, and *Event Logs*) for easier navigation.
- **Lower Memory Footprint**: Reclaims unused OS working set memory after model loads and unloads old models before loading replacements when applying changes in **Settings**.
- **Natural Sentence Streaming**: TTS streaming now splits text only on complete sentences instead of mid-sentence word counts, fixing unnatural pauses and clipped words in Supertonic and Kitten voices.

### Fixed
- **Multi-Monitor DPI Scaling**: Locked default DPI scaling so dragging the TownCrier window between monitors with different screen sizes or DPI settings (such as a laptop screen and an external desktop monitor) no longer triggers unexpected zoom or font size jumps.

---

## v0.0.2 — 2026-10-16

### Added
- **YouTube Live Support**: Connect to YouTube streams by `@handle`, channel URL, or video URL to read live chat, Super Chats, Super Stickers, and New Member events without an API key.
- **TikTok Live Support**: Connect to live TikTok rooms by `@username` to read chat, follows, shares, and gifts, with automatic 2-second gift streak grouping.
- **Kick Support**: Connect to Kick channels to read live chat, subscriptions, gifted subscriptions, and moderation events.
- **Platform Tab Toggles**: Show or hide individual streaming platform tabs from **Settings**.
- **Automatic First-Launch Model Download**: Downloads and verifies voice models automatically in the startup window on first run.

### Fixed
- Fixed mouse wheel scrolling getting stuck when hovering over unfocused text fields.

---

## v0.0.1 — 2026-10-15

### Added
- Initial Windows release featuring Twitch chat and EventSub channel alerts, offline neural TTS (Kokoro-82M), WASAPI audio output selection, global Panic Mute hotkey, two-tier priority speech queue, bot filtering, and per-viewer custom voices.
