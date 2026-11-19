# 03 — Protocole

Ce document décrit tout ce qui n'est pas une route métier : découverte, appairage, authentification, format des erreurs, identifiants, transferts et tâches. Les routes sont dans [04](04-api-v1.md).

## 1. Transport

- HTTP/1.1 sur TCP, réseau local uniquement. Pas de TLS en v1 (option prévue plus tard, voir [07](07-securite.md)).
- Port par défaut : **47821**. S'il est occupé, le pak essaie les 9 suivants. Le port réel est annoncé par Bonjour et inscrit dans le QR code.
- Préfixe de toutes les routes : `/api/v1`.
- Corps JSON en UTF-8, `Content-Type: application/json`. Noms de champs en `camelCase`. Dates en RFC 3339 UTC (`2026-11-18T16:57:40Z`). Tailles en octets (entiers 64 bits).
- Taille maximale d'un corps JSON : 1 Mio. Les fichiers passent par les routes de transfert.
- Événements poussés par la console (M14) : `GET /api/v1/events`, un flux Server-Sent Events (`text/event-stream`) signé à l'ouverture et gardé ouvert, avec un signe de vie au moins toutes les 15 s (voir [04](04-api-v1.md#get-events-m14)). Le serveur n'a pas de délai d'écriture global ; chaque écriture d'un flux a son propre délai.

## 2. Découverte (Bonjour / DNS-SD)

Le pak annonce un service :

| Champ | Valeur |
|---|---|
| Type | `_reminislink._tcp` |
| Domaine | `local.` |
| Nom d'instance | Nom de la console, ex. `Brick de Thomas` (par défaut `<Modèle> <4 derniers caractères de l'id>`) |
| Port | Port réel du serveur |

Enregistrement TXT (clé=valeur, tout en ASCII) :

| Clé | Exemple | Sens |
|---|---|---|
| `v` | `1` | Version du format TXT |
| `id` | `7f3a9c2e-...` | Identifiant unique et stable de la console (UUID v4 généré au premier lancement) |
| `api` | `1` | Version majeure de l'API |
| `model` | `brick` | Modèle (`brick`, `brickpro`, `smartpro`, `smartpros`, `h700`, `my355`, `desktop`) |
| `platform` | `tg5040` | Plateforme NextUI |
| `os` | `nextui` | OS de la console |
| `osv` | `NextUI-20261013-039a06de` | Version de l'OS si connue (chaîne de release NextUI, voir 04 `/device`) |
| `cv` | `1.0.0` | Version du pak |

Côté iOS : `NWBrowser` avec `.bonjourWithTXTRecord(type: "_reminislink._tcp", domain: nil)`. L'app identifie une console appairée par son `id`, jamais par son IP ni son nom.

Si aucune console n'est trouvée au bout de 8 secondes, l'app affiche une erreur (voir [06](06-app-ios.md)). Pas de balayage du réseau en v1.

## 3. Appairage

### 3.1 Contenu du QR code

Le pak génère au démarrage un **secret d'appairage** de 32 octets aléatoires (`crypto/rand`), valable 10 minutes et à usage unique. Le QR code encode :

```
reminislink://pair?v=1&id=<deviceId>&n=<nom URL-encodé>&s=<secret base64url sans padding>&h=<IPv4>&p=<port>
```

- `h` et `p` sont **informatifs** (affichage, outils de test comme `scripts/smoke.sh`). L'app NE DOIT PAS s'en servir pour se connecter ni les mémoriser : la console est toujours trouvée par Bonjour avec son `id`, sinon l'app affiche « Console introuvable ». *(Décision du 18/11/2026 : un repli sur l'IP permettrait à un faux pak du même réseau, annonçant l'`id` d'une console éteinte, de se faire appairer à sa place.)*
- Après chaque appairage réussi, ou à expiration, le pak génère un nouveau secret et un nouveau QR code.

**PIN de secours** : 6 chiffres affichés sous le QR code, valables 10 minutes. Ils servent si l'appareil photo n'est pas utilisable. Ce mode est plus faible (voir [07](07-securite.md)) ; l'app l'indique à l'utilisateur.

### 3.2 Échange

Notations : `HMAC(k, m)` = HMAC-SHA256 ; `HKDF` = HKDF-SHA256 (RFC 5869) ; `||` = concaténation d'octets ; `b64u` = base64url sans padding ; chaînes en UTF-8.

1. L'app génère `clientId` (UUID v4, stable pour cette installation) et `clientNonce` (32 octets).
2. `ikm` = les 32 octets du secret (mode QR) ou les octets UTF-8 du PIN (mode PIN).
3. L'app envoie `POST /api/v1/pair` (route non signée) :

```json
{
  "method": "qr",
  "clientId": "3b1c...",
  "clientName": "iPhone de Thomas",
  "clientNonce": "<b64u 32 octets>",
  "proof": "<b64u HMAC(ikm, 'RL1-PAIR-CLIENT|' + deviceId + '|' + clientId + '|' + clientNonce_b64u)>"
}
```

4. Le pak vérifie `proof` en temps constant. En cas d'échec : `401 pairing_failed`. Après 5 échecs, le secret et le PIN sont régénérés et l'appairage est bloqué 30 secondes (`429 too_many_attempts`).
5. Le pak génère `deviceNonce` (32 octets) et dérive la clé :

```
key = HKDF(ikm, salt = clientNonce || deviceNonce, info = "RL1-KEY|" + deviceId + "|" + clientId, L = 32)
```

6. Le pak enregistre le client (`clientId`, `clientName`, `key`, date) puis répond :

```json
{
  "deviceId": "7f3a...",
  "deviceName": "Brick de Thomas",
  "deviceNonce": "<b64u 32 octets>",
  "proof": "<b64u HMAC(key, 'RL1-PAIR-DEVICE|' + deviceId + '|' + clientId)>"
}
```

7. L'app dérive la même clé, vérifie `proof` (authentification mutuelle), puis enregistre `deviceId`, le nom et la clé dans le Trousseau. Si la vérification échoue, l'app jette la clé et affiche une erreur.

**La clé ne circule jamais sur le réseau.** Un observateur du Wi-Fi voit les nonces et les preuves, mais sans le secret du QR code il ne peut pas reconstruire la clé.

Un même `clientId` qui s'appaire à nouveau remplace sa clé précédente.

## 4. Authentification des requêtes

Toutes les routes sauf `GET /api/v1/hello` et `POST /api/v1/pair` DOIVENT être signées.

### 4.1 En-têtes

| En-tête | Valeur |
|---|---|
| `X-RL-Client` | `clientId` |
| `X-RL-Time` | Horodatage Unix en secondes (entier) |
| `X-RL-Nonce` | 16 octets aléatoires en `b64u`, uniques par requête |
| `X-RL-Content-SHA256` | SHA-256 du corps en hexadécimal minuscule ; corps vide = SHA-256 de la chaîne vide ; `UNSIGNED` uniquement pour `PUT /api/v1/uploads/{id}` |
| `X-RL-Signature` | `b64u(HMAC(key, chaîne canonique))` |

### 4.2 Chaîne canonique

Lignes séparées par `\n`, sans `\n` final :

```
RL1-HMAC-SHA256
<MÉTHODE en majuscules>
<chemin tel qu'envoyé, encodé, sans la requête>
<requête canonique>
<X-RL-Time>
<X-RL-Nonce>
<X-RL-Content-SHA256>
```

Requête canonique : paramètres décodés puis ré-encodés en RFC 3986 (seuls `A-Z a-z 0-9 - . _ ~` restent en clair), triés par clé puis par valeur, au format `clé=valeur` joints par `&`. Ligne vide s'il n'y a pas de paramètres.

Précisions (fixées au jalon M1, vecteurs dans `signing.json`) :
- le décodage suit `application/x-www-form-urlencoded` : `+` vaut une espace ; un `%` mal formé rend la requête invalide (`401 bad_signature`) ;
- les paires vides (`a=1&&b=2`) sont ignorées ; une clé sans `=` vaut `clé=` ;
- le tri compare les clés **encodées** puis les valeurs **encodées**, octet par octet ;
- une même clé présente plusieurs fois est refusée par le pak (`400 invalid_request`) ; l'app n'envoie jamais de clé en double. *(Décision du 18/11/2026, implémentée au jalon M2.)*
- un `;` non encodé dans la requête est refusé (`400 invalid_request`), car certains analyseurs le traitent comme un séparateur ; l'app encode toujours `;` en `%3B` (c'est le cas de `Signer.encodeQuery`). *(Jalon M2.)*
- l'app envoie toujours la requête déjà sous sa forme canonique.

### 4.3 Vérification côté pak

1. Client inconnu → `401 unknown_client` (l'app propose de refaire l'appairage).
2. `|X-RL-Time − heure console| > 300 s` → `401 clock_skew`. L'app corrige l'écart grâce à `serverTime` de `/hello` et réessaie une fois.
3. Nonce déjà vu pour ce client → `401 replayed_request`. Un nonce est conservé au moins 10 minutes, et au moins jusqu'à `X-RL-Time + 300 s` (heure murale), pour qu'un recul de l'horloge de la console ne rouvre pas de fenêtre de rejeu.
4. Signature comparée en temps constant ; différente → `401 bad_signature`. Elle porte sur l'en-tête `X-RL-Content-SHA256`, pas sur le corps : elle est donc vérifiée **avant** de lire le corps, et le corps d'une requête forgée n'est jamais lu.
5. Le nonce est alors réservé (atomiquement). Si le cache de nonces est plein → `429 too_many_attempts` avec `Retry-After`. Un échec à une étape suivante **consomme** le nonce : l'app réessaie avec un nouveau nonce.
5 bis. *(M10)* Client déconnecté depuis la console → `401 disconnected` (sauf `POST /clients/me/connect`) ; route d'écriture alors qu'un autre client tient le bail d'écriture → `423 read_only` (`details.writer`). Vérifié avant de lire le corps, sans rien modifier (voir [04, « Un seul appareil en écriture »](04-api-v1.md#un-seul-appareil-en-écriture-m10)).
6. Si `X-RL-Content-SHA256` n'est pas `UNSIGNED`, le pak lit le corps (limite fixée par route : 1 Mio par défaut, 64 Mio pour `PUT /games/{gameId}/save`, 16 Mio pour `PUT /uploads/{uploadId}`, sinon `413 too_large`), dans un budget mémoire global, puis recalcule son SHA-256 ; différent → `401 bad_signature`. `UNSIGNED` n'est accepté que sur `PUT /uploads/{uploadId}`.

*(Ordre fixé au jalon M1, décision du 18/11/2026 : la version précédente vérifiait le corps avant la signature, ce qui permettait à un tiers de faire lire 64 Mio par requête forgée.)*

### 4.4 Vecteurs de test

`protocol/test-vectors/` DOIT contenir, en JSON :
- `pairing.json` : ikm, ids, nonces → proof client, clé dérivée, proof console (mode QR et mode PIN) ;
- `signing.json` : clé, méthode, chemin, requête, heure, nonce, corps → requête canonique, chaîne canonique, signature ;
- `ids.json` : chemins relatifs (avec accents, espaces, emojis, parenthèses) → identifiants.

Les suites Go et Swift DOIVENT les lire et les vérifier.

## 5. Identifiants

- `systemId` = `b64u` du nom du dossier système (ex. `Game Boy Advance (GBA)`).
- `gameId` = `b64u` du chemin du jeu relatif à `Roms/`, séparateur `/` (ex. `Game Boy Advance (GBA)/Hacks/Sky Garden Quest DX.gba`).
- `folderId` = `b64u` du chemin d'un sous-dossier relatif à `Roms/`, même format.
- `sdFileId`, `stagingId`, `backupId`, `entryId`, `uploadId`, `jobId` : identifiants générés par le pak, opaques eux aussi.
- Ce sont des identifiants **opaques** : l'app ne les décode pas et ne les construit pas, elle les renvoie tels quels.
- Le pak DOIT décoder, refuser tout composant `..`, chemin absolu ou caractère nul, puis vérifier que le chemin final, liens symboliques résolus, reste sous la racine attendue (`400 invalid_id` sinon).
- Le décodage est **strict** : base64url non canonique, UTF-8 invalide, caractère de contrôle, `\` et chemin non canonique (`a//b`, `./a`, `a/`, `a/.`) sont refusés (`400 invalid_id`) au lieu d'être nettoyés par `filepath.Clean`, pour qu'un fichier ait un seul identifiant. *(Décision du 18/11/2026.)*
- Aucune normalisation Unicode : les octets du nom sur la carte sont utilisés tels quels ; une écriture NFC et une écriture NFD du même nom donnent deux identifiants différents.
- Les noms de fichiers fournis par l'app (envoi, renommage, nom déduit d'un téléchargement) sont **normalisés en NFC** avant tout le reste (l'iPhone fournit souvent des noms décomposés en NFD, mal affichés par NextUI et l'écran du pak ; M8). Ils DOIVENT ensuite être nettoyés : pas de `/`, `\`, `..`, caractère de contrôle, ni nom vide ; longueur ≤ 255 octets ; caractères interdits en exFAT (`" * / : < > ? \ |`) remplacés par `_`.

## 6. Erreurs

Toute réponse d'erreur a la forme :

```json
{ "error": { "code": "not_found", "message": "Game not found", "details": { } } }
```

`message` est en anglais, destiné aux journaux. L'app affiche un texte traduit choisi d'après `code`.

| HTTP | `code` | Cas |
|---|---|---|
| 400 | `invalid_request` | JSON invalide, champ manquant, corps illisible ou tronqué, clé de requête en double |
| 400 | `invalid_id` | Identifiant illisible ou hors racine |
| 400 | `invalid_name` | Nom de fichier refusé |
| 401 | `unknown_client`, `bad_signature`, `clock_skew`, `replayed_request`, `pairing_failed` | Authentification |
| 401 | `disconnected` | Client déconnecté depuis la console, jusqu'à `POST /clients/me/connect` (M10) |
| 404 | `not_found` | Ressource absente |
| 409 | `conflict` | Le fichier cible existe (`details.existing`) |
| 409 | `checksum_mismatch` | SHA-256 final différent de celui annoncé |
| 409 | `state_locked` | Action refusée sur un state verrouillé |
| 413 | `too_large` | Corps ou fichier trop gros |
| 422 | `unsupported_format` | Patch ou fichier non reconnu |
| 422 | `patch_mismatch` | La ROM ne correspond pas au patch (somme de contrôle BPS/UPS) |
| 423 | `read_only` | Un autre client tient le bail d'écriture (`details.writer` : son nom) ; rien n'a été modifié (M10) |
| 429 | `too_many_attempts` | Appairage bloqué temporairement ; limite de débit de `/hello` ; cache de nonces plein sur une route signée ; trop d'envois ouverts ou de tâches ; trop de flux d'événements (4 par client, 16 en tout, M14) ; envoi en cours de vérification après un redémarrage du pak (toujours avec `Retry-After` sur une route signée) |
| 500 | `internal` | Erreur inattendue (détails dans le journal) |
| 503 | `shutting_down` | Le pak se ferme (y compris une requête en attente du budget mémoire, annulée à la fermeture) |
| 507 | `insufficient_storage` | Espace libre insuffisant (`details.required`, `details.available`) |

## 7. Envois de fichiers (iPhone → console)

Envoi en morceaux, reprenable, inspiré de tus.

1. **Création** : `POST /api/v1/uploads` avec la destination, la taille et le SHA-256 du fichier complet (voir [04](04-api-v1.md)). Le pak vérifie l'espace libre (taille + 5 %), réserve un fichier temporaire dans la zone ReminisLink de la carte SD (même système de fichiers que la destination, pour un renommage atomique) et renvoie `uploadId` et `chunkSize` (par défaut 4 Mio, maximum 16 Mio).
2. **Morceaux** : `PUT /api/v1/uploads/{id}` avec l'en-tête `Upload-Offset` et le morceau en corps (`application/octet-stream`). Le pak refuse un `Upload-Offset` différent de l'offset courant (`409 conflict`, avec l'offset attendu dans `details.offset`). Réponse `204` avec le nouvel `Upload-Offset`.
3. **Reprise** : `GET /api/v1/uploads/{id}` renvoie l'offset reçu. L'app reprend à partir de là.
4. **Fin** : `POST /api/v1/uploads/{id}/complete`. Le pak calcule le SHA-256 (incrémental pendant la réception) ; s'il diffère : `409 checksum_mismatch` et l'envoi est supprimé. Sinon, `fsync`, application de la règle de conflit (`fail`, `rename`, `overwrite` avec sauvegarde), renommage vers la destination, puis réponse avec la ressource créée.
5. **Annulation** : `DELETE /api/v1/uploads/{id}`.

Un envoi sans activité pendant 24 h est supprimé. Les envois en cours **survivent** à la fermeture du pak : au relancement, le pak recalcule en arrière-plan le SHA-256 à partir des octets réellement présents sur la carte (l'offset repris est la taille du fichier partiel) ; pendant ce calcul, `GET /uploads/{id}` répond aussitôt et `PUT`/`complete` peuvent répondre `429` avec `Retry-After`. Un envoi purgé ou illisible répond `404` : l'app le recrée. *(Décision du 18/11/2026.)*

L'intégrité des morceaux (non signés) est garantie par le SHA-256 final, annoncé dans la requête de création, elle signée.

## 8. Tâches (opérations longues côté console)

Téléchargements depuis une URL et application de patchs. Une seule tâche à la fois s'exécute ; les autres attendent dans une file.

```json
{
  "id": "job_01J...",
  "kind": "download",
  "state": "running",
  "progress": { "done": 12582912, "total": 536870912 },
  "createdAt": "2026-11-18T17:00:00Z",
  "updatedAt": "2026-11-18T17:00:12Z",
  "result": null,
  "error": null
}
```

- `kind` : `download` ou `patch`.
- `state` : `queued`, `running`, `succeeded`, `failed`, `cancelled`.
- `progress.total` vaut `null` si inconnu.
- `result` (si `succeeded`) : la ressource créée, ex. `{ "gameId": "..." }`.
- `error` (si `failed`) : le même objet que dans les réponses d'erreur.

L'app suit une tâche par interrogation (`GET /api/v1/jobs/{id}`) toutes les secondes tant que l'écran est visible. Les tâches terminées sont conservées 1 heure en mémoire.

Depuis M14, le flux d'événements `GET /api/v1/events` envoie aussi chaque tâche (événement `job`) : à chaque changement d'état, et au plus une fois par seconde pendant qu'elle avance ; une tâche réussie qui a installé un fichier est suivie d'un événement `library`.

## 9. Versions

- La version majeure de l'API est dans le chemin (`/api/v1`) et dans le TXT (`api=1`).
- Ajouter un champ ou une route ne change pas la version. L'app DOIT ignorer les champs inconnus.
- `GET /api/v1/hello` renvoie `apiVersion` et `minClientVersion`. Si l'app est trop ancienne, elle demande une mise à jour.
