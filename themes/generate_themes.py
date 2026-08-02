#!/usr/bin/env python3
"""Generate colorful display themes from the base configs.

Each theme file is a complete user_config payload: because the display's
deepMerge replaces arrays wholesale, a theme must carry the FULL element
arrays for every page, not just color patches. Paste a theme's JSON into
pcat-manager-web -> Screen settings (it POSTs go_save_user_config.json).

Re-run this script after editing the layout in config.json /
config_debian.json so the themes pick up the new element positions.

Color roles (mapped from the base config's palette):
  [255,229,0]  on fixed_text -> accent   (section labels)
  [255,229,0]  on text       -> primary  (big data values)
  [255,229,255]              -> data     (IPs, SSIDs, small info text)
  [255,255,255]              -> subtle   (tiny % success rates)
"""

import copy
import json
import os

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)

THEMES = {
    # Current look, kept as a file so it is easy to revert to.
    "mono_classic": {
        "primary": [255, 229, 0],
        "accent":  [255, 229, 0],
        "data":    [255, 229, 255],
        "subtle":  [255, 255, 255],
    },
    # Neon cyan values with hot magenta labels - cyberpunk cockpit.
    "neon_cyber": {
        "primary": [0, 229, 255],
        "accent":  [255, 64, 160],
        "data":    [225, 242, 255],
        "subtle":  [160, 190, 215],
    },
    # Spring green values (matches the hardware PCAT_GREEN used by the
    # battery/CPU bars) with ice-blue labels - northern lights.
    "aurora": {
        "primary": [70, 235, 145],
        "accent":  [80, 170, 255],
        "data":    [215, 255, 235],
        "subtle":  [150, 210, 185],
    },
    # Amber values with violet labels - synthwave sunset.
    "sunset_synth": {
        "primary": [255, 160, 40],
        "accent":  [185, 115, 255],
        "data":    [255, 235, 215],
        "subtle":  [230, 195, 165],
    },
}

BASES = {
    "openwrt": "config.json",
    "debian":  "config_debian.json",
}


def role_of(element):
    color = element.get("color")
    if color == [255, 229, 0]:
        return "accent" if element.get("type") == "fixed_text" else "primary"
    if color == [255, 229, 255]:
        return "data"
    if color == [255, 255, 255]:
        return "subtle"
    return None


def recolor(elements, palette):
    themed = copy.deepcopy(elements)
    for els in themed.values():
        for element in els:
            role = role_of(element)
            if role is not None:
                element["color"] = list(palette[role])
    return themed


def main():
    for os_name, base_file in BASES.items():
        with open(os.path.join(ROOT, base_file)) as f:
            elements = json.load(f)["display_template"]["elements"]
        out_dir = os.path.join(HERE, os_name)
        os.makedirs(out_dir, exist_ok=True)
        for theme_name, palette in THEMES.items():
            payload = {"display_template": {"elements": recolor(elements, palette)}}
            out_path = os.path.join(out_dir, theme_name + ".json")
            with open(out_path, "w") as f:
                json.dump(payload, f, indent=4)
                f.write("\n")
            print("wrote", os.path.relpath(out_path, ROOT))


if __name__ == "__main__":
    main()
