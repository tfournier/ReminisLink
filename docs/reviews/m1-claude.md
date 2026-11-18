# Revue M1 — sous-agent Claude neuf (revue non croisée : `agy` absent)

Date : 2026-11-18. Branche relue : `m1-contrat-crypto` @ `2ac418a3`.

Verdict : à corriger

## Bloquant
- [internal/auth/middleware.go:121-128] Débordement du contrôle d'écart d'horloge : `now().Sub(time.Unix(ts,0))` sature à `minDuration` et `-minDuration` reste négatif. Une requête signée avec `X-RL-Time: 99999999999` reçoit 204 au lieu de `401 clock_skew` (écart à 03 §4.3.2) et devient rejouable une fois son nonce sorti du cache. Confirmé par l'agent principal à la lecture du code.

## À corriger
- [internal/auth/middleware.go:139-155, 30] Corps (jusqu'à 64 Mio) lu en mémoire avant la vérification HMAC : épuisement de la RAM possible avec un `clientId` observé sur le Wi-Fi. Vérifier le HMAC d'abord ; limite par défaut 1 Mio, 64 Mio réservés à `PUT /games/{id}/save`.
- [internal/library/ids.go:152-161] Une erreur `EvalSymlinks` autre que `ENOENT` (ex. `ENOTDIR`) remonte sans `ErrInvalidID`, avec le chemin absolu → risque de `500` et de fuite de chemin.
- [internal/auth/middleware.go:164-166 / openapi.yaml] `429 too_many_attempts` (cache de nonces plein) absent de 03 §6 et des réponses OpenAPI des routes signées.
- [internal/library/ids.go:15-21, 119] Écart à 03 §5 : refus des chemins non canoniques au lieu de `filepath.Clean` ; interprétations de 03 §4.2 (`+` → espace, paires vides ignorées) à inscrire dans la spec.

## Suggestions
- Clés de requête dupliquées réordonnables sans casser la signature (`url.Values.Get` prend la première) → rejeter les doublons.
- Rejeu théorique à l'instant exact T+300 s (`NonceTTL == 2×MaxClockSkew`).
- `Signer.sign` (Swift) signe le corps vide si `httpBodyStream` est utilisé.
- `Pairing.swift` : pas de contrôle de longueur (secret, ikm, nonces), contrairement au Go.
- Vecteurs circulaires (le générateur appelle le code testé) → ajouter un oracle indépendant.
- Tests manquants : vecteurs rejoués via `httptest.Server`, `ErrNonceCacheFull` → 429, `X-RL-Time` lointain, `ResolveID` sur `ENOTDIR`.
- openapi.yaml : réponses ajoutées hors spec 04 (à reporter dans 04), `clientName maxLength: 64` hors spec, `413` manquant sur des routes JSON, pas d'exemple pour les corps binaires.
- M2 : vérifier qu'un `folderId` appartient au `systemId`.

## Vérifié sans problème
Chaîne canonique, HMAC et HKDF recalculés indépendamment en Python d'après la spec (15/15 vecteurs identiques) ; Go et Swift alignés ; MAC en temps constant ; `PairingSession`, `ClientStore`, `WriteFileAtomic`, `NonceCache` ; ordre des contrôles ; aucun secret journalisé.

## Tests lancés
`make lint test` OK ; `make contract` OK ; `swift test` OK (21 tests) ; reproductions sur une copie hors dépôt (`X-RL-Time` lointain → 204 ; corps de 32 Mio lu avant refus ; doublons réordonnés → même requête canonique ; `ENOTDIR` non typé).

## Traitement par l'agent principal
Bloquant et 4 points « à corriger » renvoyés au sous-agent M1 (aller-retour 1). Les points de spec (`filepath.Clean`, `+`, 429 dans 03 §6, réponses ajoutées à 04) sont soumis à la personne. Doublons de clés : « Plus tard », à valider par la personne (sécurité).

---

# Revue ciblée des corrections (aller-retour 1) — nouvel agent Claude neuf

Périmètre : `2ac418a3..77d7d44f`. Verdict : à corriger.

Points 1 à 8 de la première revue : corrigés (horloge, HMAC avant lecture, `ENOTDIR`, 429 déclaré, borne T+300 s, Swift, 413 et exemples, oracles).

## Bloquant
- [internal/auth/middleware.go:148, :165, :179] Le nonce n'est réservé qu'après la lecture du corps : des en-têtes d'une requête légitime en cours, rejoués sur N connexions avec 64 Mio chacune, sont lus en mémoire avant le refus (reproduit : 4 rejeux = 256 Mio lus). Correction : réserver le nonce juste après le HMAC, plus un budget mémoire global.

## À corriger
- [internal/auth/nonce.go:79-88] Horloge murale pour le contrôle d'horloge, monotone pour l'expiration du cache : rejeu possible si l'heure murale recule.
- [docs/specs/03-protocole.md §4.3, §6] 429 et ordre HMAC/empreinte non reportés dans la spec.
- [protocol/openapi.yaml:1480] `Retry-After` déclaré mais jamais envoyé.

## Suggestions
- Limite de corps déduite du chemin brut (contournable avec un routeur) → la fixer par route.
- `PUT /uploads/{id}` signé (non `UNSIGNED`) limité à 1 Mio alors qu'un morceau fait 4 à 16 Mio.
- 413 et 400 possibles sur toutes les routes signées mais non déclarés.
- Constante pour `"invalid_request"`.

## Traitement par l'agent principal
Bloquant confirmé à la lecture du code ; tous les points (sauf spec, soumise à la personne) renvoyés au sous-agent M1 (aller-retour 2).

---

# Revue ciblée des corrections (aller-retour 2) — nouvel agent Claude neuf

Périmètre : `77d7d44f..bcd73fe9`. Verdict : à corriger, **aucun bloquant**. Les 5 corrections (nonce réservé avant le corps, expiration en temps mural, `Retry-After`, options par route, 400/413 déclarés) sont justes ; `go test -race -count=3` OK.

## À corriger
- [internal/auth/budget.go:32] Une requête sans corps attend derrière un gros envoi en file (reproduit : GET sans réponse avec 2 waiters).
- [internal/auth/budget.go:9-10] Commentaire faux : en HTTP/1.1 le contexte n'est pas annulé quand le client part tant que le corps n'est pas lu.
- [internal/auth/middleware.go:232] `503 shutting_down` exact seulement si M2 annule le contexte à l'arrêt.

## Suggestions
- Description de `BadRequest` à compléter (corps illisible).
- Refuser `BodyBudget < BodyLimit` à l'enregistrement.
- Rendre explicite l'invariant `ttl ≥ 2 × MaxClockSkew`.
- Imposer la même horloge au `Verifier` et au cache.
- Rejeu possible après redémarrage du pak (cache en mémoire) : à mentionner dans 07-securite.md.
- Écarts à reporter dans la spec : ordre HMAC/empreinte, nonce consommé après réservation, 429 sur routes signées.

## Traitement par l'agent principal
Points « à corriger » et trois suggestions renvoyés au sous-agent M1 (aller-retour 3, dernier). Le reste en « Plus tard » ou soumis à la personne.

---

# Aller-retour 3

Corrections `73cde9e1`, `57259f3c`, `186f40d3` (requêtes sans corps hors budget, commentaires corrigés et note M2, refus `BodyLimit > BodyBudget`, invariant du TTL, description `BadRequest`). Diff relu par l'agent principal ; `make lint test`, `go test -race -count=2 ./internal/auth/...`, `make contract`, `swift test` OK. Plus aucun point bloquant ni à corriger.
