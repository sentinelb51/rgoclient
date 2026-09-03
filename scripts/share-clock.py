"""A window whose picture is the wall clock, for the screenshare harness
(internal/app/screenshare_live_test.go, RGO_SHARE_CLOCK=scripts/share-clock.py).

Sixteen blocks on a grey field: twelve carry the 25 ms tick counter (LSB
first) and four a checksum, so a frame of the share can be decoded back
into the moment it was painted. The grey margin is what the receiver finds
the picture by, letterbox pad being black.
"""
import sys
import time
import tkinter as tk

W, H = (int(sys.argv[1]), int(sys.argv[2])) if len(sys.argv) > 2 else (800, 450)
COLS, ROWS = 8, 2
TICK_MS = 25
MARGIN = 0.15

root = tk.Tk()
root.title("RGO-CLOCK")
root.resizable(False, False)
root.geometry(f"{W}x{H}+120+120")
root.attributes("-topmost", True)

cv = tk.Canvas(root, width=W, height=H, bg="#404040", highlightthickness=0)
cv.pack()

cw, ch = W / COLS, H / ROWS
rects = []
for i in range(COLS * ROWS):
    c, r = i % COLS, i // COLS
    x0, y0 = c * cw + MARGIN * cw, r * ch + MARGIN * ch
    x1, y1 = (c + 1) * cw - MARGIN * cw, (r + 1) * ch - MARGIN * ch
    rects.append(cv.create_rectangle(x0, y0, x1, y1, fill="#000000", outline=""))


def code(v):
    check = ((v & 0xF) + ((v >> 4) & 0xF) + ((v >> 8) & 0xF) + 3) & 0xF
    return v | (check << 12)


last = -1


def tick():
    global last
    v = (int(time.time() * 1000) // TICK_MS) & 0xFFF
    if v != last:
        last = v
        bits = code(v)
        for i in range(16):
            cv.itemconfigure(rects[i], fill="#FFFFFF" if (bits >> i) & 1 else "#000000")
        root.update_idletasks()
    root.after(4, tick)


root.after(4, tick)
root.mainloop()
