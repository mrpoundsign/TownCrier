# TownCrier — Agent & Project Knowledge Base (AGENTS.md)

## 1. User Preferences & Working Rules

> [!IMPORTANT]
> Always adhere strictly to these core working preferences:
> 1. **True Pair Programming Assistant (No "Vibe Coding")**: The user is an experienced, knowledgeable developer and lead. Never act like an autonomous black box. When investigating an issue or unexpected behavior, always communicate technical findings, underlying root causes, and mechanics to the user *first*. Discuss and align before creating GitHub issues or making code changes.
> 2. **Feedback & Explicit Approval on Decisions**: Always present clear options, findings, and proposed designs, then wait for user approval before moving forward. Never jump ahead to file tickets, create branches, or write code based on assumptions. Once a specific plan is approved, executing that agreed plan autonomously is expected, but any new findings, deviations, or decisions must pause for communication and approval.
> 3. **One ticket at a time**: We work on exactly one issue/ticket at a time unless explicitly directed otherwise. Exactly **1 commit per issue**.
> 4. **Pre-Commit Quality Gate**: Before every single commit, ALWAYS run:
>    - `golangci-lint-v2 run ./...` (must report 0 issues)
>    - `go fix --diff ./...` (must report 0 diff)
>    - `go test -count=1 -v ./...` (all unit tests must pass)
> 5. **User tests GUI before committing**: Always stop and let the user manually test the GUI before any changes are committed to git.
> 6. **Prioritize Built-in Agent Tools Over Shelling Out**: Always use built-in tools (`grep_search`, `list_dir`, `view_file`) for searching code, finding files, and inspecting contents. Do not shell out when built-in tools are capable of performing the operation. Only shell out via `run_command` when strictly necessary (e.g., git operations, building/running code, tests, or `gh` CLI).
> 7. **Build Output Location**: All temporary binaries, DLLs, and build artifacts belong in `build/` (e.g., `build/bin/`), which is gitignored. Never commit binaries or model weights.
> 8. **Planning Repository Independence**: `planning/` is its own local-only git repository (no remote) ignored by the main repo. Planning commits happen independently inside `planning/` referencing ticket numbers.
> 9. **Always Link PRs, Issues, and CI Runs**: Whenever creating or referencing GitHub Pull Requests, Issues, or Actions CI runs, always include clickable Markdown links so the user can watch or inspect them directly.
> 10. **GitHub CLI Issue & PR Bodies via Files**: PowerShell mangles markdown backticks and escapes (e.g., `\t`, `\f`) when passing strings inline to `gh`. NEVER pass multiline issue or PR descriptions inline via `--body`. ALWAYS write markdown content to a temporary file in `build/` and pass `--body-file`, then clean up the temporary file.
> 11. **Strict Test Configuration & Directory Isolation**: NEVER write tests that use or default to real configuration locations (`%APPDATA%`, `~/.config`, etc.). Tests must ALWAYS use `config.NewMemoryStore()` or `config.TestLocations(t.TempDir())`. Attempting to load or save default/production config locations during test execution is strictly forbidden and will fail immediately.
> 12. **Explicit Approval for Pull Requests**: Never open a Pull Request without explicit user approval. 1 commit per ticket is an aspirational goal, not a hard requirement, and multiple commits or tickets can live on the same branch or be merged directly into `dev`.
> 13. **Release Merges to Main via Pull Request**: Releasing from `dev` to `main` always occurs via a GitHub Pull Request (`dev` → `main`) listing `Closes #<id>` for all completed issues. This leverages GitHub's native closing automation without external scripts and seamlessly updates GitHub Projects Kanban cards to Done.

---

## 2. Project Overview & Mission

**TownCrier** is an open-source Windows desktop application (Go + Fyne) that reads livestream chat messages and stream events aloud using free, local neural TTS (Kokoro-82M by default) played through a streamer-selected audio device to OBS.

- **Primary Repository**: `github.com/mrpoundsign/TownCrier`
- **License**: GNU Affero General Public License v3.0 (AGPL-3.0)
- **Visual Identity**: Obsidian Slate (`#0F172A`), Chrome White (`#F8FAFC`), and Electric Sky Blue (`#38BDF8`).
- **Core Technology Stack**:
  - **Go**: 1.27
  - **GUI Toolkit**: [Fyne v2](https://fyne.io/)
  - **TTS Engine**: [sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx) Go bindings with Kokoro-82M ONNX models running on DirectML GPU (`model.onnx`) or multi-threaded CPU fallback (`model.int8.onnx`)
  - **Audio Output**: [malgo](https://github.com/gen2brain/malgo) (miniaudio Go bindings) targeting WASAPI playback devices with gain control and instant atomic panic mute (<50ms)
  - **Secret Storage**: [go-keyring](https://github.com/zalando/go-keyring) (Windows Credential Manager) with memory fallback
  - **Cross-Compilation**: WSL Ubuntu compiling to Windows amd64 using `x86_64-w64-mingw32-gcc` and `-static-libgcc`

---

## 3. Architecture & Codebase Map

```
TownCrier/
├── assets/               # Application icons and bundled media
├── build/                # Local build output and temporary binaries (gitignored)
│   └── bin/              # Cross-compiled executables and runtime DLLs
├── cmd/
│   ├── installer/        # Fyne GUI installer & uninstaller (-tags nosherpa, via appinstaller)
│   ├── launcher/         # Single-file portable executable self-extracting launcher
│   ├── spike/            # Technical spike & audio engine validation CLI
│   └── towncrier/        # Main desktop application GUI entry point (main.go)
├── internal/
│   ├── app/              # Application lifecycle, session coordination, and wiring
│   ├── audio/            # Audio device enumeration (WASAPI), mixing, gain, emergency abort
│   ├── bots/             # Known-bot matcher, embedded bot list, ignore/allow overrides
│   ├── chat/             # Unified event model, emote stripping, phonetic filtering, voice router
│   ├── config/           # Configuration schema, global rules, platform overrides, atomic persistence
│   ├── platform/         # Platform connectors (Twitch EventSub, YouTube, Kick, TikTok)
│   ├── queue/            # Priority speech queue with 30s staleness TTL and fast catchup
│   ├── secrets/          # OS Keyring credentials management wrapper
│   ├── tts/              # Offline neural TTS engine, voice management, model downloader
│   └── ui/               # Fyne GUI components (dashboard, voices, viewers, settings, platform tabs)
├── models/               # Local ONNX model weights and voice files (gitignored)
├── planning/             # Local-only planning repository (PRDs, task lists, research)
├── build.sh              # WSL cross-compilation pipeline script
├── .golangci.yml         # Golangci-lint configuration
└── FyneApp.toml          # Fyne application package metadata
```

---

## 4. Key Architectural Patterns

1. **Global Rules with Platform Overrides (D42)**:
   - Chat rules (`speak_names`, `speak_emotes`, `speak_platform_name`, default voice/speed) are defined globally on the Settings tab.
   - Each platform tab inherits global rules with optional checkboxes to override per service.
2. **Unified Event Pipeline**:
   - All platform connectors translate inbound messages into a normalized `chat.Event`.
   - The message filter cleans text (stripping emotes if disabled, collapsing repeated punctuation/characters, filtering blocked words).
   - The voice router determines voice: `Viewer Override` → `Event Voice` → `Platform Override Voice` → `Global Default Voice`.
3. **Emergency Panic Mute (<50ms)**:
   - UI panic button and global hotkey (`Ctrl+Shift+M` or `Esc`) flip an `atomic.Bool` abort flag and flush the speech queue.
   - The miniaudio device callback immediately zeroes output buffers in <15µs, halting audio instantly without crashing the audio thread.
4. **Queue Staleness Protection (D30)**:
   - Chat messages older than 30s in the queue are dropped during chat bursts; paid/event messages are never discarded.
   - Fast catchup mode speeds up speech to 1.3x if the queue exceeds 5 items.
5. **DirectML GPU vs. CPU Model Selection**:
   - `model.onnx` (FP32, ~345MB) is for DirectML GPU execution.
   - `model.int8.onnx` (INT8, ~134MB) is for multi-threaded CPU execution.
   - Never run `model.int8.onnx` on DirectML (DirectML dequantizes int8 in shaders or falls back to CPU, giving ~0% GPU usage). `NewSherpaEngine` automatically switches model files and falls back to CPU if DirectML is unavailable.
6. **Launch Status Window & Async Subsystems**:
   - ONNX model loading takes 1–2s. Always display the lightweight Launch Status Window (`internal/ui/launcher.go`) immediately while initializing subsystems in the background, transitioning to the main window only when ready.
7. **Headless & Test Driver Constraints**:
   - `malgo.InitContext` / audio enumeration can hang in headless Linux/WSL without ALSA/PulseAudio. Always inject mock device enumerators (`view.SetDeviceEnumerator`) in tests.
   - Never run Windows `.exe` GUI binaries interactively in WSL bash (causes terminal hang). Let the user launch `build/bin/TownCrier.exe` from Windows.
8. **Frequency-Gated Audio Cache (Issue #21)**:
   - Chat messages follow a long-tail distribution. Never cache one-offs. Use frequency gating (promote to disk in `%LOCALAPPDATA%\com.github.mrpoundsign.towncrier\cache\audio\` only when count ≥ 2 or for alert templates) to maintain near-zero RAM usage.
9. **Unified Installer, Portable Launcher & Self-Updater (`@skill:goreleaser-installer-updater`)**:
   - Uses `github.com/mrpoundsign/appinstaller` (`../appinstaller`) and `.goreleaser.yaml` (GoReleaser v2 OSS).
   - `cmd/installer` is compiled with `-tags nosherpa` so `TownCrier-Setup.exe` runs as a standalone Fyne setup wizard with zero external DLL dependencies while still reusing `internal/tts` to optionally pre-download Supertonic/Kokoro model packs.
   - Startup update checks suppress modal dialogs whenever `session.IsStreamLive()` is true, showing only the header bar `"⬆ Update"` badge and status message.
