# M1 — Contrat d'API et cryptographie (Go + Swift)

## À lire

`docs/specs/03-protocole.md` (en entier, c'est le cœur de ce jalon), `docs/specs/04-api-v1.md`, `docs/specs/07-securite.md`, `docs/specs/08-tests-qualite.md` §1 à §3.

## Livrables

### 1. `protocol/openapi.yaml`

- OpenAPI 3.1, décrivant **toutes** les routes de `04-api-v1.md`, avec schémas de requêtes et réponses, codes d'erreur, en-têtes de signature (`X-RL-*`, composants `securitySchemes` documentés) et `Upload-Offset`.
- Les schémas réutilisables dans `components/schemas` (Game, System, Slot, Backup, Job, Error...).
- Un exemple par route.
- Ajoute une cible `make contract` qui valide le fichier (outil en conteneur Docker ou script ; pas de dépendance installée globalement).

### 2. `protocol/test-vectors/`

Fichiers JSON générés par un petit programme Go déterministe (`protocol/test-vectors/gen/main.go`, entrées fixes, sorties commitées) :

- `pairing.json` : au moins 3 cas en mode `qr` et 2 en mode `pin` (ikm, deviceId, clientId, nonces → proof client, clé, proof console).
- `signing.json` : au moins 8 cas : GET sans paramètre, GET avec paramètres à trier et à encoder (espaces, accents, `+`, `/`), POST JSON, corps vide, `UNSIGNED`, chemin contenant un identifiant base64url, méthode en minuscules à normaliser.
- `ids.json` : au moins 10 chemins (accents NFC, espaces, parenthèses, crochets, emoji, sous-dossiers) → identifiants, plus des identifiants **invalides** à rejeter (`..`, chemin absolu, caractère nul, base64 invalide).

### 3. Go : `internal/auth`

- `DeriveKey`, `ClientProof`, `DeviceProof` (appairage, HKDF-SHA256 selon §3.2 ; implémente HKDF avec `crypto/hmac` si `golang.org/x/crypto` n'est pas déjà une dépendance).
- `CanonicalRequest`, `Sign`, `Verify` (§4) ; middleware `net/http` qui vérifie une requête et place le client dans le contexte.
- `NonceCache` (10 min, borné en mémoire, horloge injectable).
- `ClientStore` (lecture/écriture atomique de `clients.json`, ajout, remplacement, suppression).
- `PairingSession` : secret de 32 octets, PIN à 6 chiffres, expiration 10 min, usage unique, compteur d'échecs (5 puis blocage 30 s et régénération).
- Tests : vecteurs, cas d'erreur de chaque code `401`, rejeu, décalage d'horloge, corps modifié, expiration et blocage de l'appairage.

Et `internal/library/ids.go` : encodage et décodage des identifiants avec toutes les vérifications de `03 §5`, testés avec `ids.json`.

### 4. Swift : `ios/ReminisLinkKit`

- Swift package (pas encore de projet Xcode d'app, sauf si c'est plus simple pour toi ; dans ce cas, seulement le package).
- `Signer` (CryptoKit) : chaîne canonique, signature, en-têtes.
- `Pairing` : génération des nonces, preuve client, dérivation (CryptoKit `HKDF<SHA256>`), vérification de la preuve console.
- Les vecteurs de `protocol/test-vectors/` copiés comme ressources de test par un script (`scripts/sync-test-vectors.sh`), pas à la main.
- Tests `swift test` qui passent tous les vecteurs.

## Contraintes

- Aucune autre route implémentée dans ce jalon.
- Comparaisons de MAC en temps constant des deux côtés.
- Le format exact des chaînes (préfixes `RL1-...`, séparateurs) est celui de la spec, à l'octet près. Si tu penses qu'il faut le changer, propose-le dans le rapport sans le changer.

## Fait quand

- `make lint test` et `make contract` passent.
- `cd ios/ReminisLinkKit && swift test` passe.
- Go et Swift passent exactement les mêmes vecteurs.
