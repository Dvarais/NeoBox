import os
from PIL import Image, ImageDraw

def render_neobox_icon(size, desaturated=False):
    # Supersample at 4x for crystal-clear antialiased vector rasterization
    scale = 4
    canvas_size = size * scale
    orig_viewbox = 256.0
    factor = canvas_size / orig_viewbox

    img = Image.new("RGBA", (canvas_size, canvas_size), (0, 0, 0, 0))
    draw = ImageDraw.Draw(img)

    def to_coords(pts):
        return [(x * factor, y * factor) for x, y in pts]

    def to_color(r, g, b, a=255):
        if desaturated:
            lum = int(0.299 * r + 0.587 * g + 0.114 * b)
            return (lum, lum, lum, a)
        return (r, g, b, a)

    p1 = [(52, 44), (100, 44), (100, 212), (52, 212)]
    p2 = [(100, 44), (100, 108), (156, 212), (156, 148)]
    p3 = [(156, 44), (204, 44), (204, 212), (156, 212)]
    p4 = [(100, 44), (156, 148), (100, 108)]

    draw.polygon(to_coords(p1), fill=to_color(56, 189, 248, 255))
    draw.polygon(to_coords(p2), fill=to_color(2, 132, 199, 255))
    draw.polygon(to_coords(p3), fill=to_color(79, 70, 229, 255))

    overlay = Image.new("RGBA", (canvas_size, canvas_size), (0, 0, 0, 0))
    overlay_draw = ImageDraw.Draw(overlay)
    overlay_draw.polygon(to_coords(p4), fill=to_color(14, 165, 233, 153))
    img = Image.alpha_composite(img, overlay)

    return img.resize((size, size), Image.Resampling.LANCZOS)

def main():
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    os.chdir(root)

    print("Generating 512x512 app icons...")
    icon_512 = render_neobox_icon(512, desaturated=False)
    icon_512.save("build/appicon.png", format="PNG")
    icon_512.save("build/linux/icon.png", format="PNG")
    icon_512.save("frontend/icon.png", format="PNG")

    print("Generating 64x64 tray icons (on/off)...")
    tray_on = render_neobox_icon(64, desaturated=False)
    tray_off = render_neobox_icon(64, desaturated=True)

    os.makedirs("build/tray", exist_ok=True)
    tray_on.save("build/tray/tray-on.png", format="PNG")
    tray_off.save("build/tray/tray-off.png", format="PNG")
    tray_on.save("build/linux/tray-on.png", format="PNG")
    tray_off.save("build/linux/tray-off.png", format="PNG")

    print("Generating multi-resolution Windows ICO files...")
    ico_sizes = [(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)]
    
    # Render largest for ICO
    ico_master = render_neobox_icon(256, desaturated=False)
    ico_master.save("build/windows/icon.ico", format="ICO", sizes=ico_sizes)
    ico_master.save("icon.ico", format="ICO", sizes=ico_sizes)
    if os.path.exists("build/bin"):
        ico_master.save("build/bin/icon.ico", format="ICO", sizes=ico_sizes)

    ico_off_master = render_neobox_icon(256, desaturated=True)
    ico_off_master.save("build/windows/icon-off.ico", format="ICO", sizes=ico_sizes)

    print("All icons successfully generated and updated!")

if __name__ == "__main__":
    main()
