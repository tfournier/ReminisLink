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
| M6 | en cours | `m6-patchs-go`, `m6-patchs-ios` | — | — |
| M7 | à faire | — | — | — |

## En cours
- M5 fusionné dans `main` le 2026-11-19. Sur `main` : `make lint test`, `make contract`, `go test -race ./...`, `swift test` (173), `xcodebuild test` (39 tests app, UI démo) verts. Contre le pak headless : tests UI Save, Cheats, Verrous réussis ; critère « Fait quand » vérifié (state verrouillé écrasé à la main → restauré au lancement, « Restauré » affiché, state écrasé en corbeille).
- M6 : deux sous-agents lancés le 2026-11-19 : Go dans `../reminislink-m6-go` (seul autorisé à modifier `protocol/`), iOS dans `../reminislink-m6-ios`.
- **En attente de la personne** : décisions D1–D6 (question 8) ; tests sur console (checklist, scénarios 3, 4, 5, 7, 8, 9, 10) et les 3 relevés M0 restants.

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

## Questions ouvertes pour la personne
8. **Décisions issues de M0** (détail et options dans `docs/specs/m0/questions.md`, D1 à D6). Recommandations de l'agent principal :
   - D1 certificats racine (aucun sur la console) : autoriser `certifiable` en dépendance directe (déjà tirée par gabagool, même mécanisme que Cheat Manager).
   - D2 veille : rien ne met la console en veille pendant un pak → pas de prévention à coder ; en M7, éteindre l'écran après 2 min sans interaction (rallumé par une touche), le pak reste joignable.
   - D3 formats `saveFormat`/`stateFormat` non par défaut : v1 = format par défaut seulement ; autres formats listés en lecture seule, écritures refusées avec une erreur claire.
   - D4 `osVersion` : renvoyer la chaîne de `.system/version.txt` (ex. `NextUI-20261112-1`) et corriger les exemples du contrat.
   - D5 vignettes : servies telles quelles (PNG 1024×768, cache ETag), réduction faite par l'app.
   - D6 `/systems` : lister tous les dossiers à tag (comme aujourd'hui) avec un champ `emulatorInstalled` (ajout au contrat).
1. Relevés M0 sur la console : `survey-1-before.txt`, `survey-2-after.txt` et `notes.md` rempli dans `docs/specs/m0/` (procédure dans `docs/specs/m0/README.md`).

Tranchées : premier commit de `main`, M1 avant M0, écart `DELETE /games/{gameId}`, CLI Antigravity installée, report des choix de M1 dans la spec (voir Décisions).

## Plus tard
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
