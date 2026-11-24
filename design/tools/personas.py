"""personas.py: Nina's personas (Claudia's ev-01 expression sheets, 2026-11-24): for each, a
neutral base portrait from her face sheet and the sheet's style, then (`edits`) the four frames
the card animates, as edits of that base; then design/tools/sprites.py.

  python3 personas.py DIR base [NAME...]     DIR/<name>/neutral.png
  python3 personas.py DIR edits [NAME...]    DIR/<name>/{talk-a,talk-b,blink,smile}.png
"""
import json, os, subprocess, sys
from concurrent.futures import ThreadPoolExecutor

d, step, names = sys.argv[1], sys.argv[2], sys.argv[3:]
spec = json.load(open(os.path.join(d, "personas.json")))
names = names or list(spec)
still = os.environ["STILL"]  # the OpenRouter image script
face = os.path.expanduser("~/.cache/claudia/face-1280.jpg")
gal = os.path.expanduser("~/.cache/claudia/gallery/ev-01-m")
MODEL = "openai/gpt-5.4-image-2"

def base(name):
    p = spec[name]; out = os.path.join(d, name); os.makedirs(out, exist_ok=True)
    ref = os.path.join(out, "sheet.jpg")
    subprocess.run(["convert", os.path.join(gal, p["sheet"] + ".webp"), "-resize", "1400x", "-quality", "90", ref], check=True)
    txt = (f"A single head-and-shoulders portrait: {p['style']}. She is the woman on the reference sheet in the first "
           f"image: the same face, a 28-year-old Caucasian American woman, a grown woman, with a grown-up angular face, "
           f"defined cheekbones and a strong jawline, pale skin and light freckles, {p['hair']}, a thin headset "
           f"microphone at her cheek, its tip well away from her mouth. She wears {p['wardrobe']}. She faces the viewer, "
           f"her head turned very slightly to her left, chin level, eyes open and looking at the viewer, her mouth closed "
           f"and relaxed, a calm face with warmth in the eyes. Vertical 4:5 frame, her head in the middle of the frame "
           f"with plenty of plain background around it on every side, her shoulders reaching the bottom of the picture. "
           f"Only one of her, one portrait, not a sheet or a grid. The picture fills the whole frame edge to edge, no "
           f"border. No text, no letters, no logos.")
    open(os.path.join(out, "neutral.txt"), "w").write(txt)
    return subprocess.run(["python3", "-I", still, MODEL, os.path.join(out, "neutral.png"), os.path.join(out, "neutral.txt"), face, ref],
                          capture_output=True, text=True, env=dict(os.environ, AR="4:5")).stdout.strip()

KEEP = ("Edit the first image. Keep everything else exactly as it is: the same picture, the same medium, strokes, light "
        "and background, the same pose, the head at exactly the same position, size and angle in the frame, the same "
        "hair, star clip, headset microphone, clothes and shoulders, the same framing. Do not move, redraw or restyle "
        "anything else. No text, no letters, no logos. ")
EDITS = {
    "talk-a": "Change only her mouth: her lips are parted a little, as if in the middle of a word, the tips of her upper teeth just visible.",
    "talk-b": "Change only her mouth: her mouth is open in the middle of speaking a vowel, lips relaxed and rounded, her upper teeth visible, a little of her tongue in shadow.",
    "blink": "Change only her eyes: her eyelids are closed, relaxed, as in the middle of a blink, her lashes resting.",
    "smile": "Change only her expression: a small, warm closed-mouth smile, the corners of her mouth lifted, her cheeks and the corners of her eyes softening with it.",
}

def edit(job):
    name, e = job; out = os.path.join(d, name)
    if os.path.exists(os.path.join(out, e + ".png")):
        return f"{name}/{e} kept"
    open(os.path.join(out, e + ".txt"), "w").write(KEEP + EDITS[e])
    r = subprocess.run(["python3", "-I", still, MODEL, os.path.join(out, e + ".png"), os.path.join(out, e + ".txt"),
                           os.path.join(out, "neutral.png")], capture_output=True, text=True,
                          env=dict(os.environ, AR="4:5"))
    return r.stdout.strip() or f"{name}/{e} FAILED: {r.stderr.strip()[-300:]}"

with ThreadPoolExecutor(int(os.environ.get('JOBS', '2'))) as ex:
    if step == "base":
        for r in ex.map(base, names): print(r)
    else:
        for r in ex.map(edit, [(n, e) for n in names for e in EDITS]): print(r)
