# Avancement ReminisLink

## Jalons
| Jalon | Statut | Branche | Revue Antigravity | Intégré le |
|---|---|---|---|---|
| M0 | intégré (variante code source) ; 3 vérifications console restantes | `m0-specs-source` | — (documentation ; preuves par fichier:ligne) | 2026-11-19 |
| M1 | intégré | `m1-contrat-crypto` | « prêt à fusionner » (Gemini 3.8 Flash High) — `docs/reviews/m1-antigravity.md` ; 3 revues Claude avant — `docs/reviews/m1-claude.md` | 2026-11-18 |
| M2 | intégré | `m2-serveur-lecture` | GPT-OSS 120B : prêt à fusionner (faux positifs) — `docs/reviews/m2-antigravity.md` ; 2 revues Claude (2 bloquants corrigés) — `docs/reviews/m2-claude.md` | 2026-11-18 |
| M3 | intégré (appareils connectés à afficher, avec M4) | `m3-ios-base` | GPT-OSS 120B : 1 point retenu — `docs/reviews/m3-antigravity.md` ; 2 revues Claude (aucun bloquant, corrigées) — `docs/reviews/m3-claude.md` | 2026-11-18 |
| M4 | intégré (tests console à faire) | `m4-transferts-go`, `m4-transferts-ios` | Go : GPT-OSS « prêt à fusionner » (a manqué le bloquant) — `docs/reviews/m4-go-antigravity.md` ; 3 revues Claude (2 bloquants corrigés, règle de purge tranchée par la personne) — `docs/reviews/m4-go-claude.md`. iOS : 2 revues Claude (aucun bloquant) — `docs/reviews/m4-ios-claude.md` | 2026-11-18 |
| M5 | intégré (tests console à faire) | `m5-saves-go`, `m5-saves-ios` | Go : 2 revues Claude (2 bloquants corrigés) — `docs/reviews/m5-go-claude.md` ; iOS : 1 revue Claude (1 bloquant corrigé) — `docs/reviews/m5-ios-claude.md` ; pas de revue croisée (quota Gemini) | 2026-11-19 |
| M6 | intégré (test console à faire) | `m6-patchs-go`, `m6-patchs-ios` | Go : revue Claude, aucun bloquant, formats validés par un décodeur de référence indépendant — `docs/reviews/m6-go-claude.md` ; iOS : revue Claude, 1 bloquant corrigé — `docs/reviews/m6-ios-claude.md` ; pas de revue croisée (quota Gemini) | 2026-11-19 |
| M10 | intégré (oubli + réappairage validés sur console ; Déconnecter et prise de main à deux iPhones à tester) | `m10-writer` (contrat), `m10-go`, `m10-ios`, `m10-int` | Codex : 4 tours Go (`docs/reviews/m10-codex-go*.md`, dernier point = précision de spec), 4 tours iOS (`docs/reviews/m10-codex-ios*.md`), derniers correctifs iOS vérifiés par l'agent principal ; `scripts/smoke-writer.sh` (2 clients contre le pak headless, expiration incluse) et test UI réel lecture seule → prise de main verts | 2026-11-19 |
| M9 | intégré (à tester sur iPhone ; conteneur iCloud à créer chez Apple) | `m9-sync-go`, `m9-sync-ios` | Codex : 3 tours (`docs/reviews/m9-codex*.md`), derniers points vérifiés par l'agent principal ; test UI réel (sync complète, incrémentale, restauration) vert | 2026-11-19 |
| M8 | intégré (à tester sur console et iPhone) | `m8-retours-go`, `m8-retours-ios` | Codex : 16 bloquants M3–M7 corrigés, puis 4 tours sur M8 (`docs/reviews/m8-codex*.md`) ; 1 risque accepté (07 §3) ; tests UI réels contre le pak headless verts | 2026-11-19 |
| M7 | intégré (sans compte Apple ni Pak Store ; CI à valider après push ; checklist console à faire) | `m7-ui-publication-go`, `m7-publication-ios` | Go : revue Claude, aucun bloquant — `docs/reviews/m7-go-claude.md` ; iOS : vérifié par l'agent principal — `docs/reviews/m7-ios-claude.md` | 2026-11-19 |

## En cours
- **M10 fusionné le 2026-11-19** : bail d'écriture en mémoire (un seul appareil écrit, 60 s sans signe de vie, renouvelé toutes les ~20 s par l'app au premier plan ou pendant un transfert), `423 read_only` sur les 23 routes d'écriture avant lecture du corps, `PUT/GET/DELETE /writer`, `POST /clients/me/connect`, `401 disconnected` ; console : Réglages > Appareils (Déconnecter, Oublier) ; app : bandeau « Lecture seule », « Prendre la main », actions d'écriture désactivées, envois mis en pause (jamais perdus), annulation définitive seulement après confirmation du pak. Écart à la spec choisi par l'agent principal : `/hello` ne donne que `writerHeld` (pas le nom). Le réappairage depuis la console lève aussi une déconnexion. Test de la personne : Oublier + réappairer OK.
- **M9 fusionné le 2026-11-19** : `GET /saves` (hachage de chaque save ≤ 1 Mio à chaque liste, cache racy au-delà, `os.Root`), `GET /games/{id}/save/rtc` ; app : coffre versionné (10 versions, ordre de publication) dans iCloud Drive ou sur l'iPhone, sync au bouton et à la connexion, restauration vérifiée par SHA-256 (le `.rtc` de la console est conservé). Limites : pas de restauration du `.rtc`, pas de migration local → iCloud, une save > 1 Mio réécrite sans changer date ni taille sans ctime fiable n'est pas détectée. À faire par la personne : conteneur `iCloud.link.reminis.app` (et `.dev` pour les tests) sur l'App ID. Décision du 2026-11-19 : M10 = verrou d'écriture façon KVM (09).
- **M8 fusionné le 2026-11-19** : appairage à la demande (Réglages > Appairer un nouvel appareil), image de jeu (`PUT/DELETE /games/{id}/art`), noms NFC, état en haut à gauche et batterie en haut à droite, aperçu exact de suppression (`GET /games/{id}/deletion`), type `folder-cue`, remplacements journalisés (`Trash.Replace`) et corrections des revues Codex M3–M7. À vérifier sur console : QR via memfd, valeurs de batterie, image visible dans NextUI, jeu `<Jeu>/<Jeu>.cue`, nom NFD. Décision du 2026-11-19 : course « deux clients pendant une suppression » acceptée comme risque résiduel (07 §3).
- **Code de la v1 terminé** : M0 à M7 fusionnés dans `main` le 2026-11-19. Sur `main` : `make lint test`, `make contract`, `go test -race ./...`, `make fuzz`, `swift test` (193), `xcodebuild test` (59 tests app + UI démo) verts ; `make docker-release` → `dist/ReminisLink.pak.zip` (essayé dans le rootfs du firmware). Parcours réels contre le pak headless vérifiés à chaque jalon (appairage par Bonjour, envoi 200 Mio, suppression/corbeille, saves, cheats, verrou « Restauré », patchs).
- `ios/Config/Local.xcconfig` (ignoré par Git) contient l'équipe de signature fournie par la personne ; identifiant `link.reminis.app`.
- Reste à la personne : checklist console complète (`docs/specs/checklists/console.md`) et les 3 relevés M0 ; pousser `main` pour valider la CI puis un tag de test ; licence ; compte de publication iOS (vendeur affiché) et TestFlight ; re-signature GPG de l'historique avant le premier push ; revue croisée Antigravity des jalons M3–M7 quand le quota Gemini revient (vers le 2026-11-25).

## État du dépôt (2026-11-18)
- Branche `main` sans aucun commit : tous les fichiers sont non suivis (`CLAUDE.md`, `README.md`, `docs/`, `go.mod`, `prompts/`, `scripts/`, `skills/`, `.gitignore`).
- Aucun code Go ni iOS, pas de `Makefile`, pas de `protocol/`, pas de `testdata/`.
- Les worktrees du processus (§3 de l'orchestrateur) exigent un premier commit sur `main`.
- `skills/system/run-workflow/SKILL.md` ne concerne pas le projet (outil `run_workflow` d'un autre environnement) : à confirmer avant de le commiter.
- Projet de référence présent : `../nextui-cheat-manager`.

## Outils (2026-11-18)
| Outil | Résultat |
|---|---|
| `go version` | go1.27.4 darwin/arm64 |
| `docker --version` | 29.7.2, démon actif |
| `xcodebuild -version` | Xcode 27.2 (27B52) |
| `swift --version` | Swift 6.4.2 |
| `make --version` | GNU Make 3.81 |
| `agy --version` | 1.5.11 (`~/.local/bin/agy`), installée le 2026-11-18 ; modèles Gemini 3.8 Flash |
| Simulateurs iOS | runtimes iOS 18.3 et 18.5 ; simulateur « iPhone 16 » (iOS 18.5) créé le 2026-11-18 ; runtimes iOS 26.x indisponibles |

Conséquence : tant que `agy` est absent, la revue croisée se fait avec un sous-agent Claude neuf (revue non croisée, à signaler à chaque jalon).

## Décisions
- 2026-11-18 — `PROGRESS.md` créé à la racine, non commité (le dépôt n'a pas encore de premier commit). Agent principal.
- 2026-11-18 — Premier commit de `main` **sans** `skills/` (skill système `run_workflow` d'un autre environnement, sans rapport avec le projet) ni `.claude/settings.local.json` : les deux sont ajoutés au `.gitignore`. `PROGRESS.md` est commité. Proposé par Claude (session de cadrage) ; considéré comme validé sauf objection de la personne.
- 2026-11-18 — Écart `DELETE /games/{gameId}` tranché : **tout** part dans la corbeille (ROM, save, states, cheats, jaquette) par déplacement ; nouvelles routes `GET /trash`, `DELETE /trash`, `DELETE /trash/{entryId}` (en-tête `X-RL-Confirm: empty-trash`), seules à effacer définitivement. Specs 04, 05, 06, 07, 09 et prompt M4 mis à jour. Proposé par Claude (session de cadrage) ; considéré comme validé sauf objection de la personne.
- 2026-11-18 — Premier commit de `main` fait (`bb3a311`). Agent principal.
- 2026-11-18 — Commits **non signés** pendant le développement (clé GPG sur YubiKey avec PIN, personne absente) : `commit.gpgsign=false` et `tag.gpgsign=false` en config **locale** du dépôt (la config globale n'est pas modifiée). À la fin : retirer ces deux réglages (`git config --local --unset ...`) et re-signer l'historique. Décidé par la personne.
- 2026-11-18 — M1 peut démarrer sans attendre M0 (aucune dépendance aux points « À VÉRIFIER M0 »). Proposé par Claude (session de cadrage) ; considéré comme validé sauf objection de la personne.

- 2026-11-18 — Spec mise à jour avec les choix de M1 (03 §4.2, §4.3, §5, §6 ; 04 ; 07) et `CLAUDE.md` (commandes `make contract`, `make vectors`). Doublons de clés de requête refusés (`400`), à implémenter en M2. Rejeu après redémarrage du pak : risque résiduel accepté (07). Décidé par la personne (« oui pour tout »).
- 2026-11-18 — `agy` installé ; `Bash(make *)` autorisé par la personne. Revues Antigravity : `agy -p` sans `--mode plan` (qui l'embrouille) et sans approbation automatique, avec une liste blanche de commandes en lecture seule passée par `--settings` (règles `command(<préfixe>)`), une commande à la fois. Si Gemini Flash High est saturé (503), repli sur Medium.
- 2026-11-18 — Quota Gemini d'Antigravity épuisé (réinitialisation vers le 2026-11-25). Revues croisées M2/M3 faites avec GPT-OSS 120B via `agy` ; leur qualité est faible (beaucoup de faux positifs, vérifiés un par un par l'agent principal), donc chaque jalon reçoit aussi une revue par un agent Claude neuf. Agent principal.
- 2026-11-18 — La personne demande de continuer sans s'arrêter tant que possible : M1 intégré, puis M2 et M3 lancés en parallèle sans attendre un accord de fin de jalon ; arrêts maintenus pour les décisions de spec, de contrat, de sécurité, la console et l'échec répété d'un sous-agent.

- 2026-11-18 — `GET /device` gagne `connectedClients: [{ clientName, lastSeenAt }]` (en mémoire, requête signée de moins de 30 s) pour les « appareils connectés » de 06 §4.3 ; noms de jeux commerciaux remplacés par des noms inventés dans les specs, le contrat et les vecteurs. Décidé par la personne. Specs mises à jour sur `main` ; contrat, vecteurs et serveur confiés à l'agent M2 ; affichage dans l'app après fusion de M2.

- 2026-11-18 — Appairage : aucun repli sur l'IP du QR code (`h`/`p` informatifs seulement) ; console introuvable par Bonjour = erreur. Confirmation avant de ré-appairer une console déjà appairée. Specs 02, 03 §3.1 et 06 §4.2 mises à jour. Décidé par la personne. L'app (M3 autorisait le repli pour une nouvelle console) est adaptée dans le lot iOS de M4.

- 2026-11-18 — Les envois en cours survivent à un redémarrage du pak (03 §7 mis à jour, `openapi.yaml` à aligner par l'agent Go) ; avec `keepSave=true`, `DELETE /games` garde aussi les verrous et l'historique de reprise (04 mis à jour). Décidé par la personne.

- 2026-11-18 — Purge de la corbeille par **âge crédité** (horloge propre du pak : monotone pendant les sessions, au plus 7 jours crédités par période d'extinction ; purge à 30 jours) ; `expiresAt` devient une estimation « au plus tôt ». Specs 04, 05 §2/§4, 06 mises à jour. Décidé par la personne (option B recommandée).

- 2026-11-19 — Décisions M0 (personne) : D1 certificats via `certifiable` déjà importé par gabagool (aucune nouvelle dépendance, téléchargements depuis `http.DefaultTransport.Clone()`) ; D2 comportement normal de la console (aucune gestion de veille ni d'écran) ; D3 format par défaut seulement ; D4 `osVersion` = chaîne de release NextUI (`NextUI-20261013-039a06de`) ; D5 vignettes telles quelles ; D6 champ `emulatorInstalled` dans `/systems`.

- 2026-11-19 — Bouton d'alimentation pendant le pak : comportement normal de gabagool gardé (appui court = veille, appui long = extinction) ; 05 §2 corrigé. Décidé par la personne.

- 2026-11-19 — Icône de l'app (deux maillons et traînée de pixels, `ios/Design/`) validée par la personne.

- 2026-11-19 — Identifiant iOS `link.reminis.app` (extension `.share`, groupe `group.link.reminis.app` ; domaine `reminis.link` commandé par la personne), appliqué sur `m7-publication-ios` ; l'équipe de signature fournie par la personne va dans `ios/Config/Local.xcconfig` (ignoré par Git), à appliquer après la fusion de M7. Rappel donné : le vendeur affiché sur l'App Store est le compte qui publie.

- 2026-11-19 — Licence MIT (personne). Site `site/` (Accueil, Confidentialité, Assistance, FR/EN, sans traqueur ; GitHub Pages + `reminis.link`) fusionné ; URL pour Apple : `https://reminis.link/privacy/` et `https://reminis.link/support/` ; e-mail de contact à fournir par la personne.
- 2026-11-19 — Pak de test construit (`dist/ReminisLink.pak.zip`, version `6d09a9f5`) et vérifié dans le rootfs du firmware ; app installée sur l'iPhone de la personne avec l'identifiant de test `link.reminis.app.dev` (dans `ios/Config/Local.xcconfig`, ignoré par Git) pour garder `link.reminis.app` libre pour le compte de publication. Xcode réécrit `project.pbxproj` (équipe) et les catalogues de chaînes lors d'une construction signée : ne jamais commiter ces changements.
- 2026-11-19 — Gemini CLI 0.69.0 installé ; clé AI Studio dans le Trousseau (`reminislink-gemini-api-key`, créée par la personne). Niveau gratuit : Gemini 3.1 Pro indisponible (quota 0), `gemini-3-flash-preview` fonctionne. La personne a d'abord collé une clé dans la conversation : non utilisée, remplacement conseillé.

- 2026-11-19 — Revues croisées par Codex CLI 0.170.0 (compte ChatGPT Plus de la personne, connexion faite par elle), en lecture seule (`-s read-only`) dans `../reminislink-review-codex` : M4, M3, M5, M6, M7 lancées en séquence.

- 2026-11-19 — Essais de la personne sur la vraie console : appairage, Bonjour (avahi actif), tableau de bord, bibliothèque, save, states, cheats, envoi d'une ROM, patch UPS : aucun bug. Remarques retenues pour M8 (décisions de la personne) : QR code masqué une fois appairé, menu « Appairer un nouvel appareil », appairage ouvert seulement pendant l'affichage ; image d'un jeu définissable depuis l'app (`PUT`/`DELETE /games/{gameId}/art`) ; noms en NFC avec `golang.org/x/text` (autorisé, ajouté à `CLAUDE.md`) ; pastille qui chevauche le titre ; progression d'envoi fluide. Idée v1.1 notée en 09 : interface web servie par le pak.

- 2026-11-19 — Revues Codex M3–M7 rendues (qualité élevée : 16 bloquants annoncés, surtout fenêtres de coupure de courant et cas limites de perte de données ; 2 vérifiés par l'agent principal : redirections HTTP suivies par l'app, disques partagés entre playlists `.m3u`). M8 lancé : deux sous-agents (Go, iOS), chaque point Codex vérifié avant correction.

- 2026-11-19 — Fonction Sync (M9, après M8), décisions de la personne : saves seulement, bouton + automatique à la connexion (désactivable), iCloud Drive (dossier « ReminisLink » dans Fichiers), 10 versions ; principe « Local uniquement » de 01 adapté ; 09 complété (M8, M9).

- 2026-11-19 — Écran du pak (demande de la personne) : état en haut à gauche, batterie en haut à droite (alerte sous 15 % pendant un transfert) ; lecture de la batterie implémentée en M8 (`GET /device` ne renvoie plus `null`). Ajouté au lot M8 Go.

- 2026-11-19 — M10 : Codex a dépassé 3 tours ; la personne a choisi « corriger + 4e tour » (Go), règle appliquée aussi à iOS ; le dernier correctif iOS (annulation non confirmée) a été relu par l'agent principal sans 5e tour.
- 2026-11-19 — Suggestions de la personne après essai de M10, proposées en M11 « Ergonomie » : toucher l'image pour la changer ; télécharger chaque fichier d'un jeu ; bibliothèque à plat avec recherche et filtres ; barre d'onglets (Console, Bibliothèque, Transferts, Corbeille) ; plus d'envoi depuis la vue console ; retour à la liste des consoles quand le pak se ferme.

## Questions ouvertes pour la personne

1. Relevés M0 sur la console : `survey-1-before.txt`, `survey-2-after.txt` et `notes.md` rempli dans `docs/specs/m0/` (procédure dans `docs/specs/m0/README.md`).

Tranchées : premier commit de `main`, M1 avant M0, écart `DELETE /games/{gameId}`, CLI Antigravity installée, report des choix de M1 dans la spec (voir Décisions).

## Plus tard
- M7 Go : `github.com/BurntSushi/toml` en dépendance directe, mais seulement dans `internal/l10n/l10n_test.go` (comme Cheat Manager ; déjà tirée par gabagool) ; `go-sdl2` en direct (dépendance de gabagool) — acceptés par l'agent principal.
- M7 Go : batterie et SSID toujours `null` (sources confirmées en 05 §6) ; cosmétique gabagool (pastille, « : » sans espace insécable, PIN sous le pli quand des transferts s'affichent).
- `make desktop` sur le Mac demande `brew install pkg-config sdl2 sdl2_ttf sdl2_image sdl2_gfx` (non installé).
- Images et volumes Docker laissés par M7 : `reminislink-build`, `reminislink-screenshots`, `reminislink-shots`, `rhysd/actionlint`, volumes `reminislink-*`.
- M6 Go : les `.ips`/`.bps`/`.ups` posés sous `Roms/` sont listés comme des jeux (`ignoredExt`) — vérifier si NextUI les affiche aussi.
- CI absente du dépôt : y brancher `make fuzz` en mode court (M7).
- 2026-11-19 : 4 processus `reminislink-host --headless` laissés par l'agent principal pendant M3/M4 (commande d'arrêt visant un mauvais nom de processus) ; arrêtés. Arrêter désormais par PID.
- M5 Go : fichier `.rtc` des saves non sauvegardé/restauré ni emporté par `DELETE /games` ; formats NextUI non par défaut seulement refusés (pas listés) ; jaquette d'un multi-disques au nom avec point (`TODO(M0)`).
- 2026-11-19 : interruption par limite d'utilisation (~40 min) pendant M5 ; agents repris sans perte.
- Tests UI contre le pak headless : chaque test consomme le lien d'appairage (secret à usage unique) ; lancer plusieurs tests réels d'affilée demande un lien neuf par test — écrire `scripts/run-headless-uitests.sh` (08 §3) qui relance le pak ou relit le lien entre deux tests.
- M4 iOS : ouverture de l'app depuis l'extension de partage via `openURL` remonté par la chaîne des répondeurs (pas d'API publique) — risque en revue App Store, à valider avant M7.
- M4 iOS : table extension → tag NextUI pour suggérer le système (`TODO(M0)`).
- M4 Go : certificats racine HTTPS — magasin système, `$REMINISLINK_CA_BUNDLE`, sinon `/etc/ssl/certs/ca-certificates.crt` (`TODO(M0)`) ; si absent sur la console : autoriser `certifiable` ou embarquer un bundle dans le pak (M7).
- M2 : `github.com/miekg/dns` passe en dépendance directe (déjà tirée par zeroconf), utilisée seulement par un test d'échappement du nom Bonjour ; accepté par l'agent principal.
- M2 : les `.bin` référencés par un `.cue` (et non par le `.m3u`) restent listés comme jeux : à revoir au M0.
- M2 : `make headless` laisse ses copies dans `$TMPDIR/reminislink-sd.*`.
- M2 : le vecteur `get-query-sorted-and-encoded` contient une clé en double (`a=2&a=10`) : refusé en HTTP (`400`) depuis M2 ; envisager un vecteur HTTP sans doublon.
- Revue Antigravity M1 : dans `byteBudget.acquire`, remettre à `nil` la case libérée de `waiters` après retrait (mineur).
- Revue Antigravity M1 : l'app doit toujours construire ses URL avec `Signer.encodeQuery` (à vérifier en M3).
- Revue M1 : imposer la même horloge au `Verifier` et au cache de nonces (constructeur unique).
- M2 : refuser les clés de requête en double (`400 invalid_request`, 03 §4.2).
- M2 : `ReadHeaderTimeout`, délai d'inactivité de lecture, `BaseContext` annulé à l'arrêt ; brancher `Require(RouteSave)` et `Require(RouteChunk)`.
- Revue M1 (pour M2) : vérifier qu'un `folderId` appartient au `systemId` de la route.
- Revue M1 : NFC/NFD non normalisés dans les identifiants — à vérifier en mode desktop sur macOS (noms souvent en NFD).
