# M7 — Interface de la console et publication (Go + Swift)

## À lire

`docs/specs/05-console-pak.md` §1, §2, §7, §8, §9, `docs/specs/09-feuille-de-route.md` (M7), `docs/specs/08-tests-qualite.md` §5 et §6. Dans `../nextui-cheat-manager` : `main.go` (usage de gabagool, clavier, listes, confirmations), `internal/l10n`, `Makefile`, `Dockerfile`, `scripts/setup-toolchain.sh`, `.github/workflows/ci.yml`, `pak.json`, `pak/launch.sh`.

## Côté Go

1. `internal/ui` avec gabagool v2 (comme Cheat Manager) :
   - écran principal `05 §7.1` : QR code (bibliothèque QR autorisée), PIN et expiration, nom et IP, état, appareils connectés, transferts et tâches en cours avec progression, bandeau de notification ;
   - écran sans Wi-Fi (`05 §2`) ;
   - écran « Préparation… » pendant la maintenance ;
   - réglages `05 §7.2` (nom de la console au clavier gabagool, appareils appairés, oublier tous les appareils avec confirmation, langue, version et chemin du journal) ;
   - confirmation de sortie si un transfert est en cours.
   - Le serveur tourne dans une goroutine ; l'UI lit son état par un canal ou un état protégé, jamais en bloquant les requêtes.
2. `internal/l10n` : anglais et français (structure de Cheat Manager, prête pour ses 7 langues).
3. Mode `--desktop` (`05 §9`) : même UI, fenêtre sur Mac, clavier.
4. Veille : applique la décision prise après M0 (si la veille coupe le Wi-Fi, empêcher la veille tant qu'un transfert ou une tâche est en cours, selon le mécanisme relevé).
5. **Packaging**, repris de Cheat Manager : `Makefile` (`build`, `pak`, `release`, `docker-*`, `vX.Y.Z`), `Dockerfile`, `scripts/setup-toolchain.sh`, `pak/launch.sh`, `pak.json` (`"name": "ReminisLink"`, `type` `TOOL`, `release_filename` `ReminisLink.pak.zip`, plateforme `tg5040` seulement en v1), CI GitHub Actions (lint, tests, compilation ARM64 dans `debian:bullseye`, publication sur tag ; job macOS pour `swift test` et `xcodebuild test`).
6. README : installation (Pak Store et manuelle), utilisation en 3 étapes, emplacements des fichiers (comme la section « Where are the files? » de Cheat Manager), captures d'écran (générées en mode desktop si possible).

## Côté iOS

1. Icône d'app et écran de lancement (proposer une icône simple et originale, sans élément d'une marque existante : pas de manette ou de console reconnaissable).
2. Textes App Store (FR et EN) : description qui insiste sur « apporte tes propres fichiers », aucune mention de source de jeux ; mots-clés ; texte de la politique de confidentialité (aucune donnée collectée, tout reste sur le réseau local) dans `docs/privacy.md`, publiable sur GitHub Pages.
3. Configuration de publication : numéros de version, `Config.xcconfig` pour le préfixe d'identifiants et l'équipe, archive pour TestFlight (instructions dans `ios/README.md` ; ne pas pousser vers App Store Connect toi-même).
4. Relecture des chaînes FR et EN de toute l'app.

## Fait quand

- `make docker-release` produit `dist/ReminisLink.pak.zip` qui s'installe à la main sur une Brick (`/Tools/tg5040/ReminisLink.pak/launch.sh`).
- La CI passe sur une branche, et un tag de test produit une release.
- L'app s'archive sans avertissement.
- Liste, dans ton rapport, la checklist console complète (`08 §5`) à faire par la personne avant le premier tag public.
