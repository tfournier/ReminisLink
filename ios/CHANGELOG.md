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
- iPad and Mac: the same app now runs on iPad (lists and games side by side on a wide screen, every orientation, Split View and Stage Manager) and on Macs with Apple silicon.
- Restore from the trash: tap an entry of the Trash tab to see what comes back, then restore it. A deleted game comes back with its save, states, cheats and box art; a removed app or a replaced file comes back too. A file already on the SD card is never replaced: restore under a free name instead, or, for a replaced save, replace the current save (kept first).
- Game archives: export everything of a game (save, clock file, states, cheats, box art, and the game file if you wish) as one file, and import it into the same handheld or another one. Whatever it replaces is kept first.
- Save states in iCloud Drive: states are now copied with the saves, with the same 10 versions per slot.
- Pairing on a Mac: scan the QR code with the Mac's camera, or import a picture of it; a handheld paired on your iPhone appears on your other Apple devices within seconds.

Improvements:
- After a patch is applied, the patch screen closes by itself.
- The library no longer waits for the box art of a game scrolled out of view.
- A game opens faster from a large library: the list is no longer filtered and sorted again each time you open a game, go back or open its save.
- The French interface now uses plain French words throughout.
- When this app and ReminisLink on the handheld are too far apart in version, the app says which one to update instead of showing an error.

Requires ReminisLink 1.1 on the handheld for the trash restore, the game archives and the states in iCloud Drive.

### Français

Nouveautés :
- iPad et Mac : la même app fonctionne maintenant sur iPad (listes et jeux côte à côte sur grand écran, toutes les orientations, Split View et Stage Manager) et sur les Mac à puce Apple.
- Restaurer depuis la corbeille : touche une entrée de l'onglet Corbeille pour voir ce qui revient, puis restaure-la. Un jeu supprimé revient avec sa sauvegarde, ses états, ses codes de triche et sa jaquette ; une app supprimée ou un fichier remplacé revient aussi. Un fichier déjà sur la carte SD n'est jamais remplacé : la restauration se fait alors sous un nom libre, ou, pour une sauvegarde remplacée, à la place de la sauvegarde actuelle (gardée d'abord).
- Archives de jeux : exporte tout ce qui concerne un jeu (sauvegarde, fichier d'horloge, états, codes de triche, jaquette, et le fichier du jeu si tu veux) en un seul fichier, puis importe-le sur la même console ou sur une autre. Ce qu'il remplace est gardé d'abord.
- États de jeu dans iCloud Drive : les états sont maintenant copiés avec les sauvegardes, avec les mêmes 10 versions par emplacement.
- Appairage sur Mac : scanne le QR code avec la caméra du Mac, ou importe une photo du QR code ; une console appairée sur ton iPhone apparaît sur tes autres appareils Apple en quelques secondes.

Améliorations :
- Après l'application d'un patch, l'écran du patch se ferme tout seul.
- La bibliothèque n'attend plus la jaquette d'un jeu sorti de l'écran.
- Un jeu s'ouvre plus vite dans une grande bibliothèque : la liste n'est plus filtrée et triée à nouveau chaque fois que tu ouvres un jeu, reviens en arrière ou ouvres sa sauvegarde.
- L'interface en français n'emploie plus que des mots français.
- Si l'app et ReminisLink sur la console ont des versions trop éloignées, l'app indique laquelle mettre à jour au lieu d'afficher une erreur.

La restauration depuis la corbeille, les archives de jeux et les états dans iCloud Drive demandent ReminisLink 1.1 sur la console.

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
