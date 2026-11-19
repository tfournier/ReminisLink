# Fake SD card (provisional)

A tiny NextUI SD card used by the Go tests (`internal/testutil.CopySDCard`) and
by `make headless`. Every file is a few bytes of placeholder text: there is no
real ROM, save or state here, and every game name is made up.

The layout follows the NextUI source code (analysed for milestone M0,
NextUI v6.15.0), with the default `saveFormat` and `stateFormat`:

- multi-disc games: `Roms/<System>/<Game>/<Game>.m3u`, and their save, states
  and cheats named after `<Game>.m3u`;
- saves: `Saves/<TAG>/<ROM file name>.sav`;
- states: `.userdata/shared/<TAG>-<core>/<ROM file name>.st<N>`, slot 9 being
  the resume slot; the core is the `EMU_EXE` of the emulator pak
  `.system/tg5040/paks/Emus/<TAG>.pak/launch.sh` (or
  `Emus/tg5040/<TAG>.pak/launch.sh` for the extra paks), here fake scripts
  that are never run;
- state thumbnails: `.userdata/shared/.minui/<TAG>/<ROM file name>.<N>.bmp`,
  a PNG image despite the extension (slots 0 to 7);
- settings: `.userdata/shared/minuisettings.txt` (`saveFormat=0`,
  `stateFormat=0`);
- art of games in subfolders: `.media/` next to the ROM.

What it covers (`docs/specs/08-tests-qualite.md` §2):

| Case | Where |
|---|---|
| Two folders with different tags for the same console | `Game Boy Advance (GBA)`, `Game Boy Advance (MGBA)` |
| Sort prefix removed from the label | `01) Game Boy (GB)` |
| Subfolders, two levels | `Game Boy Advance (GBA)/Hacks/Deeper/` |
| Multi-disc folder (`folder-m3u`) and loose `.m3u` (the discs it lists are part of it, not games) | `Sony PlayStation (PS)/` |
| Accents, spaces, parentheses, brackets, emoji | `Café Rêverie (France).gba`, `Lantern Keeper [b1].gba`, `Pixel Garden 🌱.gba` |
| Save with a different case than its ROM | `Saves/GBA/café rêverie (france).gba.sav` |
| States, resume slot (`.st9`), one folder per tag and core | `.userdata/shared/GBA-gpsp/`, `MGBA-mgba/`, `PS-pcsx_rearmed/` |
| Emulator paks (system and extra) giving the cores | `.system/tg5040/paks/Emus/`, `Emus/tg5040/` |
| State thumbnail (PNG in a `.bmp`) | `.userdata/shared/.minui/GBA/Starlight Courier.gba.0.bmp` |
| Cheats | `Cheats/GBA/` |
| Art (1x1 PNG) | `.media/` folders |
| Ignored and hidden files | `readme.txt`, `.hidden.gba` |
| Folders that are not systems | `No Tag Folder` (no tag), `Hidden Only (HID)` (nothing visible) |

Never run the pak directly on this folder: `make headless` works on a
temporary copy, so the pak's own files (`config.json`, `clients.json`, logs)
never end up here.
