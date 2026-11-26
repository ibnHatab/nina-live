#!/usr/bin/env bash
# link.sh: make this repo's skills visible to Claude Code (one symlink per skill in the skills folder
# of the Claude config, as metis-loop's is). Re-run after adding a skill; existing links are replaced.
#   skills/link.sh                 → ~/.claude/skills
#   CLAUDE_SKILLS=DIR skills/link.sh
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
dest=${CLAUDE_SKILLS:-$HOME/.claude/skills}
mkdir -p "$dest"
for d in "$here"/*/; do
  [ -f "$d/SKILL.md" ] || continue
  n=$(basename "$d")
  if [ -e "$dest/$n" ] && [ ! -L "$dest/$n" ]; then echo "skip $n: $dest/$n exists and is not a link" >&2; continue; fi
  ln -sfn "${d%/}" "$dest/$n"
  echo "$dest/$n -> ${d%/}"
done
