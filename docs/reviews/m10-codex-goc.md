Verdict : à corriger

## Bloquant

- [internal/server/access.go:136](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-go/internal/server/access.go:136) **Une requête vérifiée avec l’ancienne clé peut être admise après un réappairage.** Le vérificateur charge la clé avant le gate. Si la console oublie l’appareil et qu’il est réappairé avec le même identifiant entre la vérification de la signature et le gate, `admit` voit un identifiant appairé et capture la nouvelle génération. Un `PUT /save` signé avec l’ancienne clé peut alors obtenir le bail et écrire. Associer la génération au chargement de la clé, puis vérifier cette génération sous `access` dès l’admission et au commit. Les tests ajoutés déclenchent la révocation après le gate et laissent cet intervalle sans couverture.
- [internal/auth/middleware.go:416](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-go/internal/auth/middleware.go:416) **Un `413` peut encore attendre la suite du corps.** Les rejets avant toute lecture, dont la signature invalide et le `Content-Length` excessif, ferment désormais la connexion. Pour une requête signée en `Transfer-Encoding: chunked`, si le client envoie assez d’octets pour dépasser la limite puis s’interrompt avant le marqueur de fin, le `Close` différé de `readBody` tente de vider le reste. `failWith` ne peut envoyer le `413` qu’après cette attente, jusqu’au délai d’inactivité. Éviter ce drainage lors d’un rejet avec un corps incomplet et ajouter un test sur connexion réelle avec un corps chunked interrompu.

## À corriger

- [internal/server/access.go:236](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-go/internal/server/access.go:236) La présence des requêtes ordinaires est désormais enregistrée sous le même verrou que la révocation. Il reste un intervalle pour `POST /clients/me/connect`. Si la console déconnecte l’appareil entre `accept` et `connect`, le handler efface cette nouvelle déconnexion et réinscrit l’appareil dans `connectedClients`. Appliquer la reconnexion et la mise à jour de présence dans l’opération qui valide la génération.
- [internal/server/writer.go:197](/Users/tfournier/projets/github.com/tfournier/reminislink-m10-go/internal/server/writer.go:197) L’écran Appareils lit la présence avant de prendre le verrou `access`. Une déconnexion entre ces deux lectures peut produire une ligne à la fois connectée et déconnectée. Prendre l’instantané de présence dans `access.devices`, sous le même verrou que l’état de déconnexion.

## Suggestions

Aucune. L’acquisition ou la confirmation du bail est atomique face au rollback, et une lecture ne confirme plus une réservation provisoire. Une révocation survenue après le gate est détectée au commit, y compris après reconnexion ou réappairage. `PUT /writer` vérifie aussi la génération au moment de prendre la main.

## Tests lancés

- `git diff --check` sur `main...HEAD`, `m10-writer...HEAD` et `HEAD~1...HEAD` — aucune anomalie. Le lanceur Git système a tenté de créer un cache temporaire lors du premier appel. Le sandbox a refusé l’écriture, puis les vérifications ont été faites avec le binaire Git direct.
- `gofmt -l cmd internal protocol scripts` — aucun fichier signalé.
- `scripts/sync-test-vectors.sh --check` — les copies Swift correspondent aux vecteurs Go.
- `make lint test` et `make contract` — non exécutés en raison de la consigne de lecture seule. `swift test` et `xcodebuild test` — non exécutés, le diff ne touche aucun fichier iOS.

Le dépôt ne contient aucun prompt M10. La contre-revue s’appuie sur les spécifications M10 et le commit `779201ef`. La description de la PR est restée inaccessible.