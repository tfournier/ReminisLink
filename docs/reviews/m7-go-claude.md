# Revue M7 Go — agent Claude neuf

Pas de revue croisée Antigravity (quota Gemini épuisé).

## Revue complète — à corriger, aucun bloquant
- Écart spec/code : gabagool (avec `IsNextUI: true`) gère le bouton d'alimentation (appui court → `suspend`, Wi-Fi coupé ; appui long → `poweroff`). Tranché par la personne : comportement gardé, 05 §2 corrigé.
- Chemin SIGTERM/SIGINT : PNG du QR (secret) non supprimé à la sortie sur signal, gestionnaire désarmé pendant « Fermeture… », absent pendant « Préparation… ».
- Suggestions : bandeau « corbeille vidée » pour la suppression d'une seule entrée, `deviceID` lu hors verrou, `pakVersion` avec le `v` du tag, `ForceAttemptHTTP2` hérité, archive Go non vérifiée et image Debian non épinglée.
- Vérifié conforme : D1 (pool de `certifiable` utilisé, délais et règles conservés), D4, D6, aucun secret dans les journaux ni sur la carte (essai en headless + SIGTERM), cycle de vie, packaging, CI (permissions minimales, publication sur tag seulement), l10n.

## Aller-retour 1 (`3612b601`)
Arrêt sur signal à tout moment (protection des données d'abord : 503, horloge de crédit, `sync` ; puis attente, Bonjour, téléchargements), dossier du QR supprimé, suivi de l'adresse de `wlan0` toutes les 3 s (QR, lien et Bonjour mis à jour ; « Pas de Wi-Fi » sans quitter), bandeau dédié, `deviceID` immuable, version sans `v`, sha256 de l'archive Go et image épinglée. `make lint test`, `make contract`, `go test -race ./...`, tests `sdl` en conteneur, `make docker-release`, essai dans le rootfs du firmware OK.
