# Revues M4 iOS — agents Claude neufs

Pas de revue croisée Antigravity pour ce côté (quota Gemini épuisé ; GPT-OSS jugé trop peu fiable sur M2/M3).

## Revue complète (`9455870d`) — à corriger, aucun bloquant
- « Issue incertaine » perdue d'un essai à l'autre (doublon, faux 409 ou copie en corbeille après une réponse de `complete` perdue).
- Envoi suivant démarré en arrière-plan ; aller-retour rapide laissant l'envoi en pause.
- Envoi de save promettant une copie de sauvegarde que le pak M4 ne fait pas → type « Save » masqué jusqu'à M5.
- Demande partagée rouverte en boucle ; doublons après un échec d'envoi multiple.
- Vérifié conforme : `UNSIGNED` limité aux morceaux, nonce neuf à chaque essai, SHA-256 en flux, reprise à l'offset confirmé, aucun repli `h`/`p`, `X-RL-Confirm`, code démo en Debug seulement.

## Revue ciblée (`eaa9e645`) — à corriger, aucun bloquant
- Demande partagée partiellement consommée purgée à tort sur iPhone (`/private`), fenêtre d'envoi en arrière-plan entre `pump` et `process`, total de la corbeille faux sans rechargement.

## Aller-retour 2 (`76be2a64`) et ajustements (`57d6b2ab`)
Corrections faites et testées (135 tests Kit, 30 tests app, UI démo, build Release) ; expiration de la corbeille affichée « au plus tôt ». Intégration contre le vrai pak M4 : appairage par Bonjour, envoi de 200 Mio en 50 morceaux, suppression et vidage de la corbeille réussis.
