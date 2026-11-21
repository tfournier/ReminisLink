#!/usr/bin/env python3
"""Generate internal/osadapter/knulli/systems.json from Knulli's es_systems.yml.

Milestone M20. Knulli (github.com/knulli-cfw/knulli-linux) lists its
systems in package/emulationstation/knulli-es-system/es_systems.yml: the
key (or "path") is the folder under /userdata/roms, "name" the display name
and "hardware" the kind of system. The file has a regular structure: this
reads the few top-level fields it needs without a YAML library.

    python3 -I scripts/gen-knulli-systems.py es_systems.yml SOURCE_COMMIT \\
        > internal/osadapter/knulli/systems.json
"""
import json
import re
import sys

KEY = re.compile(r"^([A-Za-z0-9_.+-]+):\s*$")
FIELD = re.compile(r"^  (name|hardware|path|extensions):\s*(.*?)\s*$")


def main(path, commit):
    systems = {}
    cur = None
    for line in open(path, encoding="utf-8"):
        line = line.rstrip("\n")
        m = KEY.match(line)
        if m:
            cur = {"key": m.group(1)}
            systems[m.group(1)] = cur
            continue
        if cur is None:
            continue
        m = FIELD.match(line)
        if m:
            cur[m.group(1)] = m.group(2)
    out = []
    for key, s in systems.items():
        folder = s.get("path", key) or key
        if folder.startswith("/"):
            continue  # screenshots, recordings, library: not under roms
        exts = s.get("extensions", "").strip("[]")
        out.append({
            "folder": folder,
            "name": s.get("name", key),
            "hardware": s.get("hardware", ""),
            "extensions": [e.strip().lower() for e in exts.split(",") if e.strip()],
        })
    out.sort(key=lambda s: s["folder"])
    json.dump({"source": "knulli-cfw/knulli-linux " + commit +
               " package/emulationstation/knulli-es-system/es_systems.yml",
               "systems": out}, sys.stdout, indent=1, ensure_ascii=False)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
