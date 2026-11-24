#!/usr/bin/env bash
# Build nina (cgo: metis-lang's libmetis_rt for the home's rules; fsttm-live's sherpa-onnx and
# WebRTC VAD come with their modules).
#   METIS_LANG_DIR  the metis-lang checkout (default ../perotheia-packages/metis-lang), its runtime
#                   built there (cmake) when missing
#   ./build.sh            → bin/nina
#   ./build.sh test|vet [pkgs...]
set -euo pipefail
cd "$(dirname "$0")"
METIS_LANG_DIR="${METIS_LANG_DIR:-$(cd .. && pwd)/perotheia-packages/metis-lang}"
[ -f "$METIS_LANG_DIR/runtime/c/metis_rt.h" ] || { echo "no metis-lang runtime at $METIS_LANG_DIR (set METIS_LANG_DIR)" >&2; exit 1; }
RTLIB="$METIS_LANG_DIR/runtime/build"
[ -f "$RTLIB/CMakeCache.txt" ] || cmake -S "$METIS_LANG_DIR/runtime" -B "$RTLIB" > /dev/null
cmake --build "$RTLIB" --target metis_rt -j4 > /dev/null
export CGO_CFLAGS="-I$METIS_LANG_DIR/runtime/c"
export CGO_LDFLAGS="-L$RTLIB -Wl,-rpath,$RTLIB"
if [ "${1:-}" = "test" ] || [ "${1:-}" = "vet" ]; then
  verb=$1; shift
  exec go "$verb" "${@:-./...}"
fi
mkdir -p bin
go build -o bin/nina ./cmd/nina
echo "built → bin/nina"
