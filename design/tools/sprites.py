"""sprites.py: a talking head's frames that differ only where the face moves.

The image model redraws the whole picture on every edit, so its frames are a few pixels apart
everywhere. Each frame is registered onto the base (ECC, affine), then only a feathered patch
(the mouth, the eyes, the lower face) is taken from it; everywhere else it is the base. The
browser can then crossfade between frames without ghosting.

  python3 sprites.py DIR            DIR/neutral.png + DIR/<frame>.png -> DIR/web/<frame>.webp
Patches are ellipses (cx, cy, rx, ry) in the base's pixels, in DIR/patches.json.
"""
import json, os, sys
import cv2
import numpy as np

d = sys.argv[1]
spec = json.load(open(os.path.join(d, "patches.json")))
base = cv2.imread(os.path.join(d, "neutral.png"))
h, w = base.shape[:2]
gb = cv2.cvtColor(base, cv2.COLOR_BGR2GRAY).astype(np.float32)
out = os.path.join(d, "web")
os.makedirs(out, exist_ok=True)
cv2.imwrite(os.path.join(out, "neutral.webp"), base, [cv2.IMWRITE_WEBP_QUALITY, 82])
for name, ellipses in spec.items():
    img = cv2.imread(os.path.join(d, name + ".png"))
    g = cv2.cvtColor(img, cv2.COLOR_BGR2GRAY).astype(np.float32)
    warp = np.eye(2, 3, dtype=np.float32)
    # register on the face only (the hair and shoulders are what the model redrew most)
    mask = np.zeros((h, w), np.uint8)
    for cx, cy, rx, ry in ellipses:
        cv2.ellipse(mask, (cx, cy), (int(rx * 1.8), int(ry * 1.8)), 0, 0, 360, 255, -1)
    try:
        _, warp = cv2.findTransformECC(gb, g, warp, cv2.MOTION_AFFINE,
                                       (cv2.TERM_CRITERIA_EPS | cv2.TERM_CRITERIA_COUNT, 200, 1e-6), mask, 5)
    except cv2.error as e:
        print(name, "ECC did not converge:", e)
    aligned = cv2.warpAffine(img, warp, (w, h), flags=cv2.INTER_LINEAR + cv2.WARP_INVERSE_MAP, borderMode=cv2.BORDER_REPLICATE)
    m = np.zeros((h, w), np.float32)
    for cx, cy, rx, ry in ellipses:
        cv2.ellipse(m, (cx, cy), (rx, ry), 0, 0, 360, 1.0, -1)
    m = cv2.GaussianBlur(m, (0, 0), max(6, min(r for e in ellipses for r in e[2:]) / 3))[..., None]
    comp = (aligned * m + base * (1 - m)).astype(np.uint8)
    cv2.imwrite(os.path.join(out, name + ".webp"), comp, [cv2.IMWRITE_WEBP_QUALITY, 82])
    print(name, "shift %.1f,%.1f px" % (warp[0, 2], warp[1, 2]))
