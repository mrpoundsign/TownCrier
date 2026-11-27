#!/usr/bin/env bash
set -euo pipefail

# TownCrier Build Script (Windows, Linux & macOS)
# Usage:
#   ./build.sh          # Run lint, test, build Windows & Linux binaries, and package into dist/
#   ./build.sh win      # Build Windows GUI executable into build/bin/TownCrier.exe
#   ./build.sh linux    # Build Linux GUI executable into build/bin/towncrier
#   ./build.sh mac      # Build macOS GUI executable into build/bin/towncrier
#   ./build.sh wasm     # Build PoundControl WASM plugin into build/bin/poundcontrol-towncrier.wasm
#   ./build.sh lint     # Run go fix --diff and golangci-lint-v2
#   ./build.sh test     # Run unit tests
#   ./build.sh proto    # Regenerate Protobuf Go bindings from proto/
#   ./build.sh package  # Package zip/tar.gz distributions into dist/
#   ./build.sh package-mac # Package macOS distribution into dist/
#   ./build.sh clean    # Clean build and dist outputs

TARGET="${1:-all}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_DIR="$ROOT_DIR/build"
BIN_DIR="$BUILD_DIR/bin"
DIST_DIR="$ROOT_DIR/dist"
DEFAULT_VER=$(grep -E '^[[:space:]]*Version[[:space:]]*=' "$ROOT_DIR/FyneApp.toml" 2>/dev/null | head -n1 | sed -E 's/.*"([^"]+)".*/\1/' || true)
VERSION="${VERSION:-${DEFAULT_VER:-0.1.0-dev}}"

mkdir -p "$BUILD_DIR" "$BIN_DIR" "$DIST_DIR" "$ROOT_DIR/cmd/launcher/payload" "$ROOT_DIR/cmd/installer/payload"
touch "$ROOT_DIR/cmd/launcher/payload/placeholder.txt" "$ROOT_DIR/cmd/installer/payload/placeholder.txt"

if command -v git &>/dev/null && [ -d "$ROOT_DIR/.git" ] && [ -d "$ROOT_DIR/.githooks" ]; then
    git -C "$ROOT_DIR" config core.hooksPath .githooks 2>/dev/null || true
fi

for dir in "$HOME/.local/bin" "$HOME/go/bin" "/usr/local/go/bin" "$HOME/.asdf/shims" "$HOME/.asdf/bin" "/home/linuxbrew/.linuxbrew/bin" "/home/linuxbrew/.linuxbrew/sbin"; do
    if [ -d "$dir" ] && [[ ":$PATH:" != *":$dir:"* ]]; then
        export PATH="$dir:$PATH"
    fi
done
if command -v go &>/dev/null; then
    for dir in "$(go env GOBIN 2>/dev/null || true)" "$(go env GOPATH 2>/dev/null || true)/bin"; do
        if [ -n "$dir" ] && [ -d "$dir" ] && [[ ":$PATH:" != *":$dir:"* ]]; then
            export PATH="$dir:$PATH"
        fi
    done
fi

fetch_dlls() {
    echo "==> Verifying Windows runtime DLLs in $BIN_DIR..."
    if [ ! -f "$BIN_DIR/onnxruntime.dll" ] || [ ! -f "$BIN_DIR/sherpa-onnx-c-api.dll" ]; then
        echo "--> Fetching sherpa-onnx Windows x64 DLLs from Go module cache..."
        go mod download github.com/k2-fsa/sherpa-onnx-go-windows 2>/dev/null || true
        local win_mod_dir
        win_mod_dir=$(go list -m -f '{{.Dir}}' github.com/k2-fsa/sherpa-onnx-go-windows 2>/dev/null || true)
        if [ -n "$win_mod_dir" ] && [ -d "$win_mod_dir/lib/x86_64-pc-windows-gnu" ]; then
            cp -f "$win_mod_dir/lib/x86_64-pc-windows-gnu/"*.dll "$BIN_DIR/"
            echo "✓ Copied Windows runtime DLLs from $win_mod_dir to $BIN_DIR"
        else
            echo "Warning: Could not locate sherpa-onnx-go-windows module cache."
        fi
    else
        echo "✓ Windows runtime DLLs present."
    fi
}

fetch_linux_libs() {
    echo "==> Verifying Linux runtime shared libraries in $BIN_DIR..."
    if [ ! -f "$BIN_DIR/libonnxruntime.so" ] || [ ! -f "$BIN_DIR/libsherpa-onnx-c-api.so" ]; then
        echo "--> Fetching sherpa-onnx Linux x64 shared libraries from Go module cache..."
        go mod download github.com/k2-fsa/sherpa-onnx-go-linux 2>/dev/null || true
        local linux_mod_dir
        linux_mod_dir=$(go list -m -f '{{.Dir}}' github.com/k2-fsa/sherpa-onnx-go-linux 2>/dev/null || true)
        if [ -n "$linux_mod_dir" ] && [ -d "$linux_mod_dir/lib/x86_64-unknown-linux-gnu" ]; then
            cp -f "$linux_mod_dir/lib/x86_64-unknown-linux-gnu/"*.so "$BIN_DIR/"
            echo "✓ Copied Linux shared libraries from $linux_mod_dir to $BIN_DIR"
        else
            echo "Warning: Could not locate sherpa-onnx-go-linux module cache."
        fi
    else
        echo "✓ Linux runtime shared libraries present."
    fi
}

fetch_mac_libs() {
    echo "==> Verifying macOS runtime shared libraries in $BIN_DIR..."
    if [ ! -f "$BIN_DIR/libonnxruntime.dylib" ] || [ ! -f "$BIN_DIR/libsherpa-onnx-c-api.dylib" ]; then
        echo "--> Fetching sherpa-onnx macOS shared libraries from Go module cache..."
        go mod download github.com/k2-fsa/sherpa-onnx-go-macos 2>/dev/null || true
        local mac_mod_dir
        mac_mod_dir=$(go list -m -f '{{.Dir}}' github.com/k2-fsa/sherpa-onnx-go-macos 2>/dev/null || true)
        local arch
        arch="$(uname -m)"
        local mac_subdir="aarch64-apple-darwin"
        if [ "$arch" = "x86_64" ]; then
            mac_subdir="x86_64-apple-darwin"
        fi
        if [ -n "$mac_mod_dir" ] && [ -d "$mac_mod_dir/lib/$mac_subdir" ]; then
            cp -f "$mac_mod_dir/lib/$mac_subdir/"*.dylib "$BIN_DIR/"
            echo "✓ Copied macOS ($mac_subdir) shared libraries from $mac_mod_dir to $BIN_DIR"
        else
            echo "Warning: Could not locate sherpa-onnx-go-macos module cache for $mac_subdir."
        fi
    else
        echo "✓ macOS runtime shared libraries present."
    fi
}

run_lint() {
    echo "==> Verifying code modernizations (go fix --diff)..."
    local diff_output
    local err_file="$BUILD_DIR/gofix.err"
    if ! diff_output=$(go fix --diff ./... 2>"$err_file"); then
        echo "Error: 'go fix --diff ./...' failed or found unfixed modernizations:"
        if [ -n "$diff_output" ]; then
            echo "$diff_output"
        fi
        cat "$err_file"
        rm -f "$err_file"
        exit 1
    fi
    rm -f "$err_file"
    if [ -n "$diff_output" ]; then
        echo "Error: Unfixed code modernization diffs found:"
        echo "$diff_output"
        echo "Run 'go fix ./...' to apply them."
        exit 1
    fi
    echo "✓ Code modernizations verified (0 diff)!"

    echo "==> Running golangci-lint-v2..."
    if ! command -v golangci-lint-v2 &>/dev/null; then
        echo "Error: golangci-lint-v2 not found in PATH"
        exit 1
    fi
    golangci-lint-v2 run ./...
    echo "✓ Lint passed (0 issues)!"
}

run_test_quiet() {
    local label="$1"
    shift
    local start_ts
    start_ts=$(date +%s)

    if [ "${1:-}" = "go" ] && [ "${2:-}" = "test" ] && command -v gotestsum &>/dev/null; then
        echo "==> ${label}..."
        gotestsum --format dots -- "${@:2}"
        local elapsed=$(( $(date +%s) - start_ts ))
        echo "✓ ${label} passed (${elapsed}s)"
        return 0
    fi

    if [ "${1:-}" = "go" ] && [ "${2:-}" = "test" ] && command -v python3 &>/dev/null; then
        echo "==> ${label}..."
        local rc=0
        set +e
        go test -json "${@:3}" 2>&1 | python3 -u -c '
import json, sys, collections

passed = skipped = failed = col = 0
outputs = collections.defaultdict(list)
non_json = []
failed_tests = []
build_failed = False

def emit(ch):
    global col
    sys.stdout.write(ch)
    col += 1
    if col >= 80:
        sys.stdout.write("\n")
        col = 0
    sys.stdout.flush()

for line in sys.stdin:
    raw = line.rstrip("\n")
    try:
        ev = json.loads(raw)
    except Exception:
        if raw.strip():
            non_json.append(raw)
        continue
    act = ev.get("Action")
    pkg = ev.get("Package") or ev.get("ImportPath") or ""
    test = ev.get("Test") or ""
    key = (pkg, test)
    if act in ("output", "build-output"):
        txt = ev.get("Output", "")
        if txt:
            outputs[key].append(txt)
    elif act == "pass" and test:
        passed += 1
        emit(".")
    elif act == "skip" and test:
        skipped += 1
        emit("s")
    elif act == "fail" and test:
        failed += 1
        failed_tests.append(key)
        emit("F")
    elif act in ("fail", "build-fail") and not test:
        build_failed = True
        if key not in failed_tests:
            failed_tests.append(key)

if col > 0:
    sys.stdout.write("\n")

if failed > 0 or build_failed or non_json:
    for key in failed_tests:
        pkg, test = key
        hdr = f"{pkg} :: {test}" if test else (pkg or "build")
        print(f"\n--- FAIL: {hdr} ---")
        for out in outputs.get(key, []):
            sys.stdout.write(out)
    if non_json:
        print("\n--- STDERR / OUTPUT ---")
        for l in non_json:
            print(l)
    print(f"\n✗ Failed: {failed} failed, {passed} passed, {skipped} skipped")
    sys.exit(1)

print(f"Summary: {passed} passed, {skipped} skipped")
'
        local pipe_status=("${PIPESTATUS[@]}")
        set -e
        local go_rc="${pipe_status[0]:-0}"
        local py_rc="${pipe_status[1]:-0}"
        local elapsed=$(( $(date +%s) - start_ts ))
        if [ "$go_rc" -ne 0 ] || [ "$py_rc" -ne 0 ]; then
            echo "✗ ${label} failed (${elapsed}s)"
            exit 1
        fi
        echo "✓ ${label} passed (${elapsed}s)"
        return 0
    fi

    local log_file="$BUILD_DIR/test_output.log"
    if ! "$@" >"$log_file" 2>&1; then
        echo "✗ ${label} failed:"
        cat "$log_file"
        rm -f "$log_file"
        exit 1
    fi
    local elapsed=$(( $(date +%s) - start_ts ))
    rm -f "$log_file"
    echo "✓ ${label} passed (${elapsed}s)"
}

run_test() {
    run_test_quiet "Unit tests (with race detector)" go test -race -timeout 180s -count=1 ./...
    run_test_quiet "Concurrent race stress tests (-count=5)" go test -race -timeout 180s -count=5 ./internal/app/... ./pkg/queue/... ./pkg/platform/... ./internal/eventlog/...
}

build_wasm() {
    echo "==> Vetting & building PoundControl WASM plugin (poundcontrol-towncrier.wasm v${VERSION})..."
    local norm_ver="${VERSION#v}"
    GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go vet ./integrations/poundcontrol-wasm
    GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 \
        go build -buildmode=c-shared -trimpath \
        -ldflags="-s -w -X main.PluginVersion=${norm_ver}" \
        -o "$BIN_DIR/poundcontrol-towncrier.wasm" ./integrations/poundcontrol-wasm
    echo "✓ Built $BIN_DIR/poundcontrol-towncrier.wasm"
}

build_win() {
    fetch_dlls
    bash "$ROOT_DIR/scripts/build-whisper-vulkan.sh" win
    build_wasm

    local core_syso="$ROOT_DIR/cmd/towncrier/rsrc_windows_amd64.syso"
    local launcher_syso="$ROOT_DIR/cmd/launcher/rsrc_windows_amd64.syso"
    local installer_syso="$ROOT_DIR/cmd/installer/rsrc_windows_amd64.syso"
    if command -v x86_64-w64-mingw32-windres &>/dev/null && [ -f "$ROOT_DIR/assets/icon.ico" ]; then
        echo "==> Compiling Windows icon resources (.syso)..."
        local rc_file="$BUILD_DIR/icon.rc"
        echo 'IDI_ICON1 ICON "../assets/icon.ico"' > "$rc_file"
        (cd "$BUILD_DIR" && x86_64-w64-mingw32-windres icon.rc -O coff -o "$core_syso")
        cp -f "$core_syso" "$launcher_syso"
        cp -f "$core_syso" "$installer_syso"
        rm -f "$rc_file"
    fi

    echo "==> Building towncrier-core.exe v${VERSION} (CGO + MinGW)..."
    CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
        go build -trimpath -ldflags="-H windowsgui -s -w -X github.com/mrpoundsign/TownCrier/internal/ui.AppVersion=v${VERSION} -extldflags '-static-libgcc'" \
        -o "$BIN_DIR/towncrier-core.exe" ./cmd/towncrier
    echo "✓ Built $BIN_DIR/towncrier-core.exe"

    echo "==> Staging Windows payload for single-file launcher & setup installer..."
    local payload_dir="$ROOT_DIR/cmd/launcher/payload"
    local inst_payload_dir="$ROOT_DIR/cmd/installer/payload"
    mkdir -p "$payload_dir" "$inst_payload_dir"
    rm -f "$payload_dir/towncrier-core"* "$payload_dir/towncrier-whisper"* "$payload_dir/"*.dll "$payload_dir/"*.so "$payload_dir/"*.dylib
    rm -f "$inst_payload_dir/towncrier-core"* "$inst_payload_dir/towncrier-whisper"* "$inst_payload_dir/"*.dll "$inst_payload_dir/poundcontrol-towncrier.wasm" "$inst_payload_dir/LICENSE" "$inst_payload_dir/THIRDPARTY_NOTICES.md"
    trap 'rm -f "$ROOT_DIR/cmd/launcher/payload/towncrier-core"* "$ROOT_DIR/cmd/launcher/payload/towncrier-whisper"* "$ROOT_DIR/cmd/launcher/payload/"*.dll "$ROOT_DIR/cmd/launcher/payload/"*.so "$ROOT_DIR/cmd/launcher/payload/"*.dylib "$ROOT_DIR/cmd/installer/payload/towncrier-core"* "$ROOT_DIR/cmd/installer/payload/towncrier-whisper"* "$ROOT_DIR/cmd/installer/payload/"*.dll "$ROOT_DIR/cmd/installer/payload/poundcontrol-towncrier.wasm" "$ROOT_DIR/cmd/installer/payload/LICENSE" "$ROOT_DIR/cmd/installer/payload/THIRDPARTY_NOTICES.md" "$ROOT_DIR/cmd/towncrier/rsrc_windows_amd64.syso" "$ROOT_DIR/cmd/launcher/rsrc_windows_amd64.syso" "$ROOT_DIR/cmd/installer/rsrc_windows_amd64.syso"' RETURN
    cp -f "$BIN_DIR/towncrier-core.exe" "$payload_dir/"
    cp -f "$BIN_DIR/towncrier-whisper.exe" "$payload_dir/"
    cp -f "$BIN_DIR/onnxruntime.dll" "$payload_dir/"
    cp -f "$BIN_DIR/sherpa-onnx-c-api.dll" "$payload_dir/"
    cp -f "$BIN_DIR/sherpa-onnx-cxx-api.dll" "$payload_dir/"

    cp -f "$BIN_DIR/towncrier-core.exe" "$inst_payload_dir/"
    cp -f "$BIN_DIR/towncrier-whisper.exe" "$inst_payload_dir/"
    cp -f "$BIN_DIR/onnxruntime.dll" "$inst_payload_dir/"
    cp -f "$BIN_DIR/sherpa-onnx-c-api.dll" "$inst_payload_dir/"
    cp -f "$BIN_DIR/sherpa-onnx-cxx-api.dll" "$inst_payload_dir/"
    cp -f "$BIN_DIR/poundcontrol-towncrier.wasm" "$inst_payload_dir/"
    [ -f "$ROOT_DIR/LICENSE" ] && cp -f "$ROOT_DIR/LICENSE" "$inst_payload_dir/"
    [ -f "$ROOT_DIR/THIRDPARTY_NOTICES.md" ] && cp -f "$ROOT_DIR/THIRDPARTY_NOTICES.md" "$inst_payload_dir/"

    echo "==> Building standalone TownCrier.exe v${VERSION}..."
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
        go build -trimpath -ldflags="-H windowsgui -s -w -X main.AppVersion=${VERSION}" \
        -o "$BIN_DIR/TownCrier.exe" ./cmd/launcher

    echo "==> Building Fyne GUI installer TownCrier-Setup.exe v${VERSION} (-tags nosherpa)..."
    CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
        go build -tags nosherpa -trimpath -ldflags="-H windowsgui -s -w -X main.AppVersion=${VERSION} -extldflags '-static-libgcc'" \
        -o "$BIN_DIR/TownCrier-Setup.exe" ./cmd/installer

    if command -v x86_64-w64-mingw32-objdump &>/dev/null; then
        echo "==> Verifying TownCrier-Setup.exe DLL Dependencies..."
        x86_64-w64-mingw32-objdump -p "$BIN_DIR/TownCrier-Setup.exe" | grep "DLL Name"
    fi

    # Clean staged payload and temporary .syso from source tree
    rm -f "$payload_dir/towncrier-core"* "$payload_dir/towncrier-whisper"* "$payload_dir/"*.dll "$payload_dir/"*.so "$payload_dir/"*.dylib
    rm -f "$inst_payload_dir/towncrier-core"* "$inst_payload_dir/towncrier-whisper"* "$inst_payload_dir/"*.dll "$inst_payload_dir/poundcontrol-towncrier.wasm" "$inst_payload_dir/LICENSE" "$inst_payload_dir/THIRDPARTY_NOTICES.md"
    rm -f "$core_syso" "$launcher_syso" "$installer_syso"
    trap - RETURN

    echo "✓ Successfully built standalone single-file $BIN_DIR/TownCrier.exe and $BIN_DIR/TownCrier-Setup.exe"
}

build_linux() {
    fetch_linux_libs
    bash "$ROOT_DIR/scripts/build-whisper-vulkan.sh" linux

    echo "==> Building towncrier-core v${VERSION} (Linux amd64 CGO)..."
    CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
        go build -trimpath -ldflags="-s -w -X github.com/mrpoundsign/TownCrier/internal/ui.AppVersion=v${VERSION} -extldflags '-Wl,-rpath,\$ORIGIN'" \
        -o "$BIN_DIR/towncrier-core" ./cmd/towncrier
    echo "✓ Built $BIN_DIR/towncrier-core"

    echo "==> Staging Linux payload for single-file launcher..."
    local payload_dir="$ROOT_DIR/cmd/launcher/payload"
    mkdir -p "$payload_dir"
    rm -f "$payload_dir/towncrier-core"* "$payload_dir/towncrier-whisper"* "$payload_dir/"*.dll "$payload_dir/"*.so "$payload_dir/"*.dylib
    trap 'rm -f "$ROOT_DIR/cmd/launcher/payload/towncrier-core"* "$ROOT_DIR/cmd/launcher/payload/towncrier-whisper"* "$ROOT_DIR/cmd/launcher/payload/"*.dll "$ROOT_DIR/cmd/launcher/payload/"*.so "$ROOT_DIR/cmd/launcher/payload/"*.dylib' RETURN
    cp -f "$BIN_DIR/towncrier-core" "$payload_dir/"
    cp -f "$BIN_DIR/towncrier-whisper" "$payload_dir/"
    cp -f "$BIN_DIR/libonnxruntime.so" "$payload_dir/"
    cp -f "$BIN_DIR/libsherpa-onnx-c-api.so" "$payload_dir/"
    cp -f "$BIN_DIR/libsherpa-onnx-cxx-api.so" "$payload_dir/"

    echo "==> Building standalone towncrier v${VERSION} (Linux amd64)..."
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        go build -trimpath -ldflags="-s -w -X main.AppVersion=${VERSION}" \
        -o "$BIN_DIR/towncrier" ./cmd/launcher
    chmod +x "$BIN_DIR/towncrier"

    rm -f "$payload_dir/towncrier-core"* "$payload_dir/towncrier-whisper"* "$payload_dir/"*.dll "$payload_dir/"*.so "$payload_dir/"*.dylib
    trap - RETURN

    echo "✓ Successfully built standalone single-file $BIN_DIR/towncrier"
}

build_mac() {
    fetch_mac_libs
    bash "$ROOT_DIR/scripts/build-whisper-vulkan.sh" mac

    local arch
    arch="$(uname -m)"
    local goarch="arm64"
    if [ "$arch" = "x86_64" ]; then
        goarch="amd64"
    fi

    echo "==> Building towncrier-core v${VERSION} (macOS ${goarch} CGO)..."
    CGO_ENABLED=1 GOOS=darwin GOARCH=${goarch} \
        go build -trimpath -ldflags="-s -w -X github.com/mrpoundsign/TownCrier/internal/ui.AppVersion=v${VERSION} -extldflags '-Wl,-rpath,@executable_path'" \
        -o "$BIN_DIR/towncrier-core" ./cmd/towncrier
    echo "✓ Built $BIN_DIR/towncrier-core"

    echo "==> Staging macOS payload for single-file launcher..."
    local payload_dir="$ROOT_DIR/cmd/launcher/payload"
    mkdir -p "$payload_dir"
    rm -f "$payload_dir/towncrier-core"* "$payload_dir/towncrier-whisper"* "$payload_dir/"*.dll "$payload_dir/"*.so "$payload_dir/"*.dylib
    trap 'rm -f "$ROOT_DIR/cmd/launcher/payload/towncrier-core"* "$ROOT_DIR/cmd/launcher/payload/towncrier-whisper"* "$ROOT_DIR/cmd/launcher/payload/"*.dll "$ROOT_DIR/cmd/launcher/payload/"*.so "$ROOT_DIR/cmd/launcher/payload/"*.dylib' RETURN
    cp -f "$BIN_DIR/towncrier-core" "$payload_dir/"
    cp -f "$BIN_DIR/towncrier-whisper" "$payload_dir/"
    cp -f "$BIN_DIR/libonnxruntime.dylib" "$payload_dir/"
    cp -f "$BIN_DIR/libsherpa-onnx-c-api.dylib" "$payload_dir/"
    cp -f "$BIN_DIR/libsherpa-onnx-cxx-api.dylib" "$payload_dir/"

    echo "==> Building standalone towncrier v${VERSION} (macOS ${goarch})..."
    CGO_ENABLED=0 GOOS=darwin GOARCH=${goarch} \
        go build -trimpath -ldflags="-s -w -X main.AppVersion=${VERSION}" \
        -o "$BIN_DIR/towncrier" ./cmd/launcher
    chmod +x "$BIN_DIR/towncrier"

    rm -f "$payload_dir/towncrier-core"* "$payload_dir/towncrier-whisper"* "$payload_dir/"*.dll "$payload_dir/"*.so "$payload_dir/"*.dylib
    trap - RETURN

    echo "✓ Successfully built standalone single-file $BIN_DIR/towncrier"
}

package_mac() {
    if [ ! -f "$BIN_DIR/towncrier" ]; then
        build_mac
    fi

    local arch
    arch="$(uname -m)"
    local pkg_arch="arm64"
    if [ "$arch" = "x86_64" ]; then
        pkg_arch="amd64"
    fi

    local tar_file="$DIST_DIR/TownCrier-v${VERSION}-darwin-${pkg_arch}.tar.gz"
    echo "==> Packaging macOS (${pkg_arch}) TownCrier.app bundle into $tar_file..."
    rm -f "$tar_file"

    local staging="$BUILD_DIR/package_staging_mac"
    local app_dir="$staging/TownCrier.app"
    rm -rf "$staging"
    mkdir -p "$app_dir/Contents/MacOS" "$app_dir/Contents/Resources"

    cp "$BIN_DIR/towncrier" "$app_dir/Contents/MacOS/TownCrier"
    chmod +x "$app_dir/Contents/MacOS/TownCrier"

    # Convert assets/icon.png into icon.icns if sips + iconutil are available on macOS
    if [ -f "$ROOT_DIR/assets/icon.png" ] && command -v sips &>/dev/null && command -v iconutil &>/dev/null; then
        local iconset="$BUILD_DIR/TownCrier.iconset"
        rm -rf "$iconset"
        mkdir -p "$iconset"
        sips -z 16 16     "$ROOT_DIR/assets/icon.png" --out "$iconset/icon_16x16.png" >/dev/null 2>&1 || true
        sips -z 32 32     "$ROOT_DIR/assets/icon.png" --out "$iconset/icon_16x16@2x.png" >/dev/null 2>&1 || true
        sips -z 32 32     "$ROOT_DIR/assets/icon.png" --out "$iconset/icon_32x32.png" >/dev/null 2>&1 || true
        sips -z 64 64     "$ROOT_DIR/assets/icon.png" --out "$iconset/icon_32x32@2x.png" >/dev/null 2>&1 || true
        sips -z 128 128   "$ROOT_DIR/assets/icon.png" --out "$iconset/icon_128x128.png" >/dev/null 2>&1 || true
        sips -z 256 256   "$ROOT_DIR/assets/icon.png" --out "$iconset/icon_128x128@2x.png" >/dev/null 2>&1 || true
        sips -z 256 256   "$ROOT_DIR/assets/icon.png" --out "$iconset/icon_256x256.png" >/dev/null 2>&1 || true
        sips -z 512 512   "$ROOT_DIR/assets/icon.png" --out "$iconset/icon_256x256@2x.png" >/dev/null 2>&1 || true
        iconutil -c icns "$iconset" -o "$app_dir/Contents/Resources/icon.icns" >/dev/null 2>&1 || true
        rm -rf "$iconset"
    elif [ -f "$ROOT_DIR/assets/icon.png" ]; then
        cp "$ROOT_DIR/assets/icon.png" "$app_dir/Contents/Resources/icon.png"
    fi

    cat > "$app_dir/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key>
    <string>TownCrier</string>
    <key>CFBundleDisplayName</key>
    <string>TownCrier</string>
    <key>CFBundleIdentifier</key>
    <string>com.github.mrpoundsign.towncrier</string>
    <key>CFBundleVersion</key>
    <string>${VERSION}</string>
    <key>CFBundleShortVersionString</key>
    <string>${VERSION}</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleExecutable</key>
    <string>TownCrier</string>
    <key>CFBundleIconFile</key>
    <string>icon.icns</string>
    <key>NSHighResolutionCapable</key>
    <true/>
    <key>LSMinimumSystemVersion</key>
    <string>12.0</string>
</dict>
</plist>
EOF

    if command -v codesign &>/dev/null; then
        codesign --force --deep --sign - "$app_dir" 2>/dev/null || true
    fi

    [ -f "$ROOT_DIR/THIRDPARTY_NOTICES.md" ] && cp "$ROOT_DIR/THIRDPARTY_NOTICES.md" "$staging/"
    [ -f "$ROOT_DIR/LICENSE" ] && cp "$ROOT_DIR/LICENSE" "$staging/"
    [ -f "$ROOT_DIR/FyneApp.toml" ] && cp "$ROOT_DIR/FyneApp.toml" "$staging/"
    (cd "$staging" && tar -czf "$tar_file" .)
    rm -rf "$staging"
    echo "✓ Successfully packaged $tar_file"
}

package_dist() {
    if [ ! -f "$BIN_DIR/TownCrier.exe" ]; then
        build_win
    fi
    if [ ! -f "$BIN_DIR/towncrier" ]; then
        build_linux
    fi
    if [ ! -f "$BIN_DIR/poundcontrol-towncrier.wasm" ]; then
        build_wasm
    fi

    local zip_file="$DIST_DIR/TownCrier-v${VERSION}-windows-amd64.zip"
    echo "==> Packaging Windows distribution into $zip_file..."
    rm -f "$zip_file"

    local staging="$BUILD_DIR/package_staging"
    rm -rf "$staging"
    mkdir -p "$staging"

    cp "$BIN_DIR/TownCrier.exe" "$staging/"
    [ -f "$ROOT_DIR/THIRDPARTY_NOTICES.md" ] && cp "$ROOT_DIR/THIRDPARTY_NOTICES.md" "$staging/"
    [ -f "$ROOT_DIR/LICENSE" ] && cp "$ROOT_DIR/LICENSE" "$staging/"
    [ -f "$ROOT_DIR/FyneApp.toml" ] && cp "$ROOT_DIR/FyneApp.toml" "$staging/"

    if command -v zip &>/dev/null; then
        (cd "$staging" && zip -q -r "$zip_file" .)
    else
        python3 -c "
import os, zipfile
with zipfile.ZipFile('$zip_file', 'w', zipfile.ZIP_DEFLATED) as zf:
    for root, _, files in os.walk('$staging'):
        for file in files:
            p = os.path.join(root, file)
            zf.write(p, os.path.relpath(p, '$staging'))
"
    fi
    rm -rf "$staging"
    echo "✓ Successfully packaged $zip_file"

    if [ -f "$BIN_DIR/TownCrier-Setup.exe" ]; then
        local setup_file="$DIST_DIR/TownCrier-v${VERSION}-windows-amd64-setup.exe"
        cp -f "$BIN_DIR/TownCrier-Setup.exe" "$setup_file"
        echo "✓ Successfully packaged $setup_file"
    fi

    local tar_file="$DIST_DIR/TownCrier-v${VERSION}-linux-amd64.tar.gz"
    echo "==> Packaging Linux distribution into $tar_file..."
    rm -f "$tar_file"
    mkdir -p "$staging"
    cp "$BIN_DIR/towncrier" "$staging/"
    chmod +x "$staging/towncrier"
    [ -f "$ROOT_DIR/THIRDPARTY_NOTICES.md" ] && cp "$ROOT_DIR/THIRDPARTY_NOTICES.md" "$staging/"
    [ -f "$ROOT_DIR/LICENSE" ] && cp "$ROOT_DIR/LICENSE" "$staging/"
    [ -f "$ROOT_DIR/FyneApp.toml" ] && cp "$ROOT_DIR/FyneApp.toml" "$staging/"
    (cd "$staging" && tar -czf "$tar_file" .)
    rm -rf "$staging"
    echo "✓ Successfully packaged $tar_file"

    if [ -f "$BIN_DIR/poundcontrol-towncrier.wasm" ]; then
        cp -f "$BIN_DIR/poundcontrol-towncrier.wasm" "$DIST_DIR/poundcontrol-towncrier.wasm"
        echo "✓ Successfully packaged $DIST_DIR/poundcontrol-towncrier.wasm"
    fi
}

run_proto() {
    echo "==> Generating Protobuf Go bindings..."
    if ! command -v protoc &>/dev/null; then
        echo "Error: protoc not found in PATH"
        exit 1
    fi
    if ! command -v protoc-gen-go &>/dev/null; then
        echo "Error: protoc-gen-go not found in PATH"
        exit 1
    fi
    mkdir -p "$ROOT_DIR/internal/ws/controlv1" "$ROOT_DIR/pkg/pluginv1"
    protoc -I "$ROOT_DIR/proto" \
        --go_out="$ROOT_DIR" --go_opt=module=github.com/mrpoundsign/TownCrier \
        "$ROOT_DIR/proto/control/v1/"*.proto \
        "$ROOT_DIR/proto/plugin/v1/"*.proto
    echo "✓ Protobuf bindings generated!"
}

clean() {
    echo "==> Cleaning build artifacts..."
    rm -rf "$BUILD_DIR" "$DIST_DIR"
    echo "✓ Clean complete."
}

case "$TARGET" in
    all)
        run_lint
        run_test
        build_wasm
        build_win
        build_linux
        package_dist
        ;;
    win)
        build_win
        ;;
    linux)
        build_linux
        ;;
    mac|osx)
        build_mac
        ;;
    wasm)
        build_wasm
        ;;
    lint)
        run_lint
        ;;
    test)
        run_test
        ;;
    proto)
        run_proto
        ;;
    package)
        package_dist
        ;;
    package-mac)
        package_mac
        ;;
    fetch-dlls)
        fetch_dlls
        ;;
    clean)
        clean
        ;;
    *)
        echo "Unknown target: $TARGET"
        echo "Valid targets: all, win, linux, mac, wasm, lint, test, proto, package, package-mac, fetch-dlls, clean"
        exit 1
        ;;
esac
