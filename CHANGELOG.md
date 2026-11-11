# Changelog

## v0.0.12 — 2026-11-10

### Added
- **OBS Browser Source Overlays & Visual Style Designer (#123)**:
  - Added `GET /overlay/tts` ("Now Speaking" Caption Overlay) with transparent Obsidian Slate styling, platform badges, voice tags, animated audio equalizer bars, and live position switching (`Bottom` / `Top`).
  - Added `GET /overlay/chat` (Filtered Live Chat & Alerts Overlay) powered by Server-Sent Events (`GET /api/overlay/stream`) and initial snapshot hydration (`GET /api/overlay/chat`), supporting live selection of any saved Live Log preset, optional Events Summary bar, and primary translated text with `🌐` original foreign text subtitle.
  - Added an interactive **Visual Overlay Style Designer** (`GET /overlay/designer`) with color pickers, opacity sliders, element visibility toggles (platform tags, platform-colored usernames, TTS status icons `🗣️`/`⏳`, original foreign text line `🌐`, voice badge, card borders), font scale, and corner radius — auto-saving directly to `config.json` and live-refreshing open OBS Browser Sources on the fly.
  - Added granular local web server feature toggles (`TTSOverlayEnabled`, `ChatOverlayEnabled`, and `Enabled` for External Control API & Elgato Stream Deck) so the local HTTP/WS server only runs when at least one feature is enabled and enforces strict route-level access control.
- **Anti-Spam & Chat Hygiene Filters (#126)**:
  - Added **Strip Zalgo & Excessive Combining Marks** (`strip_zalgo`) to remove stacked Unicode combining diacritics while preserving normal accented characters.
  - Added **Strip Braille & ASCII Art Spam** (`strip_ascii_art`) to strip Braille blank/dot art (`U+2800`–`U+28FF`), box-drawing/block art (`U+2500`–`U+259F`), and repeated symbol art before TTS synthesis.
  - Added **Multi-User Copypasta Deduplication** (`deduplicate_copypasta_window_seconds`: `Off`, `15s`, `30s`, `45s`, `60s`, `90s`, `2m`, `5m`, default `30s`) to suppress identical normalized chat messages repeated across multiple chatters while never dropping paid or alert events.
- **Clickable Chatter Profile Links**:
  - Made chatter usernames clickable with hover underlines across the Dashboard and Event Logs to open the viewer's Twitch, YouTube, Kick, or TikTok channel profile in the browser.

### Changed
- **Settings Tab Reorganization**:
  - Reorganized **Settings** into clearer sub-tabs: moved **Global Default Voice**, **TTS Output Mode & Auto-AFK**, and **Audio Output & Microphone Priority** into **TTS & Hardware**, and created a dedicated **OBS Overlays & External Control** card on **General**.

---

## v0.0.11 — 2026-11-09

### Added
- **Microphone Voice Activity (VAD) Gate & Audio Ducking (#124)**:
  - Added a low-latency WASAPI/miniaudio microphone capture monitor (`pkg/audio/capture.go`) with real-time RMS level metering, threshold detection (`-60 dB` to `0 dB`, default `-35 dB`), and configurable hold release delay (`100ms` to `2000ms`, default `400ms`).
  - Added two streamer speech priority modes in **Settings → Audio Output → Microphone Priority (Duck / Pause While Speaking)**:
    - **Duck TTS Volume**: Smoothly attenuates TTS output volume to a configurable ducking level (`5%`–`80%`, default `20%`) using click-free exponential per-sample smoothing (`pkg/audio/player.go`) whenever the streamer speaks into their microphone.
    - **Pause TTS Queue**: Holds new TTS messages from starting while the streamer is actively speaking (and pauses active playback buffer output) until the hold timer expires.
  - Added a live microphone dB meter and **🎙️ Speaking — Gating TTS** status indicator in the Settings tab.
- **Automatic Username Naturalizer (#121)**:
  - Added `chat.NaturalizeUsername` (`pkg/chat/filter.go`) to convert screen-name syntax into natural, human-sounding speech before TTS synthesis: strips decorative wrapper borders (`xX_ShadowWolf_Xx` → `Shadow Wolf`), splits `snake_case`, `kebab-case`, and `dot.separated` handles, splits `CamelCase` and acronym boundaries (`DarkKnight`, `TVGuy`), strips clan tags (`[CLAN]`, `{VIP}`), and drops trailing 3+ digit discriminators (`Wolf1998` → `Wolf`, `Alex_9821` → `Alex`) while preserving meaningful numbers (`Agent007`, `Player1`, `Wolf2`) and custom Pronunciation Dictionary overrides.
  - Added global and per-service **Naturalize Usernames** checkboxes (`speak_natural_names`, enabled by default).
- **Permitted Chatter Voices Selector & Per-Service Auto-Gender Overrides**:
  - Added an interactive **Permitted Auto-Assign Voices** checklist (`AllowedChatterVoices`) with **All**, **None**, **♀ Feminine**, and **♂ Masculine** quick-filter buttons, search filtering, and inline **🔊** voice preview buttons so streamers can curate the exact pool of voices used for deterministic chatter assignment.
  - Added per-service **Smart Gender Voice Matching** override controls on all platform tabs (Twitch, YouTube, Kick, and TikTok).
- **Voice Model Update Detection & 1-Click Updating in TTS Settings**:
  - Added `CheckInstalledModelsWithUpdates` (`pkg/tts/model_manager.go`) comparing installed `<modelDir>/.manifest.json` archive SHA-256 digests against the latest upstream GitHub Release digests (`k2-fsa/sherpa-onnx@tts-models`).
  - Added an **🔄 Update Available** status badge and **🔄 Check for Model Updates / ⬇ Install Model Updates** button in **Settings → TTS Engine & Model Management** to download, verify, extract, and hot-reload updated voice model packs in-place.

### Fixed
- **WASAPI Audio Output & Microphone Capture Device Routing**:
  - Fixed a device ID matching bug (`pkg/audio/device.go`) where `malgo.DeviceID` (which implements `fmt.Stringer` returning hex) was double-hex-encoded by `fmt.Sprintf("%x", dev.ID)` during device enumeration while `DevicePlayer` and `CaptureMonitor` compared against single-hex `hex.EncodeToString(dev.ID[:])`, causing selected non-default output/capture devices to silently fall back to the OS default device. Includes full backwards-compatible matching for existing saved `config.json` device IDs.

---

## v0.0.10 — 2026-11-09

### Added
- **Smart Gender Voice Matching & Viewer Pronoun Cache**:
  - Added shared viewer pronoun & gender inference (`pkg/chat/gender.go`, `pkg/chat/pronouns.go`) combining an asynchronous Alejo.io Twitch Pronoun API lookup cache, self-referential pronoun regex scanning, and an offline 360+ common first-name lexicon to automatically assign matching feminine, masculine, or neutral TTS voices to chatters.
- **Launch Window `"Skip & Launch App"` Recovery Option**:
  - Added a **Skip & Launch App** button on the Launch Status Window whenever an optional TTS model download fails at startup so users are never trapped in a `Retry / Quit` loop and can immediately enter the main application with their installed fallback models.
- **macOS `TownCrier.app` Application Bundle Packaging**:
  - Updated macOS release packaging (`./build.sh package-mac`) to assemble a complete, ad-hoc signed `TownCrier.app` bundle (`Contents/MacOS/TownCrier`, `Contents/Resources/icon.icns`, and `Contents/Info.plist`) inside `TownCrier-v0.0.10-darwin-arm64.tar.gz`.

### Fixed
- **Dynamic GitHub Release SHA-256 Digest Verification for Voice Models**:
  - Removed fragile hardcoded SHA-256 constants for `k2-fsa/sherpa-onnx` voice model archives (`tts-models` mutable release tag) in favor of querying GitHub's official Release API asset `digest` (`sha256:...`) dynamically during download and recording verified archive hashes in `<modelDir>/.manifest.json`. This fixes the Kokoro-82M SHA-256 mismatch caused by upstream re-uploading `kokoro-multi-lang-v1_0.tar.bz2` and `kokoro-int8-multi-lang-v1_0.tar.bz2` in-place.

---

## v0.0.9 — 2026-11-09

### Added
- **Automatic Foreign Chat Language Detection, Cascading Translation & `文A` Toggle (#119)**:
  - Added multi-provider foreign chat translation (`MyMemory`, `Google Cloud Translation`, `DeepL`, `LibreTranslate`, and `Local LLM / Ollama`) with automatic source language detection, configurable priority failover / round-robin load balancing, exponential backoff, and two-tier sentence caching (LRU memory + SQLite).
  - Added optional pre-speech translation indicator chime and inline `文A` original/translated text toggle in the Dashboard and Event Logs.
- **Coalesce Consecutive Spoken Messages from the Same Viewer (#122)**:
  - Added configurable **Coalesce Consecutive Messages** window (`Off`, `15s`, `30s`, `60s`, `90s`, `2m`, `5m`, default `45s`) globally and per platform so follow-up messages from the same uninterrupted speaker omit repeating `"<user> says"` while resetting automatically on intervening speakers, stream alerts, moderation events, or Panic Mute.
- **Guided Replacement Voice Assignment Wizard (#44)**:
  - Added an interactive **Reassign Voices to Disable Model Pack** wizard modal when unchecking an in-use TTS model pack or switching to a Performance Preset that disables an in-use pack, supporting quick bulk reassignment, voice previewing, per-item voice selection, and clearing custom overrides to inherit the Global Default Voice.
- **Portable `.zip` Backup Export, Selective Import & Schema Snapshots (#129)**:
  - Added unified `.zip` backup export (`towncrier-backup-YYYY-MM-DD-HHMMSS-v<version>.zip`) packaging scrubbed configuration (`config-backup.json`), WAL-checkpointed SQLite Event Log database (`events.db`), and optional application logs (`towncrier.log`).
  - Added `.zip` and `.json` backup import supporting Selective Merge (per-section toggles), Full Restore (with local PC audio device, hardware preset, and file path preservation), and in-place `events.db` restoration.
  - Added automatic schema-versioned snapshots (`config.YYYY-MM-DD-HHMMSS-v<schema>.json` and `events.YYYY-MM-DD-HHMMSS-v<schema>.db`) and startup schema regression detection for safe downgrades.
- **Documentation & Privacy Disclosures**:
  - Added comprehensive Features & Configuration Guide (`docs/features.html`), sticky left sidebar navigation across the documentation site, and updated Privacy Policy disclosures covering optional translation providers and local backups.

### Changed & Optimized
- **Event Log Scrolling, `Config.Clone` & UI Hot-Path Optimizations (#130)**:
  - Replaced JSON marshal/unmarshal round-tripping in `(*Config).Clone()` with a native deep copy (`slices.Clone` / `maps.Clone`) and added zero-allocation `Session.ChatScale()`, eliminating 87% of UI thread CPU time when scrolling the Event Logs.
  - Cached row geometry and `MinSize` on `*logTableLayout` and skipped redundant `Move`/`Resize` passes in `panelWidthClampLayout` during scroll ticks.
  - Added thread-safe in-memory read caching to `secrets.KeyringStore` to avoid repeated OS Credential Manager syscalls when rendering log moderation icons.
  - Debounced master volume slider config persistence and avoided re-synchronizing platform connectors on volume adjustments.
  - Eliminated redundant synchronous SQLite queries when switching to the Settings or Logs tabs, and deferred `DashboardView` activity feed widget rebuilds while the Dashboard tab is inactive.
  - Added `-pprof` and `-cpuprofile` CLI flags to `cmd/towncrier` for runtime performance profiling.

---

## v0.0.8 — 2026-11-06

### Added
- **TTS Output Modes, Auto-AFK Detection & Global Mode Toggle Hotkey (#109)**:
  - Added three TTS Output Modes (`Always On`, `Auto-AFK`, and `Always Off`) selectable from the Dashboard header and Settings.
  - Added cross-platform OS input inactivity detection (`GetLastInputInfo` on Windows, X11/idle fallback on Linux/macOS) to automatically enable TTS while away from keyboard (`Auto-AFK`) and immediately silence/flush pending speech when returning to the keyboard/mouse.
  - Added configurable global hotkey (`Ctrl+Shift+T` by default) to cycle TTS Output Modes from anywhere.
- **Alert Sound Engine for Inactive TTS (#110)**:
  - Added built-in 24kHz harmonic chime synthesizer, native RIFF/WAVE decoder, and pure-Go MP3 decoder (`github.com/yatagai-mm/byrd`) with 3-second cooldown protection to play a subtle audio notification when chat messages arrive while TTS speech is inactive (`Always Off` or `Auto-AFK` while active at desk).
- **Dual-Mode Protocol Buffers WebSocket & HTTP REST Control Server (#111)**:
  - Added local control server (`internal/ws`) supporting both binary Protocol Buffers (`proto/control/v1/control.proto` via `/ws?format=protobuf`) and JSON (`/ws`), plus HTTP REST endpoints (`/api/status`, `/api/speak`, `/api/mode`, `/api/panic`, `/api/queue`, `/api/volume`) with optional Bearer token authentication.
- **Elgato Stream Deck Plugin, Version Checking & 1-Click Installer (#112)**:
  - Added native TypeScript Elgato Stream Deck plugin (`com.github.mrpoundsign.towncrier.sdPlugin`) communicating over binary Protocol Buffers WebSocket with 5 hardware actions (`TTS Output Mode`, `Emergency Panic Mute`, `Speech Queue Control`, `Custom TTS Announcement`, and `TTS Master Volume` with Stream Deck + rotary dial support) and dynamic Obsidian Slate SVG key rendering.
  - Added 1-click **Install / Update Elgato Stream Deck Plugin** button with installed version checking in Settings > General & Audio and as an optional component in `TownCrier-Setup.exe`.
- **Unified Setup Installer, Uninstaller & SHA-256 Self-Updater (#113)**:
  - Integrated `github.com/mrpoundsign/appinstaller` to build `TownCrier-Setup.exe` (`-tags nosherpa`) with optional TTS voice model pre-downloading and SHA-256 verified GitHub Release self-updates.
- **Optional `@mention` & Reply Filtering with Alert Sound (#116)**:
  - Added global and per-platform filtering to ignore `@mentions` and replies directed at other users (`Off`, `Ignore Replies Only`, `Ignore Replies & Leading @Mentions`, `Ignore All @Mentions & Replies`) while still speaking messages mentioning the streamer's own channel name.
  - Added an optional toggle to play the notification alert sound when a message is skipped by the `@mention`/reply filter.
- **Granular Per-Setting Global vs. Override Pulldowns on Platform Tabs (#118)**:
  - Replaced the single all-or-nothing platform override checkbox across Twitch, YouTube, Kick, and TikTok tabs with individual `Use Global (...)` / `Override` pulldowns for each chat rule (`Speak Names`, `Speak Platform Name`, `Speak Emotes & Emojis`, `Deduplicate Emotes`, `Max Duplicate Words`, and `Ignore @Mentions & Replies`).
- **Redesigned Application, Website & Stream Deck Icons**:
  - Updated `assets/icon.svg`, `assets/icon.png`, `assets/icon.ico`, `docs/icon.png`, and the Stream Deck plugin icon to a high-contrast Obsidian Slate & Electric Sky Blue vintage analog megaphone with conically-skewed Varsity `TC` lettering.

### Fixed
- **YouTube Private & Testing Stream Support via OAuth (#117)**:
  - Added YouTube Data API v3 `liveBroadcasts.list(mine=true)` fallback and `liveChat/messages` polling when OAuth is connected so private and unlisted testing streams are detected and read aloud alongside public InnerTube streams.
- **TikTok Live Offline Polling Quota Protection (#114)**:
  - Added lightweight HTML room status pre-check before invoking `gotiktoklive.TrackUser` so offline polling does not consume EulerStream sign API quota, plus detailed WebCast diagnostics.

---

## v0.0.7 — 2026-10-25

### Added
- **Live Stream Detection & Session Resumption (#102)**:
  - Added proactive live stream checking on startup across Twitch, Kick, and YouTube with a 5-second launch timeout.
  - Automatically resumes recent stream log sessions if within a configurable window (default 60 minutes) of the last recorded log entry.
  - Automatically finalizes dangling / crashed sessions older than 1 hour upon stream offline confirmation.

### Changed & Optimized
- **Chat Log Row Rendering Widget Caching (#103)**:
  - Cached parsed row widgets in the Live Log panel to eliminate redundant widget allocations and UI thread lag during chat bursts.

---

## v0.0.6 — 2026-10-25

### Added
- **Cross-Platform macOS (OSX) Support (#105)**:
  - Added native macOS build automation (`./build.sh mac` and `./build.sh package-mac`) and GitHub Actions release jobs on `macos-latest` (arm64 Apple Silicon and x86_64 Intel) using `github.com/k2-fsa/sherpa-onnx-go-macos`.
  - Added `DYLD_LIBRARY_PATH` runtime library resolution to the standalone single-file launcher (`cmd/launcher/main.go`).
  - Implemented dynamic, sorted checksum generation matching `InkAnim` (`checksums.txt` and `SHA256SUMS.txt`).
  - Added automated GitHub Pages (`docs/`) deployment on merges to `main` (`.github/workflows/pages.yml`).
- **Duplicate Word Limit & Once-Per-Sentence Emote/Emoji Spam Protection (#100)**:
  - Grouped Unicode emojis under the "Speak Emotes & Emojis" rule (`SpeakEmotes`); disabling it strips both platform emotes and Unicode emojis from TTS.
  - Enforced strict once-per-sentence deduplication for emotes and emojis to collapse bursts (`"😂😂😂😂"`) and repeating patterns (`"😂🔥😂🔥"`, `"Kappa LUL Kappa LUL"`) to a single occurrence while allowing fresh occurrences in subsequent sentences.
  - Added consecutive duplicate words capping (default max 2 repeats, e.g. `"what what what what"` &rarr; `"what what"`), preserving sentence punctuation.
  - Added `DeduplicateEmotes` and `MaxDuplicateWords` controls in Settings > Global Chat Rules with full per-platform override support across Twitch, Kick, YouTube, and TikTok tabs.
- **Chat Log Moderation Controls across Twitch, YouTube & Kick (Epic #90, #91, #92, #93)**:
  - **Twitch**: Added Delete Message, Timeout Chatter (1m, 5m, 10m, 1h, 24h), and Permanent Ban actions directly from Chat Log hover icons and context menus. Integrated Twitch AutoMod Held-for-Review queue with Approve/Deny buttons, automatic local TTS speech purging, and auto-disconnection on 401 token invalidation.
  - **YouTube**: Added YouTube Desktop OAuth 2.0 PKCE with local loopback callback, multi-profile selector with Google avatar and channel name display, and Chat Log moderation (Delete Message, Timeout 5m, Permanent Ban/Hide Chatter).
  - **Kick**: Added Kick OAuth 2.1 PKCE authorization with PKCE code challenge and token persistence in Windows Credential Manager. Added Kick Public API v1 channel and live stream detection (`/public/v1/channels/{slug}` and `/public/v1/livestreams`), plus Chat Log moderation (Delete Message, Timeout Chatter, Ban User).
  - **Local TTS Mute**: Added a one-click local TTS mute toggle per chatter to immediately silence and drop future speech without affecting platform moderation.
- **Twitch Full Helix & EventSub WebSocket Mode (#95)**:
  - Added authenticated Twitch Helix API integration and EventSub WebSocket connection when OAuth is connected, automatically resolving broadcaster channel name and hiding manual channel input.
- **YouTube Gifts (Jewels) Support (#94)**:
  - Added YouTube Gift (Jewels) detection and parsing in the InnerTube chat parser (`liveChatSponsorshipsGiftPurchaseAnnouncementRenderer` / jewels payload), with notification alerts, speech templating, and live log rendering.
- **Independent Chat Panel Scaling (#89)**:
  - Added independent chat panel scaling controls (`ChatScale`) with quick presets (`80%`, `100%`, `125%`, `150%`) and custom percentage adjustment for Dashboard and Pop-out Live Log windows.

### Fixed
- **Thread Safety in Emote Widgets (#105)**:
  - Added read-write mutex synchronization to `emoteInlineWidget` to eliminate data races between asynchronous emote downloads and concurrent Fyne widget layout passes.
- **Shutdown Deadlock, UI Live-Tail Storm & Single-Instance Guard (#96)**:
  - Released `Session.mu` before invoking connector and queue `Start`/`Stop`/`Close` methods to eliminate lock-inversion deadlocks during application shutdown and config reloads.
  - Coalesced live-tail UI rebuilds with a 15ms debounce window and deferred widget rebuilds while the Logs tab is hidden to eliminate UI thread lockups during chat bursts.
  - Added a 3-second shutdown watchdog in `MainWindow.SetCloseIntercept` to guarantee window close never hangs.
  - Added an OS-level single-instance process guard (`Local\TownCrier_SingleInstance_Mutex` on Windows, flock on Unix) to prevent accidental duplicate running instances.
  - Fixed Kick Pusher error 4001 loops with exponential backoff, updated Pusher app key, and added channel ID lookup link.
- **Strict Configuration Test Isolation & Explicit Locations (#87)**:
  - Replaced implicit empty string (`""`) path fallbacks with an explicit `config.Store` interface and `config.Locations` type (`ProductionLocations()`, `TestLocations()`, `NewMemoryStore()`).
  - Added strict test-time protections preventing tests from ever accessing or overwriting the host user's production configuration in `%APPDATA%` / `~/.config`.
  - Wired `config.Store` into `app.NewSession` and `cmd/towncrier/main.go`, and updated all unit tests across the repository to execute in isolated temporary directories or in-memory stores.

---

## v0.0.5 — 2026-10-22

### Added
- **Stream Event Notifications & Watch Streaks across Platforms (#81)**:
  - **Twitch**: Added event capture and speech synthesis for Watch Streaks (`viewermilestone`), Automatic Channel Point Rewards (`highlighted-message`, `gigantified-emote-message`, `animated-message`), Sub Upgrades & Pay-It-Forward (`primepaidupgrade`, `giftpaidupgrade`, `anongiftpaidupgrade`, `standardpayforward`, `communitypayforward`, `extendsub`), Bits Badge Tier milestones (`bitsbadgetier`), Charity Donations (`charity_donation`), and Chat Announcements (`/announce`).
  - **Twitch EventSub v1 Notifications**: Integrated EventSub `channel.chat.notification` v1 alongside IRC with automatic message ID deduplication to ensure zero duplicated alerts.
  - **YouTube**: Added incoming Raids / Live Redirects (`liveChatBannerRedirectRenderer`), Gift Membership Recipient notices (`liveChatSponsorshipsGiftRedemptionAnnouncementRenderer`), and YouTube Giving Charity Donations (`liveChatDonationAnnouncementRenderer`).
  - **TikTok**: Added Subscription, Resubscription, and Gifted Subscription notification rows in the TikTok tab UI, and captured Stream Shares (`USER_SHARE`).
  - **Kick**: Added Channel Reward Redemptions (`RewardRedeemedEvent` / `ChannelPointRedemptionEvent`) and expanded multi-recipient usernames in `GiftedSubscriptionsEvent`.
  - **Template Help Reference Dialog**: Added `(?)` interactive help buttons next to all notification speech template fields opening a full template variables reference and example preview modal.
- **Blocking Modal Dialog for Voice Model Downloads (#83)**:
  - Added a full-window modal blocking dialog that disables user clicks and keyboard navigation across the entire application while downloading and unpacking voice model packs or switching hardware presets.
  - Features real-time multi-model step tracking (e.g. `Model 1 of 2`), determinate progress bar with downloaded MB / total MB and percentage, clean cancellation/abort handling, automatic dialog dismissal, and seamless TTS multi-engine reload.
- **Optional Text-to-Speech (TTS) & Fast Launch (#84)**:
  - Made neural TTS an optional, independently toggleable subsystem (`config.TTSConfig.Enabled`). When disabled at launch, Kokoro download checks and ONNX neural model RAM allocation are completely skipped (<100ms startup, near-zero baseline RAM footprint).
- **Optional Event Logging (#84)**:
  - Made Event Logging an optional, independently toggleable subsystem (`config.LoggingConfig.Enabled`). When disabled, SQLite event logging and broadcast stream session tracking are bypassed while fully preserving retention policies and database history.
- **TTS-Disabled Outcome Status (#84)**:
  - Inbound chat and alerts arriving while TTS is disabled are logged with outcome `OutcomeTTSDisabled` (`🔇 TTS Disabled`) and surfaced in the live activity feed without entering the speech synthesis queue.
- **Application Features Card & Dynamic UI Removal (#84)**:
  - Added master toggles for TTS and Event Logging in **Settings → General & Audio** that cleanly remove (rather than grey out) non-applicable UI components:
    - Disabling TTS dynamically removes the *TTS & Hardware* and *Rules & Safety* sub-tabs, hides *Global Default Voice* and *Audio Output Device* cards, hides header volume controls and the emergency panic mute button, hides Dashboard playback controls, and hides platform read rules and notification speech templates.
    - Disabling Event Logging dynamically removes the *Event Logs* settings sub-tab and hides the top-level *Logs* header navigation button.

### Changed
- **Memory Impact Graph & Realistic Base App Calibration**:
  - Calibrated the base application memory baseline to 450 MB (accounting for Go runtime, Fyne/OpenGL windowing, Miniaudio WASAPI contexts, and SQLite cache).
  - Recalibrated additive per-model RAM numbers (Kokoro GPU ~850 MB active, Supertonic ~150 MB, Kitten Mini ~170 MB, Kitten Nano ~95 MB, Piper ~115 MB) so stacked memory breakdown graphs and preset labels accurately represent real-world Windows process working set.
- **Relocated Bot Filters to General & Audio (#84)**:
  - Moved the **Bot Filters** card (Ignore Known Stream Bots + Custom Bot Usernames) into **Settings → General & Audio** so bot management remains accessible even when TTS is disabled, ensuring full support for the **🤖 Hide Bots** filter in Event Logs.

---

## v0.0.4 — 2026-10-20

### Added
- **Saved Live Logs & Zero-Chrome Pop-Out Windows**: Added a **Live Logs** sub-tab under **Logs** with savable filter cards (pre-seeded with **Latest Session** and **All Events** defaults) that launch dedicated zero-chrome floating windows with real-time live tail updates and optional per-view Alerts Summary banners.
- **Latest Session Auto-Switching & Multi-Session Search**: Added a **Latest Session** filter option that automatically switches to newly started broadcast sessions as messages arrive, plus multi-session selection (`☑`) in **Search History** to filter across any combination of recorded streams.
- **Stream Sessions & Automatic Live Detection**: Automatically tracks broadcast sessions for Twitch and Kick streams with session start and end times, duration, message volume, alert totals, and unique chatter counts. Existing historical events are automatically grouped into past sessions based on a 4-hour inactivity gap, backed by automated pre-migration database backups (`events.db.bak`).
- **Chat Logs Stream Session Selector & Totals Banner**: Added a **Stream Session** selector dropdown to the **Logs** tab to jump directly between past broadcasts or the current live session, an **HH:MM:SS** stream-elapsed time range filter to isolate specific moments, and a sticky summary banner displaying total events, chat messages, stream alerts, and unique chatter counts.
- **Platform Connection Badges**: Added live platform icons in the top application header with crisp vector status indicator dots and hover tooltips showing real-time connection state for Twitch, YouTube, TikTok, and Kick.
- **Expanded Twitch & Kick Stream Events**: Added full speech synthesis and event logging for Twitch Raids, Channel Points redemptions, Charity donations, and Hype Trains, as well as Kick Subscriptions, Gifted Subs, and Channel Hosts.
- **TikTok & YouTube Gift and Sticker Rendering**: Displays YouTube Super Stickers and TikTok gifts (including Heart Me and Barrage gifts) as inline graphics in the **Dashboard** and **Logs**, powered by automatic TikTok gift catalog synchronization and disk caching.
- **Kokoro v1.0 Voices & International Pack**: Added all Kokoro v1.0 English voices (34 voices across American and British accents) and an optional Kokoro International voice pack supporting Japanese, Mandarin, Spanish, French, Hindi, Italian, and Brazilian Portuguese.
- **Built-in Profanity Filter**: Added a built-in bad words list in **Settings → Rules & Safety** with a single toggle checkbox ("Block common profanity & slurs") to easily suppress inappropriate language without manual entry.
- **TikTok Fan Club Filter & Super Fan Support**: Added a configurable minimum Fan Club level threshold and Super Fan role option in the **TikTok** tab to allow streamers to restrict TTS to loyal community members.
- **Twitch & Kick Follower Role Gating**: Added a **Follower** role checkbox under **Role Gated** on the **Twitch** and **Kick** tabs, backed by an on-demand per-chatter follower status check with a 30-minute TTL cache and instant live follow event promotion.

### Changed
- **Unified Top Navigation Bar & Relocated Emergency Stop**: Consolidated platform connection badges, primary navigation (**Dashboard**, **Viewers**, **Logs**, and **⚙️ Settings**), volume control, and **About** into a single compact header bar, relocated the **Emergency Stop** button to the bottom-right status bar, and moved platform configuration tabs (**Twitch**, **YouTube**, **TikTok**, **Kick**) into **Settings** sub-tabs.
- **High-Contrast Filter Controls & Field Labels**: Updated the Obsidian theme with recessed input wells, crisp control borders, elevated dropdown surfaces, and muted slate field labels so filter inputs and dropdowns clearly stand out from their labels.
- **Modal Voice & Speed Picker**: Replaced wide inline voice dropdowns and speed sliders across the Viewers, Dashboard, and Platform tabs with a compact button (e.g. `Bella · 1.00x`) that opens a dedicated dialog featuring voice search, speed control, and preview audio testing.
- **Streamlined Platform Settings**: Moved the Global Default Voice to **Settings → Rules & Safety** and tucked platform-specific override settings behind their toggle checkboxes across all platform tabs, keeping tabs clean and focused.
- **Sub-Role Visual Hierarchy**: Indented subscriber, moderator, and fan club role options under their parent settings in platform tabs for clearer visual grouping.

### Fixed
- **Chat Logs Platform Filter & Retention Query Plan**: Fixed platform filtering returning no results in **Logs** by normalizing platform names and optimizing the SQLite retention cutoff query plan.
- **Settings Minimum Width, UI Scale Resizing & Windows Maximize**: Clamped horizontal `MinSize` across scrollable panels so the window can be resized freely and maximized on Windows, and preserved physical window dimensions when changing **UI Scale** (`100%`–`150%`).
- **YouTube Live Stream Discovery**: Verified `videoDetails.isLive` during YouTube `/live` channel discovery so archived VODs or premieres are not mistaken for active live broadcasts.
- **Live-Updating Alerts Summary Banner**: Updated the **Logs** Alerts Summary banner in real time as incoming cheers, subscriptions, gifts, and raids arrive.

### Performance
- **Instant Chat Log Search (FTS5 Trigram)**: Upgraded chat log search with an SQLite FTS5 trigram full-text search index, query fast-paths, and debounced asynchronous background execution for instantaneous filtering across tens of thousands of archived messages.

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
