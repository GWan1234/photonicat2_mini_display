# Display color themes

Ready-to-use color themes for the mini display. Each file is a complete
`user_config.json` payload — paste its contents into **pcat-manager-web →
Screen settings** (http://172.16.10.123/screen), which saves it to the
display via `/api/v1/go_save_user_config.json`.

Pick the folder matching the device OS (`openwrt/` or `debian/`).

| Theme | Values (primary) | Labels (accent) | Info text | Feel |
|---|---|---|---|---|
| `mono_classic` | `#FFE500` yellow | `#FFE500` yellow | `#FFE5FF` | current default, easy revert |
| `neon_cyber` | `#00E5FF` cyan | `#FF40A0` magenta | `#E1F2FF` | cyberpunk cockpit |
| `aurora` | `#46EB91` green | `#50AAFF` ice blue | `#D7FFEB` | northern lights, matches the green battery/CPU bars |
| `sunset_synth` | `#FFA028` amber | `#B973FF` violet | `#FFEBD7` | synthwave sunset |

Open `preview.html` in a browser for a side-by-side mock of the palettes.

Notes:

- The display's `deepMerge` replaces JSON arrays wholesale, so each theme
  carries the **full element arrays** for every page. Do not hand-edit a
  theme down to only the `color` fields — it would wipe the page layout.
- Regenerate after any layout change in `config.json` / `config_debian.json`:
  `python3 themes/generate_themes.py`
- Colors hardcoded in Go are not themed: ping-timeout red, CPU/mem/disk bar
  colors, battery icon red/green, power graph, and SMS text.
