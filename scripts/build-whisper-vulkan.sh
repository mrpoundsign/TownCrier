#!/usr/bin/env bash
set -euo pipefail

# Builds TownCrier's standalone whisper.cpp + Vulkan/CPU helper binary from a pinned upstream commit.
# Usage:
#   ./scripts/build-whisper-vulkan.sh win
#   ./scripts/build-whisper-vulkan.sh linux
#   ./scripts/build-whisper-vulkan.sh mac

TARGET_OS="${1:-win}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="$ROOT_DIR/build"
BIN_DIR="$BUILD_DIR/bin"
WHISPER_SRC_DIR="$BUILD_DIR/whisper.cpp"

# Pinned ggml-org/whisper.cpp v1.9.5 commit hash
WHISPER_COMMIT="d1be6fde11ac6e0407606b4e42fe72d34add8037"

for dir in "/home/linuxbrew/.linuxbrew/bin" "/home/linuxbrew/.linuxbrew/sbin" "/opt/homebrew/bin" "/usr/local/bin"; do
    if [ -d "$dir" ] && [[ ":$PATH:" != *":$dir:"* ]]; then
        export PATH="$dir:$PATH"
    fi
done

mkdir -p "$BUILD_DIR" "$BIN_DIR"

ensure_whisper_source() {
    if [ -d "$WHISPER_SRC_DIR/.git" ]; then
        local cur_sha
        cur_sha="$(git -C "$WHISPER_SRC_DIR" rev-parse HEAD 2>/dev/null || true)"
        if [ "$cur_sha" = "$WHISPER_COMMIT" ]; then
            return 0
        fi
    fi
    echo "==> Fetching pinned ggml-org/whisper.cpp ($WHISPER_COMMIT)..."
    mkdir -p "$WHISPER_SRC_DIR"
    if [ ! -d "$WHISPER_SRC_DIR/.git" ]; then
        git -C "$WHISPER_SRC_DIR" init -q
        git -C "$WHISPER_SRC_DIR" remote add origin https://github.com/ggml-org/whisper.cpp.git
    fi
    git -C "$WHISPER_SRC_DIR" fetch --depth 1 origin "$WHISPER_COMMIT"
    git -C "$WHISPER_SRC_DIR" checkout -f -q FETCH_HEAD
}

find_vulkan_include() {
    for inc in "/home/linuxbrew/.linuxbrew/include" "/usr/include" "/usr/local/include"; do
        if [ -f "$inc/vulkan/vulkan.h" ]; then
            echo "$inc"
            return 0
        fi
    done
    return 1
}

build_win() {
    local out_bin="$BIN_DIR/towncrier-whisper.exe"
    local stamp_file="$BUILD_DIR/whisper-build-win/stamp.txt"
    local src_hash
    src_hash="$(sha256sum "$ROOT_DIR/cmd/whisper-helper/whisper_helper.cpp" "$ROOT_DIR/cmd/whisper-helper/CMakeLists.txt" | sha256sum | awk '{print $1}')"
    local want_stamp="${WHISPER_COMMIT}:${src_hash}"

    if [ -f "$out_bin" ] && [ -f "$stamp_file" ] && [ "$(cat "$stamp_file")" = "$want_stamp" ]; then
        echo "✓ Cached $out_bin is up to date ($WHISPER_COMMIT)."
        return 0
    fi

    ensure_whisper_source

    local vk_inc
    vk_inc="$(find_vulkan_include)" || {
        echo "Error: Vulkan headers (vulkan/vulkan.h) not found. Install vulkan-headers / libvulkan-dev." >&2
        exit 1
    }
    local glslc_bin
    glslc_bin="$(command -v glslc)" || {
        echo "Error: glslc not found. Install shaderc / glslc." >&2
        exit 1
    }

    local cmake_dir="$BUILD_DIR/whisper-build-win"
    mkdir -p "$cmake_dir"

    # Stage ONLY Vulkan headers into an isolated include directory so passing -DVulkan_INCLUDE_DIR
    # on Linux CI (where libvulkan-dev lives in /usr/include) never adds -isystem /usr/include to
    # x86_64-w64-mingw32-g++ (which would shadow MinGW's <stdint.h> with Linux glibc headers).
    local vk_staged_inc="$cmake_dir/vk-include"
    rm -rf "$vk_staged_inc"
    mkdir -p "$vk_staged_inc"
    cp -rL "$vk_inc/vulkan" "$vk_staged_inc/vulkan"
    if [ -d "$vk_inc/vk_video" ]; then
        cp -rL "$vk_inc/vk_video" "$vk_staged_inc/vk_video"
    fi

    # Generate a complete Windows x64 import library for vulkan-1.dll from vulkan_core.h
    python3 - "$vk_staged_inc/vulkan/vulkan_core.h" "$cmake_dir/vulkan-1.def" << 'PY'
import re, sys
hdr = open(sys.argv[1], "r", encoding="utf-8", errors="ignore").read()
syms = sorted(set(re.findall(r"VKAPI_CALL\s+(vk[A-Za-z0-9_]+)\s*\(", hdr)))
with open(sys.argv[2], "w", encoding="utf-8") as f:
    f.write("LIBRARY vulkan-1.dll\nEXPORTS\n")
    for s in syms:
        f.write(f"    {s}\n")
PY
    x86_64-w64-mingw32-dlltool -d "$cmake_dir/vulkan-1.def" -l "$cmake_dir/libvulkan-1.dll.a"

    cat > "$cmake_dir/mingw-toolchain.cmake" << 'EOF'
set(CMAKE_SYSTEM_NAME Windows)
set(CMAKE_SYSTEM_PROCESSOR x86_64)
set(CMAKE_C_COMPILER x86_64-w64-mingw32-gcc)
set(CMAKE_CXX_COMPILER x86_64-w64-mingw32-g++)
set(CMAKE_RC_COMPILER x86_64-w64-mingw32-windres)
set(CMAKE_FIND_ROOT_PATH_MODE_PROGRAM NEVER)
set(CMAKE_FIND_ROOT_PATH_MODE_LIBRARY ONLY)
set(CMAKE_FIND_ROOT_PATH_MODE_INCLUDE BOTH)
set(CMAKE_FIND_ROOT_PATH_MODE_PACKAGE BOTH)
EOF

    local prefix_args=()
    if [ -d "/home/linuxbrew/.linuxbrew" ]; then
        prefix_args+=("-DCMAKE_PREFIX_PATH=/home/linuxbrew/.linuxbrew")
    fi

    echo "==> Building Windows x64 towncrier-whisper.exe (Vulkan + AVX2/CPU)..."
    cmake -S "$ROOT_DIR/cmd/whisper-helper" -B "$cmake_dir" -G Ninja \
        -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_TOOLCHAIN_FILE="$cmake_dir/mingw-toolchain.cmake" \
        -DWHISPER_CPP_DIR="$WHISPER_SRC_DIR" \
        -DGGML_VULKAN=ON \
        -DGGML_NATIVE=OFF \
        -DGGML_AVX=ON \
        -DGGML_AVX2=ON \
        -DGGML_FMA=ON \
        -DGGML_F16C=ON \
        -DVulkan_INCLUDE_DIR="$vk_staged_inc" \
        -DVulkan_LIBRARY="$cmake_dir/libvulkan-1.dll.a" \
        -DVulkan_GLSLC_EXECUTABLE="$glslc_bin" \
        "${prefix_args[@]}"

    cmake --build "$cmake_dir" --config Release --target towncrier-whisper -j"$(nproc)"
    x86_64-w64-mingw32-strip --strip-unneeded "$cmake_dir/towncrier-whisper.exe"
    cp -f "$cmake_dir/towncrier-whisper.exe" "$out_bin"
    echo "$want_stamp" > "$stamp_file"
    echo "✓ Built $out_bin ($(du -h "$out_bin" | awk '{print $1}'))"
}

build_linux() {
    local out_bin="$BIN_DIR/towncrier-whisper"
    local stamp_file="$BUILD_DIR/whisper-build-linux/stamp.txt"
    local src_hash
    src_hash="$(sha256sum "$ROOT_DIR/cmd/whisper-helper/whisper_helper.cpp" "$ROOT_DIR/cmd/whisper-helper/CMakeLists.txt" | sha256sum | awk '{print $1}')"
    local want_stamp="${WHISPER_COMMIT}:${src_hash}"

    if [ -f "$out_bin" ] && [ -f "$stamp_file" ] && [ "$(cat "$stamp_file")" = "$want_stamp" ]; then
        echo "✓ Cached $out_bin is up to date ($WHISPER_COMMIT)."
        return 0
    fi

    ensure_whisper_source

    local cmake_dir="$BUILD_DIR/whisper-build-linux"
    mkdir -p "$cmake_dir"

    local prefix_args=()
    if [ -d "/home/linuxbrew/.linuxbrew" ]; then
        prefix_args+=("-DCMAKE_PREFIX_PATH=/home/linuxbrew/.linuxbrew")
    fi

    echo "==> Building Linux x64 towncrier-whisper (Vulkan + AVX2/CPU)..."
    cmake -S "$ROOT_DIR/cmd/whisper-helper" -B "$cmake_dir" -G Ninja \
        -DCMAKE_BUILD_TYPE=Release \
        -DWHISPER_CPP_DIR="$WHISPER_SRC_DIR" \
        -DGGML_VULKAN=ON \
        -DGGML_NATIVE=OFF \
        -DGGML_AVX=ON \
        -DGGML_AVX2=ON \
        -DGGML_FMA=ON \
        -DGGML_F16C=ON \
        "${prefix_args[@]}"

    cmake --build "$cmake_dir" --config Release --target towncrier-whisper -j"$(nproc)"
    strip --strip-unneeded "$cmake_dir/towncrier-whisper"
    cp -f "$cmake_dir/towncrier-whisper" "$out_bin"
    chmod +x "$out_bin"
    echo "$want_stamp" > "$stamp_file"
    echo "✓ Built $out_bin ($(du -h "$out_bin" | awk '{print $1}'))"
}

build_mac() {
    local out_bin="$BIN_DIR/towncrier-whisper"
    local stamp_file="$BUILD_DIR/whisper-build-mac/stamp.txt"
    local src_hash
    src_hash="$(shasum -a 256 "$ROOT_DIR/cmd/whisper-helper/whisper_helper.cpp" "$ROOT_DIR/cmd/whisper-helper/CMakeLists.txt" | shasum -a 256 | awk '{print $1}')"
    local want_stamp="${WHISPER_COMMIT}:${src_hash}"

    if [ -f "$out_bin" ] && [ -f "$stamp_file" ] && [ "$(cat "$stamp_file")" = "$want_stamp" ]; then
        echo "✓ Cached $out_bin is up to date ($WHISPER_COMMIT)."
        return 0
    fi

    ensure_whisper_source

    local cmake_dir="$BUILD_DIR/whisper-build-mac"
    mkdir -p "$cmake_dir"

    echo "==> Building macOS towncrier-whisper (Metal + CPU)..."
    cmake -S "$ROOT_DIR/cmd/whisper-helper" -B "$cmake_dir" \
        -DCMAKE_BUILD_TYPE=Release \
        -DWHISPER_CPP_DIR="$WHISPER_SRC_DIR" \
        -DGGML_METAL=ON \
        -DGGML_METAL_EMBED_LIBRARY=ON

    cmake --build "$cmake_dir" --config Release --target towncrier-whisper -j"$(sysctl -n hw.ncpu || echo 4)"
    strip -x "$cmake_dir/towncrier-whisper" || true
    cp -f "$cmake_dir/towncrier-whisper" "$out_bin"
    chmod +x "$out_bin"
    echo "$want_stamp" > "$stamp_file"
    echo "✓ Built $out_bin"
}

case "$TARGET_OS" in
    win|windows)
        build_win
        ;;
    linux)
        build_linux
        ;;
    mac|darwin)
        build_mac
        ;;
    *)
        echo "Unknown target OS: $TARGET_OS" >&2
        exit 1
        ;;
esac
