# ReminisLink app changelog

Changes of the iOS app, one section per App Store version, newest first.
ReminisLink on the handheld (the pak) has its own changelog in `pak.json`,
and its own version numbers.

Each section has an English and a French part: they are the App Store
"What's New" texts. The first section is copied to
`AppStore/en-US/release_notes.txt` and `AppStore/fr-FR/release_notes.txt` by
`ci_scripts/release_notes.sh sync` (the CI checks they are in sync); lines
starting with "- " become "• " there. Keep each part under 4,000 characters,
and use the words of the app's French interface (« sauvegardes », « états »,
« codes de triche », « patch »).

Releasing (see `README.md`, "Release"): rename `## Unreleased` to the
version, run `ci_scripts/release_notes.sh sync`, commit, then tag
`ios-vX.Y.Z` (or `make ios-vX.Y.Z`). A pre-release (`ios-beta-vX.Y.Z-beta.N`, from `make ios-vX.Y.Z-beta.N`)
goes to TestFlight only and needs no section of its own.

## Unreleased

### English

New:
- Restore from the trash: tap an entry of the Trash tab to see what comes back, then restore it. A deleted game comes back with its save, states, cheats and box art; a removed app or a replaced file comes back too. A file already on the SD card is never replaced: restore under a free name instead, or, for a replaced save, replace the current save (kept first).

Improvements:
- After a patch is applied, the patch screen closes by itself.
- The library no longer waits for the box art of a game scrolled out of view.
- The French interface now uses plain French words throughout.
- When this app and ReminisLink on the handheld are too far apart in version, the app says which one to update instead of showing an error.

### Français

Nouveautés :
- Restaurer depuis la corbeille : touche une entrée de l'onglet Corbeille pour voir ce qui revient, puis restaure-la. Un jeu supprimé revient avec sa sauvegarde, ses états, ses codes de triche et sa jaquette ; une app supprimée ou un fichier remplacé revient aussi. Un fichier déjà sur la carte SD n'est jamais remplacé : la restauration se fait alors sous un nom libre, ou, pour une sauvegarde remplacée, à la place de la sauvegarde actuelle (gardée d'abord).

Améliorations :
- Après l'application d'un patch, l'écran du patch se ferme tout seul.
- La bibliothèque n'attend plus la jaquette d'un jeu sorti de l'écran.
- L'interface en français n'emploie plus que des mots français.
- Si l'app et ReminisLink sur la console ont des versions trop éloignées, l'app indique laquelle mettre à jour au lieu d'afficher une erreur.

## 1.0.0

### English

First version of ReminisLink.
- Pair with your handheld by scanning its QR code
- Browse your library and add your own games, saves, patches and cheat files
- Back up and restore saves, lock save states, edit cheats and apply IPS, BPS and UPS patches
- Nothing is lost: deleted games go to the trash of the handheld, replaced saves get a backup copy

### Français

Première version de ReminisLink.
- Appaire ta console en scannant son QR code
- Parcours ta bibliothèque et ajoute tes propres jeux, sauvegardes, patchs et fichiers de codes de triche
- Copie et restaure tes sauvegardes, verrouille tes états de jeu, modifie tes codes de triche et applique des patchs IPS, BPS et UPS
- Rien ne se perd : les jeux supprimés vont dans la corbeille de la console, les sauvegardes remplacées gardent une copie de secours
