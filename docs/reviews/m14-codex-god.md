Verdict : à corriger

## Bloquant

- [internal/server/events.go:447](/Users/tfournier/projets/github.com/tfournier/reminislink-m14-go/internal/server/events.go:447) **Un événement `device` peut laisser l’indicateur de charge périmé.** A ouvre le premier flux et lit `GET /device` alors que la console est débranchée. Avant que le premier relevé périodique ait lu la batterie, B se connecte. La lecture de secours déclenchée par cette arrivée envoie `charging: false` à A. L’utilisateur branche alors la console. Le premier relevé lit `charging: true`, mais l’enregistre comme référence sans notifier A. Si les relevés suivants restent stables, A continue d’afficher la console comme débranchée. Faire passer la lecture de secours et le premier relevé par la même mise à jour de référence, puis ajouter un test qui impose cet ordre.

Les deux corrections du tour précédent sont présentes dans `7a0c45d1`. La révocation marque les flux survivants et le délai de cinq secondes repart après le `Flush` réussi.

## Tests lancés

- `gofmt -l cmd internal protocol scripts` — aucun fichier signalé.
- `git diff --check main...HEAD` et `git diff --check HEAD~1...HEAD` — réussis.
- `scripts/sync-test-vectors.sh --check` — les copies Swift correspondent aux vecteurs partagés.
- `make lint test` et `make contract` — non lancés, car ces cibles écrivent dans les caches de compilation ou dans l’état Docker. Le diff ne touche pas iOS.

Contre-revue statique. La feuille de route et les spécifications SSE ont servi de référence en l’absence de prompt M14. La description de la PR est restée inaccessible faute d’authentification GitHub CLI.