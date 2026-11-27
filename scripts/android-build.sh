#!/usr/bin/env bash
# Nina in fsttm-live's Android app: the persona's native library (metis-lang's libmetis_rt, built
# for android/arm with the NDK) and its Go package (mobile/) bound into fsttm-live's APK.
#   scripts/android-build.sh [--install]   → fsttm-live/android/out/fsttm-live.apk
# --install: adb install it and push the catalog (the composition's image) to the app's files.
# Env: REPO_ROOT (~/repo), METIS_LANG_DIR, LOCAL_FSTTM=1 (bind the fsttm-live checkout, not go.mod's).
set -euo pipefail
here=$(cd "$(dirname "$0")/.." && pwd)
REPO_ROOT="${REPO_ROOT:-$(cd "$here/.." && pwd)}"
METIS_LANG_DIR="${METIS_LANG_DIR:-$REPO_ROOT/perotheia-packages/metis-lang}"
export ANDROID_HOME="${ANDROID_HOME:-$HOME/Android/Sdk}"
export ANDROID_NDK_HOME="${ANDROID_NDK_HOME:-$(ls -d "$ANDROID_HOME"/ndk/* | sort -V | tail -1)}"
TC="$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin"
cache="${XDG_CACHE_HOME:-$HOME/.cache}/nina-android"; mkdir -p "$cache"

echo "== libmetis_rt (android/arm)"
rt="$METIS_LANG_DIR/runtime"
"$TC/armv7a-linux-androideabi25-clang++" -std=c++17 -O2 -fPIC -I"$rt/include" -I"$rt/c" -c "$rt/c/metis_rt.cpp" -o "$cache/metis_rt.o"
"$TC/llvm-ar" rcs "$cache/libmetis_rt.a" "$cache/metis_rt.o"
export CGO_CFLAGS="-I$rt/c" CGO_LDFLAGS="-L$cache -lmetis_rt -lc++_static -lc++abi"

export GOWORK=off GOTOOLCHAIN="${GOTOOLCHAIN_BIND:-go1.26.11}"   # gomobile's module asks go 1.26: a released toolchain, not the local 1.26.0
if [ "${LOCAL_FSTTM:-1}" = 1 ]; then   # the fsttm-live checkout beside it, not go.mod's pinned version:
  # gomobile copies go.mod's replaces into its own module (it does not read a go.work), so a replace
  # for the build only, go.mod and go.sum restored on exit
  cp "$here/go.mod" "$cache/go.mod.keep"; cp "$here/go.sum" "$cache/go.sum.keep"
  trap 'cp "$cache/go.mod.keep" "$here/go.mod"; cp "$cache/go.sum.keep" "$here/go.sum"' EXIT
  # and a stub for whisper.cpp's Go bindings (fsttm/go requires them at v0.0.0 through its own local
  # replace; nothing bound imports them, but gomobile's go list -m all must resolve every module)
  mkdir -p "$cache/whisper-stub" && printf 'module github.com/ggerganov/whisper.cpp/bindings/go\n\ngo 1.22\n' > "$cache/whisper-stub/go.mod"
  (cd "$here" && go mod edit -replace "github.com/ibnHatab/fsttm-live=$REPO_ROOT/fsttm-live" \
     -replace "github.com/ggerganov/whisper.cpp/bindings/go=$cache/whisper-stub" && go mod tidy > /dev/null 2>&1 || true)
fi
MODULE_DIR="$here" "$REPO_ROOT/fsttm-live/android/build.sh" github.com/ibnHatab/nina-live/mobile

if [ "${1:-}" = --install ]; then
  apk="$REPO_ROOT/fsttm-live/android/out/fsttm-live.apk"
  adb install -r "$apk"
  files=/sdcard/Android/data/live.fsttm.app/files
  adb shell mkdir -p "$files"
  adb push "$here/catalog" "$files/" > /dev/null && echo "catalog → $files/catalog"
fi
