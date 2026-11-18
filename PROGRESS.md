# Avancement ReminisLink

## Jalons
| Jalon | Statut | Branche | Revue Antigravity | Intégré le |
|---|---|---|---|---|
| M0 | en attente des relevés de la console | — | — | — |
| M1 | intégré | `m1-contrat-crypto` | « prêt à fusionner » (Gemini 3.8 Flash High) — `docs/reviews/m1-antigravity.md` ; 3 revues Claude avant — `docs/reviews/m1-claude.md` | 2026-11-18 |
| M2 | intégré | `m2-serveur-lecture` | GPT-OSS 120B : prêt à fusionner (faux positifs) — `docs/reviews/m2-antigravity.md` ; 2 revues Claude (2 bloquants corrigés) — `docs/reviews/m2-claude.md` | 2026-11-18 |
| M3 | intégré (appareils connectés à afficher, avec M4) | `m3-ios-base` | GPT-OSS 120B : 1 point retenu — `docs/reviews/m3-antigravity.md` ; 2 revues Claude (aucun bloquant, corrigées) — `docs/reviews/m3-claude.md` | 2026-11-18 |
| M4 | Go et iOS livrés, en revue | `m4-transferts-go`, `m4-transferts-ios` | Go : GPT-OSS « prêt à fusionner » ; revue Claude « à corriger », 1 bloquant confirmé (état SHA-256 en avance sur les données après coupure → fichier corrompu installé), allers-retours 1 et 2 faits ; revue ciblée n° 2 : 1 bloquant (purge sur horloge fausse mais plausible) → question 7 à la personne ; aller-retour 3 partie 1 (points hors purge) fait, contrôles OK ; partie 2 (règle de purge) en attente de la question 7. iOS : revue Claude « à corriger », aucun bloquant (issue incertaine perdue entre essais, envois démarrés en arrière-plan, pause non levée, promesse de copie de save non tenue → type Save masqué jusqu'à M5, demande partagée rouverte, doublons) ; allers-retours 1 et 2 faits et revus, contrôles OK (135 tests Kit, 30 tests app, UI démo) : **prêt** | — |
| M5 | à faire | — | — | — |
| M6 | à faire | — | — | — |
| M7 | à faire | — | — | — |

## En cours
- M2 et M3 fusionnés dans `main` le 2026-11-18. Sur `main` : `make lint test`, `make contract`, `go test -race`, `swift test` (81), `xcodebuild test` (18 tests app, 3 tests UI dont le parcours réel contre `make headless` : appairage par lien → tableau de bord → bibliothèque → fiche de jeu) tous verts.
- M4 : intégration Go + iOS testée le 2026-11-18 dans un worktree temporaire `../reminislink-m4-integ` : contre `make headless`, appairage trouvé **par Bonjour depuis le simulateur** (plus de repli IP) et parcours « envoi de 200 Mio (50 morceaux) → bibliothèque → suppression → vidage de la corbeille » réussis. Côté Go : `smoke-transfers.sh` (reprise après SIGINT à 100 Mio, téléchargement local, corbeille) OK. Non testé de bout en bout depuis l'app : coupure du pak pendant l'envoi puis reprise, et ajout par URL (couverts par les tests Go et le faux pak iOS).
- M4 (historique) : deux sous-agents lancés le 2026-11-18 : Go dans `../reminislink-m4-go` (seul autorisé à modifier `protocol/`), iOS dans `../reminislink-m4-ios` (uniquement `ios/`, faux pak étendu ; affiche aussi `connectedClients`).
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

- 2026-11-18 — Appairage : aucun repli sur l'IP du QR code (`h`/`p` informatifs seulement) ; console introuvable par Bonjour = erreur. Confirmation avant de ré-appairer une console déjà appairée. Specs 02, 03 §3.1 et 06 §4.2 mises à jour. Décidé par la personne. L'app (M3 autorisait le repli pour une nouvelle console) est adaptée dans le lot iOS de M4.

- 2026-11-18 — Les envois en cours survivent à un redémarrage du pak (03 §7 mis à jour, `openapi.yaml` à aligner par l'agent Go) ; avec `keepSave=true`, `DELETE /games` garde aussi les verrous et l'historique de reprise (04 mis à jour). Décidé par la personne.

- 2026-11-18 — Purge de la corbeille par **âge crédité** (horloge propre du pak : monotone pendant les sessions, au plus 7 jours crédités par période d'extinction ; purge à 30 jours) ; `expiresAt` devient une estimation « au plus tôt ». Specs 04, 05 §2/§4, 06 mises à jour. Décidé par la personne (option B recommandée).

## Questions ouvertes pour la personne
1. Relevés M0 sur la console : `survey-1-before.txt`, `survey-2-after.txt` et `notes.md` rempli dans `docs/specs/m0/` (procédure dans `docs/specs/m0/README.md`).

Tranchées : premier commit de `main`, M1 avant M0, écart `DELETE /games/{gameId}`, CLI Antigravity installée, report des choix de M1 dans la spec (voir Décisions).

## Plus tard
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
