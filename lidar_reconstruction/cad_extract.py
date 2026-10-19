"""Read the architect's CAD plan (vector PDF from ZWCAD): wall linework segments, dimension strings (text) -> npz (PDF points, y up)."""
import numpy as np
import pdfplumber

WALL_STROKES = {(0.4471, 0.2235, 0.0), (0.7216, 0.3608, 0.0), (0.4471, 0.0, 0.0), (0.5843, 0.4392, 0.2902), (0.3294, 0.3294, 0.3294), (0.7216, 0.0, 0.0), (0.4627, 0.4627, 0.4627)}


def _col(c):
    return tuple(round(float(x), 4) for x in c) if c is not None and hasattr(c, "__iter__") else None


def extract(pdf_path, all_colors=False):
    pg = pdfplumber.open(pdf_path).pages[0]; H = pg.height; segs = []
    for l in pg.lines:
        if all_colors or _col(l.get("stroking_color")) in WALL_STROKES:
            segs.append((l["x0"], H - l["top"], l["x1"], H - l["bottom"]))
    for r in pg.rects:
        if all_colors or _col(r.get("stroking_color")) in WALL_STROKES:
            x0, x1, t, b = r["x0"], r["x1"], H - r["top"], H - r["bottom"]
            segs += [(x0, t, x1, t), (x1, t, x1, b), (x1, b, x0, b), (x0, b, x0, t)]
    S = np.array(segs, float); L = np.hypot(S[:, 2] - S[:, 0], S[:, 3] - S[:, 1]); S = S[L > 0.5]
    words = [dict(text=w["text"], x0=w["x0"], x1=w["x1"], y=H - (w["top"] + w["bottom"]) / 2, upright=w.get("upright", True)) for w in pg.extract_words(keep_blank_chars=False, use_text_flow=False, extra_attrs=["upright"])]
    return S, words, (pg.width, H)


if __name__ == "__main__":
    S, words, size = extract("data/cad_plan.pdf"); print(len(S), "wall-linework segments;", len(words), "words;", size)
