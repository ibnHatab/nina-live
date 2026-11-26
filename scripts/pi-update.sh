#!/usr/bin/env bash
# pi-update.sh: moved (2026-11-26). The persona's build is scripts/persona-build.sh (here, nina-live);
# a machine's update (the persona, then its Home Assistant, its kiosk, its services) is acasa's
# install/update.sh. This name stays so the habit and older notes keep working.
exec "${REPO_ROOT:-$HOME/repo}/acasa/install/update.sh" "$@"
