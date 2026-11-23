# 11 — ReminisLink pendant les jeux (mode arrière-plan)

Statut : **projet, à valider** (23/11/2026). Jalon M24 ([09](09-feuille-de-route.md)). Rien n'est encore implémenté.

Les références au code de NextUI renvoient à `LoveRetro/NextUI`, branche `main`, commit `b6ec1343` (le même que [05](05-console-pak.md)). Abréviations : `minarch/` = `workspace/all/minarch/`, `common/` = `workspace/all/common/`, `nextui.c` = `workspace/all/nextui/nextui.c`, `MinUI launch.sh` = `skeleton/SYSTEM/tg5040/paks/MinUI.pak/launch.sh` (le lanceur de NextUI, à ne pas confondre avec le `launch.sh` de `ReminisLink.pak`), `bin/` = `skeleton/SYSTEM/tg5040/bin/`.

## 1. Pourquoi

Aujourd'hui le pak ne tourne qu'au premier plan, quand aucun jeu ne tourne (01, 05 §2). Pour changer un code de triche pendant une partie, il faut quitter le jeu, ouvrir ReminisLink sur la console, modifier depuis le téléphone, quitter ReminisLink et relancer le jeu, à chaque essai (retour de la personne, 23/11/2026).

Le mode arrière-plan garde un serveur disponible pendant le jeu et dans le menu de NextUI : le téléphone gère la console (envois, sauvegardes, cheats…) sans qu'on ouvre ReminisLink sur la console ; seul ce qui est en cours d'utilisation est protégé (§2).

**Limite, à dire dans l'app.** minarch ne lit le fichier `.cht` qu'au chargement du jeu (`Cheats_load()` appelé seulement par `Core_load()`, `minarch/ma_core.c:143-144`, lui-même appelé une fois, `minarch/minarch.c:208`). Son menu *Cheats* active ou désactive les codes **déjà chargés** (`minarch/ma_frontend_opts.c:476-540`, liste construite une fois puis gardée) ; *Reset* ne relit rien (`minarch/ma_core.c:152-155`). Un code enregistré pendant le jeu s'applique donc **au prochain lancement du jeu**. Le parcours devient : modifier sur le téléphone, *Menu › Quitter*, relancer le jeu (avec *Reprendre* quand NextUI le propose). L'app affiche « Relance le jeu pour appliquer le changement. »

## 2. Principe : tout est permis, sauf ce qui est en cours d'utilisation

**Décision du 23/11/2026 (personne).** Jouer à un jeu n'empêche pas d'envoyer un autre jeu, de télécharger des sauvegardes ou de gérer le reste de la carte. Le serveur d'arrière-plan offre donc les mêmes fonctions que le pak au premier plan, sauf sur **ce qui est en cours d'utilisation** sur la console (§4) :

| Ce qui tourne | Refusé, et seulement cela |
|---|---|
| Un jeu | Les fichiers de ce jeu (§4) : lire sa save, son `.rtc`, ses states, son state de reprise ou une archive qui les contient (minarch réécrit la save sur place, `minarch/ma_saves.c:75-105`, à l'ouverture du menu, avant la veille et en quittant, `minarch/ma_menu.c:1795`, `minarch/ma_menu.c:225`, `minarch/ma_core.c:164`) ; toute écriture sur ce jeu (remplacer, restaurer, supprimer, renommer, patcher, importer une archive, verrous, jaquette). Installer, mettre à jour ou supprimer l'émulateur du jeu (le pak de `Emus/` qui l'a lancé). Réponse : `409 game_running` |
| Une autre app (pak) | Mettre à jour ou supprimer cette app ; enregistrer des cheats (Cheat Manager écrit les mêmes `.cht`). Réponse : `409 console_busy` |
| Le menu de NextUI | Rien |

Exceptions, quoi qu'il tourne :

- **Cheats du jeu en cours** : permis (seule écriture permise sur ce jeu). Le `.cht` est remplacé d'un seul coup (`.tmp`, `fsync`, renommage) et minarch ne le lit qu'au chargement (§1) : il lit l'ancien ou le nouveau, jamais un mélange ; les copies faites avant (`.cht.bak`, ancien `.cht.bak` à la corbeille, 05 §5.4) touchent des fichiers que minarch ne lit pas (`minarch/ma_cheats.c:309-370`). La réponse porte `appliesOnNextLaunch: true` et l'app affiche la notice du §1. Le nombre de codes est limité à celui que minarch accepte (4 096, `minarch/ma_cheats.h:9`), dans les deux modes.
- **Appairage** : seulement avec le pak ouvert sur la console (QR code à l'écran, §6).
- **Maintenance** (archivage du state de reprise, contrôle des verrous, purges) : seulement à l'ouverture du pak, comme aujourd'hui (§6).

### 2.1 Les transitions

NextUI n'a pas de barrière de lancement qui échoue fermée : un hook `pre-launch.d` lent retarde le lancement mais ne peut pas l'annuler, et ses erreurs sont ignorées (`HOOKS.md:68-71`, `bin/run_hooks.sh:28-36`). Un jeu peut donc être lancé pendant que ReminisLink écrit dans ses fichiers. Trois protections :

1. **Le hook `pre-launch.d` synchrone** (§3) annonce au serveur ce qui va être lancé. Le serveur marque aussitôt ce jeu (ou cette app) comme en cours d'utilisation : toute nouvelle opération sur lui est refusée. Il attend la fin de l'opération en cours sur lui (15 s au plus, plus que les 10 s d'attente du verrou d'écriture, 03 §6), puis répond, et le lancement continue. C'est le cas normal.
2. **Si le hook manque ou arrive trop tard**, l'état réel est relu dans `/proc` (§4) sous le verrou d'écriture, avant chaque étape qui modifie la carte. Une opération qui trouve son jeu lancé en cours de route s'arrête avant l'étape suivante et se défait ou se reprend par son journal (comme après une coupure). Une étape déjà commencée se termine : chaque fichier est écrit de façon atomique et tout fichier remplacé est d'abord copié dans la corbeille (règle 1). minarch charge alors l'ancienne ou la nouvelle version entière ; au pire, il réécrit ensuite la save depuis sa mémoire, et la version remplacée reste dans la corbeille (la version envoyée reste sur le téléphone).
3. **Un téléchargement en cours** (save, state, archive) quand son jeu est lancé : minarch n'écrit pas la save au chargement mais à l'ouverture du menu, avant la veille et en quittant. Le serveur relit la taille et la date du fichier **avant d'envoyer le dernier bloc** et coupe la connexion s'ils ont changé ou si le jeu a été lancé entre-temps : l'app reçoit moins d'octets que la longueur annoncée, signale l'échec et recommence plus tard. Une écriture de minarch pendant la lecture change la date : elle est donc vue avant la fin.

Une app (pak) peut écrire n'importe où sur la carte : à part les cheats et l'app elle-même, ses écritures ne sont pas surveillées (choix de la personne : ne bloquer que ce qui est en cours d'utilisation). Ce que ReminisLink remplace va toujours d'abord dans la corbeille.

## 3. Activation et hooks

- Réglage du pak : **Réglages › Disponible pendant les jeux**, **désactivé par défaut**. Une seule source de vérité : la configuration du pak (05 §4), relue par chaque hook et par le serveur à son démarrage.
- **Activer** installe quatre scripts dans `$USERDATA_PATH/.hooks/` (système de hooks de NextUI, `HOOKS.md`) :
  - `pre-launch.d/reminislink.sync.sh` : synchrone, avant chaque lancement (`MinUI launch.sh:193`) ; annonce au serveur ce qui va tourner et attend sa réponse (§2.1, 16 s au plus) ; ne fait rien quand c'est `ReminisLink.pak` lui-même (la relève du §5 s'en charge) ;
  - `boot.d/reminislink.sh` : au démarrage (`MinUI launch.sh:155`), lance le serveur d'arrière-plan **détaché** (`setsid`, entrées et sorties redirigées) et rend la main tout de suite (le lanceur attend ses scripts, `bin/run_hooks.sh:28-36`) ;
  - `post-launch.d/reminislink.sh` : après chaque lancement (`MinUI launch.sh:195`), relance le serveur s'il ne tourne pas ou si son binaire a changé (mise à jour du pak par le Pak Store) ;
  - `post-resume.d/reminislink.sh` : au réveil d'une veille profonde (en arrière-plan, `bin/suspend:40-43`), prévient le serveur de refaire son écoute et son annonce (§7).
- `auto.sh` n'est **jamais** modifié (partagé entre paks ; `HOOKS.md:72` recommande les hooks). Le hook `pre-launch.d` est le seul synchrone ; il ne transmet au serveur que des données (`HOOK_TYPE`, `HOOK_EMU_PATH`, `HOOK_ROM_PATH`, `MinUI launch.sh:162-175`), jamais exécutées, par la socket de contrôle (§5), avec un numéro de lancement dont le serveur accuse réception.
- Chaque script porte une ligne marqueur (`# ReminisLink hook v1`) et sort sans rien faire si le réglage est désactivé ou si `ReminisLink.pak` n'existe plus. Il ne se supprime pas lui-même. Seul le pak installe ou retire des hooks, et seulement les siens : un fichier du même nom dont le contenu n'est pas exactement celui qu'écrit une version du pak (marqueur compris) n'est jamais touché, l'activation ou la désactivation le signale ; un hook retiré ou remplacé part à la corbeille (règle 1).
- **Désactiver** écrit d'abord le réglage, puis arrête le serveur, puis retire les scripts. Un retrait ou une installation interrompus laissent des scripts inertes ou manquants ; le pak remet tout d'accord avec le réglage à son lancement suivant.
- **[À VÉRIFIER]** : le lanceur exécute les scripts directement (`( "$script" )`, `bin/run_hooks.sh:31-33`) ; sur la carte exFAT, le droit d'exécution vient des options de montage (pas de `chmod`, règle 4 de CLAUDE.md). Si l'exécution échoue, le serveur ne démarre pas et la protection 1 du §2.1 manque : à vérifier sur la console avant de proposer le réglage.

## 4. Ce qui tourne

Le serveur lit l'activité dans `/proc` (le hook `pre-launch.d` ne fait que l'annoncer plus tôt) :

- `minarch.elf` vivant : **jeu** ; ses arguments (`/proc/<pid>/cmdline`, exacts, sans échappement) donnent le cœur et le chemin de la ROM (`skeleton/SYSTEM/tg5040/paks/Emus/GB.pak/launch.sh:14`). Le chemin est résolu en jeu par la bibliothèque, confiné (règle 2), sans la casse ; l'argument est le chemin d'origine (une ROM d'archive n'est extraite dans `/tmp/nextarch/<TAG>` qu'ensuite et sous condition, `minarch/ma_game.c:46` et suivantes, `163-178`) ; à la reprise d'un jeu multi-disque, NextUI lance le disque de la reprise (`nextui.c:1286-1311`) ; **[À VÉRIFIER]** sur la console pour ces cas et les raccourcis de M23. Les fichiers du jeu sont résolus et confinés comme toute requête (règle 2), sans la casse (exFAT), puis étendus à **tout** son ensemble : ROM et son archive, tous les disques d'un jeu multi-disque, la cible d'un raccourci, save, `.rtc`, states, state de reprise, verrous, copies et jaquette. Un chemin qui ne se résout pas : **tous les jeux de ce système** sont traités comme en cours d'utilisation (prudence).
- `nextui.elf` vivant sans `minarch.elf` : **menu**.
- Ni l'un ni l'autre : **app** (pak) ou transition (raccourci d'outil par `SHORTCUT.pak`, extinction). L'app est connue par le hook (`HOOK_EMU_PATH`) ou par le chemin de l'exécutable des processus ; inconnue, seuls les cheats sont refusés (§2).

L'activité décide de ce qui est refusé (§2) : en cas de doute, le serveur refuse plus, jamais moins.

## 5. Un seul serveur à la fois (relève)

Le même binaire tourne soit au premier plan (pak ouvert, écran gabagool, toutes les fonctions), soit en arrière-plan (`--background`, sans écran), jamais les deux : ils partagent l'identité de la console, le certificat TLS, `clients.json` et le port.

- **Verrou** : un verrou exclusif (`flock`) sur `/tmp/reminislink/lock` (dossier créé avec les droits du seul root) est la seule preuve de propriété. Il est pris **avant** toute initialisation et gardé jusqu'à la fin de la fermeture ; aucun processus n'ouvre le serveur ni n'écrit sur la carte sans lui. Deux démarrages simultanés : le second attend ou sort.
- **Démarrage en arrière-plan** : le serveur ne crée ni ne migre l'identité, le certificat ou la configuration (s'ils manquent ou sont d'une version plus ancienne, il ne démarre pas : il faut d'abord ouvrir le pak) ; il reprend d'abord les opérations laissées en cours (journaux de renommage, corbeille, envois) qui ne touchent pas ce qui est en cours d'utilisation, les autres attendent. L'état « déconnecté par la console » (M10, en mémoire) passe à la relève avec le cache de nonces.
- **Ouverture du pak** : le `launch.sh` de `ReminisLink.pak` demande la fermeture au serveur d'arrière-plan par sa socket de contrôle (Unix, dans `/tmp/reminislink/`). Le serveur refuse alors toute nouvelle requête signée, termine l'étape d'écriture en cours, écrit son cache de nonces dans `/tmp/reminislink/` (mémoire vive, jamais sur la carte), libère le verrou et sort. Le pak attend le verrou (10 s au plus) et importe le cache avant d'accepter une requête.
- **Cache de nonces** : avant de l'écrire, le serveur cesse d'admettre des requêtes signées et attend celles en cours de vérification ; le fichier est publié de façon atomique, avec les échéances absolues ; le successeur refuse un fichier absent, périmé ou illisible (cas d'arrêt forcé, ci-dessous).
- **Serveur qui ne répond pas** : passé ce délai, le pak arrête le détenteur du verrou (`SIGKILL`), après avoir vérifié par `/proc/<pid>/exe` et la date de démarrage du processus (`/proc/<pid>/stat`) que c'est bien son binaire (un PID réutilisé entre la vérification et le signal est accepté comme improbable). Sans cache valide (arrêt forcé, plantage), le successeur **refuse toute requête signée avant son propre démarrage** (horodatage antérieur à son heure de démarrage) : une requête capturée avant ne peut pas être rejouée (décision 5, proposée par la personne). Une app dont l'horloge retarde reçoit `401 clock_skew`, recale son horloge sur `serverTime` de `GET /hello` et renvoie sa requête ; si cela échoue, elle affiche « Erreur de communication avec la console. Réessaie. ». Les opérations interrompues se reprennent par leurs journaux, comme après une coupure.
- **Sortie du pak** : si le réglage est actif, le `launch.sh` de `ReminisLink.pak` relance le serveur d'arrière-plan détaché, qui attend le verrou, importe le cache de nonces laissé par le pak, et n'accepte qu'ensuite des requêtes.
- **Pour les apps** : l'événement `shutdown` (M14) gagne un champ optionnel `restarting: true` pour une relève ; `reason` garde ses valeurs actuelles. L'app garde la console ouverte et se reconnecte d'elle-même (30 s au plus, Bonjour compris). Une app plus ancienne ignore le champ et revient à la liste des consoles, comme aujourd'hui quand on quitte le pak. Après un arrêt forcé (aucun `shutdown`), l'app voit le flux tomber et suit ses règles de reconnexion actuelles (M14).

## 6. Le serveur d'arrière-plan

- **Sans écran** : aucune fenêtre SDL, aucune entrée manette, aucune gestion du bouton d'alimentation.
- **Même API**, même identité, même authentification (07). **Pas d'appairage** : `pairingOpen` vaut toujours `false`, `POST /pair` répond `401 pairing_failed` sans compter d'échec. Un nouvel appareil s'appaire en ouvrant ReminisLink sur la console.
- **Pas de maintenance** ni de migration : l'archivage du state de reprise, le contrôle des verrous et les purges (05 §2 étape 3) restent faits à l'ouverture du pak.
- **Sobriété** (à confirmer par les mesures, §9) : `GOMAXPROCS=1`, `GOMEMLIMIT` (32 Mio de départ), `nice 10` ; aucune minuterie propre en dehors de la surveillance du réseau (§7), de Bonjour et des flux d'événements ouverts.
- **Journal** : même fichier (`ReminisLink.txt`), avec le mode à chaque démarrage.

## 7. Veille, extinction et réseau

- La veille de NextUI a deux étapes. Veille légère : écran éteint, son en pause, `keymon`, `batmon` et `audiomon` arrêtés (`common/api.c:4341-4373`) ; le Wi-Fi reste actif et le serveur joignable. Après `suspendTimeout` (30 s par défaut, `common/config.h:231` ; `common/api.c:4410-4444`, avec repli sur l'extinction ; autrement en charge, sur USB ou sans veille profonde), veille profonde par `bin/suspend` (appelé par `common/api.c:4469-4492`) : hooks `pre-sleep.d`, arrêt du Wi-Fi (`bin/suspend:16-36`), `echo mem` (`bin/suspend:69`) ; au réveil, Wi-Fi relancé s'il tournait (`bin/suspend:40-49`).
- Le serveur survit à la veille, pas ses connexions. Au signal du hook `post-resume.d` (§3), il rouvre son écoute et refait l'annonce Bonjour, avec de nouveaux essais bornés, même si l'adresse n'a pas changé. En secours (hook manquant, Wi-Fi changé sans veille), il relit l'adresse et l'état du lien de l'interface sans fil toutes les 10 s (lecture locale) et fait de même à tout changement. Les apps relisent `GET /device` à chaque reconnexion de leur flux d'événements.
- **Extinction** pendant une écriture : l'ancien fichier ou le nouveau, jamais un mélange, puis reprise par les journaux, sous réserve du comportement réel d'exFAT à la coupure **[À VÉRIFIER]** (essais de coupure sur la console ; l'écriture atomique actuelle ignore les erreurs de synchronisation du dossier, à corriger, `internal/store/atomic.go`).
- Le Wi-Fi reste celui de NextUI : le serveur ne l'allume ni ne l'éteint. Lien gardé pendant une longue partie **[À VÉRIFIER]** sur la console.

## 8. Contrat et apps

**Contrat** (`protocol/openapi.yaml` d'abord) :

- `GET /hello` (non signé) : tous les champs actuels gardés, plus `mode` (`foreground` ou `background`), optionnel. **Rien sur l'activité** (aucune donnée de jeu dans `/hello`).
- `GET /device` (signé) : `mode` et `activity`, optionnels : `activity` vaut `{ state: menu | game | pak, gameId?, systemTag?, name? }` (`name` : le nom du jeu ; pour un pak, aucun nom n'est connu, l'app affiche « Une app est ouverte sur la console »).
- Nouvel événement `activity` (même contenu) dans le flux signé à chaque changement.
- `EventShutdown` : champ optionnel `restarting` (§5). Champs obligatoires et valeurs existants inchangés.
- Erreurs (03 §6) : `409 game_running` (`details.gameId`, ou `details.systemId` quand le jeu n'a pas pu être identifié) et `409 console_busy` (une app ouverte sur la console).
- Réponse d'enregistrement des cheats : `appliesOnNextLaunch` optionnel (le jeu tourne).
- Compatibilité : `ios-v1.1.0` traite un code inconnu comme une erreur générique (`ErrorCode.other`, `APIError.swift`), ignore les clés et événements inconnus ; l'app Android fait de même dans le code (`ui/Errors.kt`), à vérifier sur la première version publiée. `minClientVersion` et `apiVersion` inchangés. Une app ancienne voit des erreurs génériques sur les actions refusées (seulement sur le jeu ou l'app en cours) et revient à la liste à une relève : c'est acceptable pour une version **MINOR**, à confirmer en essayant les apps publiées (relève, veille, `409`) avant de fixer le numéro.

**Apps iOS et Android** (parité) :

- Tableau de bord : bandeau selon `activity` : « En jeu : *nom du jeu* » ou « Une app est ouverte sur la console ».
- Jeu en cours : ses actions sont désactivées avec la raison (« Ce jeu tourne sur la console. Quitte-le pour modifier ou télécharger sa sauvegarde. ») ; ses cheats restent modifiables, avec la notice du §1. Tout le reste de l'app fonctionne normalement.
- La sync des saves saute le jeu en cours et le reprend à l'événement `activity` qui annonce sa fin.
- Messages traduits pour `game_running` et `console_busy` ; un téléchargement coupé (§2.1, protection 3) est un échec ordinaire, à relancer. Relève : reconnexion silencieuse.
- Aucun réglage dans l'app : le mode se règle sur la console.

## 9. Mesures et critères d'acceptation

Sur une TrimUI Brick (tg5040), serveur d'arrière-plan actif, sans app puis avec une app connectée et son flux d'événements :

- mémoire résidente du serveur : < 30 Mio ;
- CPU pendant un jeu : < 1 % en moyenne sur 10 min ;
- batterie : écart mesuré sur 1 h de jeu avec et sans le serveur, publié dans la spec ;
- aucune image perdue de plus qu'à vide sur un cœur exigeant, rembobinage activé ;
- après une veille profonde, console retrouvée par l'app en moins de 30 s ;
- relèves répétées (ouvrir et quitter ReminisLink, serveur qui ne répond pas) : un seul serveur à la fois, aucun rejeu accepté, même après un arrêt forcé ;
- lancement d'un jeu pendant une restauration de sa save, avec et sans le hook `pre-launch.d` : aucune version perdue (corbeille), opération défaite ou reprise ;
- règles du §2 couvertes par des tests Go (processus `nextui.elf` et `minarch.elf` simulés, mode desktop) et par l'émulation de la CI ; sélecteur de jeux, reprise au démarrage et extinction essayés sur la console.

## 10. Plateformes

- **tg5040** d'abord (Brick, Smart Pro).
- **tg5050, h700, my355** : mêmes hooks attendus **[À VÉRIFIER]** dans leurs lanceurs.
- **Knulli** : hors de M24 (autre lanceur, pas ces hooks).

## 11. Sécurité (revue de 07)

- **Exposition** : réglage actif, le service écoute sur le Wi-Fi tant que la console est allumée, et non quelques minutes. Les mesures de 07 §3 restent : routes métier signées, TLS épinglé, appairage **fermé** en arrière-plan, `/hello` limité par IP, corps bornés. Le serveur n'écoute que sur l'adresse de l'interface sans fil (pas `0.0.0.0`).
- **Ce qu'un inconnu du réseau voit** : par Bonjour et `/hello`, le nom, l'identifiant stable, la version et le mode de la console, en permanence et sur tous les réseaux où elle se connecte (décision 1). Rien sur les jeux.
- **Ce qu'un client appairé peut faire** en arrière-plan : la même chose qu'avec le pak ouvert, sauf appairer et toucher ce qui est en cours d'utilisation.
- **Rejeu** : inchangé après une relève propre (cache de nonces passé) ; après un arrêt forcé, les requêtes signées avant le démarrage du successeur sont refusées (§5) : le risque résiduel de 07 (relance du pak moins de 300 s après une requête capturée) disparaît aussi pour le pak au premier plan si la même règle lui est appliquée (à décider à l'implémentation).
- **Hooks** : lancés en root par NextUI, même niveau de confiance que le pak ; ils n'exécutent que le binaire du pak et ne sont pas modifiables par l'API (comme la socket de contrôle et le binaire, protégés du navigateur de la carte, 05 §3.4).
- 07 §2 (« Le pak n'écoute que lorsqu'il est ouvert ») est complété par ce mode.

## 12. Décisions

| # | Question | Décision |
|---|---|---|
| 1 | Sur quels réseaux le serveur d'arrière-plan écoute-t-il ? | **Tous** (validé le 23/11/2026) : la sécurité repose sur les signatures ; un réseau public voit le nom, l'identifiant, la version et le mode de la console, rien de plus |
| 2 | Que permettre en arrière-plan ? | **Tout, sauf ce qui est en cours d'utilisation** (personne, 23/11/2026) : le jeu en cours (sauf ses cheats) et l'app ouverte ; §2 et ses protections pendant les transitions (§2.1) |
| 3 | Cheats pendant qu'une autre app est ouverte (Cheat Manager peut modifier les mêmes fichiers) | **À valider** : les refuser (`console_busy`) tant qu'une app est ouverte, pour qu'une modification faite dans Cheat Manager et une autre faite depuis le téléphone ne s'écrasent pas |
| 4 | Réglage par défaut | **Désactivé** (validé le 23/11/2026) |
| 5 | Après un arrêt forcé du serveur | **Couper plutôt que risquer** (personne, 23/11/2026) : le successeur refuse les requêtes signées avant son démarrage ; l'app recale son horloge et réessaie, sinon affiche une erreur de communication |
