"""Generate Camera Connect brand icon (camera + sync arrows).

Outputs:
  assets/icon.png        1024x1024 master (README logo, wails appicon source)
  assets/icon.ico        multi-size Windows icon
  frontend/public/icon.png  256px favicon
"""
import math
import os
from PIL import Image, ImageDraw

SS = 4            # supersample factor
S = 1024
W = S * SS

BG_TOP = (36, 40, 48)
BG_BOT = (20, 22, 28)
ACCENT = (37, 99, 235)      # #2563eb theme accent
ACCENT_L = (96, 165, 250)   # lighter blue for arrow highlight
WHITE = (240, 244, 250)
INK = (14, 17, 23)


def lerp(a, b, t):
    return tuple(int(a[i] + (b[i] - a[i]) * t) for i in range(3))


def rounded(d, box, r, **kw):
    d.rounded_rectangle(box, radius=r, **kw)


def arrow_arc(d, cx, cy, r, start_deg, end_deg, width, color, head_deg=None, head_len=None):
    """Arc with an arrowhead at the END angle (CCW sweep start->end)."""
    d.arc([cx - r, cy - r, cx + r, cy + r], start=start_deg, end=end_deg,
          fill=color, width=width)
    ha = head_deg if head_deg is not None else end_deg
    hl = head_len if head_len is not None else width * 2.6
    # point on circle at ha (screen coords: 0deg=3 o'clock, CW positive in PIL)
    a = math.radians(ha)
    px, py = cx + r * math.cos(a), cy + r * math.sin(a)
    # tangent direction at ha for CW sweep is (sin a, -cos a)*sign of direction;
    # for arc drawn start->end (PIL goes CW visually), tangent = (-sin a, cos a)
    tx, ty = math.sin(a), -math.cos(a)
    tipx, tipy = px + tx * hl, py + ty * hl
    # two barbs
    bl = hl * 0.62
    d.polygon([(tipx, tipy),
               (px - ty * bl * 0.5, py + tx * bl * 0.5),
               (px + tx * hl * 0.55, py + ty * hl * 0.55),
               (px + ty * bl * 0.5, py - tx * bl * 0.5)], fill=color)


def draw_icon(size_s):
    im = Image.new("RGBA", (size_s, size_s), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    s = size_s

    # rounded-square background, vertical gradient
    bg = Image.new("RGB", (1, s))
    for y in range(s):
        bg.putpixel((0, y), lerp(BG_TOP, BG_BOT, y / s))
    bg = bg.resize((s, s))
    mask = Image.new("L", (s, s), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, s - 1, s - 1], radius=int(s * 0.22), fill=255)
    im.paste(bg, (0, 0), mask)

    # subtle top sheen
    sheen = Image.new("L", (s, s), 0)
    ImageDraw.Draw(sheen).ellipse([int(s * 0.05), int(-s * 0.55), int(s * 0.95), int(s * 0.45)], fill=26)
    im.paste(Image.new("RGB", (s, s), (255, 255, 255)), (0, 0), Image.composite(sheen, Image.new("L", (s, s), 0), mask))

    # camera body
    bx0, by0, bx1, by1 = int(s * 0.13), int(s * 0.33), int(s * 0.87), int(s * 0.80)
    rounded(d, [bx0, by0, bx1, by1], int(s * 0.075), fill=ACCENT)
    # body top highlight
    rounded(d, [bx0, by0, bx1, int(by0 + s * 0.045)], int(s * 0.03), fill=lerp(ACCENT, (255, 255, 255), 0.35))

    # viewfinder hump
    hw = int(s * 0.20)
    hx0 = int(s * 0.5) - hw // 2
    rounded(d, [hx0, int(s * 0.245), hx0 + hw, int(s * 0.375)], int(s * 0.05), fill=ACCENT)
    rounded(d, [hx0, int(s * 0.245), hx0 + hw, int(s * 0.285)], int(s * 0.03), fill=lerp(ACCENT, (255, 255, 255), 0.35))

    # shutter button
    rounded(d, [int(s * 0.72), int(s * 0.27), int(s * 0.815), int(s * 0.315)], int(s * 0.02),
            fill=lerp(ACCENT, (255, 255, 255), 0.2))

    # flash dot
    fr = int(s * 0.028)
    d.ellipse([int(s * 0.70) - fr, int(s * 0.40) - fr, int(s * 0.70) + fr, int(s * 0.40) + fr], fill=WHITE)

    # lens: white ring + dark glass
    cx, cy = s // 2, int(s * 0.575)
    r_out = int(s * 0.235)
    r_glass = int(s * 0.175)
    d.ellipse([cx - r_out, cy - r_out, cx + r_out, cy + r_out], fill=WHITE)
    d.ellipse([cx - r_glass, cy - r_glass, cx + r_glass, cy + r_glass], fill=INK)

    # sync arrows inside the lens (two curved arrows chasing each other)
    r_arc = int(s * 0.105)
    aw = max(6, int(s * 0.030))
    # top arrow sweeping right-down (90deg -> ~5deg visually top to right)
    arrow_arc(d, cx, cy, r_arc, start_deg=200, end_deg=75, width=aw, color=ACCENT_L, head_deg=60)
    # bottom arrow sweeping left-up
    arrow_arc(d, cx, cy, r_arc, start_deg=20, end_deg=255, width=aw, color=WHITE, head_deg=240)

    return im


def with_status_dot(base, color):
    """Small status dot at bottom-right for tray variants."""
    im = base.copy()
    d = ImageDraw.Draw(im)
    s = im.width
    r = int(s * 0.13)
    x1, y1 = int(s * 0.98), int(s * 0.98)
    # white halo for contrast on taskbar backgrounds
    d.ellipse([x1 - r - int(s*0.025), y1 - r - int(s*0.025), x1 + int(s*0.02), y1 + int(s*0.02)], fill=WHITE)
    d.ellipse([x1 - r, y1 - r, x1, y1], fill=color)
    return im


def main():
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    assets = os.path.join(root, "assets")
    os.makedirs(assets, exist_ok=True)

    big = draw_icon(W)
    icon1024 = big.resize((S, S), Image.LANCZOS)
    icon1024.save(os.path.join(assets, "icon.png"))

    sizes = [16, 24, 32, 48, 64, 128, 256]
    icon1024.save(os.path.join(assets, "icon.ico"), sizes=[(z, z) for z in sizes])

    pub = os.path.join(root, "frontend", "public")
    os.makedirs(pub, exist_ok=True)
    icon1024.resize((256, 256), Image.LANCZOS).save(os.path.join(pub, "icon.png"))

    # Tray status variants — dot overlay communicates state at a glance.
    tray_dir = os.path.join(root, "cmd", "agent")
    for name, color in [
        ("tray_blue", (59, 130, 246)),
        ("tray_green", (34, 197, 94)),
        ("tray_orange", (249, 115, 22)),
        ("tray_red", (239, 68, 68)),
        ("tray_gray", (107, 114, 128)),
    ]:
        v = with_status_dot(big, color).resize((S, S), Image.LANCZOS)
        v.save(os.path.join(tray_dir, f"{name}.ico"), sizes=[(z, z) for z in sizes])
    print("icons written:", os.listdir(assets), os.listdir(pub), os.listdir(tray_dir))


if __name__ == "__main__":
    main()
