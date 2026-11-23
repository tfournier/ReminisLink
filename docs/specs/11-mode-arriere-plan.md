# 11 — ReminisLink pendant les jeux (mode arrière-plan)

Statut : **projet, à valider** (23/11/2026). Jalon M24 ([09](09-feuille-de-route.md)). Rien n'est encore implémenté.

Les références au code de NextUI renvoient à `LoveRetro/NextUI`, branche `main`, commit `b6ec1343` (le même que [05](05-console-pak.md)). Chemins relatifs à `workspace/all/minarch/` sauf mention contraire ; `launch.sh` désigne `skeleton/SYSTEM/tg5040/paks/MinUI.pak/launch.sh` et `bin/` désigne `skeleton/SYSTEM/tg5040/bin/`.

## 1. Pourquoi

Aujourd'hui le pak ne tourne qu'au premier plan, quand aucun jeu ne tourne (01, 05 §2). Pour changer un code de triche pendant une partie, il faut quitter le jeu, ouvrir ReminisLink sur la console, modifier depuis le téléphone, quitter ReminisLink et relancer le jeu, et recommencer à chaque essai (retour de la personne, 23/11/2026).

Le mode arrière-plan garde le serveur disponible pendant le jeu et dans le menu de NextUI : le téléphone peut agir sans qu'on ouvre ReminisLink sur la console.

**Limite, à dire dans l'app.** minarch ne lit le fichier `.cht` qu'au chargement du jeu (`Cheats_load()` appelé seulement par `Core_load()`, `ma_core.c:143-144`, lui-même appelé une fois, `minarch.c:208`). Son menu *Cheats* active ou désactive les codes **déjà chargés** (`ma_frontend_opts.c:476-540`, liste construite une fois puis gardée) ; *Reset* ne relit rien (`ma_core.c:152-155`). Un code enregistré pendant le jeu s'applique donc **au prochain lancement du jeu**. Le parcours devient : modifier sur le téléphone, *Menu › Quitter* (ou sauvegarde rapide puis Quitter), relancer depuis *Récents*. L'app affiche « Relance le jeu pour appliquer le changement. »

## 2. Activation

- Réglage du pak : **Réglages › Disponible pendant les jeux**, **désactivé par défaut**. La valeur est gardée dans la configuration du pak (05 §4).
- **Activer** installe trois scripts dans `$USERDATA_PATH/.hooks/` (système de *hooks* de NextUI, `HOOKS.md`, lanceur `bin/run_hooks.sh`) :
  - `boot.d/reminislink.sh` : au démarrage de la console (`launch.sh:155`), lance le serveur d'arrière-plan **détaché** (`setsid`, entrées et sorties redirigées) ; le lanceur attend la fin de ses scripts (`run_hooks.sh:28-36`), le script doit donc rendre la main tout de suite ;
  - `pre-launch.d/reminislink.sync.sh` : avant chaque lancement (`launch.sh:193`), en mode synchrone (suffixe `.sync.sh`), annonce au serveur ce qui démarre (§4) ;
  - `post-launch.d/reminislink.sh` : après chaque lancement (`launch.sh:195`), annonce la fin, déclenche la maintenance du jeu quitté (§6) et relance le serveur s'il est mort ou si son binaire a changé (mise à jour du pak par le Pak Store).
- `auto.sh` n'est **jamais** modifié : il est partagé entre paks et `HOOKS.md:72` recommande les hooks.
- Chaque script commence par vérifier que `ReminisLink.pak` existe toujours ; sinon il se supprime et sort (pak désinstallé sans désactiver le réglage).
- **Désactiver** retire les trois scripts et arrête le serveur d'arrière-plan.
- Les scripts sont écrits de façon atomique (`.tmp`, `fsync`, renommage), comme tout fichier écrit par le pak. Le dossier `.hooks` est hors de `/.system/` (05 §1).
- Le serveur d'arrière-plan démarre au prochain démarrage de la console, ou en quittant ReminisLink juste après l'activation (§3).

## 3. Un seul serveur à la fois

Le même binaire tourne soit au premier plan (pak ouvert, écran gabagool), soit en arrière-plan (`--background`, sans écran). Jamais les deux : ils partagent l'identité de la console, le certificat TLS, `clients.json`, la corbeille et le port.

- **Verrou** : chaque mode prend un verrou exclusif (`flock`) sur `/tmp/reminislink.lock` (tmpfs) avant d'ouvrir quoi que ce soit, et écrit son PID dans `/tmp/reminislink.pid`. Un second processus qui ne l'obtient pas sort aussitôt (arrière-plan) ou passe par la relève (premier plan).
- **Relève à l'ouverture du pak** : `launch.sh` envoie `SIGTERM` au serveur d'arrière-plan, attend qu'il libère le verrou (12 s au plus : sa fermeture attend déjà les écritures 10 s, 05 §2), puis lance le pak normalement. Passé ce délai, `SIGKILL` (les écritures sont atomiques, la reprise au lancement existe déjà).
- **Relève à la sortie du pak** : si le réglage est actif, `launch.sh` relance le serveur d'arrière-plan détaché après la sortie du pak.
- **Pour les apps** : l'événement `shutdown` (M14) gagne un champ `restarting: true` quand la fermeture est une relève. L'app garde alors la console ouverte et se reconnecte d'elle-même (nouvel essai pendant 30 s au plus, Bonjour compris) au lieu de revenir à la liste. Une app plus ancienne ignore le champ et revient à la liste, comme aujourd'hui.
- 05 §2 (« le pak NE DOIT PAS laisser de processus en arrière-plan ») devient : sans le réglage, aucun processus ne reste ; avec le réglage, seul le serveur d'arrière-plan reste, et un seul.

## 4. Le serveur d'arrière-plan

- **Sans écran** : aucune fenêtre SDL, aucune entrée manette, aucune gestion du bouton d'alimentation (c'est minarch ou `nextui.elf` qui gère la veille).
- **Même API**, même identité, même authentification (07). **Pas d'appairage** : `pairingOpen` vaut toujours `false`, `POST /pair` répond `401 pairing_failed` sans compter d'échec. Un nouvel appareil s'appaire en ouvrant ReminisLink sur la console.
- **Démarrage** : contrôle Wi-Fi (05 §2), puis la maintenance complète de 05 §2 étape 3, puis serveur et Bonjour. Sans Wi-Fi, il attend une adresse (§7) au lieu de sortir.
- **Sobriété** (à confirmer par les mesures, §10) :
  - `GOMAXPROCS=1` et `GOMEMLIMIT` (32 Mio de départ) ;
  - priorité basse : `nice 10` et classe d'entrée-sortie *idle* (`ionice -c3`) pour les écritures en arrière-plan (présence de `ionice` dans le BusyBox du firmware **[À VÉRIFIER]**, sinon `ioprio_set` depuis Go) ;
  - **aucun travail périodique pendant un jeu** : pas de purge, pas de relevé de batterie non demandé, pas de minuterie plus fréquente que celles de Bonjour et des flux d'événements ouverts ;
  - minarch ne fixe aucune affinité CPU (aucun `sched_setaffinity`) et n'utilise surtout qu'un fil d'émulation : les autres cœurs devraient suffire.
- **Journal** : même fichier (`ReminisLink.txt`), avec le mode en tête de chaque démarrage.

## 5. Jeu en cours et règles d'écriture

### 5.1 Savoir ce qui tourne

- Le hook `pre-launch.d` reçoit `HOOK_TYPE` (jeu ou pak), `HOOK_ROM_PATH` et `HOOK_EMU_PATH` (`launch.sh:162-175`). Il écrit `/tmp/reminislink/running.json` (atomique) puis prévient le serveur (`SIGUSR1`) et attend son accusé (`/tmp/reminislink/ack`, 3 s au plus). Pendant ce délai le serveur laisse finir l'écriture en cours sur ce jeu (le verrou d'écriture de M13 les fait passer une à une) et refuse les suivantes ; passé 3 s le jeu démarre quand même (une écriture plus longue que 3 s sur ce jeu est un envoi en fin de vérification, rare).
- Quand le programme lancé est `ReminisLink.pak` lui-même, le hook ne fait rien : la relève du §3 s'en charge.
- Le hook `post-launch.d` efface ce fichier et prévient le serveur de la même façon.
- Le serveur résout `HOOK_ROM_PATH` en `gameId` par la bibliothèque. **[À VÉRIFIER]** : chemin reçu pour une ROM dans une archive (extraite dans `/tmp/nextarch/<TAG>`, `ma_game.c:175-177`), pour un jeu multi-disque (`.m3u` ou disque), pour un raccourci de M23 (`.shortcut`). Un chemin non résolu donne `runningGame` avec `gameId: null` et le système seul : toutes les écritures de ce système sont alors refusées tant qu'il tourne.
- **Au démarrage du serveur** (relance pendant un jeu) : `/tmp/next` existe tant qu'un jeu ou un pak tourne (`nextui.c:1086-1090`, effacé après `eval $CMD`, `launch.sh:190-196`) ; la commande a la forme `'<launch.sh de l'émulateur>' '<rom>'`. Le serveur s'en sert comme repli, en plus de `running.json`.

### 5.2 Pendant un jeu

| Fichiers | Lecture | Écriture |
|---|---|---|
| Les autres jeux, leurs saves, states, cheats ; ajouts de ROMs ; captures | Oui | Oui |
| Cheats du jeu en cours | Oui | Oui, avec la notice « Relance le jeu pour appliquer le changement. » |
| ROM, save, states, state de reprise, verrous, jaquette du jeu en cours ; le renommer, le supprimer, le patcher, restaurer ou importer une archive dans ce jeu | Oui | **Non** : `409 game_running` |
| Apps (paks) | Oui | Installation, mise à jour et suppression refusées si c'est l'émulateur du jeu en cours (`HOOK_EMU_PATH`) : `409 game_running` |
| Corbeille | Oui | Restaurer vers le jeu en cours : `409 game_running` ; le reste, oui |

Raison : minarch réécrit la save à l'ouverture de son menu, avant la veille et en quittant (`ma_menu.c:1795`, `ma_menu.c:225`, `ma_core.c:164`), et le state de reprise (emplacement 9) avant la veille et à la sauvegarde rapide (`ma_menu.c:224-228`, `ma_frontend_opts.c:472`) : un fichier remplacé pendant le jeu serait écrasé sans prévenir. Rien n'est mis en attente : l'app explique et l'utilisateur recommence après la partie.

**Lire la save du jeu en cours** (téléchargement, sync des saves de l'app) : permis, avec un contrôle. Le serveur relève taille et date du fichier avant et après la lecture et recommence (3 fois au plus) s'ils ont changé ; sinon `409 game_running`. La save n'est à jour que depuis la dernière ouverture du menu de minarch (elle n'est pas écrite périodiquement) : l'app le dit sur la fiche du jeu en cours.

Les envois en cours continuent pendant un jeu (zone d'attente, priorité basse) ; seule leur fin (`complete`) est refusée si elle vise le jeu en cours.

### 5.3 Pendant un autre pak

Quand `HOOK_TYPE` indique un pak (Pak Store, Cheat Manager, gestionnaire de fichiers…), ce pak peut écrire n'importe où sur la carte. **Toutes les écritures** sont refusées avec `409 console_busy` jusqu'à sa sortie ; les lectures restent permises. Le lancement de ReminisLink lui-même passe par la relève (§3), pas par cette règle.

### 5.4 Dans le menu de NextUI

Aucun jeu ni pak : tout est permis, comme avec le pak au premier plan.

## 6. Maintenance

- **Au démarrage** du serveur d'arrière-plan : la maintenance complète (05 §2 étape 3).
- **À la sortie d'un jeu** (hook `post-launch.d`) : seulement pour ce jeu, archivage de son state de reprise s'il a changé (05 §5.3) et contrôle de ses verrous (05 §5.2). Avec le mode arrière-plan, l'historique du state de reprise est donc tenu après chaque partie, et plus seulement à l'ouverture de ReminisLink.
- **Purge** (corbeille, envois expirés, zone d'attente) : au démarrage, puis une fois par jour au plus, jamais pendant un jeu.

## 7. Veille et réseau

- La veille de NextUI a deux étapes. D'abord une veille légère (écran éteint, son en pause, `keymon`, `batmon` et `audiomon` arrêtés par `SIGSTOP`, `api.c:4341`, `4367-4369`) : le Wi-Fi reste actif et le serveur reste joignable. Après `suspendTimeout` (30 s par défaut, `config.h:231`, boucle `api.c:4406-4447`), veille profonde par `bin/suspend` : hooks `pre-sleep.d` synchrones, arrêt de `wpa_supplicant`, `echo mem` ; au réveil, Wi-Fi relancé en arrière-plan et hooks `post-resume.d` (`bin/suspend:16-59`).
- Le serveur survit à la veille (processus figé). Au réveil, l'adresse peut changer (DHCP) : il surveille l'adresse de l'interface sans fil (toutes les 5 s, seulement tant qu'elle est absente ou vient de changer) et refait l'annonce Bonjour avec la nouvelle. Un hook `post-resume.d` n'est pas nécessaire ; à ajouter si la surveillance ne suffit pas.
- Les envois et flux d'événements coupés par la veille reprennent comme aujourd'hui (03 §7, M14).
- Le Wi-Fi n'est pas coupé au lancement d'un jeu (minarch s'en sert lui-même pour RetroAchievements, `ra_integration.c:1760, 1922`).

## 8. Contrat (`protocol/openapi.yaml` d'abord)

- `GET /hello` et `GET /device` : nouveaux champs `mode` (`foreground` ou `background`) et `runningGame` (`null`, ou `{ kind: game|pak, gameId, systemTag, name }`, `gameId` pouvant être `null`, §5.1).
- Nouvel événement `running` (même contenu que `runningGame`) envoyé aux flux ouverts au début et à la fin d'un jeu ou d'un pak.
- `EventShutdown` : champ optionnel `restarting` (booléen, §3).
- Erreurs (03 §6) : `409 game_running` (`details.gameId`) et `409 console_busy` (`details.pakName`).
- Compatibilité : les apps iOS et Android traitent déjà un code inconnu comme une erreur générique (`ErrorCode.other` dans `APIError.swift`, `error_generic` dans `ui/Errors.kt`) et ignorent les champs inconnus : changement **MINOR** du pak. **[À VÉRIFIER]** à l'implémentation : le décodage de `EventShutdown` et de `GET /hello` tolère le champ ajouté sur les versions publiées des deux apps.

## 9. Apps iOS et Android (parité)

- Tableau de bord : bandeau « En jeu : *nom du jeu* » (ou « *nom du pak* ouvert sur la console »), mis à jour par l'événement `running`.
- Fiche du jeu en cours : actions d'écriture désactivées avec la raison (« Ce jeu tourne sur la console. Quitte-le pour modifier sa sauvegarde. ») ; mention que la save date de la dernière ouverture du menu.
- Cheats du jeu en cours : enregistrement permis, puis la notice « Relance le jeu pour appliquer le changement. »
- Messages traduits pour `game_running` et `console_busy`.
- Relève (`restarting: true`) : reconnexion silencieuse, sans retour à la liste.
- Aucun réglage dans l'app : le mode se règle sur la console.

## 10. Mesures et critères d'acceptation

Sur une TrimUI Brick (tg5040), serveur d'arrière-plan actif, aucune app connectée puis une app connectée avec son flux d'événements :

- mémoire résidente du serveur au repos : < 30 Mio ;
- CPU au repos pendant un jeu : < 1 % en moyenne sur 10 min ;
- batterie : écart mesuré sur 1 h de jeu avec et sans le serveur, publié dans la spec (pas de seuil fixé d'avance) ;
- aucune image perdue de plus qu'à vide sur un cœur exigeant (compteur d'images de minarch), avec et sans le serveur ;
- reprise après veille profonde : console retrouvée par l'app en moins de 30 s après le réveil ;
- les règles du §5 couvertes par des tests Go (hooks simulés, mode desktop) et par l'émulation de la CI.

## 11. Plateformes

- **tg5040** d'abord (Brick, Smart Pro) : les hooks et les chemins ci-dessus.
- **tg5050, h700, my355** : mêmes hooks attendus **[À VÉRIFIER]** dans leurs `launch.sh` et `bin/suspend`.
- **Knulli** : hors du périmètre de M24 (autre lanceur, pas de ces hooks).

## 12. Sécurité (revue de 07)

- **Exposition** : réglage actif, le service écoute sur le Wi-Fi tant que la console est allumée (et non quelques minutes). Les mesures de 07 §3 s'appliquent sans changement : toutes les routes métier signées, TLS épinglé, appairage **fermé** en arrière-plan, `/hello` limité par IP, corps bornés.
- **Annonce Bonjour** : le nom de la console est visible en permanence sur les réseaux où elle se connecte, réseaux publics compris (décision 1, §13).
- **Rejeu** : le cache de nonces vit tant que le serveur vit ; le risque résiduel de 07 (relance moins de 300 s après une requête capturée) devient plus rare.
- **Hooks** : scripts écrits par le pak dans `.userdata`, lancés en root par NextUI ; même niveau de confiance que le pak lui-même. Ils n'exécutent que le binaire du pak, jamais une valeur venue du réseau ou de `HOOK_*` (ces valeurs ne sont lues que comme des données).
- 07 §2 (« Le pak n'écoute que lorsqu'il est ouvert ») est complété par ce mode.

## 13. Décisions à prendre

| # | Question | Recommandation |
|---|---|---|
| 1 | Sur quels réseaux le serveur d'arrière-plan écoute-t-il ? Tous, ou seulement les réseaux Wi-Fi mémorisés à l'activation | **Tous** : plus simple, la sécurité repose sur les signatures ; seul le nom de la console est visible sur un réseau public |
| 2 | Pendant un autre pak (Pak Store, Cheat Manager…) | **Refuser toutes les écritures** (`console_busy`), lectures permises |
| 3 | Télécharger la save du jeu en cours | **Permis**, avec le contrôle avant et après la lecture et la mention de sa date |
| 4 | Réglage par défaut | **Désactivé** : l'utilisateur l'active en connaissance de cause (batterie) |
