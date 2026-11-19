Verdict : à corriger

## Bloquant

- [internal/server/writer.go:125] **Une prise de main en cours peut rétablir le bail après « Déconnecter » ou « Oublier ».** Un `PUT /writer` franchit le contrôle d’accès, puis attend son corps. Pendant cette attente, la console peut retirer le bail et déconnecter ou oublier l’appareil. Lorsque le corps arrive, le handler acquiert quand même le bail. Les autres appareils reçoivent alors `423` avec le nom d’un appareil déconnecté, ou un nom vide si l’appareil a été oublié. Revérifier l’appairage et la déconnexion au moment de l’acquisition, dans la même section critique que les opérations de révocation. Appliquer cette synchronisation aussi à l’acquisition implicite dans `gate`.
- [internal/auth/middleware.go:297] **Le serveur HTTP peut lire le corps avant d’envoyer `423`.** Sans `Expect: 100-continue`, sur une connexion HTTP/1.1 persistante, `net/http` peut vider un petit corps avant d’émettre la réponse. Si l’appareil en lecture seule envoie les en-têtes d’un petit `PUT /save` puis tarde à envoyer le corps, le `423` attend aussi. Le test avec `httptest.ResponseRecorder` ne couvre pas ce comportement. Configurer le chemin de rejet pour répondre sans drainage préalable, puis vérifier sur une vraie connexion que le `423` arrive alors que le corps est encore retenu côté client.
- [internal/server/writer.go:69] **Une écriture rejetée peut prendre ou prolonger le bail.** Si le bail est libre, un `PUT /save` dont la signature des en-têtes est valide mais dont le corps échoue à la vérification SHA-256 reçoit `401 bad_signature`. L’appelant détient pourtant le bail pendant 60 secondes, et l’écriture valide d’un autre appareil reçoit `423`. Le même chemin prolonge le bail d’un détenteur dont la requête est rejetée. Préserver le contrôle avant lecture et l’arbitrage atomique entre premiers écrivains, puis confirmer ou renouveler le bail après validation du corps et annuler toute réservation provisoire en cas de rejet.

## À corriger

- [internal/server/presence.go:74] Après un `POST /clients/me/connect` réussi, l’appareil reste absent de la liste des appareils connectés jusqu’à sa prochaine requête signée. Le middleware vérifie la présence avant que le handler lève la déconnexion. Enregistrer la présence lors de la reconnexion effective, sans prendre le bail.

## Tests lancés

- `gofmt -l cmd internal protocol scripts` et `git diff --check m10-writer...HEAD` — aucune anomalie.
- `scripts/sync-test-vectors.sh --check` — les copies Swift correspondent aux vecteurs Go.
- `make lint test` et `make contract` — non lancés. Leurs recettes écrivent dans des caches ou des répertoires temporaires. Les suites Go et la validation OpenAPI restent à vérifier.
- `swift test` et `xcodebuild test` — non lancés. Le diff ne touche aucun fichier iOS.

Le diff a été relu depuis `m10-writer` avec `e85f0180` comme référence. Aucun prompt M10 n’est présent dans `prompts/`. La description de PR n’a pas pu être consultée, car `gh` demandait une authentification.