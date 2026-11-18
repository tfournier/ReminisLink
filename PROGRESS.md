# Avancement ReminisLink

## Jalons
| Jalon | Statut | Branche | Revue Antigravity | Intégré le |
|---|---|---|---|---|
| M0 | en attente des relevés de la console | — | — | — |
| M1 | intégré | `m1-contrat-crypto` | « prêt à fusionner » (Gemini 3.8 Flash High) — `docs/reviews/m1-antigravity.md` ; 3 revues Claude avant — `docs/reviews/m1-claude.md` | 2026-11-18 |
| M2 | en revue | `m2-serveur-lecture` | GPT-OSS 120B (Antigravity) : prêt à fusionner (3 remarques, faux positifs) ; revue Claude neuve : à corriger, 1 bloquant confirmé (journal de la carte SD remplissable par un client non authentifié), 4 à corriger (coût de `cheatCount`, échappement Bonjour, identifiants insensibles à la casse, erreur fatale non journalisée) ; aller-retour 1 fait (13 points + `connectedClients` + noms inventés), contrôles OK ; revue ciblée en cours | — |
| M3 | en revue | `m3-ios-base` | GPT-OSS 120B (Antigravity) : à corriger, 1 point retenu (IP de secours du QR limitée au réseau local), le reste faux positifs ; revue Claude neuve : à corriger, aucun bloquant (annulation de l'appairage, ré-appairage silencieux d'une console déjà appairée, session annulée laissée en erreur, `notPaired` sans action) ; aller-retour 1 fait et revu (aucun bloquant, 3 points d'annulation restants) ; aller-retour 2 en cours | — |
| M4 | à faire | — | — | — |
| M5 | à faire | — | — | — |
| M6 | à faire | — | — | — |
| M7 | à faire | — | — | — |

## En cours
- M2 : livré (12 commits). Contrôles §4 OK : `make lint test` (13 paquets), `make contract`, `go test -race`, 12 `TODO(M0)` isolés dans l'adaptateur et `device`. « Fait quand » vérifié par l'agent principal : `make headless`, annonce vue par `dns-sd -B`, `scripts/smoke.sh` OK, arrêt propre. Dépendances : `grandcat/zeroconf`, `skip2/go-qrcode` (autorisées) ; `golang.org/x/net`, `x/sys`, `x/crypto`, `miekg/dns` en indirect (tirées par zeroconf, `x/net` et `x/sys` montées pour compiler avec Go 1.27 ; accepté par l'agent principal). Revue Antigravity en cours.
- M3 : livré (5 commits). Contrôles §4 OK : `swift test` 73 tests, `xcodebuild test` iPhone 16 / iOS 18.5 réussi (test démo passé, test contre le pak réel ignoré tant que M2 n'est pas fusionné), diff limité à `ios/`. Revue Antigravity en cours. Vérification fonctionnelle (§5.3) faite le 2026-11-18 : test XCUITest contre le pak M2 en `make headless` **réussi** (lien d'appairage → `/pair` → `/device` → `/systems` → jeux → fiche de jeu ; `404` sur `/trash` attendu avant M4). Découverte Bonjour depuis le simulateur à confirmer (le test peut avoir utilisé l'IP du QR).
- M0 : en attente des relevés de la console.

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

## Questions ouvertes pour la personne
4. **Ré-appairage d'une console déjà appairée (sécurité, M3)** : 06 §4.2 dit qu'un lien `reminislink://pair` « lance directement l'appairage ». La revue a montré qu'un faux pak sur le même Wi-Fi peut ainsi remplacer la clé d'une console déjà appairée (console réelle éteinte, repli sur l'IP du QR). L'agent principal fait implémenter : confirmation quand la console est déjà appairée, et dans ce cas pas de repli sur l'IP du QR (Bonjour obligatoire) ; l'IP de secours n'est acceptée que si elle est privée ou de lien local. À valider, puis reporter dans 06 §4.2 et 03 §3.1.
1. Relevés M0 sur la console : `survey-1-before.txt`, `survey-2-after.txt` et `notes.md` rempli dans `docs/specs/m0/` (procédure dans `docs/specs/m0/README.md`).

Tranchées : premier commit de `main`, M1 avant M0, écart `DELETE /games/{gameId}`, CLI Antigravity installée, report des choix de M1 dans la spec (voir Décisions).

## Plus tard
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
