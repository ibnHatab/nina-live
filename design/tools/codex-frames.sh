#!/usr/bin/env bash
# codex-frames.sh: the personas' missing frames made by Codex's image generation (the user's
# ChatGPT plan) instead of OpenRouter. One `codex exec` per persona: its neutral portrait
# attached, each missing frame an edit of it (the prompt in <frame>.txt), saved as <frame>.png.
#   design/tools/codex-frames.sh [persona...]     (default: the clay personas)
set -u
P=$(cd "$(dirname "$0")/../stills/personas" && pwd)
for n in "${@:-snapshot sketch tinted porcelain}"; do
  for name in $n; do
    d=$P/$name
    todo=""
    for f in talk-a talk-b blink smile; do [ -f "$d/$f.png" ] || todo="$todo $f"; done
    [ -z "$todo" ] && { echo "== $name: complete"; continue; }
    echo "== $name:$todo ($(date +%T))"
    list=""
    for f in $todo; do list="$list
- $f.png: $(cat "$d/$f.txt")"; done
    codex exec --skip-git-repo-check -i "$d/neutral.png" -s workspace-write -C "$d" "You are making animation frames of one portrait. The attached image is neutral.png in this folder (a 896x1120 portrait).

For each frame below, use your image generation tool to make an EDIT of the attached image (the attached image is the reference: the same picture, the same person, the same framing; only the change named). Make each one separately, a vertical portrait as close as you can to the attached image's 4:5 proportions. After each one, copy the generated PNG from ~/.codex/generated_images into this folder under the frame's file name (overwrite nothing else), then go on to the next.

Frames (file name: what to change):$list

When all are saved, list the files you wrote with their pixel sizes (use the 'file' command). Do not edit any other file." 2>&1 | tail -40
  done
done
echo "== done ($(date +%T))"
