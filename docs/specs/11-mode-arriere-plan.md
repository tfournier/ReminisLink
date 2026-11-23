# 11 — ReminisLink pendant les jeux (mode arrière-plan)

Statut : **projet, à valider** (23/11/2026). Jalon M24 ([09](09-feuille-de-route.md)). Rien n'est encore implémenté.

Les références au code de NextUI renvoient à `LoveRetro/NextUI`, branche `main`, commit `b6ec1343` (le même que [05](05-console-pak.md)). Abréviations : `minarch/` = `workspace/all/minarch/`, `common/` = `workspace/all/common/`, `nextui.c` = `workspace/all/nextui/nextui.c`, `launch.sh` = `skeleton/SYSTEM/tg5040/paks/MinUI.pak/launch.sh`, `bin/` = `skeleton/SYSTEM/tg5040/bin/`.

## 1. Pourquoi

Aujourd'hui le pak ne tourne qu'au premier plan, quand aucun jeu ne tourne (01, 05 §2). Pour changer un code de triche pendant une partie, il faut quitter le jeu, ouvrir ReminisLink sur la console, modifier depuis le téléphone, quitter ReminisLink et relancer le jeu, à chaque essai (retour de la personne, 23/11/2026).

Le mode arrière-plan garde le serveur disponible pendant le jeu et dans le menu de NextUI : le téléphone agit sans qu'on ouvre ReminisLink sur la console.

**Limite, à dire dans l'app.** minarch ne lit le fichier `.cht` qu'au chargement du jeu (`Cheats_load()` appelé seulement par `Core_load()`, `minarch/ma_core.c:143-144`, lui-même appelé une fois, `minarch/minarch.c:208`). Son menu *Cheats* active ou désactive les codes **déjà chargés** (`minarch/ma_frontend_opts.c:476-540`, liste construite une fois puis gardée) ; *Reset* ne relit rien (`minarch/ma_core.c:152-155`). Un code enregistré pendant le jeu s'applique donc **au prochain lancement du jeu**. Le parcours devient : modifier sur le téléphone, *Menu › Quitter*, relancer depuis *Récents* (la reprise automatique ramène la partie). L'app affiche « Relance le jeu pour appliquer le changement. »

## 2. Principe de sûreté : n'écrire que quand le menu de NextUI est ouvert

Le lanceur de NextUI est une boucle : il lance `nextui.elf` (le menu), qui écrit la commande choisie dans `/tmp/next` et **se termine** (`nextui.c:1086-1090`) ; le lanceur exécute alors les hooks `pre-launch.d`, la commande (jeu ou pak), les hooks `post-launch.d`, efface `/tmp/next` et relance `nextui.elf` (`launch.sh:185-199`).

D'où la règle de M24 : **la console est « libre » seulement tant qu'un processus `nextui.elf` tourne** (lu dans `/proc`). Libre, tout est permis comme avec le pak au premier plan. Sinon (jeu, autre pak, lancement ou extinction en cours) la console est **occupée** et seules les écritures sûres pendant un jeu sont permises (§5).

Cette règle ne dépend pas des hooks : NextUI ignore leurs erreurs et un hook `pre-launch.d` ne peut pas empêcher un lancement (`HOOKS.md:68-71`, `bin/run_hooks.sh:28-36`). Les hooks servent seulement à aller plus vite et à nommer ce qui tourne (§4).

- L'état est relu **sous le verrou d'écriture**, juste avant chaque étape qui modifie la carte (chaque renommage final, chaque fichier d'une opération en plusieurs fichiers). Une opération qui trouve la console occupée en cours de route s'arrête avant l'étape suivante et se défait ou se reprend par son journal quand la console redevient libre (comme après une coupure de courant : corbeille, renommages, envois).
- **Risque résiduel**, à accepter (décision 3, §13) : une étape commencée juste avant la sortie de `nextui.elf` peut se terminer pendant le chargement du jeu. Le hook `pre-launch.d` synchrone le réduit : il ferme l'admission et attend la fin de l'étape en cours (15 s au plus, plus que les 10 s d'attente du verrou d'écriture, 03 §6) avant de rendre la main.

## 3. Activation et hooks

- Réglage du pak : **Réglages › Disponible pendant les jeux**, **désactivé par défaut**. Une seule source de vérité : la configuration du pak (05 §4), relue par chaque hook et par le serveur à son démarrage.
- **Activer** installe trois scripts dans `$USERDATA_PATH/.hooks/` (système de hooks de NextUI, `HOOKS.md`) :
  - `boot.d/reminislink.sh` : au démarrage (`launch.sh:155`), lance le serveur d'arrière-plan **détaché** (`setsid`, entrées et sorties redirigées) et rend la main tout de suite (le lanceur attend ses scripts, `bin/run_hooks.sh:28-36`) ;
  - `pre-launch.d/reminislink.sync.sh` : synchrone, avant chaque lancement (`launch.sh:193`) ; §2 et §4 ;
  - `post-launch.d/reminislink.sh` : après chaque lancement (`launch.sh:195`) ; prévient le serveur, et le relance s'il ne tourne pas ou si son binaire a changé (mise à jour du pak par le Pak Store).
- `auto.sh` n'est **jamais** modifié (partagé entre paks ; `HOOKS.md:72` recommande les hooks).
- Chaque script porte une ligne marqueur (`# ReminisLink hook v1`) et sort sans rien faire si le réglage est désactivé ou si `ReminisLink.pak` n'existe plus. Il ne se supprime pas lui-même : seul le pak supprime des hooks, et seulement ceux qui portent son marqueur ; un fichier du même nom sans marqueur n'est ni remplacé ni supprimé (l'activation échoue avec un message).
- **Désactiver** écrit d'abord le réglage, puis arrête le serveur, puis retire les scripts. Un retrait interrompu laisse des scripts inertes (ils lisent le réglage) ; le pak les retire à son lancement suivant. Une installation interrompue est reprise de même.
- Écriture atomique (`.tmp`, `fsync`, renommage). Le dossier `.hooks` est hors de `/.system/` (05 §1).
- **[À VÉRIFIER]** : le lanceur exécute les scripts directement (`( "$script" )`, `bin/run_hooks.sh:31-33`) ; sur la carte exFAT, le droit d'exécution vient des options de montage (pas de `chmod`, règle 4 de CLAUDE.md). Si l'exécution échoue, rien ne casse (la règle du §2 tient seule) mais le serveur ne démarre pas au démarrage : à vérifier sur la console avant de proposer le réglage.

## 4. Ce qui tourne

- `HOOK_TYPE` vaut `rom` ou `pak` ; `HOOK_EMU_PATH` et `HOOK_ROM_PATH` sont tirés de la commande par `sed` (`launch.sh:162-175`).
- **Source principale : `/proc`.** Pendant un jeu, `minarch.elf` tourne avec le cœur puis le chemin de la ROM en arguments (`skeleton/SYSTEM/tg5040/paks/Emus/GB.pak/launch.sh:14`) ; le serveur lit `/proc/<pid>/cmdline` (arguments exacts, sans les échappements de la commande). Les variables `HOOK_*` ne servent que d'indication : une apostrophe échappée par `escapeSingleQuotes()` (`nextui.c:1323-1328`) n'est pas décodée par le `sed` du lanceur.
- **Fichiers du jeu en cours** : résolus et confinés comme toute requête (règle 2), sans la casse (exFAT), puis étendus à **tout** son ensemble : ROM, archive d'origine (une ROM d'un `.zip` est extraite dans `/tmp/nextarch/<TAG>` par minarch, `minarch/ma_game.c:175-177`), tous les disques d'un jeu multi-disque (NextUI peut lancer le disque de la reprise, `nextui.c:1295-1328`), la cible d'un raccourci (M23), save, `.rtc`, states, state de reprise, verrous et copies. **[À VÉRIFIER]** sur la console : le chemin reçu dans chacun de ces cas.
- **Incertain = tout protégé** : si la ROM ne se résout pas en un jeu, ou si ce qui tourne n'est pas `minarch.elf` (un pak, un raccourci d'outil lancé par `SHORTCUT.pak` alors que `HOOK_TYPE` vaut `rom`), la console est traitée comme occupée par un pak (§5.2).
- `/tmp/next` n'est jamais exécuté ni pris pour l'état réel : il existe avant le lancement et jusqu'après les hooks `post-launch.d` (`launch.sh:190-196`).
- Les hooks préviennent le serveur par un point de contrôle local (socket Unix dans un dossier `/tmp/reminislink/` créé par le serveur, accessible au seul root), avec un numéro de lancement : le serveur accuse réception de **ce** numéro. Les fichiers de contrôle, les hooks et le binaire du pak sont hors d'atteinte des routes d'écriture de la carte (05 §3.4).

## 5. Règles pendant que la console est occupée

### 5.1 Pendant un jeu

| Action | Pendant un jeu |
|---|---|
| Lectures : bibliothèque, fiches, cheats, captures, état de la console | Oui |
| Lire la save, les states, le state de reprise du jeu en cours, ou une archive qui les contient (export M19, navigateur de la carte) | **Non** (`409 game_running`) : minarch réécrit la save sur place (`minarch/ma_saves.c:75-105`) à l'ouverture du menu, avant la veille et en quittant (`minarch/ma_menu.c:1795`, `minarch/ma_menu.c:225`, `minarch/ma_core.c:164`) ; une lecture pendant cette écriture rendrait un fichier mélangé, et une fois l'envoi de la réponse commencé on ne peut plus répondre par une erreur |
| Lire la save ou les states des **autres** jeux | Oui |
| Enregistrer les cheats de **n'importe quel** jeu, y compris le jeu en cours | Oui (écriture atomique ; minarch ne lit le `.cht` qu'au chargement). Pour le jeu en cours, la réponse porte `appliesOnNextLaunch: true` et l'app affiche la notice du §1 |
| Envoyer un nouveau fichier (ROM, fichier de la carte) dont la destination **n'existe pas** | Oui ; les morceaux vont dans la zone d'attente, priorité basse ; la fin (`complete`) est refusée (`409 game_running`) si elle remplacerait un fichier |
| Toute autre écriture (remplacer, restaurer, supprimer, renommer, patcher, importer, installer une app, vider ou restaurer la corbeille, verrous) | **Non** (`409 game_running`), y compris pour les autres jeux dans cette première version (décision 2, §13) |

### 5.2 Pendant un autre pak, ou si ce qui tourne est incertain

Un pak (Pak Store, Cheat Manager, gestionnaire de fichiers…) peut écrire n'importe où, `.cht` compris. Lectures permises sauf saves, states et archives qui en contiennent ; **toutes les écritures** refusées (`409 console_busy`). Les envois en cours continuent dans la zone d'attente ; leur fin attend que la console redevienne libre.

### 5.3 Tâches et envois

- Une tâche (patch, installation) refusée ne démarre pas ; une tâche commencée quand la console était libre suit la règle des étapes du §2.
- Les envois sont repris après une relève ou une veille comme aujourd'hui (03 §7). Les **tâches**, elles, sont annulées à la fermeture du serveur (comportement actuel) : l'app l'affiche comme un échec à relancer.

## 6. Un seul serveur à la fois (relève)

Le même binaire tourne soit au premier plan (pak ouvert, écran gabagool), soit en arrière-plan (`--background`, sans écran), jamais les deux : ils partagent l'identité de la console, le certificat TLS, `clients.json`, la corbeille et le port.

- **Verrou** : un verrou exclusif (`flock`) sur `/tmp/reminislink/lock` est la seule preuve de propriété. Aucun processus n'ouvre le serveur ni n'écrit sur la carte sans lui. Le PID n'est qu'une indication : avant tout signal, le pak vérifie par `/proc/<pid>/exe` que c'est bien son binaire.
- **Ouverture du pak** : `launch.sh` demande la fermeture au serveur d'arrière-plan par le point de contrôle (§4), puis attend le verrou (20 s au plus). Passé ce délai, `SIGKILL`, puis attente du verrou. Le nouveau propriétaire reprend d'abord les opérations laissées en cours (journaux de renommage, corbeille, envois) avant d'accepter des écritures, comme après une coupure.
- **Sortie du pak** : si le réglage est actif, `launch.sh` relance le serveur d'arrière-plan détaché, qui attend lui aussi le verrou.
- **Rejeu** : à la relève, le cache de nonces est passé au successeur (fichier dans `/tmp/reminislink/`, mémoire vive, jamais sur la carte) ; le risque résiduel de 07 reste donc le même. Au démarrage de la console, le cache est vide comme aujourd'hui.
- **Pour les apps** : l'événement `shutdown` (M14) gagne un champ optionnel `restarting: true` pour une relève ; `reason` garde ses valeurs actuelles. L'app garde la console ouverte et se reconnecte d'elle-même (30 s au plus, Bonjour compris). Une app plus ancienne ignore le champ et revient à la liste, comme aujourd'hui.

## 7. Le serveur d'arrière-plan

- **Sans écran** : aucune fenêtre SDL, aucune entrée manette, aucune gestion du bouton d'alimentation (minarch ou `nextui.elf` gèrent la veille).
- **Même API**, même identité, même authentification (07). **Pas d'appairage** : `pairingOpen` vaut toujours `false`, `POST /pair` répond `401 pairing_failed` sans compter d'échec. Un nouvel appareil s'appaire en ouvrant ReminisLink sur la console.
- **Sobriété** (à confirmer par les mesures, §10) : `GOMAXPROCS=1`, `GOMEMLIMIT` (32 Mio de départ), `nice 10`, classe d'entrée-sortie *idle* pour les écritures (`ioprio_set` depuis Go) ; aucune minuterie propre pendant que la console est occupée, à part la surveillance du réseau (§8).
- **Journal** : même fichier (`ReminisLink.txt`), avec le mode à chaque démarrage.

### 7.1 Maintenance

- La maintenance du lancement (05 §2 étape 3 : archivage du state de reprise, contrôle et restauration des verrous, purges) ne tourne **que quand la console est libre**. Un serveur démarré pendant un jeu (relance par `post-launch.d`, ou démarrage de la console avec reprise automatique d'un jeu) l'attend.
- **À la sortie d'un jeu** : l'archivage de son state de reprise (05 §5.3) et le contrôle de ses verrous (05 §5.2) passent **avant** toute écriture admise sur ce jeu. Avec le mode arrière-plan, l'historique du state de reprise est donc tenu après chaque partie.
- Purges (corbeille, envois expirés, zone d'attente) : au démarrage puis une fois par jour au plus, console libre seulement.

## 8. Veille, extinction et réseau

- La veille de NextUI a deux étapes (`common/api.c:4341` et suivantes, appelée par `common/api.c:4469-4492`). D'abord une veille légère : écran éteint, son en pause, `keymon`, `batmon` et `audiomon` arrêtés (`common/api.c:4367-4369`) ; le Wi-Fi reste actif et le serveur joignable. Après `suspendTimeout` (30 s par défaut, `common/config.h:231` ; boucle `common/api.c:4406-4447` ; autrement en charge, sur USB ou sans veille profonde), veille profonde par `bin/suspend` : hooks `pre-sleep.d` synchrones, arrêt de `wpa_supplicant`, `echo mem` (`bin/suspend:61-69`). Au réveil, les hooks `post-resume.d` partent en arrière-plan, puis le Wi-Fi est relancé (`bin/suspend:40-50`). Une veille profonde qui échoue peut finir en extinction.
- **Avant la veille profonde** : un hook `pre-sleep.d/reminislink.sync.sh` ferme l'admission des écritures et attend l'étape en cours (5 s au plus). Le processus survit à la veille, pas ses connexions.
- **Au réveil** : un hook `post-resume.d/reminislink.sh` prévient le serveur ; en plus, le serveur relit l'adresse de l'interface sans fil toutes les 10 s (lecture locale, sans réseau) et, à tout changement d'adresse ou de lien, rouvre son écoute si besoin et refait l'annonce Bonjour (nouvel essai borné). Les apps relisent `GET /device` à chaque reconnexion de leur flux d'événements.
- **Extinction** pendant une écriture : écritures atomiques et reprise par les journaux au démarrage suivant, comme aujourd'hui.
- Le lanceur ne coupe pas le Wi-Fi au lancement d'un jeu (seul `bin/suspend` arrête `wpa_supplicant`) **[À VÉRIFIER]** sur la console : lien gardé pendant une longue partie.

## 9. Contrat et apps

**Contrat** (`protocol/openapi.yaml` d'abord) :

- `GET /hello` (non signé) : seulement `mode` (`foreground` ou `background`), optionnel. **Rien sur le jeu en cours** (aucune donnée de jeu dans `/hello`).
- `GET /device` (signé) : `mode` et `activity`, tous deux optionnels : `activity` vaut `{ state: free | game | pak | busy, gameId?, systemTag?, name? }` (`busy` : lancement, extinction ou cible incertaine).
- Nouvel événement `activity` (même contenu) dans le flux signé à chaque changement.
- `EventShutdown` : champ optionnel `restarting` (§6). Champs obligatoires existants inchangés.
- Erreurs (03 §6) : `409 game_running` (`details.gameId`, éventuellement `null`) et `409 console_busy` (`details.state`).
- Réponse d'enregistrement des cheats : `appliesOnNextLaunch` optionnel.
- Compatibilité : `ios-v1.1.0` traite un code inconnu comme une erreur générique (`ErrorCode.other`, `APIError.swift`), ignore les clés et événements inconnus ; l'app Android fait de même dans le code (`ui/Errors.kt`), à vérifier sur la première version publiée. **MINOR** si les apps publiées passent les essais de relève et de veille sans régression (à faire avant de fixer le numéro).

**Apps iOS et Android** (parité) :

- Tableau de bord : bandeau « En jeu : *nom du jeu* » ou « *Nom du pak* ouvert sur la console ».
- Pendant un jeu : actions refusées désactivées avec la raison (« La console est en jeu. Quitte le jeu pour modifier la carte. ») ; save et states du jeu en cours non téléchargeables (« Disponible quand le jeu sera fermé »). La sync des saves saute le jeu en cours et reprend à l'événement `activity` suivant.
- Cheats : enregistrement permis, puis la notice du §1 pour le jeu en cours.
- Messages traduits pour `game_running` et `console_busy`. Relève : reconnexion silencieuse.
- Aucun réglage dans l'app : le mode se règle sur la console.

## 10. Mesures et critères d'acceptation

Sur une TrimUI Brick (tg5040), serveur d'arrière-plan actif, sans app puis avec une app connectée et son flux d'événements :

- mémoire résidente du serveur : < 30 Mio ;
- CPU pendant un jeu : < 1 % en moyenne sur 10 min ;
- batterie : écart mesuré sur 1 h de jeu avec et sans le serveur, publié dans la spec ;
- aucune image perdue de plus qu'à vide sur un cœur exigeant, rembobinage activé (minarch a aussi des fils pour le rembobinage, la préparation des images et RetroAchievements) ;
- après une veille profonde, console retrouvée par l'app en moins de 30 s ;
- règles du §5 et relève du §6 couvertes par des tests Go (processus `nextui.elf` et `minarch.elf` simulés, mode desktop) et par l'émulation de la CI.

## 11. Plateformes

- **tg5040** d'abord (Brick, Smart Pro).
- **tg5050, h700, my355** : mêmes hooks et même boucle du lanceur attendus **[À VÉRIFIER]** dans leurs `launch.sh` et `bin/suspend`.
- **Knulli** : hors de M24 (autre lanceur, pas ces hooks).

## 12. Sécurité (revue de 07)

- **Exposition** : réglage actif, le service écoute sur le Wi-Fi tant que la console est allumée, et non quelques minutes. Les mesures de 07 §3 restent : routes métier signées, TLS épinglé, appairage **fermé** en arrière-plan, `/hello` limité par IP, corps bornés. Le serveur n'écoute que sur l'adresse de l'interface sans fil (pas `0.0.0.0`).
- **Ce qu'un inconnu du réseau voit** : par Bonjour et `/hello`, le nom, l'identifiant et la version de la console, en permanence et sur tous les réseaux où elle se connecte (décision 1, §13). Rien sur les jeux.
- **Rejeu** : inchangé grâce au passage du cache de nonces (§6).
- **Hooks** : lancés en root par NextUI, même niveau de confiance que le pak. Ils n'exécutent que le binaire du pak, ne passent les valeurs `HOOK_*` qu'en données, et ne sont pas modifiables par l'API.
- 07 §2 (« Le pak n'écoute que lorsqu'il est ouvert ») est complété par ce mode.

## 13. Décisions à prendre

| # | Question | Recommandation |
|---|---|---|
| 1 | Sur quels réseaux le serveur d'arrière-plan écoute-t-il ? Tous, ou seulement les réseaux Wi-Fi mémorisés à l'activation | **Tous** : la sécurité repose sur les signatures ; un réseau public ne voit que le nom et la version de la console |
| 2 | Pendant un jeu, quelles écritures ? Cheats et nouveaux fichiers seulement, ou aussi les autres jeux | **Cheats et nouveaux fichiers** pour commencer : c'est le besoin, et c'est sûr même si le jeu en cours est mal identifié ; élargir ensuite si besoin |
| 3 | Risque résiduel : une étape d'écriture commencée juste avant un lancement peut finir pendant le chargement du jeu (réduit par le hook, §2) | **L'accepter** : une étape est un renommage ou un petit fichier, et tout remplacement garde sa copie dans la corbeille |
| 4 | Save du jeu en cours | **Non téléchargeable** pendant le jeu (plus tard, peut-être une copie vérifiée prise à la sortie du jeu) |
| 5 | Réglage par défaut | **Désactivé** : l'utilisateur l'active en connaissance de cause (batterie) |
