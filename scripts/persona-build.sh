#!/usr/bin/env bash
# persona-build.sh: build Nina the persona (nina-live) and what she is made of: her voice (fsttm-live, a
# Go module pinned in go.mod; its checkout for the radar), the Metis compiler and its repos. Nothing of a
# home here: no Home Assistant, no dashboards, no kiosk, no service restarted. A home's deployment
# (acasa: install/update.sh) calls this, then applies its own parts and restarts what it runs.
#
#   scripts/persona-build.sh            build what is checked out
#   scripts/persona-build.sh --pull     pull the persona's repos first (fast-forward only)
#
#   REPO_ROOT   where the repos are checked out side by side (default: this repo's parent)
#   METISC      the Metis compiler (default: metis-lang's, built here when missing or moved)
# Prints "persona: built <nina-live commit>" last; exits non-zero on any failure (the build's image
# check included: catalog/domains/image.golden).
set -euo pipefail
R=${REPO_ROOT:-$(cd "$(dirname "$0")/../.." && pwd)}
export PATH=/usr/local/go/bin:$PATH GOFLAGS=${GOFLAGS:--mod=mod}   # GOTOOLCHAIN as the machine has it: auto fetches the go.mod release where the installed Go is older (Pascal: 1.22)
PULL=0; [ "${1:-}" = "--pull" ] && PULL=1

# the persona's repos: nina-live itself, her rules (nina-narratives), her voice, and Metis (the compiler, its runtime and agent)
REPOS="nina-live nina-narratives fsttm-live perotheia-packages/metis-lang perotheia-packages/metis-loop
       perotheia-packages/metis-agent perotheia-packages/metis-worlds"
# her rules are nina-narratives' (catalog/domains links to ../../nina-narratives/devices since the redesign's
# step 2): a machine installed before it gets the clone here
[ -d "$R/nina-narratives/.git" ] || git clone -q https://github.com/ibnHatab/nina-narratives.git "$R/nina-narratives"
moved=""
if [ "$PULL" = 1 ]; then
  for d in $REPOS; do
    [ -d "$R/$d/.git" ] || { echo "persona: no checkout $R/$d" >&2; exit 1; }
    before=$(git -C "$R/$d" rev-parse HEAD)
    git -C "$R/$d" pull -q --ff-only
    after=$(git -C "$R/$d" rev-parse HEAD)
    [ "$before" != "$after" ] && moved="$moved $d"
    printf '%-34s %s%s\n' "$d" "${after:0:7}" "$([ "$before" != "$after" ] && echo "  (was ${before:0:7})")"
  done
fi

M=$R/perotheia-packages/metis-lang/src
if [ -z "${METISC:-}" ]; then
  # always make: incremental (a no-op when metisc is newer than its sources), and a metis-lang pulled some other
  # way (not --pull here) gets its compiler too (2026-11-28: a stale metisc refused the composition's person: kinds)
  echo "== metisc"; make -s -C "$M" metisc
  METISC=$M/metisc
fi

echo "== nina (the composition's image, the persona compiled, bin/nina-whatsapp; bin/nina is the deployment's: acasa live/build.sh)"
(cd "$R/nina-live" && METISC="$METISC" ./build.sh)
echo "== the radar (fsttm-live cmd/radar: her presence sensor)"
(cd "$R/fsttm-live" && go build -o bin/radar ./cmd/radar)
echo "persona: built $(git -C "$R/nina-live" rev-parse --short HEAD)"
