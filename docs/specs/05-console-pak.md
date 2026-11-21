# 05 — Le pak (console)

## 1. Installation et lancement

- Pak de type `TOOL`, affiché **ReminisLink** dans le menu Outils de NextUI.
- Emplacement : `/Tools/<platform>/ReminisLink.pak/` (ex. `/Tools/tg5040/ReminisLink.pak/launch.sh`).
- Archive de publication : `ReminisLink.pak.zip`, publiée par la CI sur un tag `vX.Y.Z`, lue par le Pak Store (un tag `vX.Y.Z-beta.N` ou `-rc.N` donne une « Pre-release » GitHub que le Pak Store ignore, M17) (même chaîne que Cheat Manager : `pak.json`, Makefile, workflow).
- `launch.sh` : comme celui de Cheat Manager (bibliothèques embarquées dans `resources/lib`, journal d'erreurs dans `$LOGS_PATH/ReminisLink.stderr.txt`, `sync` à la fin). Il DOIT commencer par `cd "$(dirname "$0")"` : NextUI le lance avec `eval` depuis le dossier de `MinUI.pak`, au premier plan et en root (code NextUI `MinUI.pak/launch.sh:182-196`, `nextui.c:1220-1230`).
- Ne rien écrire dans `/.system/` : ce dossier est remplacé à chaque mise à jour de NextUI (`PAKS.md:5`).

## 2. Cycle de vie

1. **Démarrage** : lecture de la configuration, création de l'identifiant de console et du certificat TLS (`tls/`, M13) au premier lancement ; effacement des clients RL1 au premier lancement M13.
2. **Contrôle Wi-Fi** : une adresse IPv4 non loopback doit exister sur l'interface sans fil (`wlan0` sur tg5040, code NextUI `etc/wifi/wifi_init.sh:3`). Sinon : écran « Pas de Wi-Fi. Active le Wi-Fi dans les réglages de NextUI. » et sortie avec B.
3. **Maintenance** (avant d'ouvrir le serveur, en affichant « Préparation… ») :
   - archivage des states de reprise qui ont changé (§5.3) ;
   - contrôle des verrous et restauration automatique (§5.2) ;
   - purge de la corbeille (entrées de plus de 30 jours d'âge crédité, voir §4), des envois expirés et des fichiers `staging` (> 24 h).
4. **Serveur** : écoute en HTTPS (M13) sur `0.0.0.0:47821` (ou le premier port libre parmi les 9 suivants), puis annonce Bonjour.
5. **Écran principal** (§7).
6. **Fermeture** (B, ou signal SIGTERM/SIGINT) : refus des nouvelles requêtes (`503 shutting_down`), événement `shutdown` aux flux d'événements (M14, 1 s au plus pour les écrire), `sync`, retrait de l'annonce (sans attendre les requêtes ni les flux), attente des écritures en cours (10 s maximum), `sync`, sortie.

Le pak NE DOIT PAS laisser de processus en arrière-plan après sa fermeture.

**Mise en veille** (analyse du code, M0) :

- **Personne ne met la console en veille pendant un pak.** Dans NextUI, la veille (bouton, délai d'écran) et l'extinction sont gérées par `PWR_update()`, que chaque programme appelle dans sa propre boucle (`nextui.elf`, `minarch`) ; aucun démon ne surveille le bouton d'alimentation (`keymon` ne gère que volume, luminosité, MENU et jack) (code NextUI `api.c:4196-4204`, `keymon.c:27-33`). **Correction (M7) :** gabagool v2.26.6, avec `IsNextUI: true` (comme Cheat Manager), gère le bouton d'alimentation sur tg5040 (`internal/power.go`, `window.initPowerButtonHandling`) : appui court → `$SYSTEM_PATH/bin/suspend` (veille, Wi-Fi coupé, serveur injoignable jusqu'au réveil ; les envois reprennent ensuite, 03 §7) ; appui de 2 s ou plus → `/sbin/poweroff` (le pak reçoit SIGTERM et s'arrête proprement s'il en a le temps ; sinon, écritures atomiques et reprise au lancement : corbeille, marqueurs, envois). **Décision du 19/11/2026 (personne) : ce comportement normal est gardé**, comme pour les autres paks. Aucun délai d'écran ni veille automatique.
- Conséquence : tant que le pak est ouvert, la console reste allumée, écran compris, et le bouton d'alimentation n'a d'effet que si le pak le gère.
- Si le pak déclenchait une veille par `$SYSTEM_PATH/bin/suspend`, ce script arrête `wpa_supplicant` avant `echo mem > /sys/power/state` : le Wi-Fi est coupé pendant la veille et le serveur injoignable (code NextUI `bin/suspend:33-36, 69`).
- Reste à observer sur la console : effet réel du bouton d'alimentation (appui court et long) pendant un pak gabagool, et maintien de `wlan0` plusieurs minutes sans interaction (économie d'énergie du pilote) **[À VÉRIFIER M0]**. La conduite à tenir (gestion du bouton, extinction de l'écran, fermeture automatique) est une décision ouverte : voir [m0/questions.md](m0/questions.md).

## 3. Adaptateur d'OS

Toute connaissance de l'organisation des fichiers passe par une interface. Le reste du code ne construit jamais un chemin NextUI lui-même.

```go
type Adapter interface {
    Name() string                               // "nextui" ou "knulli"
    Roots() Roots                               // SD, Roms, Saves, States, Cheats, Userdata
    Systems() ([]System, error)                 // dossiers de Roms/ avec un tag
    GameFiles(g Game) (GameFiles, error)        // ROM, save, states, cheats, art
    SavePath(g Game) string
    StatePath(g Game, slot int) string
    StateThumbnailPath(g Game, slot int) string // "" si non géré
    ResumeSlot() (slot int, ok bool)
    CheatPath(g Game) string
    ArtPath(g Game) string
    DeviceInfo() (DeviceInfo, error)            // modèle, plateforme, version OS, batterie
}
```

Un seul adaptateur à la fois, choisi au démarrage (M20) : `nextui` par défaut, `knulli` avec l'option `--os knulli` ou la variable `REMINISLINK_OS=knulli` posée par le lanceur Knulli. L'adaptateur Knulli réutilise le parcours des dossiers de l'adaptateur NextUI (dossiers, `.m3u`, multi-disques) avec ses propres règles (`nextui.Layout` : noms des systèmes, des saves et des states, dossier des jaquettes, console). Les racines viennent des variables d'environnement avec des valeurs par défaut ; les règles 2 (confinement) et 4 (exFAT) s'appliquent à tous.

### 3.1 Organisation NextUI

Variables d'environnement avec valeurs par défaut, comme Cheat Manager. NextUI les exporte toutes pour les paks (code NextUI `MinUI.pak/launch.sh:14-26, 50-59`) :

| Variable | Valeur sur tg5040 |
|---|---|
| `PLATFORM` | `tg5040` (la Brick est un TG3040 côté matériel, rangée sous tg5040 par NextUI) |
| `DEVICE` | `brick`, `brickpro` ou `smartpro` |
| `SDCARD_PATH` | `/mnt/SDCARD` |
| `ROMS_PATH`, `SAVES_PATH`, `CHEATS_PATH`, `BIOS_PATH` | `$SDCARD_PATH/Roms`, `/Saves`, `/Cheats`, `/Bios` |
| `USERDATA_PATH` | `$SDCARD_PATH/.userdata/tg5040` |
| `SHARED_USERDATA_PATH` | `$SDCARD_PATH/.userdata/shared` |
| `LOGS_PATH` | `$USERDATA_PATH/logs`, soit `/mnt/SDCARD/.userdata/tg5040/logs` |
| `SYSTEM_PATH` | `$SDCARD_PATH/.system/tg5040` |

Notations : `<rom.ext>` = nom du fichier ROM avec son extension, tel que `readdir` le renvoie ; `<rom>` = ce nom sans sa dernière extension (retirée seulement si elle fait 1 à 4 caractères) ; `<TAG>` = tag du dossier de premier niveau sous `Roms/` ; `<cœur>` = nom du cœur libretro de l'émulateur (voir States). Pour un multi-disques, `<rom.ext>` vaut `<Jeu>.m3u`.

| Élément | Chemin | Statut |
|---|---|---|
| Système | `Roms/<Nom> (<TAG>)/`. Tag = texte entre la **dernière** `(` et la `)` suivante ; sans parenthèses, le nom entier du dossier. Dossiers cachés : nom commençant par `.`, finissant par `.disabled`, ou `map.txt` | Confirmé (code source NextUI `utils.c:36-37, 369-377`) |
| Système affiché par NextUI | Seulement s'il existe un pak émulateur `<TAG>.pak/launch.sh` dans `.system/tg5040/paks/Emus/` ou `/Emus/tg5040/`, **et** au moins une entrée non cachée. Les dossiers de même préfixe jusqu'à `(` inclus sont fusionnés à l'affichage (`Game Boy Advance (GBA)` + `Game Boy Advance (MGBA)` = une entrée). Alias : `Roms/map.txt` | Confirmé (code source NextUI `nextui.c:535-541, 676-699, 1052-1073`) |
| ROM | `Roms/<Système>/[sous-dossiers/]<rom.ext>`. NextUI ne filtre **aucune** extension : tout fichier non caché est une ROM ; un sous-dossier `*.pak` est un pak. Tri `strcasecmp` sur le nom d'affichage | Confirmé (code source NextUI `nextui.c:1011-1033`) |
| Multi-disques | `Roms/<Système>/<Jeu>/<Jeu>.m3u` (nom du `.m3u` = nom exact du dossier, casse ignorée sur exFAT ; chemins relatifs au dossier). Variante : `<Jeu>/<Jeu>.cue`, lancée automatiquement. Un `.m3u` isolé est une ROM ordinaire. Le jeu s'appelle `<Jeu>.m3u` pour la save, les states et les vignettes | Confirmé (code source NextUI `nextui.c:548-575`, `ma_game.c:108-134`) |
| Cheats | `Cheats/<TAG>/<rom.ext>.cht` | Confirmé (Cheat Manager) |
| Jaquette | `<dossier qui contient l'entrée>/.media/<nom de l'entrée sans sa dernière extension>.png` : `Roms/<Système>/.media/<rom>.png` au premier niveau, `Roms/<Système>/<sous-dossier>/.media/<rom>.png` dans un sous-dossier, `Roms/<Système>/.media/<Jeu>.png` pour un multi-disques. Seul le **dernier** point est retiré, même pour un dossier (`Sky Garden Quest Vol. 3` → `Sky Garden Quest Vol.png`) | Confirmé (code source NextUI `nextui.c:2979-2993, 3034`) |
| Save | `Saves/<TAG>/<rom.ext>.sav` avec le réglage par défaut (`saveFormat=0`). Autres valeurs : `1` → `<rom>.srm` compressé rzip ; `2` → `<rom>.sav` ; `3` → `<rom>.srm` non compressé | Confirmé (code source NextUI `ma_core.c:75`, `ma_saves.c:31-41`, `config.h:24-31, 236`) |
| Horloge du jeu | `Saves/<TAG>/<rom.ext>.rtc`, quel que soit `saveFormat` ; fait partie des fichiers du jeu | Confirmé (code source NextUI `ma_saves.c:111`) |
| States | `.userdata/shared/<TAG>-<cœur>/<rom.ext>.st<N>` avec le réglage par défaut (`stateFormat=0`). `<cœur>` = nom du `.so` sans `_libretro` (coupé au dernier `_`) : par exemple `GB-gambatte`, `GBA-gpsp`, `PS-pcsx_rearmed` ; il dépend du pak émulateur (`EMU_EXE=` de son `launch.sh`), cherché comme NextUI d'abord dans `<SD>/Emus/<PLATFORM>/<TAG>.pak/`, puis dans `<SD>/.system/<PLATFORM>/paks/Emus/<TAG>.pak/` (`utils.c:381-384`). Un verrou ou une entrée d'historique de reprise note le dossier `<TAG>-<cœur>` de sa copie : si le cœur a changé, rien n'est restauré (statut `core-changed`). Avec `useExtractedFileName=1`, les saves et states d'un `.zip` portent le nom du fichier extrait : ReminisLink refuse alors les écritures pour ces jeux (`422 unsupported_format`). Autres formats : `1` (rzip) et `2` → `<rom>.state.<N>` ; `3` (rzip) et `4` → `<rom>.state` (emplacement 0), `<rom>.state<N>`, et `<rom>.state.auto` pour la reprise. Emplacements : 0 à 7 pour l'utilisateur (affichés 1 à 8), 8 = state « par défaut » caché, lu au lancement s'il existe mais jamais écrit par NextUI | Confirmé (code source NextUI `ma_core.c:14-17, 74`, `ma_saves.c:166-195, 231`, `ma_menu.c:117`) |
| Vignette de state | `.userdata/shared/.minui/<TAG>/<rom.ext>.<N>.bmp`, emplacements 0 à 7 seulement. Le contenu est un **PNG** malgré l'extension (détection par la signature `\x89PNG`) ; capture plein écran, 1024×768 sur Brick (résolution HDMI en HDMI) | Confirmé (code source NextUI `ma_menu.c:179, 1617, 1713-1714`, `platform.h:141-142`) |
| Fichiers annexes des states | `.userdata/shared/.minui/<TAG>/<rom.ext>.txt` (dernier emplacement utilisé, lu par « Reprendre » dans la liste) ; `<rom.ext>.<N>.txt` (disque du state, multi-disques) | Confirmé (code source NextUI `ma_menu.c:1618, 1694-1697`, `nextui.c:1295-1310`) |
| State de reprise | Emplacement **9** (`<rom.ext>.st9`). Écrit avant la mise en veille en jeu, avant l'extinction en jeu, par l'option « Quicksave » et au changement HDMI ; **pas** quand on quitte le jeu. Le chemin de la ROM va dans `.userdata/shared/.minui/auto_resume.txt`, supprimé au démarrage suivant ou au réveil ; le `.st9` reste en place jusqu'à la prochaine écriture. Pas de vignette | Confirmé (code source NextUI `defines.h:27-28`, `ma_menu.c:224-228, 1795-1796`, `nextui.c:1185-1215`) |
| Réglages de format | `.userdata/shared/minuisettings.txt` : `saveFormat`, `stateFormat`, `useExtractedFileName` (défaut 0 : avec 1, un `.zip` que le cœur ne lit pas prend le nom du fichier extrait) | Confirmé (code source NextUI `config.c:151, 1654`, `config.h:236-238`) |
| Version NextUI | `/mnt/SDCARD/.system/version.txt` : ligne 1 nom de release `NextUI-AAAAMMJJ-N` (ex. `NextUI-20261013-0` pour la v6.15.0), ligne 2 hash git court. Le numéro `v6.x.y` n'y figure pas. Système d'origine : `/etc/version` (ex. `1.1.1`) | Confirmé (code source NextUI `makefile:249`, `settings.cpp:1118`, `platform.c:327-329` ; firmware v1.1.1 `/etc/version`) |
| Batterie | Niveau : `/sys/class/power_supply/axp2202-battery/capacity`. En charge : `axp2202-usb/online == 1` **et** `axp2202-battery/time_to_full_now > 0` (méthode de NextUI, qui n'utilise pas `status`) | Confirmé (code source NextUI `platform.c:183-188`) |
| SSID | `wpa_cli -p /etc/wifi/sockets -i wlan0 status` : ligne `ssid=` si `wpa_state=COMPLETED`. Sans commande externe : datagramme `STATUS` sur la socket unix `/etc/wifi/sockets/wlan0` (protocole wpa_ctrl) | Confirmé (code source NextUI `generic_wifi.c:24-25, 272-297`, `platform.c:836` ; firmware v1.1.1 `/usr/sbin/wpa_cli`) |
| Fichiers à ne pas toucher | `.userdata/shared/.minui/recent.txt`, `auto_resume.txt`, `game_switcher.txt` ; tout `/.system/` | Confirmé (code source NextUI) |

Source : analyse du code NextUI au commit `b6ec1343` (identique à v6.15.0 pour ces fichiers) et du firmware TrimUI v1.1.1, détaillée dans [m0/source-analysis.md](m0/source-analysis.md). Le script `scripts/verify-device.sh` (lecture seule) permet de reconfirmer ces valeurs sur une vraie console.

**Cas particuliers à gérer** : deux dossiers pour la même console (ex. `Game Boy Advance (GBA)` et `Game Boy Advance (MGBA)` : chacun a son propre tag, donc ses propres dossiers de saves et de states, même si NextUI les fusionne à l'affichage) ; plusieurs dossiers `<TAG>-<cœur>` pour un même tag (changement de pak émulateur) ; des saves et states dans un format non par défaut (`minuisettings.txt`) ; noms avec accents, parenthèses, crochets, emojis ; jeux dans des sous-dossiers ; casse différente entre la ROM et ses fichiers (exFAT est insensible à la casse, la comparaison DOIT l'être aussi).

### 3.2 Autres plateformes NextUI (M20, expérimental)

Mêmes variables, mêmes chemins et mêmes règles qu'en §3.1, sous le nom de la plateforme (`.userdata/<plateforme>`, `.system/<plateforme>`, `Tools/<plateforme>`, `Emus/<plateforme>`). Ce qui change :

| Plateforme | Consoles | `DEVICE` exporté par `MinUI.pak/launch.sh` | Modèle de l'API | Batterie (`/sys/class/power_supply/`) | glibc | Statut |
|---|---|---|---|---|---|---|
| `tg5040` | Brick, Brick Pro, Smart Pro | `brick`, `brickpro`, `smartpro` | le même | `axp2202-battery`, `axp2202-usb` | 2.33 | Confirmé (console) |
| `tg5050` | Smart Pro S | `smartpros`, seulement si `MainUI` contient « Trimui Smart Pro S » | `smartpros` (aussi sans `DEVICE`, avec `IS_NEXT=yes`) | `axp2202-battery`, `axp2202-usb` | 2.33 | Source |
| `h700` | Anbernic RG28XX, RG34XX(SP), RG35XX Plus/H/Pro/SP, RG40XX H/V, RG CubeXX | `rg28xx`, `rg34xx`, `rg34xxsp`, `rg35xxplus`, `rg35xxh`, `rg35xxpro`, `rg35xxsp`, `rg40xxh`, `rg40xxv`, `rgcubexx`, `rgsp` (défaut `rg35xxplus`) | `h700` | `axp2202-battery`, `axp2202-usb` | 2.35 | Source |
| `my355` | Miyoo Flip | `my355` | `my355` | `battery`, `ac` | 2.28 | Source (branche) |

Sources : NextUI `main` au commit `b6ec1343` : `skeleton/SYSTEM/tg5050/paks/MinUI.pak/launch.sh` (variables, `DEVICE`, `IS_NEXT`), `skeleton/SYSTEM/h700/paks/MinUI.pak/launch.sh`, `skeleton/SYSTEM/h700/bin/h700-init.sh:5-19` (carte montée en `vfat` sur `/mnt/sdcard`, `/mnt/SDCARD` en bind ou en lien symbolique), `skeleton/SYSTEM/h700/bin/h700-device.sh`, `workspace/tg5050/platform/platform.c:208-213`, `workspace/h700/platform/platform.c:657-662` ; NextUI branche `my355-latest` au commit `a5c755bb` : `skeleton/SYSTEM/my355/paks/MinUI.pak/launch.sh`, `workspace/my355/platform/platform.c:204-209`. glibc : `LoveRetro/gcc-arm-10.3-aarch64-tg5050` `816e71bd` `aarch64.config:371` (2.33), `LoveRetro/gcc-arm-8.3-aarch64-my355` `66f7801c` `aarch64.config:359` (2.28), `LoveRetro/h700-toolchain` `04faa348` `Dockerfile:47-48` (système d'origine Ubuntu 22.04, glibc 2.35). gabagool v2.26.6 choisit l'écran et les boutons d'après `PLATFORM` (`pkg/gabagool/init.go:82-98`, `internal/input_mapper.go:216-237`).

**Un binaire, un zip.** Les quatre plateformes sont ARM64 ; le binaire de la Brick (Debian 11, CGO pour SDL) ne demande que glibc 2.17 (mesuré par `scripts/emulation/elfneeds`) et lit `PLATFORM` au lancement. Le Pak Store n'a qu'un `release_filename` par pak : il installe le même zip dans `Tools/<PLATFORM>/<nom>.pak/` pour chaque plateforme listée dans `platforms` de `pak.json` (`LoveRetro/nextui-pak-store` `669cc4a7` : `utils/functions.go` `GetPlatform`, `UnzipPakArchive`, `state/helpers.go` `GetBrowsePaks` ; le Pak Store lui-même et Cheat Manager font ainsi). ReminisLink suit donc le Pak Store : un binaire et un zip pour toutes les plateformes, au lieu du « binaire et pak par plateforme » de la feuille de route (écart signalé, à valider). Le Pak Store marque un pak « expérimental » dans son catalogue (`experimental_paks` de `storefront_base.json`), pas dans `pak.json`.

**Hypothèses** (non vérifiables sans les consoles ; à confirmer par les volontaires, voir [appel aux testeurs](../appel-testeurs.md)) :

- P1. `my355` n'existe que sur la branche `my355-latest` de NextUI : ses chemins sont ceux de cette branche. Son lanceur n'exporte pas `ROMS_PATH` : la valeur par défaut `$SDCARD_PATH/Roms` s'applique.
- P2. La carte h700 est en FAT32 ou exFAT, insensible à la casse comme sur tg5040. `/mnt/SDCARD` peut y être un lien symbolique vers `/mnt/sdcard` : la racine de la carte peut être un lien (testé), aucun lien n'est suivi en dessous (règle 2).
- P3. SDL2, SDL2_image et SDL2_ttf sont fournis par NextUI (`$SYSTEM_PATH/lib`) ou par le système d'origine sur `tg5050` et `my355`. Sur `h700`, le pak embarque `libSDL2_ttf` (`resources/lib/h700`, chargée par `launch.sh` sur cette plateforme seulement), comme le Pak Store (`launch.sh`, `taskfile.yml`). `libSDL2_gfx` est embarquée partout.
- P4. Wi-Fi sur `wlan0` et SSID par `wpa_cli -p /etc/wifi/sockets` : vérifié sur tg5040 seulement ; ailleurs le SSID peut être `null`.
- P5. L'écran, les boutons et le QR code (gabagool) n'ont été vus sur aucune de ces plateformes.

### 3.3 Knulli (M20, expérimental)

Knulli est un firmware Linux basé sur Batocera (EmulationStation et RetroArch). Les données de l'utilisateur sont sous `/userdata` (partition SHARE).

| Élément | Chemin | Source |
|---|---|---|
| Racine | `/userdata` (`SDCARD_PATH` la remplace, comme `--sd`) ; `ROMS_PATH`, `SAVES_PATH`, `CHEATS_PATH`, `USERDATA_PATH` (`/userdata/system`), `LOGS_PATH` sont lus aussi | `batoceraPaths.py:14-30` |
| Système | `roms/<dossier>/`, dossier = clé (ou `path`) d'une entrée de `es_systems.yml`, nom exact ; libellé = `name`. Les entrées `hardware: tool`, `emulator`, `system`, `library` (dont `tools`) ne sont pas des systèmes de jeux. Tag (dossier des saves) = le dossier | `es_systems.yml` (copié dans `internal/osadapter/knulli/systems.json` par `scripts/gen-knulli-systems.py`) |
| Système affiché | tout système connu dont le dossier a une entrée visible ; `es_systems.cfg` est généré par carte selon les cœurs construits, que la carte ne dit pas | Hypothèse |
| Jeu | fichier dont l'extension (casse ignorée) est dans `extensions` du système ; dossiers `images`, `videos`, `manuals`, `media`, `bezels`, `thumbnails`, `downloaded_images`, `downloaded_videos` ignorés ; `.m3u` et dossiers multi-disques comme NextUI ; fichiers cachés ignorés | `es_systems.yml`, `SystemData.cpp:260-279` |
| Save | `saves/<système>/<rom>.srm` (`<rom>` = nom sans la dernière extension) ; horloge `saves/<système>/<rom>.rtc` | `libretroConfig.py:240-243` (`savefile_directory`, `sort_savefiles_enable false`) |
| States | `saves/<système>/<rom>.state` (emplacement 0), `<rom>.state<N>` (1 à 99), vignette `<fichier du state>.png` ; `<rom>.state.auto` est la reprise automatique de RetroArch, pas un emplacement (pas de « state de reprise » au sens de §5.3) | `libretroConfig.py:241-242`, `libretroRetroarchCustom.py:70` |
| Cheats | non gérés : RetroArch lit `/userdata/cheats/saves/<cœur>/<jeu>.cht` et le cœur se choisit par système ou par jeu dans les réglages ; les routes des cheats répondent `422 unsupported_format` | `libretroRetroarchCustom.py:96-97` |
| Jaquette | `roms/<système>/images/<rom>-image.png` (dossier `images` de la racine du système, même pour un jeu dans un sous-dossier) | `batocera-emulationstation` `FileData.cpp:170` (`findLocalArt`) |
| Paks | aucun : `GET /paks` est vide, une installation est refusée | — |
| Données ReminisLink | `/userdata/system/ReminisLink/` ; journal `/userdata/system/logs/ReminisLink.txt` | `batoceraPaths.py:19, 30` |
| Lanceur | `/userdata/roms/tools/ReminisLink.sh` et le dossier caché `/userdata/roms/tools/.ReminisLink/` (binaire, `lib/libSDL2_gfx-1.0.so.0`) ; Knulli lance les `.sh` du système Outils avec `/bin/bash` ; le script pose `REMINISLINK_OS=knulli`. Archive : `ReminisLink.knulli.zip` (`make knulli-release`), à décompresser dans `/userdata/roms/tools/` | `es_systems.yml:2538-2550`, `generators/sh/shGenerator.py`, `SystemData.cpp:278`, `FileSystemUtil.cpp:1096` (dossier caché) |
| Console | `/boot/boot/knulli.board` → modèle (`trimui-brick` → `brick`, `trimui-brick-pro` → `brickpro`, `trimui-smart-pro` → `smartpro`, cartes Anbernic H700 → `h700`, autres → `desktop` faute de modèle dans le contrat) ; `platform` = le contenu de `knulli.board` ; `osVersion` = `/usr/share/knulli/knulli.version` ; batterie : première alimentation dont le nom contient `BAT` ou `bat` (`capacity`, `status`) ; Wi-Fi `wlan0`, SSID non lu | `board/*/fsoverlay/etc/init.d/rcS`, `knulli-version`, `knulli-battery-check` |
| glibc | 2.40 (buildroot 2024.11 de Knulli) | `knulli-cfw/buildroot` `c2d7985b` `package/glibc/glibc.mk:10` |

Sources : `knulli-cfw/knulli-linux` au commit `57afa662` (`package/system/knulli-configgen/configgen/configgen/…`, `package/emulationstation/knulli-es-system/es_systems.yml`), `knulli-cfw/batocera-emulationstation` au commit `f7c5ae10`. Carte factice : `testdata/knulli/` (avec son README).

**Hypothèses** :

- K1. Casse : `/userdata` peut être en ext4 (sensible à la casse) ou en exFAT. Le pak compare les noms sans la casse en préférant l'orthographe exacte, comme sur NextUI ; sur ext4, une save dont le nom ne diffère que par la casse est montrée alors que RetroArch ne la lit pas (limite connue), et les fichiers écrits par le pak portent le nom attendu par RetroArch. Un dossier de système doit être écrit exactement (`gba`, pas `GBA`). Les deux cas sont testés (tests Go sur APFS insensible à la casse, `make emulation` sur le système de fichiers sensible à la casse des conteneurs).
- K2. SDL2, SDL2_image et SDL2_ttf sont présents dans Knulli (EmulationStation) : non vérifié. Le binaire et le lanceur ont tourné en mode headless dans un Debian 12 avec les SDL de Debian ; l'interface gabagool (sans NextUI ni `PLATFORM`) n'a été vue sur aucune console.
- K3. Les states incrémentaux (option de Knulli) au-delà de l'emplacement 99 ne sont pas listés.
- K4. Les `.state` sont ceux de RetroArch ; un émulateur autonome (hors libretro) range ses saves ailleurs et n'est pas vu.

## 4. Données de ReminisLink sur la carte SD

Racine : `/.userdata/shared/ReminisLink/` (partagée entre plateformes, comme les saves).

```
ReminisLink/
├── config.json            # deviceId, deviceName, port préféré, langue
├── clients.json           # clients appairés : clientId, nom, plateforme, clé publique Ed25519 (b64u), plateformes vues, dates
├── tls/                   # certificat auto-signé du pak (cert.pem) et sa clé privée ECDSA P-256 (key.pem), M13
├── backups/
│   └── <TAG>/<fichier ROM>/
│       ├── saves/<backupId>.sav [+ <backupId>.rtc] + <backupId>.json   # métadonnées : libellé, raison, date, sha256 (et rtcSha256)
│       └── resume/<entryId>.st + <entryId>.json
├── locks/
│   └── <TAG>/<fichier ROM>/slot<N>.st + slot<N>.json  # sha256, date de verrouillage
├── trash/
│   └── <trashEntryId>/
│       ├── entry.json     # raison, libellé, date, liste des fichiers d'origine, taille
│       └── <chemin d'origine relatif à la SD>
├── uploads/               # fichiers temporaires des envois et des patchs
├── staging/               # patchs et .cht en attente d'utilisation ; paks en cours d'installation (pk_*, M12)
└── patches/               # patchs déposés par l'utilisateur (listés par /sd/files)
```

- `clients.json` ne contient que des **clés publiques** (M13) : une copie de la carte ne permet pas de signer à la place d'un client. La clé privée TLS du pak (`tls/key.pem`) est sur la carte, sans protection (exFAT n'a pas de permissions) : une copie permet de se faire passer pour la console sur le réseau local, pas pour les clients ; risque accepté (voir [07](07-securite.md)). Au premier lancement du pak M13, les clients RL1 (clé HMAC partagée) sont effacés.
- **Corbeille** : tout fichier de l'utilisateur supprimé ou remplacé par ReminisLink y est *déplacé* (renommage, même carte SD), jamais effacé directement. Seules la purge automatique (> 30 jours d'âge crédité) et les routes `DELETE /trash` effacent définitivement.
- **Âge crédité** (décision du 18/11/2026) : l'horloge de la console n'est pas fiable. Faits (analyse M0) : RTC matérielle `/dev/rtc0` lue au démarrage par `sysfixtime` (`hwclock -s`) ; NextUI s'éteint par `poweroff_next`, qui contourne `sysfixtime stop` : l'heure n'est **pas** réécrite dans la RTC à l'extinction (code NextUI `poweroff_next.c:145-152` ; firmware v1.1.1 `/etc/init.d/sysfixtime`) ; NTP par busybox ntpd (`sysntpd`, réglage NextUI « Network time sync »), seulement une fois le Wi-Fi connecté, sans recopie dans la RTC. Que la RTC tienne l'heure console éteinte dépend du matériel **[À VÉRIFIER M0]**. Le pak tient une horloge propre, cumulée et enregistrée dans `config.json` : pendant qu'il tourne, elle avance selon l'horloge monotone du système (enregistrée régulièrement et à la fermeture) ; à chaque lancement, elle est créditée du temps écoulé selon l'horloge murale depuis la plus grande heure murale jamais enregistrée (`creditWallMax` dans `config.json`, borne haute persistante mise à jour à chaque enregistrement ; M8, revue Codex de M4), **borné entre 0 et 7 jours**. Une horloge qui oscille entre une heure en retard (RTC au démarrage, avant NTP) et l'heure juste n'est donc créditée qu'une fois ; une horloge passée une fois dans le futur ne fait que retarder la purge (le temps de session compte toujours). Une entrée de corbeille note la valeur de cette horloge à sa création ; elle est purgée quand l'écart atteint 30 jours. Une horloge murale fausse ne peut donc jamais vieillir une entrée de plus de 7 jours d'un coup ; au pire, la purge est retardée. Un arrêt brutal fait perdre au plus le temps depuis le dernier enregistrement (dans le sens prudent).
- Toute écriture de fichier JSON est atomique (`.tmp`, `fsync`, renommage).
- Les dossiers `uploads/`, `staging/` et `trash/` sont sur la même carte SD que les destinations : le renommage final est atomique.

## 5. Fonctions métier

### 5.1 Sauvegardes de save

- Identifiant : `bk_<AAAAMMJJTHHMMSSZ>_<4 hex>`.
- Pas de limite automatique sur les copies `manual`. Les copies automatiques (`before-*`) sont limitées à 10 par jeu ; au-delà, les plus anciennes sont supprimées.
- **Fichier d'horloge** (`Saves/<TAG>/<rom.ext>.rtc`, v1.0.1) : il appartient à la save. Toute copie (manuelle ou automatique) le copie à côté de la save (`<backupId>.rtc`, écriture atomique, SHA-256 dans `rtcSha256` des métadonnées) ; la copie est écrite avant les métadonnées, une copie sans métadonnées n'existe pas. Une copie supprimée (ou au-delà de la limite) emporte son `.rtc` dans la même entrée de corbeille.
- Restauration : le `.rtc` de la copie est restauré avec la save (installé avant elle : une coupure entre les deux laisse l'ancienne save avec l'horloge restaurée, et les deux anciens fichiers sont dans la copie `before-restore`). Une copie sans `.rtc` restaure la save seule et laisse le `.rtc` actuel en place (voir 04, choix et raison).
- Remplacement depuis l'app (`PUT /games/{gameId}/save`, envoi ou téléchargement vers une save) : le `.rtc` reste en place ; il est copié avec l'ancienne save dans la copie `before-replace`.
- Suppression du jeu (`DELETE /games/{gameId}`) : le `.rtc` part dans la même entrée de corbeille que la save (ou reste avec elle si `keepSave=true`) ; la purge ou `DELETE /trash/{entryId}` efface l'entrée entière. Il est listé (rôle `rtc`) par `GET /games/{gameId}`, `GET /games/{gameId}/deletion` et téléchargeable par `GET /games/{gameId}/files/{fileId}`.

### 5.2 Verrous de states

Le pak ne tourne jamais en même temps qu'un jeu : il ne peut donc pas *empêcher* l'émulateur d'écraser un state. Le verrou fonctionne ainsi :

1. **Verrouiller** : copie du state dans `locks/`, avec son SHA-256.
2. **À chaque lancement du pak** : pour chaque verrou, comparer le state actuel à la copie.
   - Identique : rien à faire (`intact`).
   - Différent : le state actuel (écrasé en jeu) part dans la corbeille, la copie verrouillée est restaurée (`restored`, affiché dans l'app jusqu'à la consultation suivante).
   - Absent : la copie verrouillée est restaurée (`restored`).
3. **Déverrouiller** : suppression de la copie et du verrou.

L'app DOIT expliquer cette limite à l'utilisateur : « L'état est restauré à la prochaine ouverture de ReminisLink sur la console s'il a été écrasé en jeu. »

### 5.3 Historique du state de reprise

À chaque lancement, pour chaque jeu qui a un state de reprise (emplacement 9, `<rom.ext>.st9` avec `stateFormat=0` ; code NextUI `defines.h:28`) : si son SHA-256 diffère de la dernière entrée archivée, une copie est ajoutée à `backups/.../resume/`. On garde les 5 dernières entrées.

NextUI écrit l'emplacement 9 à la mise en veille et à l'extinction en jeu, pas quand on quitte le jeu, et ne l'efface jamais : un `.st9` peut donc être ancien. L'entrée « Reprendre » de la liste de NextUI utilise le dernier emplacement utilisé (`.minui/<TAG>/<rom.ext>.txt`), pas forcément le 9.

Pour que le lancement reste rapide, le pak compare d'abord la date de modification et la taille avec la dernière entrée, et ne calcule le SHA-256 que si elles ont changé.

### 5.4 Cheats

Réutiliser l'analyseur et l'écriture de Cheat Manager : clés inconnues conservées, fichier précédent gardé en `.cht.bak`, même convention de nommage. Règle 1 de `CLAUDE.md` : l'ancien `.cht.bak` n'est jamais écrasé sans passer par la corbeille (aucune version d'un `.cht` ne disparaît). *(M5.)*

### 5.5 Patchs

Implémentation en Go pur, sans dépendance :

- **IPS** : enregistrements classiques et RLE, marqueur `EOF`, extension de troncature de 3 octets après `EOF`.
- **BPS** : actions `SourceRead`, `TargetRead`, `SourceCopy`, `TargetCopy` ; vérification des CRC32 source, cible et patch (`422 patch_mismatch` si la source ne correspond pas).
- **UPS** : XOR par blocs, vérification des CRC32 source et cible.

L'application se fait vers un fichier temporaire, puis vérification (pour BPS et UPS), puis renommage. La ROM d'origine n'est jamais modifiée sur place.

### 5.6 Téléchargements (retirés de la v1)

Le pak ne télécharge rien et ne se connecte jamais à Internet : aucun client HTTP sortant, aucun magasin de certificats racine (téléchargement depuis un lien retiré le 20/11/2026 et écarté, décision de la personne). L'utilisateur télécharge un fichier sur l'iPhone, puis l'envoie depuis Fichiers ou le menu de partage. Les fichiers partiels laissés par un pak plus ancien dans `uploads/` sont effacés au lancement, comme tout fichier temporaire orphelin.

## 6. Informations de la console

| Champ | Source |
|---|---|
| `model` | Variable `DEVICE` exportée par NextUI (`brick`, `smartpro`...) |
| `platform` | Variable `PLATFORM` |
| `osVersion` | Ligne 1 de `/mnt/SDCARD/.system/version.txt` (`NextUI-AAAAMMJJ-N`), plus le hash de la ligne 2 (code NextUI `makefile:249`). Lecture robuste : accepter un vrai retour à la ligne ou un `\n` littéral comme séparateur. `null` si le fichier manque. Format exact de la chaîne : voir [m0/questions.md](m0/questions.md) |
| `battery` | `percent` = `/sys/class/power_supply/axp2202-battery/capacity` ; `charging` = `axp2202-usb/online == 1` et `axp2202-battery/time_to_full_now > 0` (code NextUI `platform.c:183-188`). `null` si ces fichiers manquent |
| `storage` | `statfs` sur `SDCARD_PATH` |
| `network.ipv4` | Interfaces réseau (`net.Interfaces`) |
| `network.ssid` | Réponse `STATUS` de `wpa_supplicant` (socket `/etc/wifi/sockets/wlan0`, ou `wpa_cli -p /etc/wifi/sockets -i wlan0 status`), ligne `ssid=` si `wpa_state=COMPLETED` (code NextUI `generic_wifi.c:272-297`) ; sinon `null` |

## 7. Écrans

Interface gabagool, thème NextUI, mêmes principes que Cheat Manager. Toutes les chaînes passent par la traduction (anglais et français en v1).

### 7.1 Écran principal

```
┌──────────────────────────────────────┐
│ ReminisLink                   ● Prêt │
│                                      │
│          ██████████████              │
│          ██ QR code   ██             │
│          ██████████████              │
│                                      │
│  Scanne avec l'app ReminisLink       │
│  (expire dans 9 min)                 │
│  Brick de Thomas · 192.168.1.42      │
│                                      │
│  Appareils connectés : iPhone de T.  │
│  ▸ Envoi : Sky Garden Quest DX.gba  62 % │
│                                      │
│ A Masquer le QR   Y Réglages   B Quitter │
└──────────────────────────────────────┘
```

- **Appairage seulement à la demande** (M8, décision du 19/11/2026) : tant qu'aucun appareil n'est appairé, l'écran principal affiche le QR code (comme ci-dessus). Dès qu'au moins un appareil est appairé, l'écran principal ne montre plus de QR code (nom, IP, état, appareils connectés, transferts) ; un nouvel appareil s'appaire depuis **Réglages > Appairer un nouvel appareil**, qui affiche le QR code. L'appairage n'est **ouvert que pendant que le QR code est affiché** : sinon `POST /pair` répond `401 pairing_failed` sans compter d'échec, et `GET /hello` renvoie `pairingOpen: false`. Le secret est régénéré à chaque ouverture de l'écran d'appairage et invalidé à sa fermeture. **Plus de PIN depuis M13** (décision du 19/11/2026) : seul le QR code est affiché.
- L'état **en haut à gauche** : `Prêt`, `Connecté` (une requête signée depuis moins de 30 s, ou un flux d'événements ouvert, M14), `Transfert en cours` ; la pastille ne chevauche jamais le titre.
- **En haut à droite, le niveau de batterie** (pourcentage et indicateur de charge, source 05 §6), rafraîchi régulièrement ; sous 15 % et sans charge, affichage d'alerte et, pendant un transfert, bandeau « Batterie faible : branche la console » (M8, demande de la personne).
- La liste des transferts et tâches en cours avec leur progression.
- Un bandeau de notification bref quand une action modifie la carte SD (« Sauvegarde restaurée : Sky Garden Quest »). M12 : « App installée », « Échec de l'installation de l'app » et « App mise à la corbeille », avec le nom du pak ; une installation en cours apparaît dans la liste des tâches.

### 7.2 Réglages (Y)

- **Appairer un nouvel appareil** : affiche le QR code (sans PIN depuis M13) et ouvre l'appairage jusqu'au retour (M8).
- **Nom de la console** (clavier gabagool), utilisé dans Bonjour.
- **Appareils** (M10, décision du 19/11/2026) : la liste des appareils appairés avec leur **plateforme** (M13 : *Apple*, *Android*, *Web* ou *Autre*, avec pour une identité Apple les plateformes vues récemment, par exemple « iPhone, iPad », d'après l'en-tête `X-RL-Platform`) et leur état : *connecté* (une requête signée acceptée depuis moins de 30 s, ou un flux d'événements ouvert, M14), *déconnecté* ; un appareil sans état affiche sa date d'appairage. L'état *a la main* est retiré avec le bail d'écriture (M13). A sur un appareil ouvre ses actions :
  - **Déconnecter** : refuse ses requêtes (`401 disconnected`) jusqu'à ce qu'il se reconnecte volontairement depuis l'app (`POST /clients/me/connect`). Tenu en mémoire : la fermeture du pak lève la déconnexion.
  - **Oublier** (avec confirmation) : supprime son appairage ; l'appareil devra se réappairer.
- **Oublier tous les appareils** (confirmation).
- **Langue**.
- **Version et journal** : version du pak, chemin du journal.

### 7.3 Quitter (B)

Si un transfert ou une tâche est en cours : demande de confirmation (« Un transfert est en cours. Quitter quand même ? »).

## 8. Journal

`$LOGS_PATH/ReminisLink.txt` via `log/slog` : démarrage, adresse et port, appairages (sans secrets), chaque requête (méthode, route, code, durée, client), chaque opération destructive avec le chemin de la sauvegarde créée. Jamais de clé, de secret, de PIN ni de signature dans le journal.

Protection de la carte SD (jalon M2) : un appareil non appairé ne doit pas pouvoir remplir le journal.
- Les lignes qu'une requête **non acceptée par la vérification de signature** peut provoquer (requêtes refusées, routes non signées `/hello` et `/pair`, redirections du routeur, erreurs du serveur HTTP) sont échantillonnées (quelques lignes par seconde) ; les lignes perdues sont comptées par type dans une ligne récapitulative.
- Les valeurs venant du réseau sont bornées (chemin 512 octets, méthode 16 octets) ou validées (`clientId`) avant d'être journalisées ; en-têtes limités à 16 Kio.
- Au lancement, un journal de plus de 5 Mio est renommé en `ReminisLink.old.txt` (l'ancien est remplacé) ; au-delà de 20 Mio pendant une session, seules les lignes des requêtes acceptées et les erreurs du pak sont encore écrites.

## 9. Mode desktop

`reminislink --desktop --sd ./testdata/sdcard` lance le pak sur Mac ou Linux, avec la même UI dans une fenêtre et une carte SD factice. `--headless` lance seulement le serveur et la découverte, sans UI ni SDL. Ces deux modes permettent de développer et tester l'app iOS sans console (voir [08](08-tests-qualite.md)).
