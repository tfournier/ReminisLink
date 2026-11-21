# Fake Knulli card (milestone M20)

A small `/userdata` rebuilt from the public Knulli sources, without a
console. It is used by `internal/osadapter/knulli` and by the layout smoke
test (`cmd/reminislink/smoke_layout_test.go`). Details and assumptions:
`docs/specs/05-console-pak.md` §3.3.

- `userdata/roms/<system>/`: one folder per system of `es_systems.yml`
  (key or `path`), games with the extensions of the system; `images/`
  holds the art EmulationStation finds on its own (`<rom>-image.png`).
  `notasystem/` is not a Knulli system: it is not listed.
- `userdata/roms/tools/ReminisLink.sh` and `.ReminisLink/`: where the
  Knulli launcher of the pak goes (`tools` runs `.sh` files with
  `/bin/bash`; a folder starting with a dot is hidden from
  EmulationStation).
- `userdata/saves/<system>/`: RetroArch saves (`<rom without extension>.srm`,
  `.rtc`) and states (`.state`, `.state<N>`, `.state.auto`, thumbnails
  `<state>.png`).
- `device/`: the console files the adapter reads, pointed to by
  `KNULLI_BOARD_FILE`, `KNULLI_VERSION_FILE` and `POWER_SUPPLY_PATH`.

Sources: `github.com/knulli-cfw/knulli-linux` 57afa662
(`package/system/knulli-configgen/configgen/configgen/batoceraPaths.py`,
`generators/libretro/libretroConfig.py` lines 240-243,
`generators/libretro/libretroRetroarchCustom.py` lines 70 and 93-97,
`generators/sh/shGenerator.py`,
`package/emulationstation/knulli-es-system/es_systems.yml`) and
`github.com/knulli-cfw/batocera-emulationstation` f7c5ae10
(`es-app/src/SystemData.cpp` line 278, `es-core/src/utils/FileSystemUtil.cpp`
line 1096: hidden files).
