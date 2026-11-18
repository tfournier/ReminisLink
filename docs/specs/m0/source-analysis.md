# M0 — Analyse du code source de NextUI et du firmware d'origine de la TrimUI Brick

Rédigé le 19/11/2026. Lecture seule : aucun script, Makefile ni binaire du clone n'a été exécuté. Seule exception : le test `busybox uname` du rootfs, dans un conteneur, demandé par l'agent principal.

## Sources analysées

- **Dépôt** : `https://github.com/LoveRetro/NextUI`. C'est un fork déclaré de `shauninman/MinUI` (`gh repo view` : `isFork: true`, parent `shauninman/MinUI`). Le README le décrit ainsi : « A CFW based of MinUI […] for the TrimUI Brick and Smart Pro ».
- **Commit analysé** : `b6ec1343c0a2636ddaf2502fde52cff237c67377` (branche `main`, 12/11/2026, « Fix: use cached Bluetooth state during status drawing (#896) »).
- **Dernière release** : `v6.15.0` (13/10/2026, commit `39a06dea…`). Ses fichiers : `NextUI-20261013-0-all.zip` (243 439 698 o), `-base.zip`, `-extras.zip`.
- **Écart avec main** : `git diff v6.15.0 HEAD` sur tous les fichiers cités ci-dessous ne touche qu'`api.c` (2 lignes, sans rapport avec ces points). Les conclusions valent donc aussi pour v6.15.0.
- Clone : `/private/tmp/claude-501/nextui-src/NextUI/`. Les chemins ci-dessous sont relatifs à ce dossier.

> **Point important** : sur tg5040, l'émulateur de NextUI est `minarch`, mais le nommage des saves et des states **dépend de deux réglages de l'utilisateur** (`saveFormat`, `stateFormat` dans `/.userdata/shared/minuisettings.txt`). Les valeurs par défaut correspondent à la convention MinUI. Les autres formats existent et doivent être gérés, ou au moins détectés.

---

## 1. Saves

**Réponse**
- Dossier : `Saves/<TAG>/`. Le TAG est tiré du dossier de la ROM (dernier `(...)`), pas du nom du pak émulateur.
- Nom par défaut (`saveFormat=0`) : `<nom de fichier du jeu, extension comprise>.sav`, par exemple `Saves/GB/Tetris (World).gb.sav`.
- Autres formats possibles :
  - `saveFormat=1` : `<nom sans extension>.srm`, **compressé rzip**.
  - `saveFormat=2` : `<nom sans extension>.sav`.
  - `saveFormat=3` : `<nom sans extension>.srm`, non compressé.
  - Le retrait d'extension n'enlève que la dernière, et seulement si elle fait 1 à 4 caractères.
- Fichier annexe : `Saves/<TAG>/<nom avec extension>.rtc` (horloge du jeu, par ex. Pokémon), toujours dans ce format, quel que soit le réglage.
- `.zip` : si le cœur ne lit pas le zip et que `useExtractedFileName=1` (défaut 0), le nom devient celui du fichier extrait du zip, pas celui du zip.
- **Multi-disques** (dossier `<Jeu>/` qui contient `<Jeu>.m3u`) : le nom du jeu devient `<Jeu>.m3u`, d'où `Saves/<TAG>/<Jeu>.m3u.sav`. Une seule save pour tous les disques.
- `.m3u` isolé (pas dans un dossier du même nom) : il est passé tel quel à l'émulateur, donc `<x>.m3u.sav`.
- Dossier avec `<Jeu>/<Jeu>.cue` : lancement automatique de la `.cue`, donc `<Jeu>.cue.sav`.
- Casse : le nom est celui de `readdir`, tel quel. Sur exFAT, la recherche doit être insensible à la casse.

**Preuve**
- `workspace/all/minarch/ma_core.c:75` : `sprintf((char*)core.saves_dir, SDCARD_PATH "/Saves/%s", core.tag);`
- `workspace/all/minarch/minarch.c:162` : `getEmuName(rom_path, tag_name);` (le TAG vient du chemin de la ROM)
- `workspace/all/minarch/ma_saves.c:41` : `sprintf(filename, "%s/%s.sav", core.saves_dir, game.alt_name);` ; lignes 31-39 pour SRM et GEN ; ligne 111 pour `.rtc`.
- `workspace/all/common/config.h:24-31` : `// MinUI: Game.gba.sav … //Retroarch: Game.srm … // Generic: Game.sav` ; ligne 236 : `#define CFG_DEFAULT_SAVEFORMAT SAVE_FORMAT_SAV` ; ligne 238 : `CFG_DEFAULT_EXTRACTEDFILENAME false`.
- `workspace/all/common/config.c:151` : `"%s/minuisettings.txt", SHARED_USERDATA_PATH` ; ligne 1654 : `fprintf(file, "saveFormat=%i\n", …)`.
- `workspace/all/minarch/ma_game.c:130-133` : `if (exists(m3u_path)) { … strcpy((char*)game.name, strrchr(m3u_path, '/')+1); strcpy((char*)game.alt_name, game.name);`

**Confiance** : haute.

**Reste à vérifier sur la console** : rien d'indispensable. Lire `saveFormat`, `stateFormat` et `useExtractedFileName` dans `minuisettings.txt` pour savoir quels formats rencontrer en pratique.

---

## 2. States

**Réponse — la spec est fausse sur le dossier.**
- Dossier des states : `/.userdata/shared/<TAG>-<cœur>/`, par exemple `.userdata/shared/GB-gambatte/` ou `.userdata/shared/PS-pcsx_rearmed/`. Ce n'est **pas** `.userdata/shared/.minui/<TAG>/` : ce dossier-là ne contient que les vignettes et les fichiers `.txt` (point 3).
- Le nom du cœur vient du `.so` : `<cœur>_libretro.so` donne `<cœur>`, découpé au dernier `_`. Il dépend donc du pak émulateur :
  - GB → `gambatte`, GBC → `gambatte`, GBA → `gpsp`, FC → `fceumm`, SFC → `snes9x`, MD → `picodrive`, PS → `pcsx_rearmed`.
  - Un pak d'extras peut avoir son propre cœur (par ex. MGBA → `mgba`).
  - Pour l'adaptateur : soit lire `EMU_EXE=` dans `launch.sh` du pak (`.system/tg5040/paks/Emus/<TAG>.pak/` ou `/Emus/tg5040/<TAG>.pak/`), soit chercher `.userdata/shared/<TAG>-*/`.
- Nommage par défaut (`stateFormat=0`) : `<nom de fichier du jeu avec extension>.st<N>`. Autres formats :
  - `stateFormat=1` (rzip) et `2` : `<nom sans ext>.state.<N>`, et `.state.auto` pour la reprise.
  - `stateFormat=3` (rzip) et `4` : `<nom sans ext>.state` pour l'emplacement 0, `.state<N>` pour les autres, `.state.auto` pour la reprise.
- Emplacements :
  - 0 à 7 sont les emplacements de l'utilisateur, affichés 1 à 8 (`MENU_SLOT_COUNT 8`).
  - 8 est un state « par défaut » caché : il est lu au lancement normal s'il existe, mais NextUI ne l'écrit jamais.
  - **9 est la reprise automatique** (`AUTO_RESUME_SLOT 9`).
- Quand l'emplacement 9 est écrit (`Menu_beforeSleep`, qui écrit aussi la save `.sav`/`.rtc`) :
  - mise en veille en jeu (bouton, veille automatique, veille demandée par le matériel) ;
  - extinction en jeu (appui long sur le bouton, `BTN_POWEROFF`) ;
  - option de menu « Quicksave » (sauvegarde puis extinction) ;
  - changement HDMI.
  - **Pas** quand on quitte le jeu normalement (Quit n'écrit que `.sav`/`.rtc`).
  - En même temps, le chemin de la ROM est écrit dans `.userdata/shared/.minui/auto_resume.txt`. Au démarrage suivant, NextUI relance ce jeu sur l'emplacement 9 et supprime ce fichier. Au réveil, le fichier est aussi supprimé. **Le `.st9` reste en place** jusqu'à la prochaine écriture : le pak peut donc l'archiver (§5.3).
- Fichiers annexes dans `.userdata/shared/.minui/<TAG>/` (point 3) :
  - `<nom>.txt` : dernier emplacement utilisé. C'est lui que l'entrée « Reprendre » de la liste utilise, pas l'emplacement 9.
  - `<nom>.<N>.bmp` : vignette.
  - `<nom>.<N>.txt` : disque associé, pour les multi-disques.
- Réglages par jeu ou par cœur (hors states) : `.userdata/tg5040/<TAG>-<cœur>/minarch.cfg` et `<jeu>.cfg`.

**Preuve**
- `workspace/all/minarch/ma_core.c:74` : `sprintf((char*)core.states_dir, SHARED_USERDATA_PATH "/%s-%s", core.tag, core.name);`
- `workspace/all/minarch/ma_core.c:14-17` : `Core_getName` coupe au dernier `_`.
- `workspace/all/minarch/ma_saves.c:195` : `sprintf(filename, "%s/%s.st%i", core.states_dir, game.alt_name, state_slot);` ; lignes 166-193 pour les formats `.state*`.
- `workspace/all/common/defines.h:27-28` : `#define AUTO_RESUME_PATH SHARED_USERDATA_PATH "/.minui/auto_resume.txt"` / `#define AUTO_RESUME_SLOT 9`
- `workspace/all/minarch/ma_menu.c:224-228` : `void Menu_beforeSleep() { SRAM_write(); RTC_write(); State_autosave(); putFile(AUTO_RESUME_PATH, …`
- `workspace/all/common/api.c:4170-4174` (extinction) et `4196-4204` (veille) : `before_sleep()` est appelé avant `PWR_powerOff` / `PWR_sleep`.
- `workspace/all/minarch/ma_frontend_opts.c:471-473` (Quicksave) et `minarch.c:123` (HDMI).
- `workspace/all/minarch/ma_menu.c:1795-1796` : à la sortie du menu ou du jeu, seulement `SRAM_write(); RTC_write();`
- `workspace/all/minarch/ma_menu.c:117` : `#define MENU_SLOT_COUNT 8` ; `ma_saves.c:231` : `// st8 is a default state in MiniUI and may not exist`.
- `workspace/all/nextui/nextui.c:1185-1215` : `autoResume()` lit puis supprime `auto_resume.txt`, puis `putInt(RESUME_SLOT_PATH, AUTO_RESUME_SLOT)`.

**Confiance** : haute.

**Reste à vérifier sur la console** : lister `.userdata/shared/` pour confirmer les dossiers `<TAG>-<cœur>`, et voir un `.st9` après une mise en veille en jeu.

---

## 3. Vignettes de states

**Réponse**
- Oui, NextUI enregistre une vignette, mais seulement pour les emplacements 0 à 7. L'emplacement 9 n'en a pas, ni l'emplacement 8.
- Chemin : `.userdata/shared/.minui/<TAG>/<nom du jeu avec extension>.<N>.bmp`. Pour un multi-disques, le nom est `<Jeu>.m3u`.
- **Format** : le contenu est un **PNG**, malgré l'extension `.bmp` (écrit par `IMG_SavePNG_RW`). Le servir en `image/png` est correct. Pour le détecter, vérifier l'en-tête `\x89PNG`, pas l'extension.
- Taille : une capture de toute la surface affichée, en ARGB.
  - Brick : 1024×768 (Smart Pro : 1280×720).
  - En HDMI, la capture est à la résolution HDMI.
  - Ce n'est pas une miniature : prévoir de la réduire côté pak ou app.

**Preuve**
- `workspace/all/minarch/ma_menu.c:179` : `sprintf(menu.minui_dir, SHARED_USERDATA_PATH "/.minui/%s", emu_name);`
- `workspace/all/minarch/ma_menu.c:1617` : `sprintf(menu.bmp_path, "%s/%s.%d.bmp", menu.minui_dir, game.name, menu.slot);`
- `workspace/all/minarch/ma_menu.c:1713-1714` : `SDL_RWops* rw = SDL_RWFromFile(menu.bmp_path, "wb"); IMG_SavePNG_RW(menu.bitmap, rw,1);` (même chose dans le thread, ligne 1647)
- `workspace/tg5040/platform/platform.h:141-142` : `FIXED_WIDTH (is_brick||is_brickpro?1024:1280)` / `FIXED_HEIGHT (…?768:720)` ; `generic_video.c:2365-2371` : la capture est faite sur le viewport `device_width × device_height`.

**Confiance** : haute (format et chemin). Moyenne sur la taille exacte en HDMI ou avec un mode d'affichage particulier.

**Reste à vérifier sur la console** : `file` ou `xxd` sur un `.minui/<TAG>/*.0.bmp` pour confirmer la signature PNG et la taille 1024×768.

---

## 4. Multi-disques

**Réponse**
- Convention reconnue : un dossier `Roms/<Système>/<Jeu>/` qui contient `<Jeu>.m3u`, avec **exactement le nom du dossier**.
  - Sur exFAT, `exists()` ne tient pas compte de la casse.
  - Les chemins du `.m3u` sont relatifs à ce dossier ; les lignes vides sont ignorées.
- Ouvrir le dossier lance directement le premier disque. NextUI lance le **disque**, et c'est minarch qui retrouve le `.m3u` dans le dossier parent.
- Variante : un dossier `<Jeu>/<Jeu>.cue` est aussi lancé automatiquement.
- Un `.m3u` isolé (sans dossier du même nom) est un fichier ROM comme un autre, passé tel quel au cœur.
- Nommage : `game.name` = `<Jeu>.m3u`.
  - Save : `Saves/<TAG>/<Jeu>.m3u.sav`.
  - State : `<TAG>-<cœur>/<Jeu>.m3u.st<N>`.
  - Vignette : `.minui/<TAG>/<Jeu>.m3u.<N>.bmp`.
  - Disque du state : `.minui/<TAG>/<Jeu>.m3u.<N>.txt`, qui contient le chemin du disque relatif au dossier (ou absolu).
- Récents : `.userdata/shared/.minui/recent.txt` garde le chemin du `.m3u`.

**Preuve**
- `workspace/all/nextui/nextui.c:548-575` : `hasM3u()`, dont `// dir_name is also our m3u file name`.
- `workspace/all/nextui/nextui.c:1444-1460` : `openDirectory` → `hasCue(...) && auto_launch` → `openRom` ; sinon `.m3u` → `getFirstDisc` → `openRom`.
- `workspace/all/minarch/ma_game.c:108-134` : même calcul côté minarch, puis `game.name = <dossier>.m3u`.
- `workspace/all/minarch/ma_menu.c:1618, 1694-1697` : `txt_path … "%s/%s.%d.txt"` / `putFile(menu.txt_path, disc_path + strlen(menu.base_path));`
- `workspace/all/nextui/nextui.c:1295-1310` : relecture de `.minui/<EMU>/<rom>.<slot>.txt` pour choisir le disque à la reprise.

**Confiance** : haute.

**Reste à vérifier sur la console** : rien.

---

## 5. Jaquettes

**Réponse**
- Chemin : `<dossier qui contient l'entrée>/.media/<nom de l'entrée sans sa dernière extension>.png`.
  - Jeu au premier niveau : `Roms/<Système>/.media/<rom sans ext>.png` (confirmé).
  - Jeu dans un sous-dossier : `Roms/<Système>/<sous-dossier>/.media/<rom sans ext>.png`. C'est le `.media` du sous-dossier, pas celui du système.
  - Jeu multi-disques (entrée de type dossier `<Jeu>/`) : `Roms/<Système>/.media/<Jeu>.png`, avec le nom du dossier.
- Seul **le dernier** point est retiré. Un dossier `Sky Garden Quest Vol. 3` donnerait donc `Sky Garden Quest Vol.png` : c'est un défaut de NextUI à reproduire tel quel si on veut la même jaquette.
- Fonds d'écran (hors périmètre) : `<dossier>/.media/bg.png`, `bglist.png`, et `/.media/quick_*.png` à la racine de la carte.

**Preuve**
- `workspace/all/nextui/nextui.c:2979-2993` : `rompath = dirname(path_copy)` ; `char* dot = strrchr(res_copy, '.'); if (dot) *dot = '\0';`
- `workspace/all/nextui/nextui.c:3034` : `snprintf(thumbpath, sizeof(thumbpath), "%s/.media/%s.png", rompath, res_copy);`

**Confiance** : haute.

**Reste à vérifier sur la console** : rien.

---

## 6. Systèmes

**Réponse**
- **Tag** : texte entre la **dernière** `(` et la `)` qui suit, dans le nom du dossier de premier niveau sous `Roms/`.
  - Sans parenthèses, le nom entier du dossier sert de tag (« bare tag name »).
  - Pour un ROM en sous-dossier, c'est toujours le dossier de premier niveau qui compte.
- **Visibilité** : un système n'apparaît que s'il existe un pak émulateur `<TAG>.pak/launch.sh`, dans `.system/tg5040/paks/Emus/` ou dans `/Emus/tg5040/`, **et** au moins une entrée non cachée dans le dossier.
  - L'adaptateur peut lister les autres dossiers, mais NextUI ne les affiche pas.
- **Regroupement** : les dossiers qui partagent le préfixe jusqu'à « ( » inclus sont fusionnés à l'affichage. Par exemple `Game Boy Advance (GBA)` et `Game Boy Advance (MGBA)` donnent une seule entrée, qui affiche les deux contenus. Les doublons de nom d'affichage sont éliminés.
  - Les saves restent séparées par tag, conformément à la spec.
- **Cachés** : nom qui commence par `.`, nom qui finit par `.disabled`, fichier `map.txt`.
- **Aucune extension n'est filtrée** par NextUI : tout fichier non caché est une ROM (`ENTRY_ROM`). Un sous-dossier `*.pak` est une entrée pak.
- **Tri** : `strcasecmp` sur le nom d'affichage.
  - Le nom d'affichage retire les extensions de 1 à 4 caractères (en boucle, par ex. `.p8.png`), puis les `(...)`/`[...]` de fin, puis les espaces de fin.
  - Le préfixe de tri `NNN) ` (chiffres puis `)`) reste dans la clé de tri. Il n'est retiré qu'à l'affichage (`trimSortingMeta`).
- **Alias** : `Roms/map.txt` (`<nom de dossier>\t<alias>`) renomme les systèmes. Un `map.txt` dans un dossier de jeux sert aux alias de jeux (`getAlias`).

**Preuve**
- `workspace/all/common/utils.c:369-377` (`getEmuName`) : `tmp = strrchr(tmp, '('); … tmp = strchr(out_name,')'); tmp[0] = '\0';`
- `workspace/all/common/utils.c:36-37` : `return file_name[0]=='.' || suffixMatch(".disabled", file_name) || exactMatch("map.txt", file_name);`
- `workspace/all/nextui/nextui.c:535-541` (`hasEmu`) et `676-699` (`hasRoms`).
- `workspace/all/nextui/nextui.c:1052-1073` : regroupement `// keep the opening parenthesis to avoid collating "Game Boy Color" and "Game Boy Advance"`.
- `workspace/all/nextui/nextui.c:171-174` : `strcasecmp(item1->name, item2->name)` ; `utils.c:314-350` (`getDisplayName`) ; `utils.c:401-415` (`trimSortingMeta`, `// eg. 001) `).
- `workspace/all/nextui/nextui.c:1011-1033` (`addEntries`) : aucun filtre d'extension.

**Confiance** : haute.

**Reste à vérifier sur la console** : rien.

---

## 7. Version de NextUI

**Réponse**
- `/mnt/SDCARD/.system/version.txt`, sur deux lignes :
  1. le nom de release `NextUI-AAAAMMJJ-N` (par ex. `NextUI-20261013-0` pour la v6.15.0) ;
  2. le hash git court.
- Attention : le numéro semver `v6.15.0` **n'apparaît pas** dans ce fichier.
- À côté : `.system/commits.txt` (liste des commits).
- La version du système TrimUI d'origine est dans `/etc/version` (lu par NextUI pour « Stock OS version »). Le firmware 1.1.1 contient `1.1.1`.
- Proposition : `osVersion = "NextUI-20261013-0 (<hash>)"`, éventuellement avec `stock 1.1.1`.

**Preuve**
- `makefile:249` : `cd ./build/SYSTEM && echo "$(RELEASE_NAME)\n$(BUILD_HASH)" > version.txt`
- `makefile:33-35` : `RELEASE_BASE=NextUI-$(RELEASE_TIME)$(RELEASE_BETA)` / `RELEASE_NAME ?= $(RELEASE_BASE)-$(RELEASE_DOT)`
- `workspace/all/settings/settings.cpp:1118` : `std::ifstream t(ROOT_SYSTEM_PATH "/version.txt");`
- `workspace/tg5040/platform/platform.c:327-329` : `getFile("/etc/version", …)`.
- Noms des fichiers de la release v6.15.0 : `NextUI-20261013-0-*.zip`.

**Confiance** : haute pour le chemin. Moyenne pour le séparateur exact : `echo "…\n…"` dépend du shell de la CI. Avec dash, on a un vrai retour à la ligne ; sinon un `\n` littéral.

**Reste à vérifier sur la console** : `cat /mnt/SDCARD/.system/version.txt | od -c | head` pour voir le séparateur.

---

## 8. Batterie

**Réponse**
- Niveau : `/sys/class/power_supply/axp2202-battery/capacity`.
- En charge : NextUI n'utilise **pas** `status`. Il considère que la console charge si `/sys/class/power_supply/axp2202-usb/online == 1` **et** `/sys/class/power_supply/axp2202-battery/time_to_full_now > 0`.
- Le noyau expose probablement aussi `axp2202-battery/status` (classe power_supply standard), mais ce n'est pas prouvé par le code.

**Preuve**
- `workspace/tg5040/platform/platform.c:183-188` : `getInt("/sys/class/power_supply/axp2202-battery/time_to_full_now")` / `getInt("/sys/class/power_supply/axp2202-usb/online")` / `*charge = getInt("/sys/class/power_supply/axp2202-battery/capacity");`

**Confiance** : haute pour `capacity` et `online`. Basse pour `status`.

**Reste à vérifier sur la console** : `ls /sys/class/power_supply/axp2202-battery/` et `cat …/status`, sur secteur et sur batterie.

---

## 9. Variables d'environnement et lancement d'un pak TOOL

**Réponse**
- Variables exportées par `.system/tg5040/paks/MinUI.pak/launch.sh` et héritées par tous les paks :

  | Variable | Valeur |
  |---|---|
  | `PLATFORM` | `tg5040` |
  | `SDCARD_PATH` | `/mnt/SDCARD` |
  | `BIOS_PATH` | `$SDCARD_PATH/Bios` |
  | `ROMS_PATH` | `$SDCARD_PATH/Roms` |
  | `SAVES_PATH` | `$SDCARD_PATH/Saves` |
  | `CHEATS_PATH` | `$SDCARD_PATH/Cheats` |
  | `SYSTEM_PATH` | `$SDCARD_PATH/.system/tg5040` |
  | `CORES_PATH` | `$SYSTEM_PATH/cores` |
  | `USERDATA_PATH` | `$SDCARD_PATH/.userdata/tg5040` |
  | `SHARED_USERDATA_PATH` | `$SDCARD_PATH/.userdata/shared` |
  | `LOGS_PATH` | `$USERDATA_PATH/logs`, c'est-à-dire `/mnt/SDCARD/.userdata/tg5040/logs` |
  | `HOOKS_PATH` | `$USERDATA_PATH/.hooks` |
  | `HOME` | `$USERDATA_PATH` |
  | `TRIMUI_MODEL` | `Trimui Brick`, … (tiré de `strings /usr/trimui/bin/MainUI`) |
  | `DEVICE` | `brick`, `brickpro` ou `smartpro` (valeur par défaut si ce n'est pas une Brick) |
  | `IS_NEXT` | `yes` |
  | `LD_LIBRARY_PATH` | `$SYSTEM_PATH/lib:/usr/trimui/lib:…` |
  | `PATH` | `$SYSTEM_PATH/bin:/usr/trimui/bin:…` |

- Avant un pak, s'ajoutent les variables des hooks : `HOOK_CMD`, `HOOK_TYPE=pak`, `HOOK_EMU_PATH`, `HOOK_ROM_PATH`, `HOOK_LAST`.
- Note : la Brick est un TG3040 côté matériel, mais NextUI la range sous la plateforme **tg5040**.
- Lancement d'un pak TOOL :
  1. `nextui.elf` écrit `'<…>/Tools/tg5040/<X>.pak/launch.sh'` dans `/tmp/next`, puis quitte.
  2. La boucle de `MinUI.pak/launch.sh` lance le gouverneur CPU en `performance`, exécute les hooks `pre-launch.d`, puis fait `eval $CMD` (le pak tourne **au premier plan**, en root).
  3. Au retour : hooks `post-launch.d`, suppression de `/tmp/next`, nouvelle boucle sur `nextui.elf`.
  - Le répertoire courant est celui de `MinUI.pak`, pas celui du pak : un `launch.sh` doit faire `cd "$(dirname "$0")"`.
  - Le pak est aussi lançable par une touche FN (`runFnAction`).
- Les démons qui tournent pendant le pak : `keymon.elf`, `batmon.elf`, `audiomon.elf`, `trimui_inputd`, `syslogd`, plus ceux du système d'origine (point 10 et Firmware).

**Preuve**
- `skeleton/SYSTEM/tg5040/paks/MinUI.pak/launch.sh:14-26` (exports) ; `:50-59` (`DEVICE`, `IS_NEXT`) ; `:104-105` (`LD_LIBRARY_PATH`, `PATH`) ; `:182-196` (boucle `nextui.elf`, `eval $CMD`).
- `workspace/all/nextui/nextui.c:1220-1230` (`openPak`) : `sprintf(cmd, "'%s/launch.sh'", escapeSingleQuotes(path)); queueNext(cmd);`
- `PAKS.md:9` : « On tg5040 that is `brick`, `brickpro`, or `smartpro` ».

**Confiance** : haute.

**Reste à vérifier sur la console** : rien. `verify-device.sh` le confirmera au passage.

---

## 10. Réseau et mise en veille

**Réponse**
- Interface : **`wlan0`**. Wi-Fi géré par `wpa_supplicant`, socket de contrôle `/etc/wifi/sockets`, configuration `/etc/wifi/wpa_supplicant.conf`, DHCP par `udhcpc -i wlan0`.
- **SSID** : NextUI exécute `wpa_cli -p /etc/wifi/sockets -i wlan0 status` et lit la ligne `ssid=` quand `wpa_state=COMPLETED`.
  - `wpa_cli` est présent dans le rootfs d'origine (`/usr/sbin`).
  - En Go sans commande externe : envoyer `STATUS` en datagramme unix à `/etc/wifi/sockets/wlan0` (protocole wpa_ctrl).
- **Veille pendant un pak** : NextUI ne fait **rien**.
  - La veille (bouton, veille automatique après le délai d'écran) et l'extinction sont gérées par `PWR_update()`, que chaque application appelle dans sa propre boucle (`nextui.elf`, `minarch`).
  - Aucun démon ne surveille le bouton d'alimentation : `keymon` ne gère que volume, luminosité, MENU et prise jack.
  - Un pak Go qui n'appelle pas d'équivalent n'est donc **jamais mis en veille automatiquement**. Le bouton d'alimentation n'a d'effet que si le pak (ou gabagool) le gère.
  - Si le pak déclenche une veille via `$SYSTEM_PATH/bin/suspend`, ce script **arrête `wpa_supplicant`** (Wi-Fi coupé) avant `echo mem > /sys/power/state`, puis le relance au réveil. Le serveur perd donc le réseau pendant la veille.

**Preuve**
- `skeleton/SYSTEM/tg5040/etc/wifi/wifi_init.sh:3` : `WIFI_INTERFACE="wlan0"` ; `workspace/all/common/generic_wifi.c:24-25` : `#define WPA_CLI_CMD "wpa_cli -p " WIFI_SOCK_DIR " -i " WIFI_INTERFACE` ; `workspace/tg5040/platform/platform.c:836` : `#define WIFI_SOCK_DIR "/etc/wifi/sockets"`.
- `workspace/all/common/generic_wifi.c:272-297` : `status`, `wpa_state=…COMPLETED`, `"\nssid="`.
- `workspace/all/common/api.c:4196-4204` : la veille passe par `PWR_update` (`screenOffDelay`, `BTN_SLEEP`).
- `workspace/tg5040/keymon/keymon.c:27-33` : seuls `CODE_MENU*`, `PLUS`, `MINUS`, `MUTE`, `JACK` sont gérés.
- `skeleton/SYSTEM/tg5040/bin/suspend:33-36` : `if pgrep wpa_supplicant; then … /etc/wifi/wifi_init.sh stop` ; ligne 69 : `echo mem >/sys/power/state`.

**Confiance** : haute pour `wlan0` et le SSID. Haute pour « NextUI ne met pas en veille pendant un pak ». **Inconnu** pour le comportement de gabagool : son code n'était pas dans le périmètre.

**Reste à vérifier sur la console** : pendant que Cheat Manager (gabagool) tourne, appuyer sur le bouton d'alimentation et attendre au-delà du délai d'écran. Ou bien lire le code de gabagool v2 pour savoir s'il gère la veille ou le bouton.

---

## 11. Horloge

**Réponse**
- Il y a une RTC matérielle `/dev/rtc0`.
- Au démarrage, `sysfixtime` (système d'origine, S00) fait `hwclock -s`. Sans RTC, il remet l'heure à la date du fichier le plus récent de `/etc`.
- `sysfixtime stop` (K90) sauvegarde l'heure dans la RTC à l'extinction, mais **seulement par le chemin normal des scripts rc**. NextUI s'éteint par `poweroff_next`, qui appelle directement `reboot(POWER_OFF)` après `sync` : l'heure n'est donc **pas** sauvegardée à l'extinction.
- NextUI n'écrit la RTC que quand l'utilisateur règle l'heure à la main (`date -s …; hwclock -u -w`).
- NTP : réglage NextUI « Network time sync » (uci `system.ntp.enable` + `/etc/init.d/ntpd`, busybox ntpd).
  - Le système d'origine lance aussi `sysntpd` au démarrage (S98).
  - Dans la config d'origine, la clé est `enable 1`, alors que `sysntpd` valide `enabled` (défaut 1). L'option de NextUI n'a donc probablement aucun effet sur `sysntpd`.
  - ntpd ne recopie pas l'heure dans la RTC : le seul hook ntp concerne dnsmasq.
- Fuseau : uci `zonename` et `/tmp/localtime`. Valeur d'origine `Asia/Shanghai`, modifiable dans NextUI.
- Conclusion pour la corbeille : l'heure murale dépend de la RTC (qui tient si elle est alimentée) et de NTP une fois le Wi-Fi connecté. Elle n'est **pas garantie** au lancement du pak. La décision « âge crédité borné à 7 jours » est justifiée.

**Preuve**
- `workspace/tg5040/platform/platform.c:664` : `sprintf(cmd, "date -s '%d-%d-%d %d:%d:%d'; hwclock -u -w", …)` ; `:801-823` : `uci get/set system.ntp.enable`, `/etc/init.d/ntpd reload|stop`.
- `workspace/tg5040/poweroff_next/poweroff_next.c:145-152` : `sync(); … syscall(SYS_reboot, …, LINUX_REBOOT_CMD_POWER_OFF, NULL)`.
- rootfs `etc/init.d/sysfixtime` : `start() { … $HWCLOCK -s -f $RTC_DEV }` / `stop() { … $HWCLOCK -w -u -f $RTC_DEV`.
- rootfs `etc/config/system` : `config timeserver ntp … option enable 1` ; `etc/init.d/sysntpd` : `'enabled:bool:1'`.

**Confiance** : moyenne. Le code est clair, mais deux points dépendent du matériel : la pile ou le supercondensateur de la RTC, et l'état réel de ntpd.

**Reste à vérifier sur la console** :
- `hwclock -r` et `date`, puis éteindre sans Wi-Fi et rallumer quelques heures plus tard : la RTC a-t-elle tenu ?
- `pgrep -a ntpd` pour savoir si ntpd tourne.

---

## 12. Certificats racine HTTPS

**Réponse**
- **Aucun magasin de certificats** n'existe sur le système d'origine 1.1.1 : pas de `/etc/ssl` du tout, aucun `*.crt` ni `*.pem` dans le rootfs. libcurl y est compilé avec `/etc/ssl/certs`, mais ce dossier n'existe pas.
- NextUI n'en fournit pas pour tg5040. Seule la plateforme h700 embarque `ca-certificates.crt` (`CURL_CA_BUNDLE`).
- Le client HTTP Go ne trouvera donc rien dans ses chemins Linux par défaut. **Le pak doit embarquer son magasin** : bundle dans `resources/` plus `SSL_CERT_FILE` dans `launch.sh`, ou certificats compilés dans le binaire.
- Le mécanisme de Cheat Manager (gabagool/certifiable) n'a pas été vérifié (hors périmètre) : le point « [À VÉRIFIER : même mécanisme] » de 05 §5.6 reste ouvert.

**Preuve**
- `skeleton/SYSTEM/h700/bin/h700-init.sh:27` : `export CURL_CA_BUNDLE="$SYSTEM_PATH/etc/ssl/certs/ca-certificates.crt"`. Il n'y a pas d'équivalent sous `skeleton/SYSTEM/tg5040`.
- rootfs : `ls etc/ssl` donne « No such file or directory » ; `find -name '*.crt' -o -name '*.pem'` ne trouve rien ; `strings usr/lib/libcurl.so*` contient `/etc/ssl/certs`.

**Confiance** : haute pour le système d'origine et NextUI. Un autre pak pourrait avoir installé des certificats dans l'overlay (`/overlay`), mais il ne faut pas compter dessus.

**Reste à vérifier sur la console** : `ls -la /etc/ssl/certs/` (absent attendu).

---

## 13. Autres points

- **mDNS / Bonjour : avahi-daemon tourne déjà.** Le système d'origine le lance au démarrage (`/etc/rc.d/S61avahi-daemon`, avant `S99runtrimui` qui lance NextUI). Il écoute donc déjà UDP 5353 et publie `TinaLinux.local` (hostname par défaut).
  - `grandcat/zeroconf` se lie à `224.0.0.0:5353`. Une adresse multicast fait poser `SO_REUSEADDR` par Go, ce qui devrait permettre de coexister avec avahi, mais avec deux répondeurs mDNS sur la même machine.
  - Alternative : publier via avahi par D-Bus (`libavahi-client` présent, mais aucune commande `avahi-publish`).
  - Confiance : moyenne. **À tester sur la console.**
- **`DEVICE`** : valeurs `brick`, `brickpro`, `smartpro`. Le firmware 1.1.1 contient bien la chaîne `Trimui Brick` dans `MainUI`, donc `DEVICE=brick`.
- **Récents et reprise** : fichiers sous `.userdata/shared/.minui/` (`recent.txt`, `auto_resume.txt`, `game_switcher.txt`). À ne pas toucher.
- **`/tmp/resume_slot.txt`** est transitoire, ignorable.
- **`.system/` est remplacé à chaque mise à jour de NextUI** (`PAKS.md:5`) : rien de ReminisLink ne doit y être écrit. Cela confirme le choix `.userdata/shared/ReminisLink/`.

---

## Corrections à apporter aux specs (05 §3.1, 04)

| Élément | Spec actuelle | Valeur relevée dans le code |
|---|---|---|
| Multi-disques | `Roms/<Sys>/<Jeu>/<Jeu>.m3u` | Confirmé. Le jeu s'appelle `<Jeu>.m3u` dans les saves et states. Variante `<Jeu>/<Jeu>.cue`. |
| Save | `Saves/<TAG>/<rom.ext>.sav` | Confirmé par défaut (`saveFormat=0`). Plus `.rtc`. Formats `.srm` (rzip ou non) et `<rom>.sav` selon `minuisettings.txt`. |
| States | `.userdata/shared/.minui/<TAG>/<rom>.st<N>` | **Faux** : `.userdata/shared/<TAG>-<cœur>/<rom.ext>.st<N>`. Emplacements 0-7 (affichés 1-8), 8 caché, 9 reprise. Formats `.state*` selon `stateFormat`. |
| Vignette | ? | `.userdata/shared/.minui/<TAG>/<rom.ext>.<N>.bmp`, contenu **PNG**, 1024×768 sur Brick, emplacements 0-7 seulement. |
| Reprise | emplacement 9 ? | 9, confirmé (`.st9`). Écrit en veille, à l'extinction en jeu et par Quicksave ; pas en quittant. |
| Version | ? | `/mnt/SDCARD/.system/version.txt` (ligne 1 `NextUI-AAAAMMJJ-N`, ligne 2 hash) ; système d'origine dans `/etc/version`. |
| Batterie | `/sys/class/power_supply/*/capacity`, `status` | `axp2202-battery/capacity` ; charge = `axp2202-usb/online` et `axp2202-battery/time_to_full_now > 0`. |
| `LOGS_PATH` | (défaut à fixer) | `/mnt/SDCARD/.userdata/tg5040/logs` |
| SSID | ? | `wpa_cli -p /etc/wifi/sockets -i wlan0 status`, ligne `ssid=` |
| `resumeSlot` (04) | ? | `9` (`null` si `stateFormat` ≠ 0, car le fichier s'appelle alors `.state.auto`). À décider côté contrat. |

---

## Liste réduite des vérifications sur console

1. **Veille et bouton d'alimentation pendant un pak gabagool** : la console se met-elle en veille ? Que fait le bouton ? Vérifier aussi que `wlan0` reste actif plusieurs minutes sans toucher la console.
2. **Coexistence mDNS** : avec avahi-daemon actif (`pgrep -a avahi`), l'annonce `zeroconf` du pak est-elle visible depuis l'iPhone ? Sinon, prévoir le passage par avahi (D-Bus).
3. **RTC et NTP** : l'heure tient-elle après extinction et rallumage sans Wi-Fi (`hwclock -r`, `date`) ? ntpd tourne-t-il (`pgrep -a ntpd`) ?
4. **`/sys/class/power_supply/axp2202-battery/status`** existe-t-il, et avec quelles valeurs ?
5. **Séparateur dans `.system/version.txt`** (`od -c`).
6. **Confirmation rapide** (le script actuel le fait déjà) : un dossier `.userdata/shared/<TAG>-<cœur>/` avec `.st0`/`.st9`, une vignette `.minui/<TAG>/*.0.bmp` (signature PNG, 1024×768), et les valeurs de `minuisettings.txt` (`saveFormat`, `stateFormat`).

À corriger dans `scripts/verify-device.sh` :
- la section « Save state thumbnails » cherche `*.st[0-9].png` alors que les vignettes s'appellent `*.<N>.bmp` ;
- `find -name '*.st9'` sous `.userdata` est correct ;
- ajouter `cat $SHARED_USERDATA_PATH/minuisettings.txt` et `ls /sys/class/power_supply/axp2202-*`.

---

## Firmware et rootfs

### Source officielle
- Dépôt GitHub de l'organisation **trimui** : `https://github.com/trimui/firmware_brick` (« Firmware release for TG3040 Brick », pas un fork).
- Page : `https://github.com/trimui/firmware_brick/releases/tag/v1.1.1-20251126` (dernière release, publiée le 01/12/2025).
- Fichiers de la release :

| Fichier | Taille | SHA-256 publié par GitHub | Contenu |
|---|---|---|---|
| `TG3040_Brick_firmware_v1.1.1.zip` | 118 313 481 o | `488db02176e81ad4fdcf9e0bb4dbf406c883199f5b3521445e038bf81c8916ef` | Mise à jour : zip **LZMA** contenant `trimui_tg3040.awimg` (584 260 608 o) |
| `sd_recovery_BRICK_tg3040_v1.1.1_20251126.zip` | 2 017 199 416 o | `bb7cf5cf17aab9f23cda4981622e0a3e7a41ce692f6760c9efc456039348f865` | Image de récupération pour carte SD (non téléchargée) |

- URL du fichier téléchargé : `https://github.com/trimui/firmware_brick/releases/download/v1.1.1-20251126/TG3040_Brick_firmware_v1.1.1.zip`.
- **SHA-256 vérifié** : identique au condensat publié.

### Extraction (commandes exactes, dans `/private/tmp/claude-501/brick-fw/`)

1. Téléchargement et vérification :
   ```sh
   curl -sSL -o TG3040_Brick_firmware_v1.1.1.zip <URL ci-dessus>
   shasum -a 256 TG3040_Brick_firmware_v1.1.1.zip
   ```
2. Décompression. Le zip utilise LZMA : `unzip` échoue avec le code 81, on passe par 7z dans un conteneur :
   ```sh
   docker run --rm -v /private/tmp/claude-501/brick-fw:/fw debian:bookworm sh -c \
     'apt-get update -qq && apt-get install -y -qq p7zip-full; cd /fw && 7z e -y -o/fw TG3040_Brick_firmware_v1.1.1.zip trimui_tg3040.awimg release_note_tg3040_brick_20251126.txt'
   ```
3. Format de l'image : `IMAGEWTY` (Allwinner PhoenixSuit) v3, **non chiffrée**. La table des fichiers est faite d'entrées de 1024 octets à partir de l'octet 1024 ; nom à +36, puis longueur stockée à +292, longueur d'origine à +300, offset à +308. L'entrée `RFSFAT16 / ROOTFS_FEX000000` (`rootfs.fex`) est à l'octet 35840 : offset `0x01131000`, longueur `0x21c00000`.
   ```sh
   xxd -s $((35840+292)) -l 24 -e trimui_tg3040.awimg
   dd if=trimui_tg3040.awimg bs=4096 skip=$((0x01131000/4096)) count=$((0x21c00000/4096)) of=rootfs.fex
   ```
   `file rootfs.fex` donne « ext2 filesystem data … (extents) », c'est-à-dire de l'**ext4**.
4. Extraction de l'ext4, sans montage :
   ```sh
   docker run --rm -v /private/tmp/claude-501/brick-fw:/fw debian:bookworm sh -c \
     'apt-get update -qq && apt-get install -y -qq e2fsprogs; debugfs -R "rdump / /fw/rootfs" /fw/rootfs.fex'
   ```
   Ensuite, une archive tar faite sous Linux (sensible à la casse, droits conservés) pour l'import Docker :
   `debugfs -R "rdump / /r" …; tar -C /r -cf /fw/rootfs.tar .`
   Les deux extractions comptent 4068 entrées : pas de collision de casse sur le Mac.

Fichiers laissés en place : `rootfs/` (436 Mo, extrait sur APFS), `rootfs.tar`, `rootfs.fex` (SHA-256 `023de6fe…12f8`), `trimui_tg3040.awimg` (SHA-256 `d9189936…8d98`), le zip et les notes de version.

### Relevés dans le rootfs

| Élément | Valeur | Preuve |
|---|---|---|
| Distribution | **Tina Linux** (OpenWrt Allwinner) « Neptune », `tina.trimuidev.20251126.034222 4.0.0`, cible `a133-aw3/generic` | `etc/openwrt_release` |
| Version TrimUI | `1.1.1` | `etc/version` |
| Architecture | aarch64 (ELF 64 bits) | `file bin/busybox` |
| libc | **glibc 2.33** (`lib/libc-2.33.so`, `ld-linux-aarch64.so.1 → ld-2.33.so`) | `DISTRIB_TAINTS='no-all glibc busybox'` |
| BusyBox | **v1.27.2** | `strings bin/busybox` |
| Noyau (modules) | 4.9.191 | `lib/modules/` |
| Certificats racine | **absents** (aucun `/etc/ssl`, aucun `.crt`/`.pem`) | voir point 12 |
| OpenSSL | `libssl.so.1.1`, `libcrypto.so.1.1`, et `libssl3.so` (NSS) | `usr/lib` |
| Outils réseau | `wpa_cli`, `iw`, `curl`, `wget` | `usr/sbin`, `usr/bin` |
| Heure | `sysfixtime` (S00 au démarrage, K90 à l'arrêt, `hwclock` ↔ `/dev/rtc0`) ; `sysntpd` (S98, busybox ntpd, serveurs aliyun et openwrt) ; `/etc/init.d/ntpd` (variante sans uci) ; fuseau d'origine `Asia/Shanghai` | `etc/init.d/sysfixtime`, `etc/config/system` |
| mDNS | **avahi-daemon** lancé au démarrage (`S61avahi-daemon`), `publish-addresses=yes` ; `libavahi-client`, `libavahi-compat-libdnssd`, dbus | `etc/rc.d`, `etc/avahi/avahi-daemon.conf` |
| Autres services | dnsmasq, adbd, **sshd** (ajouté en 1.1.1), mtp, udev, cron | `etc/rc.d` |
| Lancement de NextUI | `etc/init.d/runtrimui` (S99) lance `/usr/trimui/bin/runtrimui.sh`. NextUI remplace ce script (copié de `skeleton/BOOT/trimui/app/runtrimui.sh`) par une version qui exécute `/mnt/SDCARD/.tmp_update/updater` s'il existe, sinon `runtrimui-original.sh` | `etc/init.d/runtrimui` ; `skeleton/BOOT/trimui/app/runtrimui.sh:16-21` |
| Partitions | rootfs en lecture seule de fait, avec overlay `/overlay` (`rootfs_data`), `/mnt/UDISK`, et `/mnt/SDCARD` = `/dev/mmcblk1` monté `rw,sync,iocharset=utf8` | `etc/config/fstab` |

**NextUI garde le système d'origine.** Il ne flashe rien : il se greffe sur `/usr/trimui/bin/runtrimui.sh` (via l'overlay) et tourne depuis la carte SD (`.system/tg5040`, bibliothèques en tête de `LD_LIBRARY_PATH`). Le pak tournera donc bien sur ce rootfs : glibc 2.33, BusyBox 1.27.2, noyau 4.9, plus les bibliothèques de NextUI dans `$SYSTEM_PATH/lib`.

Réserve : la console de l'utilisateur peut avoir une autre version du firmware d'origine. NextUI conseille 1.0.6 ou plus récent, et `/etc/version` le dira.

Conséquence pour la compilation Go :
- avec `CGO_ENABLED=0`, aucune dépendance à la libc ;
- avec CGO (SDL), la cible glibc doit être ≤ 2.33. La toolchain Debian 11 (glibc 2.31) convient.

### Essai Docker (point 4 de la demande)
```sh
docker import --platform linux/arm64 rootfs.tar reminislink/brick-rootfs:1.1.1
docker run --rm --platform linux/arm64 reminislink/brick-rootfs:1.1.1 /bin/busybox uname -a
# → Linux 93c4c032a8df 7.0.12-linuxkit … aarch64 GNU/Linux
```
**Réussi** : l'image `reminislink/brick-rootfs:1.1.1` (`sha256:4111a48411d2…`) est **conservée**. Le noyau est celui de Docker Desktop, pas le 4.9 de la console. Rien d'autre du rootfs n'a été exécuté.

Sources : [trimui/firmware_brick releases](https://github.com/trimui/firmware_brick/releases), [LoveRetro/NextUI](https://github.com/LoveRetro/NextUI), [NextUI v6.15.0](https://github.com/LoveRetro/NextUI/releases/tag/v6.15.0).
