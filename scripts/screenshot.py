#!/usr/bin/env python3
"""Render docs/screenshot.png from `devdash -demo`.

The dashboard is captured through a pty so it emits colour, the ANSI is parsed
into styled cells, and those are drawn with a monospace font. Every glyph devdash
draws is plain Unicode, so no patched font is needed — any monospace face with
reasonable symbol coverage renders the image correctly.

    pip install Pillow
    go build -o devdash .
    python3 scripts/screenshot.py ./devdash docs/screenshot.png

Demo data is used, so the image never contains anyone's real tickets.
"""

import argparse
import fcntl
import os
import pty
import re
import select
import struct
import sys
import termios
import time

from PIL import Image, ImageDraw, ImageFont

COLS, ROWS = 118, 44
FONT_DIRS = [
    os.path.expanduser("~/Library/Fonts"),
    os.path.expanduser("~/.local/share/fonts"),
    "/usr/share/fonts",
]
# Any monospace pair works. These are tried in order; the last is stock macOS, so
# the script runs without installing anything.
FONT_CANDIDATES = [
    ("IosevkaTermNerdFontMono-Regular.ttf", "IosevkaTermNerdFontMono-Bold.ttf"),
    ("JetBrainsMonoNerdFontMono-Regular.ttf", "JetBrainsMonoNerdFontMono-Bold.ttf"),
    ("MesloLGSNerdFontMono-Regular.ttf", "MesloLGSNerdFontMono-Bold.ttf"),
    ("DejaVuSansMono.ttf", "DejaVuSansMono-Bold.ttf"),
]

BG = (0x14, 0x16, 0x1B)
FG = (0xD0, 0xD4, 0xDC)
CHROME = (0x7A, 0x82, 0x90)
FONT_SIZE, LINE_HEIGHT = 26, 34
PAD_X, PAD_Y, TITLE_H = 26, 22, 40


def find_fonts():
    """Locate a monospace regular/bold pair, or explain what to install."""
    for regular, bold in FONT_CANDIDATES:
        for d in FONT_DIRS:
            r, b = os.path.join(d, regular), os.path.join(d, bold)
            if os.path.exists(r) and os.path.exists(b):
                return r, b
    sys.exit(
        "No monospace font found. Install any monospace face (for example "
        "JetBrains Mono) "
        f"into {FONT_DIRS[0]}, or add its filenames to FONT_CANDIDATES."
    )


def capture(binary):
    """Run the binary in a pty and return its raw output, colour included."""
    pid, fd = pty.fork()
    if pid == 0:
        os.environ["TERM"] = "xterm-256color"
        # Demo mode reaches no API, but clear these so it cannot possibly try.
        for v in ("JIRA_URL", "JIRA_USERNAME", "JIRA_API_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"):
            os.environ.pop(v, None)
        os.execv(binary, [binary, "-once", "-demo"])

    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))
    buf, deadline = b"", time.time() + 15
    while time.time() < deadline:
        if not select.select([fd], [], [], 0.2)[0]:
            continue
        try:
            chunk = os.read(fd, 65536)
        except OSError:
            break
        if not chunk:
            break
        buf += chunk
    try:
        os.close(fd)
    except OSError:
        pass
    try:
        os.waitpid(pid, 0)
    except ChildProcessError:
        pass
    return buf.decode("utf8", "replace")


def xterm256():
    """The 256-colour palette, so SGR indices resolve to RGB."""
    base = [
        (0, 0, 0), (205, 49, 49), (13, 188, 121), (229, 229, 16),
        (36, 114, 200), (188, 63, 188), (17, 168, 205), (229, 229, 229),
        (102, 102, 102), (241, 76, 76), (35, 209, 139), (245, 245, 67),
        (59, 142, 234), (214, 112, 214), (41, 184, 219), (255, 255, 255),
    ]
    levels = [0, 95, 135, 175, 215, 255]
    cube = [(r, g, b) for r in levels for g in levels for b in levels]
    greys = [(8 + i * 10,) * 3 for i in range(24)]
    return base + cube + greys


PALETTE = xterm256()


def resolve(style):
    """Flatten a style for drawing, applying reverse video as a real swap."""
    st = dict(style)
    if st.pop("reverse", False):
        st["fg"], st["bg"] = (st["bg"] or BG), st["fg"]
    else:
        st.pop("reverse", None)
    return st


def parse(text):
    """Turn ANSI output into rows of (character, style) pairs."""
    text = re.sub(r"\x1b\]8;;[^\x07\x1b]*(\x1b\\|\x07)", "", text)  # hyperlinks
    text = re.sub(r"\x1b\][^\x07\x1b]*(\x1b\\|\x07)", "", text)     # other OSC
    text = re.sub(r"\x1b\[\?[0-9;]*[a-zA-Z]", "", text)             # private modes

    fresh = {"fg": FG, "bg": None, "bold": False, "reverse": False}
    rows, style = [[]], dict(fresh)
    i = 0
    while i < len(text):
        if text.startswith("\x1b[", i):
            sgr = re.match(r"\x1b\[([0-9;]*)m", text[i:])
            if sgr:
                codes = [int(c) for c in sgr.group(1).split(";") if c] or [0]
                j = 0
                while j < len(codes):
                    c = codes[j]
                    if c == 0:
                        style = dict(fresh)
                    elif c == 1:
                        style["bold"] = True
                    elif c == 22:
                        style["bold"] = False
                    # Reverse video is load-bearing: it is how the most urgent
                    # states read on a terminal with no colour, so the image has
                    # to show it rather than drop it to a plain foreground.
                    elif c == 7:
                        style["reverse"] = True
                    elif c == 27:
                        style["reverse"] = False
                    elif c == 39:
                        style["fg"] = FG
                    elif c == 49:
                        style["bg"] = None
                    elif c == 38 and codes[j + 1 : j + 2] == [5]:
                        style["fg"] = PALETTE[codes[j + 2]]
                        j += 2
                    elif c == 48 and codes[j + 1 : j + 2] == [5]:
                        style["bg"] = PALETTE[codes[j + 2]]
                        j += 2
                    elif 30 <= c <= 37:
                        style["fg"] = PALETTE[c - 30]
                    elif 90 <= c <= 97:
                        style["fg"] = PALETTE[c - 90 + 8]
                    j += 1
                i += sgr.end()
                continue
            other = re.match(r"\x1b\[[0-9;]*[A-Za-z]", text[i:])
            if other:
                i += other.end()
                continue

        ch = text[i]
        if ch == "\n":
            rows.append([])
        elif ch not in ("\r", "\x1b"):
            rows[-1].append((ch, resolve(style)))
        i += 1

    while rows and not any(c.strip() for c, _ in rows[-1]):
        rows.pop()
    return rows


def draw(rows, regular_path, bold_path, out):
    regular = ImageFont.truetype(regular_path, FONT_SIZE)
    bold = ImageFont.truetype(bold_path, FONT_SIZE)
    cell = round(regular.getlength("M"))

    width = PAD_X * 2 + cell * COLS
    height = PAD_Y * 2 + TITLE_H + LINE_HEIGHT * len(rows)
    img = Image.new("RGB", (width, height), BG)
    d = ImageDraw.Draw(img)

    # A window frame, so the image reads as a terminal.
    d.rounded_rectangle([0, 0, width - 1, height - 1], radius=14, fill=BG)
    for k, colour in enumerate([(0xFF, 0x5F, 0x57), (0xFE, 0xBC, 0x2E), (0x28, 0xC8, 0x40)]):
        x = PAD_X + k * 22
        d.ellipse([x, 16, x + 13, 29], fill=colour)
    d.text((width // 2, 22), "devdash", font=regular, fill=CHROME, anchor="ma")

    for ri, row in enumerate(rows):
        y = PAD_Y + TITLE_H + ri * LINE_HEIGHT
        for ci, (ch, st) in enumerate(row):
            x = PAD_X + ci * cell
            if st["bg"]:
                d.rectangle([x, y - 4, x + cell, y + LINE_HEIGHT - 4], fill=st["bg"])
            if ch != " ":
                d.text((x, y), ch, font=bold if st["bold"] else regular, fill=st["fg"])

    img.save(out)
    print(f"wrote {out} ({width}x{height}, {len(rows)} rows)")


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("binary", nargs="?", default="./devdash")
    ap.add_argument("out", nargs="?", default="docs/screenshot.png")
    args = ap.parse_args()

    if not os.access(args.binary, os.X_OK):
        sys.exit(f"{args.binary} is not executable; run: go build -o devdash .")

    rows = parse(capture(os.path.abspath(args.binary)))
    if not rows:
        sys.exit("captured no output from the dashboard")
    regular, bold = find_fonts()
    draw(rows, regular, bold, args.out)


if __name__ == "__main__":
    main()
