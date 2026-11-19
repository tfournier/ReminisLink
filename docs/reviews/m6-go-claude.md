# Revue M6 Go — agent Claude neuf

Pas de revue croisée Antigravity (quota Gemini épuisé ; GPT-OSS jugé trop peu fiable).

## Revue complète — à corriger, aucun bloquant
- Formats exacts : décodeur de référence indépendant (sémantique beat/Flips) identique octet pour octet sur 3000 essais IPS/BPS/UPS ; robustesse (bornes, aucune allocation tirée du patch, mémoire constante ≈ 450 Kio, aucun panic).
- À corriger : extension du nom de sortie non contrôlée ; ROM non remise en place si le renommage échoue en mode `replace` (et fenêtre de coupure de courant).
- Suggestions : `TargetCopy` à courte distance et RLE IPS lents, `Inspect` non annulable, `readFooter` et `io.EOF`, espace réservé, `.ips/.bps/.ups` listés comme jeux, troncature IPS qui allonge, champ `verified`, oracle de fuzzing différentiel.

## Aller-retour 1 et 2 (`ca1900d5`)
Tout corrigé : remise en place depuis la corbeille et marqueur d'installation en attente repris au lancement, copies par motifs, oracle différentiel (`reference_test.go`, `FuzzIPSReference`) qui a trouvé et fait corriger un saut UPS refusé à tort, `verified`, détails de `source-mismatch`, état `running` + `cancelRequested` jusqu'à la vraie fin d'une tâche annulée ; nom de sortie : extension de la ROM ajoutée si absente (jamais de refus). `make lint test`, `make contract`, `go test -race ./...`, `make fuzz` (6 cibles), `smoke-patch.sh` OK.
