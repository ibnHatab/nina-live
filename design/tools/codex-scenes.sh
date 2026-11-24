#!/usr/bin/env bash
# codex-scenes.sh: Nina's domain sequences (design/stills/scenes/scenes.json) by Codex's image
# generation: per scene, frame 1 from Claudia's face sheet and the boot still's look, then each next
# frame as an edit of frame 1. Skips frames made.   design/tools/codex-scenes.sh [scene...]
set -u
D=$(cd "$(dirname "$0")/../stills/scenes" && pwd)
FACE=~/.cache/claudia/face-1280.jpg
LOOK=$(cd "$(dirname "$0")/../stills/round1" && pwd)/boot-gpt.png
J=$D/scenes.json
scenes=$(python3 -c 'import json,sys; print(" ".join(json.load(open(sys.argv[1]))["scenes"]))' "$J")
for s in ${@:-$scenes}; do
  mkdir -p "$D/$s"
  n=$(python3 -c 'import json,sys; print(len(json.load(open(sys.argv[1]))["scenes"][sys.argv[2]]))' "$J" "$s")
  for i in $(seq 1 "$n"); do
    out="$D/$s/$i.png"; [ -f "$out" ] && { echo "== $s/$i kept"; continue; }
    text=$(python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(d["scenes"][sys.argv[2]][int(sys.argv[3])-1])' "$J" "$s" "$i")
    echo "== $s/$i ($(date +%T))"
    if [ "$i" = 1 ]; then
      ident=$(python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(d["identity"]+" "+d["look"])' "$J")
      codex exec --skip-git-repo-check -i "$FACE" "$LOOK" -s workspace-write -C "$D/$s" "Use your image generation tool to make ONE new image: $text $ident The first attached image is her face reference sheet (identity only); the second is the style reference (light, colour, grain only: not its scene). Save the generated PNG from ~/.codex/generated_images into this folder as $i.png, then report its pixel size with the file command. Do not edit any other file." 2>&1 | tail -4
    else
      codex exec --skip-git-repo-check -i "$D/$s/1.png" -s workspace-write -C "$D/$s" "Use your image generation tool to make an EDIT of the attached image (1.png in this folder): keep everything else exactly as it is (the same woman, face, hair, streak, star clip, headset microphone, clothes, room, light, framing and grain). Change only this: $text No text, no letters, no numbers, no logos. Save the generated PNG from ~/.codex/generated_images into this folder as $i.png, then report its pixel size with the file command. Do not edit any other file." 2>&1 | tail -4
    fi
  done
done
echo "== done ($(date +%T))"
