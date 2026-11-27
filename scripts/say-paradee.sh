#!/usr/bin/env bash
# say "text": Pascal's Paradee voice (its socket, /opt/xsel-to-speech) through the speakers: a test voice for
# a device's mic (the Android app, 2026-11-27). No "Nina" in the text: Pascal's own Nina wakes on it.
printf '1.00 af_heart\n%s' "$1" | socat -t 60 - "UNIX-CONNECT:$XDG_RUNTIME_DIR/paradee-tts.sock" | aplay -q
