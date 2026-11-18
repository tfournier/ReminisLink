# Revue M1 — Antigravity (Gemini 3.8 Flash High)

Date : 2026-11-18. Branche relue : `m1-contrat-crypto` @ `f6f11aca`. Lancée avec `agy -p` (sans approbation automatique, liste blanche de commandes en lecture seule).

Verdict : prêt à fusionner

### Bloquant
Aucun.

### À corriger
Aucun.

### Suggestions
- [[internal/auth/budget.go:56-61](internal/auth/budget.go)] Lors de l'annulation d'un contexte dans `byteBudget.acquire`, le retrait de `w` de la tranche `b.waiters` via `append(b.waiters[:i], b.waiters[i+1:]...)` ne met pas à `nil` la case résiduelle en fin de capacité (`b.waiters[len(b.waiters)] = nil`). Même si la structure `budgetWaiter` est légère, cela évite de conserver temporairement en mémoire un pointeur orphelin avant la prochaine réallocation (pratique déjà appliquée dans `notifyLocked` ligne 81).
- [[internal/auth/middleware.go:73-77](internal/auth/middleware.go)] `Verifier` prend séparément `Nonces *NonceCache` et `Now func() time.Time`. Un constructeur dédié (ex. `NewVerifier(...)`) qui injecte automatiquement la même horloge au vérificateur et au cache de nonces éviterait tout risque de divergence d'horloge lors de l'assemblage du serveur en M2.
- [[internal/auth/signing.go:57-89](internal/auth/signing.go)] Conformément à `docs/specs/03-protocole.md §4.2`, le rejet des requêtes contenant des clés dupliquées (`400 invalid_request`) sera à câbler dans le routeur HTTP et l'analyse des requêtes au jalon M2.
- [[ios/ReminisLinkKit/Sources/ReminisLinkKit/Signer.swift:102-128](ios/ReminisLinkKit/Sources/ReminisLinkKit/Signer.swift)] Pour `Signer.sign(request:)`, il reste essentiel que l'app utilise toujours `Signer.encodeQuery(...)` lors de la construction initiale de ses URLs afin de garantir que l'encodage RFC 3986 envoyé sur le réseau corresponde strictement à la requête canonique signée.

---

### Tests lancés : commandes et résultats

1. **`make lint`** : succès (0 erreur)
   - Vérification du formatage Go (`gofmt -l internal protocol`) : aucun fichier non formaté.
   - Vérification statique (`CGO_ENABLED=0 go vet ./...`) : aucune anomalie.

2. **`make test`** : succès (0 échec)
   - `CGO_ENABLED=0 go test ./...`
   - `internal/auth` : 1.24s (tests unitaires, horloge, rejeu, épuisement mémoire, 401, vecteurs HTTP).
   - `internal/library` : 1.12s (identifiants, résolutions de chemins, symlinks, fuzzing).
   - `internal/store` : 0.90s (écritures atomiques, synchronisation).
   - `protocol/test-vectors/gen` : 0.64s (générateur de vecteurs et cohérence).

3. **`go test -race ./...`** : succès (0 course)
   - Détection de courses de données sur l'ensemble des paquetages Go : aucune condition de course détectée.

4. **`make contract`** : succès (0 erreur)
   - Validation Redocly CLI (`redocly/cli:1.34.5` dans Docker) : `openapi.yaml: validated in 117ms. Woohoo! Your API description is valid. 🎉`.
   - Contrôle du générateur de vecteurs : `CGO_ENABLED=0 go run ./protocol/test-vectors/gen -check -out protocol/test-vectors` (vecteurs valides et à jour).
   - Contrôle de synchronisation des vecteurs Swift : `scripts/sync-test-vectors.sh --check` (vecteurs Swift synchronisés et identiques).

5. **`cd ios/ReminisLinkKit && swift test`** : succès (23 tests exécutés, 0 échec)
   - Suite `Signer` (5 tests) : calcul de hachage de corps vide, génération de nonces 16 octets, canonicalisation de requêtes, rejets d'entrées malformées, rejet des flux de corps non signés.
   - Suite `Pairing` (8 tests) : format UUID minuscule, nonces aléatoires 32 octets, encodage UTF-8 du PIN, échange complet QR, contrat JSON, rejet de preuve erronée, rejet de réponses malformées, contrôle des longueurs.
   - Suite `Shared test vectors` (3 tests) : validation complète de tous les cas de `pairing.json`, `signing.json` et `ids.json`.
   - Suite `Test vector copies` (4 tests) : vérification de l'intégrité et de la présence des copies de ressources JSON.
