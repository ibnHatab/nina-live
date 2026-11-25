#!/usr/bin/env bash
# pi-update.sh: on the Raspberry Pi, bring Nina up to date from GitHub (the user, 2026-11-24:
# deployment through GitHub, not rsync from Pascal). Every repo is public, so the Pi pulls without
# credentials; Go fetches the modules itself. Run on the Pi:   ~/repo/nina-live/scripts/pi-update.sh
#   pulls nina-live, acasa, fsttm-live and the Metis repos (fast-forward only), rebuilds metisc when
#   metis-lang moved, builds nina, applies the look and feel to the Pi's Home Assistant, and restarts
#   Nina's service only if it is enabled here (on the Pi it is off until she moves there).
#   --kiosk also installs acasa's kiosk files and restarts the kiosk's Firefox.
set -euo pipefail
R=$HOME/repo
export PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=local GOFLAGS=-mod=mod
KIOSK=0; [ "${1:-}" = "--kiosk" ] && KIOSK=1
moved=""
for d in nina-live acasa fsttm-live perotheia-packages/metis-lang perotheia-packages/metis-loop \
         perotheia-packages/metis-agent perotheia-packages/metis-worlds; do
  before=$(git -C "$R/$d" rev-parse HEAD)
  git -C "$R/$d" pull -q --ff-only
  after=$(git -C "$R/$d" rev-parse HEAD)
  [ "$before" != "$after" ] && moved="$moved $d"
  printf '%-34s %s%s\n' "$d" "${after:0:7}" "$([ "$before" != "$after" ] && echo "  (was ${before:0:7})")"
done
M=$R/perotheia-packages/metis-lang/src
if [ ! -x "$M/metisc" ] || [[ "$moved" == *metis-lang* ]]; then
  echo "== metisc"; make -s -C "$M" metisc
fi
echo "== nina"
(cd "$R/nina-live" && METISC="$M/metisc" ./build.sh)
echo "== the radar"
(cd "$R/fsttm-live" && go build -o bin/radar ./cmd/radar)
if systemctl --user is-enabled -q nina-radar 2>/dev/null; then systemctl --user restart nina-radar; echo "nina-radar: $(systemctl --user is-active nina-radar)"; fi
echo "== the look and feel"
# this machine's own Home Assistant (homeassistant.local is the first Pi's name: on another machine,
# another instance)
(cd "$R/acasa" && go run ./cmd/ha-ui -url http://127.0.0.1:8123 -config "$HOME/homeassistant/config" 2>&1 | grep -E "dashboard|themes|rror" || true)
if [ "$KIOSK" = 1 ]; then
  echo "== the kiosk"
  K=$R/acasa/ha-ui/kiosk
  sudo install -m 755 "$K/firefox-kiosk.sh" /usr/local/bin/firefox-kiosk.sh
  sudo install -m 755 "$K/backlight-control.sh" /usr/local/bin/backlight-control.sh
  sudo install -D -m 644 "$K/starting.html" /usr/local/share/ha-pi-kiosk/starting.html
  sudo install -m 644 "$K/logo.png" /usr/local/share/ha-pi-kiosk/logo.png
  # the kiosk shows this machine's own Home Assistant (the repo's file names the first Pi's)
  sed "s|homeassistant.local|$(hostname).local|g" "$K/config.env" | sudo install -D -m 644 /dev/stdin /etc/ha-pi-kiosk/config.env
  pkill -x firefox || true
fi
if systemctl --user is-enabled -q nina 2>/dev/null; then
  systemctl --user restart nina && sleep 8 && echo "nina: $(systemctl --user is-active nina)"
else
  echo "nina: not enabled here (built, not started)"
fi
