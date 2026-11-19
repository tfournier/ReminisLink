# Revues M5 iOS — agent Claude neuf

Pas de revue croisée Antigravity (quota Gemini épuisé ; GPT-OSS jugé trop peu fiable).

## Revue complète — à corriger
- **Bloquant (confirmé)** : `downloadSigned` perdait le corps d'erreur → toute erreur de téléchargement devenait « réponse inattendue » (`401 unknown_client` sans « Refaire l'appairage », `429`/`Retry-After` perdus).
- À corriger : message de déverrouillage inexact pour un state absent ou différent ; `lockStatus` inconnus affichés comme un simple cadenas ; messages 409/422 trompeurs ; « Réessayer » des cheats qui abandonnait les modifications ; clé « Save » ambiguë (FR) ; suppression d'une copie annoncée irrécupérable (elle part en corbeille) ; import par `upload` au lieu de `staging`.
- Vérifié conforme : `PUT /save` signé avec le SHA-256 calculé en flux, nonce neuf, téléchargements sur disque et noms nettoyés, `412` jamais contourné, `extra` conservé, numérotation 1–8 et reprise à part, code démo en Debug, chaînes EN/FR.

## Aller-retour 1 (`a82c49c3`)
13 points corrigés (dont le bloquant, testé sur le code d'erreur exact) ; `swift test` 173 tests, `xcodebuild test` 39 tests app + UI démo.

## Intégration contre le vrai pak M5 (agent principal, 2026-11-19)
Tests UI réels Save, Cheats, Verrous réussis (un lien d'appairage neuf par test) ; critère « Fait quand » : state verrouillé écrasé à la main → restauré au lancement suivant, l'app affiche « Restauré », state écrasé en corbeille.
