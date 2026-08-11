#!/usr/bin/env python3
"""Rewrite the GPS page (last page) in config.json / config_debian.json.

Layout is icon-and-number only — no text labels. Reading top to bottom:

    [ rotating compass tape + bearing ]     <- gps_compass graph element
    ------------------------------------
              32  km/h                      <- huge, unit implies "speed"
    ------------------------------------
     [mtn] 412m        [sat] 7/12           <- altitude / satellites
     [crosshair] +/-4m     3D Fix           <- accuracy / fix type
    ------------------------------------
      31.2304 N
     121.4737 E                             <- position, hemisphere = label

Colors use the two theme roles the rest of the config uses, so themes/
generate_themes.py recolors this page along with every other page:
  [255,229,0]   -> primary (values) / accent (on fixed_text)
  [255,229,255] -> data (small info text)
"""

import json
import os

HERE = os.path.dirname(os.path.abspath(__file__))

PRIMARY = [255, 229, 0]
DATA = [255, 229, 255]

W = 172  # panel width

# Vertical rhythm inside the 266px middle frame. The page carries no horizontal
# rules at all: the compass tape ends in its own baseline, and the icon column
# already groups the rows, so the dividers were pure clutter. Spacing does the
# separating instead.
Y_COMPASS = 4
Y_SPEED = 50
Y_ROW_1 = 118
Y_ROW_2 = 160
Y_LAT = 209
Y_LON = 235


def icon(path, x, y, w, h, color=PRIMARY):
    return {
        "type": "icon",
        "icon_path": f"assets/svg/{path}",
        "position": {"x": x, "y": y},
        "size": {"width": w, "height": h},
        "color": color,
        "enable": 1,
    }


def text(label, key, x, y, font, color=PRIMARY, units="", units_font="unit", units_dy=0):
    el = {
        "type": "text",
        "label": label,
        "position": {"x": x, "y": y},
        "font": font,
        "color": color,
        "data_key": key,
        "units_font": units_font,
        "enable": 1,
    }
    if units:
        el["units"] = units
    if units_dy:
        el["units_dy"] = units_dy
    return el


def build_gps_page():
    return [
        # --- Heading tape: the page's hero element, PUBG-style. ---
        {
            "type": "graph",
            "label": "Heading",
            "position": {"x": 0, "y": Y_COMPASS},
            "size": {"width": W, "height": 46},
            "color": PRIMARY,
            "graph_config": {"graph_type": "gps_compass"},
            "enable": 1,
        },
        # --- Speed: biggest number on the page. "km/h" is the only label, and
        # the value is whole-number so the glyphs can run this large. The unit
        # rides 4px above the shared baseline: at 62px the digits' lower right
        # is exactly where a baseline-aligned "km/h" wants to sit, so the two
        # overlapped. ---
        # x=4 and a "tiny" unit so the widest case still fits: "188" measures
        # 128px at colossal, and a 15px "unit" km/h added 42px more — together
        # they overran the 172px panel and clipped the unit to "km/". At 12px
        # the unit is 34px, which fits, and the smaller size reads better
        # against 62px digits anyway.
        text("GPS Speed", "GpsSpeed", 4, Y_SPEED, "colossal",
             units="km/h", units_font="tiny", units_dy=-4),

        # --- Row 1: altitude (mountain) | satellites (dish). No "m" on the
        # altitude: the mountain already says what it is, and a 4-digit value
        # ("1284") plus a unit is wider than the column, so the suffix collided
        # with the satellite icon. Both sit a couple of px lower than the row
        # baseline so they optically centre against the taller left glyphs.
        icon("mountain.svg", 6, Y_ROW_1 + 11, 22, 18),
        text("Altitude", "GpsAlt", 31, Y_ROW_1 + 2, "big"),
        icon("satellite.svg", 103, Y_ROW_1 + 10, 18, 18),
        text("Sats", "GpsSats", 125, Y_ROW_1 + 7, "tiny"),

        # --- Row 2: accuracy (crosshair) | fix type. ---
        icon("crosshair.svg", 8, Y_ROW_2 + 4, 19, 19),
        text("Accuracy", "GpsAccuracy", 32, Y_ROW_2, "reg", units="m"),
        text("Fix", "GpsFix", 102, Y_ROW_2 + 3, "unit", color=DATA),

        # --- Position: hemisphere letter carries the meaning. ---
        text("Lat", "GpsLat", 10, Y_LAT, "unit", color=DATA),
        text("Lon", "GpsLon", 10, Y_LON, "unit", color=DATA),
    ]


def main():
    for name in ("config.json", "config_debian.json"):
        path = os.path.join(HERE, name)
        if not os.path.exists(path):
            print(f"skip {name} (missing)")
            continue
        with open(path) as fh:
            cfg = json.load(fh)
        els = cfg["display_template"]["elements"]
        # The GPS page is the highest-numbered page (detectGpsPage relies on
        # this too); find it by the Gps* data keys rather than a hardcoded name.
        target = None
        for key, page in els.items():
            if any(str(e.get("data_key", "")).startswith("Gps") for e in page):
                target = key
        if target is None:
            print(f"skip {name}: no GPS page found")
            continue
        els[target] = build_gps_page()
        with open(path, "w") as fh:
            json.dump(cfg, fh, indent=4, ensure_ascii=False)
            fh.write("\n")
        print(f"rewrote {name} {target}")


if __name__ == "__main__":
    main()
