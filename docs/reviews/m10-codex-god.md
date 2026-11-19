Verdict : à corriger

## Bloquant

- [internal/server/access.go:62](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-go/internal/server/access.go:62) **Une reconnexion envoyée avant une déconnexion peut encore l’annuler.** L’iPhone envoie un `POST /clients/me/connect` signé. Le réseau retarde la fin des en-têtes. La console déconnecte l’iPhone, puis le serveur reçoit les derniers octets et charge la clé avec la nouvelle génération. La requête passe les deux contrôles de génération, efface la déconnexion et reçoit `204`. La [spec modifiée par le commit](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-go/docs/specs/04-api-v1.md:86) prévoit `401 disconnected` pour un appel parti avant la déconnexion. Si la garantie voulue commence au chargement de la clé côté serveur, préciser cette borne dans la spec. Si elle doit commencer à l’envoi, lier la demande signée à une génération connue du client et couvrir le cas où les en-têtes arrivent après la déconnexion.

## À corriger

Aucun autre point dans le périmètre de cette contre-revue.

## Suggestions

Aucune. Les quatre scénarios du tour précédent sont corrigés à la lecture du code. L’ancienne clé est liée à sa génération, le `413` est envoyé avant la fermeture du corps, la reconnexion et la présence sont mises à jour sous le verrou qui valide la génération, et l’écran Appareils lit la présence sous ce même verrou.

## Tests lancés

- `git diff --check` sur `main...HEAD`, `m10-writer...HEAD` et `HEAD~1...HEAD` — aucune anomalie. Le lanceur Git système a tenté de créer un cache temporaire et le sandbox a refusé l’écriture. Les vérifications ont ensuite utilisé le binaire Git direct.
- `gofmt -l cmd internal protocol scripts` — aucun fichier signalé.
- `scripts/sync-test-vectors.sh --check` — les copies Swift correspondent aux vecteurs partagés.
- `make lint test` et `make contract` — non lancés, car ces commandes écrivent. `swift test` et `xcodebuild test` — non lancés, aucun fichier iOS dans le diff. Les nouveaux tests Go ont été relus.

Aucun prompt M10 n’est présent dans le dépôt. La contre-revue s’appuie sur les spécifications M10 et le commit `c5595b09`. La description de la PR est restée inaccessible.