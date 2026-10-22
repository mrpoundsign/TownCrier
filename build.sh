#!/usr/bin/env bash
set -euo pipefail

# TownCrier Build Script (Windows & Linux)
# Usage:
#   ./build.sh          # Run lint, test, build Windows & Linux binaries, and package into dist/
#   ./build.sh win      # Build Windows GUI executable into build/bin/TownCrier.exe
#   ./build.sh linux    # Build Linux GUI executable into build/bin/towncrier
#   ./build.sh lint     # Run go fix --diff and golangci-lint-v2
#   ./build.sh test     # Run unit tests
#   ./build.sh package  # Package zip/tar.gz distributions into dist/
#   ./build.sh clean    # Clean build and dist outputs

TARGET="${1:-all}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_DIR="$ROOT_DIR/build"
BIN_DIR="$BUILD_DIR/bin"
DIST_DIR="$ROOT_DIR/dist"
VERSION="${VERSION:-0.0.5}"

mkdir -p "$BUILD_DIR" "$BIN_DIR" "$DIST_DIR" "$ROOT_DIR/cmd/launcher/payload"
touch "$ROOT_DIR/cmd/launcher/payload/placeholder.txt"

if command -v git &>/dev/null && [ -d "$ROOT_DIR/.git" ] && [ -d "$ROOT_DIR/.githooks" ]; then
    git -C "$ROOT_DIR" config core.hooksPath .githooks 2>/dev/null || true
fi

for dir in "$HOME/go/bin" "/usr/local/go/bin" "$HOME/.asdf/shims" "$HOME/.asdf/bin" "/home/linuxbrew/.linuxbrew/bin" "/home/linuxbrew/.linuxbrew/sbin"; do
    if [ -d "$dir" ] && [[ ":$PATH:" != *":$dir:"* ]]; then
        export PATH="$dir:$PATH"
    fi
done

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

run_test() {
    echo "==> Running unit tests..."
    go test -timeout 60s -count=1 -v ./...
    echo "✓ All tests passed!"
}

build_win() {
    fetch_dlls

    local core_syso="$ROOT_DIR/cmd/towncrier/rsrc_windows_amd64.syso"
    local launcher_syso="$ROOT_DIR/cmd/launcher/rsrc_windows_amd64.syso"
    if command -v x86_64-w64-mingw32-windres &>/dev/null && [ -f "$ROOT_DIR/assets/icon.ico" ]; then
        echo "==> Compiling Windows icon resources (.syso)..."
        local rc_file="$BUILD_DIR/icon.rc"
        echo 'IDI_ICON1 ICON "../assets/icon.ico"' > "$rc_file"
        (cd "$BUILD_DIR" && x86_64-w64-mingw32-windres icon.rc -O coff -o "$core_syso")
        cp -f "$core_syso" "$launcher_syso"
        rm -f "$rc_file"
    fi

    echo "==> Building towncrier-core.exe v${VERSION} (CGO + MinGW)..."
    CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
        go build -trimpath -ldflags="-H windowsgui -s -w -X github.com/mrpoundsign/TownCrier/internal/ui.AppVersion=v${VERSION} -extldflags '-static-libgcc'" \
        -o "$BIN_DIR/towncrier-core.exe" ./cmd/towncrier
    echo "✓ Built $BIN_DIR/towncrier-core.exe"

    echo "==> Staging Windows payload for single-file launcher..."
    local payload_dir="$ROOT_DIR/cmd/launcher/payload"
    mkdir -p "$payload_dir"
    rm -f "$payload_dir/towncrier-core"* "$payload_dir/"*.dll "$payload_dir/"*.so
    trap 'rm -f "$ROOT_DIR/cmd/launcher/payload/towncrier-core"* "$ROOT_DIR/cmd/launcher/payload/"*.dll "$ROOT_DIR/cmd/launcher/payload/"*.so "$ROOT_DIR/cmd/towncrier/rsrc_windows_amd64.syso" "$ROOT_DIR/cmd/launcher/rsrc_windows_amd64.syso"' RETURN
    cp -f "$BIN_DIR/towncrier-core.exe" "$payload_dir/"
    cp -f "$BIN_DIR/onnxruntime.dll" "$payload_dir/"
    cp -f "$BIN_DIR/sherpa-onnx-c-api.dll" "$payload_dir/"
    cp -f "$BIN_DIR/sherpa-onnx-cxx-api.dll" "$payload_dir/"

    echo "==> Building standalone TownCrier.exe v${VERSION}..."
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
        go build -trimpath -ldflags="-H windowsgui -s -w -X main.AppVersion=${VERSION}" \
        -o "$BIN_DIR/TownCrier.exe" ./cmd/launcher

    # Clean staged payload and temporary .syso from source tree
    rm -f "$payload_dir/towncrier-core"* "$payload_dir/"*.dll "$payload_dir/"*.so "$core_syso" "$launcher_syso"
    trap - RETURN

    echo "✓ Successfully built standalone single-file $BIN_DIR/TownCrier.exe"
}

build_linux() {
    fetch_linux_libs

    echo "==> Building towncrier-core v${VERSION} (Linux amd64 CGO)..."
    CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
        go build -trimpath -ldflags="-s -w -X github.com/mrpoundsign/TownCrier/internal/ui.AppVersion=v${VERSION} -extldflags '-Wl,-rpath,\$ORIGIN'" \
        -o "$BIN_DIR/towncrier-core" ./cmd/towncrier
    echo "✓ Built $BIN_DIR/towncrier-core"

    echo "==> Staging Linux payload for single-file launcher..."
    local payload_dir="$ROOT_DIR/cmd/launcher/payload"
    mkdir -p "$payload_dir"
    rm -f "$payload_dir/towncrier-core"* "$payload_dir/"*.dll "$payload_dir/"*.so
    trap 'rm -f "$ROOT_DIR/cmd/launcher/payload/towncrier-core"* "$ROOT_DIR/cmd/launcher/payload/"*.dll "$ROOT_DIR/cmd/launcher/payload/"*.so' RETURN
    cp -f "$BIN_DIR/towncrier-core" "$payload_dir/"
    cp -f "$BIN_DIR/libonnxruntime.so" "$payload_dir/"
    cp -f "$BIN_DIR/libsherpa-onnx-c-api.so" "$payload_dir/"
    cp -f "$BIN_DIR/libsherpa-onnx-cxx-api.so" "$payload_dir/"

    echo "==> Building standalone towncrier v${VERSION} (Linux amd64)..."
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        go build -trimpath -ldflags="-s -w -X main.AppVersion=${VERSION}" \
        -o "$BIN_DIR/towncrier" ./cmd/launcher
    chmod +x "$BIN_DIR/towncrier"

    rm -f "$payload_dir/towncrier-core"* "$payload_dir/"*.dll "$payload_dir/"*.so
    trap - RETURN

    echo "✓ Successfully built standalone single-file $BIN_DIR/towncrier"
}

package_dist() {
    if [ ! -f "$BIN_DIR/TownCrier.exe" ]; then
        build_win
    fi
    if [ ! -f "$BIN_DIR/towncrier" ]; then
        build_linux
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
    lint)
        run_lint
        ;;
    test)
        run_test
        ;;
    package)
        package_dist
        ;;
    fetch-dlls)
        fetch_dlls
        ;;
    clean)
        clean
        ;;
    *)
        echo "Unknown target: $TARGET"
        echo "Valid targets: all, win, linux, lint, test, package, fetch-dlls, clean"
        exit 1
        ;;
esac
