"""depth.py: a depth map for each persona (Depth Anything V2 Small, once, on Pascal's GPU), for
the card's parallax. Near is white. Smoothed, so the shift bends instead of tearing at her hair's
edge; one map per persona (its frames differ only at the mouth and eyes).

  <venv with torch+transformers>/bin/python depth.py DIR    DIR/<persona>/neutral.png -> web/depth.webp
"""
import os, sys
import numpy as np
import torch
from PIL import Image, ImageFilter
from transformers import pipeline

d = sys.argv[1]
pipe = pipeline("depth-estimation", model="depth-anything/Depth-Anything-V2-Small-hf", device=0 if torch.cuda.is_available() else -1)
for name in sorted(os.listdir(d)):
    src = os.path.join(d, name, "neutral.png")
    if not os.path.exists(src):
        continue
    img = Image.open(src).convert("RGB")
    depth = pipe(img)["predicted_depth"]
    dm = torch.nn.functional.interpolate(depth[None, None] if depth.dim() == 2 else depth[None], size=img.size[::-1], mode="bicubic")[0, 0].cpu().numpy()
    lo, hi = np.percentile(dm, 2), np.percentile(dm, 98)
    dm = np.clip((dm - lo) / (hi - lo), 0, 1)
    out = Image.fromarray((dm * 255).astype(np.uint8)).resize((img.width // 2, img.height // 2), Image.BILINEAR)
    out = out.filter(ImageFilter.GaussianBlur(6))
    os.makedirs(os.path.join(d, name, "web"), exist_ok=True)
    out.save(os.path.join(d, name, "web", "depth.webp"), quality=85)
    print(name, out.size)
