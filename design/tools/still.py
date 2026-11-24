# still.py: one OpenRouter image (an edit of the given references), key from ~/.config/metis/openrouter.env; AR=4:5 etc.
# still.py MODEL OUT PROMPTFILE [REF...]: one OpenRouter image edit, refs as images; prints the cost
import base64, json, sys, urllib.request, os, re
model, out, pf, refs = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4:]
key = re.search(r'OPENROUTER_API_KEY="?([^"\n]+)', open(os.path.expanduser("~/.config/metis/openrouter.env")).read()).group(1)
content = [{"type": "text", "text": open(pf).read()}]
for r in refs:
    mt = "image/png" if r.endswith(".png") else "image/jpeg"
    content.append({"type": "image_url", "image_url": {"url": f"data:{mt};base64," + base64.b64encode(open(r, "rb").read()).decode()}})
body = {"model": model, "modalities": ["image", "text"], "messages": [{"role": "user", "content": content}],
        "image_config": {"aspect_ratio": os.environ.get("AR","4:5")}, "usage": {"include": True}}
req = urllib.request.Request("https://openrouter.ai/api/v1/chat/completions", json.dumps(body).encode(),
        {"Authorization": "Bearer " + key, "Content-Type": "application/json"})
d = json.load(urllib.request.urlopen(req, timeout=300))
msg = d["choices"][0]["message"]
imgs = msg.get("images") or []
if not imgs:
    print("no image:", (msg.get("content") or "")[:300]); sys.exit(1)
url = imgs[0]["image_url"]["url"]
open(out, "wb").write(base64.b64decode(url.split(",", 1)[1]))
print(out, "cost $%.4f" % d.get("usage", {}).get("cost", 0))
