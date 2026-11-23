# 11 — ReminisLink pendant les jeux (mode arrière-plan)

Statut : **projet, à valider** (23/11/2026). Jalon M24 ([09](09-feuille-de-route.md)). Rien n'est encore implémenté.

Les références au code de NextUI renvoient à `LoveRetro/NextUI`, branche `main`, commit `b6ec1343` (le même que [05](05-console-pak.md)). Abréviations : `minarch/` = `workspace/all/minarch/`, `common/` = `workspace/all/common/`, `nextui.c` = `workspace/all/nextui/nextui.c`, `MinUI launch.sh` = `skeleton/SYSTEM/tg5040/paks/MinUI.pak/launch.sh` (le lanceur de NextUI, à ne pas confondre avec le `launch.sh` de `ReminisLink.pak`), `bin/` = `skeleton/SYSTEM/tg5040/bin/`.

## 1. Pourquoi

Aujourd'hui le pak ne tourne qu'au premier plan, quand aucun jeu ne tourne (01, 05 §2). Pour changer un code de triche pendant une partie, il faut quitter le jeu, ouvrir ReminisLink sur la console, modifier depuis le téléphone, quitter ReminisLink et relancer le jeu, à chaque essai (retour de la personne, 23/11/2026).

Le mode arrière-plan garde un serveur disponible pendant le jeu et dans le menu de NextUI : le téléphone consulte la console et modifie les cheats sans qu'on ouvre ReminisLink sur la console.

**Limite, à dire dans l'app.** minarch ne lit le fichier `.cht` qu'au chargement du jeu (`Cheats_load()` appelé seulement par `Core_load()`, `minarch/ma_core.c:143-144`, lui-même appelé une fois, `minarch/minarch.c:208`). Son menu *Cheats* active ou désactive les codes **déjà chargés** (`minarch/ma_frontend_opts.c:476-540`, liste construite une fois puis gardée) ; *Reset* ne relit rien (`minarch/ma_core.c:152-155`). Un code enregistré pendant le jeu s'applique donc **au prochain lancement du jeu**. Le parcours devient : modifier sur le téléphone, *Menu › Quitter*, relancer le jeu (avec *Reprendre* quand NextUI le propose). L'app affiche « Relance le jeu pour appliquer le changement. »

## 2. Principe de sûreté : un serveur d'arrière-plan qui n'écrit que des cheats

NextUI n'a pas de barrière de lancement qui échoue fermée : un hook `pre-launch.d` lent retarde le lancement mais ne peut pas l'annuler, et ses erreurs sont ignorées (`HOOKS.md:68-71`, `bin/run_hooks.sh:28-36`). Les transitions (lancement, sélecteur de jeux, reprise au démarrage, extinction) laissent des moments où le menu, un jeu ou un pak démarrent pendant qu'une écriture se termine (`MinUI launch.sh:185-208`, `nextui.c:1182-1217, 2253, 2455-2469`). Une écriture de ReminisLink ne peut donc jamais être sûre de ne pas croiser minarch ou un autre pak.

D'où le choix de M24 : le serveur d'arrière-plan n'admet **que** des opérations sûres à tout moment, quoi qu'il tourne :

- **consultation**, par une liste fermée de routes (méthode et chemin ; tout le reste refusé, `HEAD` implicite compris) : `GET /hello`, `/device`, `/events`, `/systems`, `/systems/{systemId}/games`, `/games`, `/games/{gameId}`, `/trash`, `/paks`, `/sd/browse`, et `/sd/files` seulement pour choisir un `.cht` à importer. Aucun contenu de fichier n'est servi, sauf les cheats eux-mêmes (`GET /games/{gameId}/cheats` rend les codes lus dans le `.cht`). Restent au premier plan : téléchargements (ROM, archives, jaquettes, fichiers de la carte, saves, `.rtc`, copies, states et vignettes, captures), et les lectures qui touchent des fichiers vivants ou préparent une écriture : `/saves` (il calcule l'empreinte de la save et du `.rtc`), inventaires des states, copies et historique de reprise, captures, aperçus de suppression et de restauration, envois, tâches, inspection d'archive ;
- **enregistrement des cheats** d'un jeu (`PUT /games/{gameId}/cheats` et l'import, depuis le corps de la requête ou depuis un `.cht` de la carte ; pas depuis un envoi déjà en attente) : le `.cht` est remplacé par un renommage atomique (`.tmp`, `fsync`, renommage) et minarch ne le lit qu'au chargement : il lit l'ancien ou le nouveau, jamais un mélange. Les copies faites avant (`.cht.bak`, ancien `.cht.bak` à la corbeille, 05 §5.4, règle 1) touchent des fichiers que minarch ne lit pas (son chargeur ignore `.bak`, `minarch/ma_cheats.c:309-370`).
  - **Limite au lancement** : minarch choisit le fichier avant de l'ouvrir. Une liste vidée (le `.cht` supprimé, après sa copie) juste à ce moment fait démarrer le jeu sans cheats ; il suffit de le relancer. Le nombre de codes est limité à celui que minarch accepte (4 096, `minarch/ma_cheats.h:9`), au premier plan comme en arrière-plan.
  - **Contre une écriture d'un autre pak pendant la transaction** : juste avant le renommage final, le serveur vérifie que le `.cht` est toujours celui qu'il vient de copier en `.cht.bak` (même contenu) ; sinon il abandonne (`409 conflict`, l'app relit) sans rien remplacer. Reste la fenêtre entre cette vérification et le renommage (décision 3).
  - **Transaction interrompue** (coupure, arrêt forcé) : si le serveur d'arrière-plan trouve au démarrage une entrée de corbeille ou une copie inachevée, il refuse les cheats (`409 background_mode`) jusqu'à la reprise par le pak au premier plan.

Tout le reste (envois, téléchargements de fichiers, saves, states, captures, archives, patchs, apps, corbeille, renommages, suppressions, verrous) répond `409 background_mode` : « Ouvre ReminisLink sur la console pour cette action. » Ces actions restent celles du pak au premier plan, inchangées.

**Exception, pendant un autre pak.** Un pak (Cheat Manager surtout) peut écrire les mêmes `.cht` : pendant qu'un pak tourne, l'enregistrement des cheats répond `409 console_busy`. Un pak qui démarre pendant un enregistrement lit l'ancien ou le nouveau fichier ; le contrôle du contenu ci-dessus empêche d'effacer une version sans copie, sauf dans une fenêtre de quelques microsecondes (décision 3).

Ce choix rend inutiles une barrière au lancement, une maintenance d'arrière-plan et des reprises de transactions par le serveur d'arrière-plan. L'élargir (saves des autres jeux, ajout de ROMs pendant un jeu) demanderait un lanceur qui attend ReminisLink avant de lancer un jeu, par exemple un hook bloquant proposé à NextUI (décision 2).

## 3. Activation et hooks

- Réglage du pak : **Réglages › Disponible pendant les jeux**, **désactivé par défaut**. Une seule source de vérité : la configuration du pak (05 §4), relue par chaque hook et par le serveur à son démarrage.
- **Activer** installe trois scripts dans `$USERDATA_PATH/.hooks/` (système de hooks de NextUI, `HOOKS.md`) :
  - `boot.d/reminislink.sh` : au démarrage (`MinUI launch.sh:155`), lance le serveur d'arrière-plan **détaché** (`setsid`, entrées et sorties redirigées) et rend la main tout de suite (le lanceur attend ses scripts, `bin/run_hooks.sh:28-36`) ;
  - `post-launch.d/reminislink.sh` : après chaque lancement (`MinUI launch.sh:195`), relance le serveur s'il ne tourne pas ou si son binaire a changé (mise à jour du pak par le Pak Store) ;
  - `post-resume.d/reminislink.sh` : au réveil d'une veille profonde (en arrière-plan, `bin/suspend:40-43`), prévient le serveur de refaire son écoute et son annonce (§7).
- `auto.sh` n'est **jamais** modifié (partagé entre paks ; `HOOKS.md:72` recommande les hooks). Aucun hook synchrone : le serveur n'en a pas besoin (§2).
- Chaque script porte une ligne marqueur (`# ReminisLink hook v1`) et sort sans rien faire si le réglage est désactivé ou si `ReminisLink.pak` n'existe plus. Il ne se supprime pas lui-même. Seul le pak installe ou retire des hooks, et seulement les siens : un fichier du même nom dont le contenu n'est pas exactement celui qu'écrit une version du pak (marqueur compris) n'est jamais touché, l'activation ou la désactivation le signale ; un hook retiré ou remplacé part à la corbeille (règle 1).
- **Désactiver** écrit d'abord le réglage, puis arrête le serveur, puis retire les scripts. Un retrait ou une installation interrompus laissent des scripts inertes ou manquants ; le pak remet tout d'accord avec le réglage à son lancement suivant.
- **[À VÉRIFIER]** : le lanceur exécute les scripts directement (`( "$script" )`, `bin/run_hooks.sh:31-33`) ; sur la carte exFAT, le droit d'exécution vient des options de montage (pas de `chmod`, règle 4 de CLAUDE.md). Si l'exécution échoue, le serveur ne démarre simplement pas : à vérifier sur la console avant de proposer le réglage.

## 4. Ce qui tourne

Le serveur lit l'activité dans `/proc`, sans dépendre des hooks :

- `minarch.elf` vivant : **jeu** ; ses arguments (`/proc/<pid>/cmdline`, exacts, sans échappement) donnent le cœur et le chemin de la ROM (`skeleton/SYSTEM/tg5040/paks/Emus/GB.pak/launch.sh:14`). Le chemin est résolu en jeu par la bibliothèque, confiné (règle 2), sans la casse ; l'argument est le chemin d'origine (une ROM d'archive n'est extraite dans `/tmp/nextarch/<TAG>` qu'ensuite et sous condition, `minarch/ma_game.c:46` et suivantes, `163-178`) ; à la reprise d'un jeu multi-disque, NextUI lance le disque de la reprise (`nextui.c:1286-1311`) ; **[À VÉRIFIER]** sur la console pour ces cas et les raccourcis de M23. Non résolu : jeu inconnu.
- `nextui.elf` vivant sans `minarch.elf` : **menu**.
- Ni l'un ni l'autre : **pak** ou transition (pak lancé, raccourci d'outil par `SHORTCUT.pak`, extinction). Traité comme un pak (§2).

L'activité ne sert qu'à l'affichage dans l'app et à la notice des cheats ; aucune règle de sûreté ne dépend de son exactitude, sauf le refus des cheats pendant un pak (où une erreur ne laisse qu'un risque de mise à jour perdue, avec la copie).

## 5. Un seul serveur à la fois (relève)

Le même binaire tourne soit au premier plan (pak ouvert, écran gabagool, toutes les fonctions), soit en arrière-plan (`--background`, sans écran), jamais les deux : ils partagent l'identité de la console, le certificat TLS, `clients.json` et le port.

- **Verrou** : un verrou exclusif (`flock`) sur `/tmp/reminislink/lock` (dossier créé avec les droits du seul root) est la seule preuve de propriété. Il est pris **avant** toute initialisation et gardé jusqu'à la fin de la fermeture ; aucun processus n'ouvre le serveur ni n'écrit sur la carte sans lui. Deux démarrages simultanés : le second attend ou sort.
- **Démarrage en arrière-plan** : le serveur n'écrit rien d'autre que les cheats. Il ne crée ni ne migre l'identité, le certificat ou la configuration (s'ils manquent ou sont d'une version plus ancienne, il ne démarre pas : il faut d'abord ouvrir le pak) et ne crédite pas l'âge de la corbeille (05 §4) ; `DELETE /clients/me` (désappairer) répond `409 background_mode`. L'état « déconnecté par la console » (M10, en mémoire) passe à la relève avec le cache de nonces.
- **Ouverture du pak** : le `launch.sh` de `ReminisLink.pak` demande la fermeture au serveur d'arrière-plan par sa socket de contrôle (Unix, dans `/tmp/reminislink/`). Le serveur refuse alors toute nouvelle requête signée, termine l'écriture en cours (un fichier de cheats), écrit son cache de nonces dans `/tmp/reminislink/` (mémoire vive, jamais sur la carte), libère le verrou et sort. Le pak attend le verrou (10 s au plus) et importe le cache avant d'accepter une requête.
- **Cache de nonces** : avant de l'écrire, le serveur cesse d'admettre des requêtes signées et attend celles en cours de vérification ; le fichier est publié de façon atomique, avec les échéances absolues ; le successeur refuse un fichier absent, périmé ou illisible (cas d'arrêt forcé, ci-dessous).
- **Serveur qui ne répond pas** : passé ce délai, le pak arrête le détenteur du verrou (`SIGKILL`), après avoir vérifié par `/proc/<pid>/exe` et la date de démarrage du processus (`/proc/<pid>/stat`) que c'est bien son binaire (un PID réutilisé entre la vérification et le signal est accepté comme improbable). Sans cache valide, le successeur se trouve dans le cas déjà accepté par 07 pour une relance du pak : une requête capturée reste rejouable tant que son horodatage est dans la fenêtre de ±300 s (jusqu'à environ 600 s pour une requête datée dans le futur). Ce cas devient possible aussi lors d'une relance automatique : décision 5. Une écriture de cheats interrompue laisse l'ancien ou le nouveau `.cht` ; la corbeille se reprend au lancement du pak comme après une coupure.
- **Sortie du pak** : si le réglage est actif, le `launch.sh` de `ReminisLink.pak` relance le serveur d'arrière-plan détaché, qui attend le verrou, importe le cache de nonces laissé par le pak, et n'accepte qu'ensuite des requêtes.
- **Pour les apps** : l'événement `shutdown` (M14) gagne un champ optionnel `restarting: true` pour une relève ; `reason` garde ses valeurs actuelles. L'app garde la console ouverte et se reconnecte d'elle-même (30 s au plus, Bonjour compris). Une app plus ancienne ignore le champ et revient à la liste des consoles, comme aujourd'hui quand on quitte le pak. Après un arrêt forcé (aucun `shutdown`), l'app voit le flux tomber et suit ses règles de reconnexion actuelles (M14).

## 6. Le serveur d'arrière-plan

- **Sans écran** : aucune fenêtre SDL, aucune entrée manette, aucune gestion du bouton d'alimentation.
- **Même API**, même identité, même authentification (07). **Pas d'appairage** : `pairingOpen` vaut toujours `false`, `POST /pair` répond `401 pairing_failed` sans compter d'échec. Un nouvel appareil s'appaire en ouvrant ReminisLink sur la console.
- **Pas de maintenance** ni d'écriture de suivi (crédit d'âge de la corbeille, migrations) : l'archivage du state de reprise, le contrôle des verrous et les purges (05 §2 étape 3) restent faits à l'ouverture du pak.
- **Sobriété** (à confirmer par les mesures, §9) : `GOMAXPROCS=1`, `GOMEMLIMIT` (32 Mio de départ), `nice 10` ; aucune minuterie propre en dehors de la surveillance du réseau (§7), de Bonjour et des flux d'événements ouverts.
- **Journal** : même fichier (`ReminisLink.txt`), avec le mode à chaque démarrage.

## 7. Veille, extinction et réseau

- La veille de NextUI a deux étapes. Veille légère : écran éteint, son en pause, `keymon`, `batmon` et `audiomon` arrêtés (`common/api.c:4341-4373`) ; le Wi-Fi reste actif et le serveur joignable. Après `suspendTimeout` (30 s par défaut, `common/config.h:231` ; `common/api.c:4410-4444`, avec repli sur l'extinction ; autrement en charge, sur USB ou sans veille profonde), veille profonde par `bin/suspend` (appelé par `common/api.c:4469-4492`) : hooks `pre-sleep.d`, arrêt du Wi-Fi (`bin/suspend:16-36`), `echo mem` (`bin/suspend:69`) ; au réveil, Wi-Fi relancé s'il tournait (`bin/suspend:40-49`).
- Le serveur survit à la veille, pas ses connexions. Au signal du hook `post-resume.d` (§3), il rouvre son écoute et refait l'annonce Bonjour, avec de nouveaux essais bornés, même si l'adresse n'a pas changé. En secours (hook manquant, Wi-Fi changé sans veille), il relit l'adresse et l'état du lien de l'interface sans fil toutes les 10 s (lecture locale) et fait de même à tout changement. Les apps relisent `GET /device` à chaque reconnexion de leur flux d'événements.
- **Extinction** pendant un enregistrement de cheats : l'ancien fichier ou le nouveau, jamais un mélange, sous réserve du comportement réel d'exFAT à la coupure **[À VÉRIFIER]** (essais de coupure sur la console ; l'écriture atomique actuelle ignore les erreurs de synchronisation du dossier, à corriger, `internal/store/atomic.go`).
- Le Wi-Fi reste celui de NextUI : le serveur ne l'allume ni ne l'éteint. Lien gardé pendant une longue partie **[À VÉRIFIER]** sur la console.

## 8. Contrat et apps

**Contrat** (`protocol/openapi.yaml` d'abord) :

- `GET /hello` (non signé) : tous les champs actuels gardés, plus `mode` (`foreground` ou `background`), optionnel. **Rien sur l'activité** (aucune donnée de jeu dans `/hello`).
- `GET /device` (signé) : `mode` et `activity`, optionnels : `activity` vaut `{ state: menu | game | pak, gameId?, systemTag?, name? }` (`name` : le nom du jeu ; pour un pak, aucun nom n'est connu, l'app affiche « Une app est ouverte sur la console »).
- Nouvel événement `activity` (même contenu) dans le flux signé à chaque changement.
- `EventShutdown` : champ optionnel `restarting` (§5). Champs obligatoires et valeurs existants inchangés.
- Erreurs (03 §6) : `409 background_mode` (l'action demande le pak au premier plan) et `409 console_busy` (`details.state`).
- Réponse d'enregistrement des cheats : `appliesOnNextLaunch` optionnel (le jeu tourne).
- Compatibilité : `ios-v1.1.0` traite un code inconnu comme une erreur générique (`ErrorCode.other`, `APIError.swift`), ignore les clés et événements inconnus ; l'app Android fait de même dans le code (`ui/Errors.kt`), à vérifier sur la première version publiée. `minClientVersion` et `apiVersion` inchangés. Une app ancienne voit des erreurs génériques sur les actions refusées et revient à la liste à une relève : c'est acceptable pour une version **MINOR**, à confirmer en essayant les apps publiées (relève, veille, `409`) avant de fixer le numéro.

**Apps iOS et Android** (parité) :

- Tableau de bord : bandeau selon `mode` et `activity` : « En jeu : *nom du jeu* », « Une app est ouverte sur la console », ou « Mode arrière-plan : ouvre ReminisLink sur la console pour tout gérer ».
- En mode arrière-plan, les actions refusées sont désactivées avec la raison ; les cheats restent modifiables ; la notice du §1 après un enregistrement pour le jeu en cours.
- La sync des saves et les envois en file attendent le pak au premier plan (ils reprennent à la relève).
- Messages traduits pour `background_mode` et `console_busy`. Relève : reconnexion silencieuse.
- Aucun réglage dans l'app : le mode se règle sur la console.

## 9. Mesures et critères d'acceptation

Sur une TrimUI Brick (tg5040), serveur d'arrière-plan actif, sans app puis avec une app connectée et son flux d'événements :

- mémoire résidente du serveur : < 30 Mio ;
- CPU pendant un jeu : < 1 % en moyenne sur 10 min ;
- batterie : écart mesuré sur 1 h de jeu avec et sans le serveur, publié dans la spec ;
- aucune image perdue de plus qu'à vide sur un cœur exigeant, rembobinage activé ;
- après une veille profonde, console retrouvée par l'app en moins de 30 s ;
- relèves répétées (ouvrir et quitter ReminisLink, serveur qui ne répond pas) : un seul serveur à la fois, aucun rejeu accepté après une relève propre ;
- règles du §2 couvertes par des tests Go (processus `nextui.elf` et `minarch.elf` simulés, mode desktop) et par l'émulation de la CI ; sélecteur de jeux, reprise au démarrage et extinction essayés sur la console.

## 10. Plateformes

- **tg5040** d'abord (Brick, Smart Pro).
- **tg5050, h700, my355** : mêmes hooks attendus **[À VÉRIFIER]** dans leurs lanceurs.
- **Knulli** : hors de M24 (autre lanceur, pas ces hooks).

## 11. Sécurité (revue de 07)

- **Exposition** : réglage actif, le service écoute sur le Wi-Fi tant que la console est allumée, et non quelques minutes. Les mesures de 07 §3 restent : routes métier signées, TLS épinglé, appairage **fermé** en arrière-plan, `/hello` limité par IP, corps bornés. Le serveur n'écoute que sur l'adresse de l'interface sans fil (pas `0.0.0.0`).
- **Ce qu'un inconnu du réseau voit** : par Bonjour et `/hello`, le nom, l'identifiant stable, la version et le mode de la console, en permanence et sur tous les réseaux où elle se connecte (décision 1). Rien sur les jeux.
- **Ce qu'un client appairé peut faire** en arrière-plan : lire les métadonnées et écrire des cheats, rien d'autre.
- **Rejeu** : inchangé après une relève propre (cache de nonces passé) ; après un arrêt forcé, risque déjà accepté par 07.
- **Hooks** : lancés en root par NextUI, même niveau de confiance que le pak ; ils n'exécutent que le binaire du pak et ne sont pas modifiables par l'API (comme la socket de contrôle et le binaire, protégés du navigateur de la carte, 05 §3.4).
- 07 §2 (« Le pak n'écoute que lorsqu'il est ouvert ») est complété par ce mode.

## 12. Décisions à prendre

| # | Question | Recommandation |
|---|---|---|
| 1 | Sur quels réseaux le serveur d'arrière-plan écoute-t-il ? Tous, ou seulement les réseaux Wi-Fi mémorisés à l'activation | **Tous** : la sécurité repose sur les signatures ; un réseau public voit le nom, l'identifiant, la version et le mode de la console, rien de plus |
| 2 | Que permettre en arrière-plan ? Consultation et cheats seulement, ou aussi d'autres écritures | **Consultation et cheats seulement** : c'est le besoin, et c'est sûr quelle que soit la transition en cours. Élargir plus tard seulement avec un lanceur qui attend ReminisLink (hook bloquant à proposer à NextUI) |
| 3 | Cheats modifiés en même temps par ReminisLink et un autre pak (Cheat Manager) | **Refuser pendant un pak**, vérifier le contenu juste avant le renommage, et accepter la fenêtre restante (quelques microsecondes, un pak qui écrit le même `.cht` juste à ce moment) : une version pourrait alors être remplacée sans copie |
| 4 | Réglage par défaut | **Désactivé** : l'utilisateur l'active en connaissance de cause (batterie) |
| 5 | Rejeu après un arrêt forcé du serveur d'arrière-plan (cache de nonces perdu) | **L'accepter** comme le cas de relance déjà accepté par 07 : l'arrêt forcé est rare et la requête rejouée doit dater de moins de 300 s environ |
