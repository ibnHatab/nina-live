#!/usr/bin/env bash
# Deploy nina-live to the Raspberry Pi: develop on Pascal, run on the Pi (2026-11-24).
#   scripts/deploy-pi.sh            sources and the Go module cache to the Pi, build there, restart nina
#   PI=nina@10.0.0.26 scripts/deploy-pi.sh
# The Pi builds with its own toolchain (Go 1.26 arm64, metisc from Debian's OCaml); the private
# modules come from Pascal's module cache, served to the Pi's Go as a file proxy (no GitHub there).
set -euo pipefail
cd "$(dirname "$0")/.."
PI="${PI:-nina@homeassistant.local}"
rsync -a --delete --exclude bin/ --exclude runs/ --exclude 'catalog/domains/build/' --exclude 'catalog/domains/*/build/' \
  --exclude __pycache__ ./ "$PI:repo/nina-live/"
rsync -a "$HOME/go/pkg/mod/cache/download/" "$PI:go-proxy/"
ssh "$PI" 'export PATH=/usr/local/go/bin:$PATH GOPROXY=file://$HOME/go-proxy GOSUMDB=off GOFLAGS=-mod=mod GOTOOLCHAIN=local
  cd ~/repo/nina-live && ./build.sh && systemctl --user restart nina && sleep 8 && systemctl --user is-active nina'
