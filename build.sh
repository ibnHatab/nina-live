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
if [ "${1:-}" = "exp" ]; then        # the experiments (cmd/nina-exp): ./build.sh exp [flags]
  shift
  exec go run ./cmd/nina-exp "$@"
fi
if [ "${1:-}" = "test" ] || [ "${1:-}" = "vet" ]; then
  verb=$1; shift
  export METISC="${METISC:-$METIS_LANG_DIR/src/metisc}"   # core's rigs run the rules (they skipped without it)
  exec go "$verb" "${@:-./...}"
fi
# the home's composition image (catalog/domains/build/, not committed: image.golden pins its hash)
METISC="${METISC:-$METIS_LANG_DIR/src/metisc}"
# the image just built (metisc's "image home v-…" line), not the first one left in build/ (an older one
# stays there after a rules change: the check had failed on pero, 2026-11-26)
out=$(cd catalog/domains && "$METISC" build .)
grep -E '^(image|result)' <<<"$out"
built=$(grep -o '^image home v-[0-9a-f]*' <<<"$out" | grep -o 'v-[0-9a-f]*' | head -1)
[ "$built" = "v-$(cat catalog/domains/image.golden)" ] || { echo "the composition's image ($built) is not image.golden's: metisc build . changed it (commit the new hash when meant)" >&2; exit 1; }
# the images it superseded go: nina refuses to start with two in build/ (pero, 2026-11-26)
for d in catalog/domains/build/home.image.v-*; do [ "$d" = "catalog/domains/build/home.image.$built" ] || rm -rf "$d"; done
mkdir -p bin
go build -o bin/nina ./cmd/nina
go build -o bin/nina-whatsapp ./cmd/nina-whatsapp # her WhatsApp, linked by hand (whatsapp/)
echo "built → bin/nina, bin/nina-whatsapp"
