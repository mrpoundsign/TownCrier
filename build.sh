#!/usr/bin/env bash
set -euo pipefail

# OctochorpeBot Build Script (WSL Cross-Compilation to Windows)
# Usage:
#   ./build.sh          # Run lint, test, build Windows binary, and package into dist/
#   ./build.sh win      # Build Windows GUI executable into build/bin/
#   ./build.sh lint     # Run go fix --diff and golangci-lint-v2
#   ./build.sh test     # Run unit tests
#   ./build.sh package  # Package zip distribution into dist/
#   ./build.sh clean    # Clean build and dist outputs

TARGET="${1:-all}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_DIR="$ROOT_DIR/build"
BIN_DIR="$BUILD_DIR/bin"
DIST_DIR="$ROOT_DIR/dist"
VERSION="0.1.0"

mkdir -p "$BUILD_DIR" "$BIN_DIR" "$DIST_DIR"

if ! command -v go &>/dev/null; then
    if [ -x "/usr/local/go/bin/go" ]; then
        export PATH="/usr/local/go/bin:$PATH"
    elif [ -x "$HOME/go/bin/go" ]; then
        export PATH="$HOME/go/bin:$PATH"
    elif [ -d "$HOME/.asdf/shims" ]; then
        export PATH="$HOME/.asdf/shims:$PATH"
    fi
fi

fetch_dlls() {
    echo "==> Verifying runtime DLLs in $BIN_DIR..."
    if [ ! -f "$BIN_DIR/onnxruntime.dll" ] || [ ! -f "$BIN_DIR/sherpa-onnx-c-api.dll" ]; then
        echo "--> Fetching sherpa-onnx Windows x64 DLLs from Go module cache..."
        local win_mod_dir
        win_mod_dir=$(go list -m -f '{{.Dir}}' github.com/k2-fsa/sherpa-onnx-go-windows 2>/dev/null || true)
        if [ -n "$win_mod_dir" ] && [ -d "$win_mod_dir/lib/x86_64-pc-windows-gnu" ]; then
            cp "$win_mod_dir/lib/x86_64-pc-windows-gnu/"*.dll "$BIN_DIR/"
            echo "✓ Copied runtime DLLs from $win_mod_dir to $BIN_DIR"
        else
            echo "Warning: Could not locate sherpa-onnx-go-windows module cache."
        fi
    else
        echo "✓ Runtime DLLs present."
    fi
}

run_lint() {
    echo "==> Verifying code modernizations (go fix --diff)..."
    local diff_output
    diff_output=$(go fix --diff ./... 2>&1 || true)
    if [ -n "$diff_output" ]; then
        echo "Error: Unfixed code modernization diffs found:"
        echo "$diff_output"
        echo "Run 'go fix ./...' to apply them."
        exit 1
    fi
    echo "✓ Code modernizations verified (0 diff)!"

    echo "==> Running golangci-lint-v2..."
    if command -v golangci-lint-v2 &>/dev/null; then
        golangci-lint-v2 run ./...
    elif command -v golangci-lint &>/dev/null; then
        golangci-lint run ./...
    else
        echo "Error: golangci-lint-v2 / golangci-lint not found in PATH"
        exit 1
    fi
    echo "✓ Lint passed (0 issues)!"
}

run_test() {
    echo "==> Running unit tests..."
    go test -count=1 -v ./...
    echo "✓ All tests passed!"
}

build_win() {
    fetch_dlls
    echo "==> Building OctochorpeBot Desktop GUI for Windows (amd64 via MinGW)..."
    CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
        go build -trimpath -ldflags="-H windowsgui -s -w -extldflags '-static-libgcc'" \
        -o "$BIN_DIR/OctochorpeBot.exe" ./cmd/octochorpebot
    echo "✓ Successfully built $BIN_DIR/OctochorpeBot.exe"
}

package_dist() {
    if [ ! -f "$BIN_DIR/OctochorpeBot.exe" ]; then
        build_win
    fi
    local zip_file="$DIST_DIR/OctochorpeBot-v${VERSION}-windows-amd64.zip"
    echo "==> Packaging distribution into $zip_file..."
    rm -f "$zip_file"

    local staging="$BUILD_DIR/package_staging"
    rm -rf "$staging"
    mkdir -p "$staging"

    cp "$BIN_DIR/OctochorpeBot.exe" "$staging/"
    if [ -f "$BIN_DIR/onnxruntime.dll" ]; then
        cp "$BIN_DIR/onnxruntime.dll" "$staging/"
    fi
    if [ -f "$BIN_DIR/sherpa-onnx-c-api.dll" ]; then
        cp "$BIN_DIR/sherpa-onnx-c-api.dll" "$staging/"
    fi
    if [ -f "$BIN_DIR/sherpa-onnx-cxx-api.dll" ]; then
        cp "$BIN_DIR/sherpa-onnx-cxx-api.dll" "$staging/"
    fi
    if [ -f "$ROOT_DIR/LICENSE" ]; then
        cp "$ROOT_DIR/LICENSE" "$staging/"
    fi
    if [ -f "$ROOT_DIR/FyneApp.toml" ]; then
        cp "$ROOT_DIR/FyneApp.toml" "$staging/"
    fi

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
        package_dist
        ;;
    win)
        build_win
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
        echo "Valid targets: all, win, lint, test, package, fetch-dlls, clean"
        exit 1
        ;;
esac
