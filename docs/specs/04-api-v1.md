# 04 — API v1

Toutes les routes commencent par `/api/v1`. Sauf mention contraire, elles sont **signées** (voir [03 §4](03-protocole.md#4-authentification-des-requêtes)) et répondent en JSON. Ce document est la base de `protocol/openapi.yaml`, qui reste la référence une fois écrit.

## Sommaire

| Méthode | Route | Rôle |
|---|---|---|
| GET | `/hello` | Présentation de la console (**non signée**) |
| POST | `/pair` | Appairage (**non signée**) |
| DELETE | `/clients/me` | Se désappairer |
| POST | `/clients/me/connect` | Se reconnecter après une déconnexion par la console (M10) |
| GET | `/device` | État de la console |
| GET / PUT / DELETE | `/writer` | Détenteur du bail d'écriture / le prendre ou le renouveler / le relâcher (M10) |
| GET | `/systems` | Liste des systèmes |
| GET | `/systems/{systemId}/games` | Contenu d'un système (dossiers et jeux) |
| GET | `/games/{gameId}` | Détail d'un jeu et de ses fichiers |
| GET | `/games/{gameId}/art` | Jaquette (PNG) |
| DELETE | `/games/{gameId}` | Supprimer un jeu et ses données (vers la corbeille) |
| GET | `/trash` | Contenu et taille de la corbeille |
| DELETE | `/trash` | Vider la corbeille |
| DELETE | `/trash/{entryId}` | Supprimer définitivement une entrée de la corbeille |
| GET | `/saves` | Toutes les saves de la console, avec SHA-256 (sync, M9) |
| GET / PUT | `/games/{gameId}/save` | Télécharger / remplacer la save |
| GET | `/games/{gameId}/save/rtc` | Télécharger le fichier d'horloge de la save (M9) |
| GET / POST | `/games/{gameId}/save/backups` | Lister / créer des copies de la save |
| POST | `/games/{gameId}/save/backups/{backupId}/restore` | Restaurer une copie |
| GET / DELETE | `/games/{gameId}/save/backups/{backupId}` | Télécharger / supprimer une copie |
| GET | `/games/{gameId}/states` | Emplacements de states |
| GET / DELETE | `/games/{gameId}/states/{slot}` | Télécharger / supprimer un state |
| GET | `/games/{gameId}/states/{slot}/thumbnail` | Vignette du state |
| PUT / DELETE | `/games/{gameId}/states/{slot}/lock` | Verrouiller / déverrouiller |
| POST | `/games/{gameId}/states/{slot}/restore` | Restaurer la copie verrouillée |
| GET | `/games/{gameId}/resume-history` | Historique du state de reprise |
| POST | `/games/{gameId}/resume-history/{entryId}/restore` | Restaurer une entrée de l'historique |
| GET / PUT | `/games/{gameId}/cheats` | Lire / remplacer la liste des cheats |
| POST | `/games/{gameId}/cheats/import` | Importer des cheats |
| GET | `/sd/files` | Lister les `.cht` et patchs présents sur la carte SD |
| POST | `/uploads` | Créer un envoi |
| GET / PUT / DELETE | `/uploads/{uploadId}` | État / morceau / annulation |
| POST | `/uploads/{uploadId}/complete` | Terminer un envoi |
| POST | `/downloads` | Téléchargement d'une URL par la console (crée une tâche) |
| POST | `/games/{gameId}/patch` | Appliquer un patch (crée une tâche) |
| GET | `/jobs` | Tâches récentes |
| GET / DELETE | `/jobs/{jobId}` | État / annulation |

---

## Console

### `GET /hello` (non signée)

Sert à vérifier qu'on parle bien à la bonne console, à mesurer l'écart d'horloge et à vérifier la compatibilité.

```json
{
  "deviceId": "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10",
  "deviceName": "Brick de Thomas",
  "model": "brick",
  "platform": "tg5040",
  "os": "nextui",
  "osVersion": "NextUI-20261013-039a06de",
  "pakVersion": "1.0.0",
  "apiVersion": 1,
  "minClientVersion": "1.0.0",
  "serverTime": "2026-11-18T17:00:00Z",
  "pairingOpen": true,
  "writerHeld": false
}
```

`writerHeld` (M10) : `true` quand un client tient le bail d'écriture ; le nom de ce client n'est donné qu'aux clients appairés (`GET /device`, `GET /writer`, `423 read_only`), pour ne pas exposer le nom d'un iPhone à tout le réseau.

Aucune autre information (pas de liste de jeux, pas de clients). Limitée à 10 requêtes par seconde par IP (au-delà : `429 too_many_attempts`).

### `POST /pair` (non signée)

Voir [03 §3](03-protocole.md#3-appairage). `method` : `qr` ou `pin`. `clientName` : 1 à 64 caractères. `clientId` et `deviceId` : UUID en minuscules.

### `DELETE /clients/me`

Supprime le client appelant de la console. Réponse `204`. L'app supprime ensuite la clé de son Trousseau. N'exige pas le bail d'écriture ; libère le bail si l'appelant le tenait.

### `POST /clients/me/connect` (M10)

Lève la déconnexion décidée depuis la console (Réglages > Appareils, [05 §7.2](05-console-pak.md#72-réglages-y)) pour le client appelant. Réponse `204`, sans effet si le client n'était pas déconnecté ; ne prend pas le bail d'écriture. L'app ne l'appelle que sur une action de l'utilisateur (ouverture de cette console, bouton « Se reconnecter »), jamais lors d'une reconnexion automatique. C'est la seule route signée permise à un client déconnecté. Un appel dont le pak a chargé la clé avant une nouvelle déconnexion par la console ne lève pas celle-ci (`401 disconnected`). Le pak ne peut pas savoir quand l'app a envoyé la requête : un appel dont les en-têtes arrivent après la déconnexion la lève, comme une nouvelle action de l'utilisateur.

### `GET /device`

```json
{
  "deviceId": "7f3a...",
  "deviceName": "Brick de Thomas",
  "model": "brick",
  "platform": "tg5040",
  "os": "nextui",
  "osVersion": "NextUI-20261013-039a06de",
  "pakVersion": "1.0.0",
  "network": { "ipv4": "192.168.1.42", "ssid": "MaBox" },
  "battery": { "percent": 78, "charging": false },
  "storage": [
    { "id": "sd", "label": "SD", "totalBytes": 127865454592, "freeBytes": 40265318400 }
  ],
  "uptimeSeconds": 512,
  "connectedClients": [
    { "clientName": "iPhone de Thomas", "lastSeenAt": "2026-11-18T17:00:00Z" }
  ],
  "writer": { "clientName": "iPhone de Thomas", "you": true, "expiresAt": "2026-11-18T17:01:00Z" }
}
```

`writer` (M10) : le client qui tient le bail d'écriture (`you` : c'est l'appelant ; `expiresAt` : fin du bail s'il n'est pas renouvelé), ou `null` si le bail est libre. Voir « Un seul appareil en écriture » ci-dessous.

`connectedClients` : les clients appairés dont une requête signée a été acceptée dans les 30 dernières secondes (même règle que l'état « Connecté » de l'écran de la console, [05 §7.1](05-console-pak.md#71-écran-principal)), le client appelant compris. Tenu **en mémoire** par le pak, sans écriture sur la carte SD ; vide au lancement. *(Ajouté le 18/11/2026.)*

`ssid`, `battery` et `osVersion` PEUVENT être `null` si l'information n'est pas disponible. Sources sur NextUI ([05 §6](05-console-pak.md#6-informations-de-la-console)) : `osVersion` = `.system/version.txt` (chaîne de release telle que l'affiche NextUI, ex. `NextUI-20261013-039a06de` : date de build et hash court ; jamais le numéro `v6.x.y` ; décision D4 du 19/11/2026) ; `battery` = `axp2202-battery/capacity` et `axp2202-usb/online` + `time_to_full_now` ; `ssid` = `wpa_supplicant` (`null` si `wpa_state` ≠ `COMPLETED`).

---

## Un seul appareil en écriture (M10)

Décision du 19/11/2026. Un seul client appairé à la fois tient le **bail d'écriture** ; les autres sont en lecture seule. Le bail est tenu **en mémoire** par le pak : libre au lancement, perdu à la fermeture du pak.

- **Routes d'écriture** : toutes celles qui modifient la carte SD, c'est-à-dire chaque opération dont les réponses comprennent `423` dans `protocol/openapi.yaml` (envois et leurs morceaux, fin et annulation d'un envoi, suppressions, remplacements, restaurations, copies de save, verrous de states, cheats, jaquette, corbeille, téléchargements par la console, patch, annulation d'une tâche ; 23 opérations). Une telle requête prend le bail s'il est libre (ou expiré). Si un autre client le tient, le pak répond **`423 read_only`** avec `details.writer` (le `clientName` du détenteur), **avant de lire le corps** et sans rien modifier. La vérification est faite à un seul endroit, juste après la signature et la réservation du nonce. Le bail pris ainsi avant la lecture du corps est **provisoire** : il n'est confirmé (et un bail déjà tenu n'est renouvelé) qu'une fois la requête acceptée, corps vérifié ; si la requête est refusée ensuite (`401 bad_signature`, `413 too_large`…), le bail provisoire est rendu. Toute réponse d'erreur donnée avant la lecture complète du corps (`423`, `401`, `413`…) ferme la connexion (`Connection: close`) au lieu d'attendre le corps.
- **Toujours permis** : les routes de lecture (`GET`, téléchargements et Sync M9 compris), `PUT` / `DELETE /writer`, `POST /clients/me/connect` et `DELETE /clients/me`.
- **Renouvellement** : chaque requête signée acceptée du détenteur (lecture comprise) renouvelle le bail confirmé (une lecture ne confirme jamais un bail provisoire), comme `PUT /writer` (l'app en envoie un environ toutes les 20 s tant qu'elle est au premier plan ou qu'un de ses transferts est en cours). Le bail expire **60 s** après le dernier renouvellement.
- **Changement de main** : une requête déjà acceptée (corps vérifié) quand le bail change de main va jusqu'au bout ; une requête dont le corps était encore en cours de lecture est refusée à son acceptation si son client a été déconnecté ou oublié entre-temps (`401`), même s'il s'est reconnecté ou a été réappairé depuis, ou si un autre client a pris la main (`423`), et les tâches déjà lancées par la console continuent. La requête d'écriture suivante de l'ancien détenteur reçoit `423 read_only` ; un envoi est mis en pause par l'app, jamais perdu (il reprend avec `GET /uploads/{uploadId}` une fois le bail repris).

### `GET /writer`

```json
{ "writer": { "clientName": "iPhone de Thomas", "you": false, "expiresAt": "2026-11-18T17:01:00Z" } }
```

`writer` vaut `null` quand le bail est libre.

### `PUT /writer`

Corps facultatif : `{ "force": false }` (un corps vide vaut `{}`). Prend le bail s'il est libre ou déjà tenu par l'appelant (renouvellement : fin repoussée à 60 s). Si un autre client le tient : `423 read_only`, sauf avec `force: true` (« Prendre la main », après confirmation dans l'app) : le bail passe à l'appelant. Réponse `200` avec le même corps que `GET /writer` (`you: true`).

### `DELETE /writer`

Relâche le bail si l'appelant le tient, sans effet sinon. Réponse `204`. L'app l'appelle quand elle passe en arrière-plan sans transfert en cours.

### Déconnecter et oublier depuis la console

- **Déconnecter** (Réglages > Appareils) : le bail du client est retiré, il disparaît de `connectedClients`, et toutes ses requêtes signées reçoivent **`401 disconnected`** (après la vérification de la signature et la réservation du nonce) jusqu'à ce qu'il appelle `POST /clients/me/connect`. État tenu en mémoire seulement : la fermeture du pak le lève. Un nouvel appairage du même client depuis l'écran d'appairage de la console le lève aussi.
- **Oublier** : l'appairage est supprimé de `clients.json` (écriture atomique) ; ses requêtes reçoivent ensuite `401 unknown_client`, et son bail comme sa déconnexion sont oubliés.

### Erreurs

| Code | HTTP | Sens |
|---|---|---|
| `read_only` | 423 | Un autre client tient le bail d'écriture (`details.writer`). Rien n'a été modifié. |
| `disconnected` | 401 | Le client a été déconnecté depuis la console ; levé par `POST /clients/me/connect`. |

---

## Bibliothèque

### `GET /systems`

```json
{
  "systems": [
    {
      "id": "R2FtZSBCb3kgQWR2YW5jZSAoR0JBKQ",
      "label": "Game Boy Advance",
      "folder": "Game Boy Advance (GBA)",
      "tag": "GBA",
      "gameCount": 42
    }
  ]
}
```

Mêmes règles que Cheat Manager : seuls les dossiers de `Roms/` avec un suffixe `(TAG)` et au moins un élément visible. Le numéro de tri en tête du nom (`01) `, `02_`...) est retiré du `label`. Tri alphabétique insensible à la casse.

### `GET /systems/{systemId}/games?folderId=<id>`

Liste un dossier du système. `folderId` (optionnel) est un identifiant de dossier renvoyé par un appel précédent, pour descendre dans les sous-dossiers. Sans `folderId`, la racine du système.

```json
{
  "folders": [ { "id": "...", "name": "Hacks", "gameCount": 3 } ],
  "games": [
    {
      "id": "R2FtZSBCb3kgQWR2YW5jZSAoR0JBKS9Ta3kgR2FyZGVuIFF1ZXN0LmdiYQ",
      "name": "Sky Garden Quest",
      "fileName": "Sky Garden Quest.gba",
      "kind": "file",
      "sizeBytes": 16777216,
      "modifiedAt": "2026-10-02T09:12:00Z",
      "hasSave": true,
      "stateCount": 3,
      "lockedStateCount": 1,
      "cheatCount": 12,
      "hasArt": true
    }
  ]
}
```

- `kind` : `file` (ROM simple), `m3u` (fichier `.m3u`), `folder-m3u` (dossier contenant un `.m3u` du même nom, jeu multi-disques), `folder-cue` (M8 : dossier contenant une `.cue` du même nom et pas de tel `.m3u` ; NextUI lance la `.cue`, la save et les states portent son nom ; un client traite un type inconnu comme `folder-m3u`).
- Les fichiers ignorés sont ceux de Cheat Manager (`.txt`, `.png`, `.sav`, `.cht`, `.bak`...) et les fichiers cachés.
- Les fichiers cités par un `.m3u` du même dossier (les disques d'un jeu multi-disques) ne sont pas listés comme jeux séparés ; la taille du jeu `m3u` inclut ses disques.
- Les identifiants désignent un nom **exact** du dossier : un identifiant qui ne diffère que par la casse donne `404 not_found` (un seul identifiant par fichier, 03 §5).
- Le comptage `stateCount` et `cheatCount` DOIT rester rapide (≤ 300 ms pour 500 jeux sur la console) : une lecture de dossier par système, pas un `stat` par jeu.

### `GET /games/{gameId}`

```json
{
  "id": "...",
  "systemId": "...",
  "name": "Sky Garden Quest",
  "fileName": "Sky Garden Quest.gba",
  "kind": "file",
  "files": [
    { "role": "rom",   "path": "Roms/Game Boy Advance (GBA)/Sky Garden Quest.gba", "sizeBytes": 16777216, "modifiedAt": "..." },
    { "role": "save",  "path": "Saves/GBA/Sky Garden Quest.gba.sav", "sizeBytes": 131072, "modifiedAt": "..." },
    { "role": "state", "slot": 0, "path": "...", "sizeBytes": 401408, "modifiedAt": "..." },
    { "role": "cheats","path": "Cheats/GBA/Sky Garden Quest.gba.cht", "sizeBytes": 2048, "modifiedAt": "..." },
    { "role": "art",   "path": "Roms/Game Boy Advance (GBA)/.media/Sky Garden Quest.png", "sizeBytes": 50211, "modifiedAt": "..." }
  ],
  "totalBytes": 17377096
}
```

`path` est **informatif** (affiché à l'utilisateur, relatif à la racine de la carte SD). L'app ne DOIT jamais le renvoyer au serveur.

### `GET /games/{gameId}/art`

`image/png`, ou `404 not_found`. En-têtes `ETag` et `Cache-Control: private, max-age=86400` ; l'app met en cache et envoie `If-None-Match` (réponse `304` si inchangée).

### `PUT /games/{gameId}/art` et `DELETE /games/{gameId}/art` (M8)

Définir ou retirer la jaquette d'un jeu (décision du 19/11/2026). Corps de `PUT` : un PNG (`Content-Type: image/png`, 4 Mio au plus, signature PNG et dimensions vérifiées, au plus 1024 × 1024) ; l'app convertit et réduit l'image choisie (Photos ou Fichiers) avant l'envoi. Le pak l'enregistre de façon atomique à l'emplacement que NextUI lit (`.media/<nom>.png`, voir 05 §3.1) ; l'ancienne jaquette part d'abord à la corbeille (raison `overwrite`). `DELETE` déplace la jaquette à la corbeille. Réponses : `200` avec le jeu mis à jour (`hasArt`), `404` si le jeu n'existe pas, `413`, `422 unsupported_format` si le corps n'est pas un PNG valide.

### `GET /games/{gameId}/deletion?keepSave=false` (M8)

Aperçu en lecture seule de `DELETE /games/{gameId}` avec le même `keepSave`, calculé par la même sélection que la suppression (source unique) : l'app affiche la **liste exacte** avant de demander confirmation (06). Réponse `200` : `{ "moved": [DeletionItem], "kept": [DeletionItem], "movedBytes": int64 }`, où `DeletionItem` = `{ "name", "role", "size" }`. Les dossiers sont développés en fichiers, récursivement. `name` est informatif, jamais absolu : relatif au dossier du système pour les fichiers de `Roms/`, relatif à la carte SD pour les autres fichiers du jeu (`Saves/GBA/<rom>.sav`), `ReminisLink/...` pour les copies du pak (verrous, historique de reprise). `role` : un rôle de `GET /games/{gameId}` (`rom`, `save`, `rtc`, `state`, `cheats`, `art`) ou `other` (annexes des states, `.cht.bak`, fichiers d'un jeu dossier hors playlist, copies du pak). `moved` : ce qui partirait à la corbeille, copies du pak comprises ; `kept` : ce qui reste volontairement en place (disques encore listés par une autre playlist et pistes de leur `.cue` ; avec `keepSave=true`, save, `.rtc`, states et annexes, verrous et historique). `movedBytes` : taille de tous les fichiers de `moved`. Erreurs : celles de `GET /games/{gameId}`.

### `DELETE /games/{gameId}?keepSave=false`

1. **Déplace** dans la corbeille ReminisLink (`trash/<horodatage>/`, en conservant le chemin d'origine, voir [05](05-console-pak.md)) : la ROM (ou le `.m3u` et son dossier), la save, les states, les cheats et la jaquette. Rien n'est effacé définitivement par cette route : c'est un renommage sur la même carte SD, donc instantané.
2. Déplace aussi dans cette entrée de corbeille les verrous et l'historique de reprise du jeu (copies internes de ReminisLink, non listées).
3. Avec `keepSave=true`, la save et les states restent en place, **ainsi que les verrous et l'historique de reprise**, qui les protègent. *(Décision du 18/11/2026.)*

Réponse :

```json
{
  "trashEntryId": "tr_20261118T170000Z_a1b2",
  "moved": ["Roms/...", "Saves/...", "Cheats/..."],
  "movedBytes": 17377096
}
```

L'espace n'est libéré qu'à la purge de la corbeille : automatiquement après 30 jours d'âge crédité (au lancement du pak, voir [05 §4](05-console-pak.md#4-données-de-reminislink-sur-la-carte-sd)), ou tout de suite avec `DELETE /trash` ou `DELETE /trash/{entryId}`. L'app le dit à l'utilisateur. Le champ `expiresAt` d'une entrée est une **estimation** (« au plus tôt ») : la purge peut avoir lieu plus tard, jamais plus tôt.

### `GET /trash`

```json
{
  "entries": [
    {
      "id": "tr_20261118T170000Z_a1b2",
      "trashedAt": "2026-11-18T17:00:00Z",
      "reason": "delete-game",
      "label": "Sky Garden Quest",
      "files": ["Roms/Game Boy Advance (GBA)/Sky Garden Quest.gba", "Saves/GBA/Sky Garden Quest.gba.sav"],
      "sizeBytes": 17377096,
      "expiresAt": "2026-12-18T17:00:00Z"
    }
  ],
  "totalBytes": 17377096
}
```

Une entrée regroupe tout ce qu'une même opération a mis à la corbeille. `reason` : `delete-game`, `delete-state`, `overwrite`, `replace-rom`, `lock-restore`, `restore`, `delete-backup` (suppression d'une copie de save, copies automatiques au-delà de 10, entrées d'historique au-delà de 5).

### `DELETE /trash` et `DELETE /trash/{entryId}`

Suppression **définitive**, seule route qui efface réellement des données de l'utilisateur. Exige l'en-tête `X-RL-Confirm: empty-trash` (sinon `400 invalid_request`), en plus de la signature. Réponse : `{ "freedBytes": 17377096 }`.

La restauration depuis la corbeille n'est pas dans la v1 (voir [09](09-feuille-de-route.md)) ; les fichiers restent récupérables à la main dans `/.userdata/shared/ReminisLink/trash/`.

---

## Saves

### `GET /saves` (M9)

Liste, en lecture seule et en une requête, toutes les saves de tous les jeux, pour la synchronisation vers l'iPhone (09, M9) : l'app ne télécharge que les saves dont le `sha256` a changé depuis sa dernière sync.

```json
{
  "saves": [
    {
      "gameId": "...",
      "systemId": "...",
      "gameName": "Sky Garden Quest",
      "fileName": "Sky Garden Quest.gba",
      "path": "Saves/GBA/Sky Garden Quest.gba.sav",
      "sizeBytes": 131072,
      "modifiedAt": "2026-11-18T16:55:00Z",
      "sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
      "rtc": { "path": "Saves/GBA/Sky Garden Quest.gba.rtc", "sizeBytes": 16, "modifiedAt": "...", "sha256": "..." }
    }
  ],
  "truncated": false,
  "generatedAt": "2026-11-19T10:00:00Z"
}
```

- Mêmes jeux et même résolution de la save que `GET /systems/{systemId}/games` et `GET /games/{gameId}` (types `file`, `m3u`, `folder-m3u`, `folder-cue`, sous-dossiers compris ; save nommée d'après la ROM, le `.m3u` ou la `.cue`, sans tenir compte de la casse).
- Un jeu n'est listé que s'il a une save (`.sav`). Le fichier d'horloge (`.rtc`) appartient à sa save (M5) : il est donné dans `rtc` (`null` s'il n'y en a pas), jamais listé seul ; un jeu qui n'a qu'un `.rtc` n'est pas listé.
- Deux jeux d'un même système portant le même nom de fichier dans des dossiers différents partagent une save sur NextUI : les deux sont listés, avec le même `path` et le même `sha256`.
- Les saves de 1 Mio au plus (presque toutes) sont hachées à chaque requête : une save réécrite sur place peut garder sa taille et sa date, et exFAT n'a pas d'autre indicateur de changement. Au-delà, le pak réutilise un SHA-256 gardé en mémoire tant que le fichier (périphérique et inode), sa taille, sa date de modification et, si la plateforme en a une, sa date de changement d'état sont inchangés, et seulement si la date de modification précède d'au moins 3 s le calcul du hash (règle « racy git »). Les liens symboliques, les fichiers atteints par un lien (même resté dans le dossier des saves) et ceux hors du dossier des saves sont ignorés. Une seule liste est calculée à la fois (une seconde requête attend). La requête s'arrête si le client se déconnecte.
- Au plus 10 000 saves ; au-delà, `truncated` vaut `true` et l'app ne DOIT rien déduire d'une save absente de la liste. Pas de pagination (comme les autres listes).
- `path` est informatif, jamais renvoyé ; `modifiedAt` vient de l'horloge de la console (informatif) : l'app compare `sha256`.
- Téléchargement : `GET /games/{gameId}/save` et, si `rtc` n'est pas `null`, `GET /games/{gameId}/save/rtc`.

### `GET /games/{gameId}/save`

`application/octet-stream`, `Content-Disposition: attachment; filename="<nom>"`. `404` si pas de save.

### `GET /games/{gameId}/save/rtc` (M9)

Le fichier d'horloge (`.rtc`) écrit par NextUI à côté de la save, pour que l'app le garde avec sa copie de la save. `application/octet-stream`, `Content-Disposition` comme ci-dessus. `404` si le jeu n'a pas de fichier d'horloge, ou s'il est un lien symbolique, atteint par un lien ou hors du dossier des saves.

### `PUT /games/{gameId}/save`

Corps : la nouvelle save (≤ 64 Mio, sinon passer par `/uploads` avec `target.kind = "save"`). L'ancienne save est d'abord copiée en sauvegarde avec le libellé `Avant remplacement`. Réponse : `200` avec la nouvelle copie de sauvegarde créée (`backup`).

### `GET /games/{gameId}/save/backups`

```json
{
  "backups": [
    { "id": "bk_20261118T170000Z_a1b2", "label": "Avant le boss final", "createdAt": "2026-11-18T17:00:00Z", "sizeBytes": 131072, "reason": "manual" }
  ]
}
```

`reason` : `manual`, `before-replace`, `before-restore`, `before-delete`, `before-patch`.

### `POST /games/{gameId}/save/backups`

Corps : `{ "label": "Avant le boss final" }` (optionnel, 80 caractères maximum). Crée une copie de la save actuelle. C'est la fonction « dupliquer la save ». Réponse `201` avec la copie.

### `POST /games/{gameId}/save/backups/{backupId}/restore`

Copie d'abord la save actuelle (`before-restore`), puis remplace la save par la copie choisie. Réponse `200` : `{ "backup": {...} }`, la copie de sécurité créée, ou `null` s'il n'y avait pas de save.

### `GET` / `DELETE /games/{gameId}/save/backups/{backupId}`

Télécharger le fichier de la copie ; supprimer la copie (`204`).

---

## States

### `GET /games/{gameId}/states`

```json
{
  "slots": [
    { "slot": 0, "sizeBytes": 401408, "modifiedAt": "...", "hasThumbnail": true, "locked": true, "lockStatus": "intact" },
    { "slot": 1, "sizeBytes": 401408, "modifiedAt": "...", "hasThumbnail": false, "locked": false, "lockStatus": null }
  ],
  "resumeSlot": 9
}
```

- `lockStatus` : `intact` (identique à la copie verrouillée : taille et date, SHA-256 en cas de doute), `restored` (écrasé en jeu, restauré automatiquement au lancement du pak ; signalé une seule fois), `missing` (le state a disparu, la copie verrouillée existe), `modified` (différent de la copie et non restauré : maintenance sautée, copie corrompue, format non géré), `core-changed` (le pak émulateur utilise un autre cœur que celui de la copie : rien n'est restauré). *(Valeurs `modified` et `core-changed` ajoutées en M5.)*
- `slot` : numéro d'emplacement de NextUI. Les emplacements 0 à 7 sont ceux de l'utilisateur (affichés 1 à 8 dans NextUI, l'app DEVRAIT afficher `slot + 1`), 8 est un state « par défaut » caché, 9 la reprise automatique (code NextUI `ma_menu.c:117`, `defines.h:28`).
- `resumeSlot` : numéro de l'emplacement de reprise automatique, `9` sur NextUI (code NextUI `defines.h:28`), ou `null`. Représentation de la reprise quand `stateFormat` ≠ 0 (fichier `.state.auto`) : décision ouverte, voir [m0/questions.md](m0/questions.md).

### `GET` / `DELETE /games/{gameId}/states/{slot}`

Télécharger ; supprimer (le state est déplacé dans la corbeille). La suppression d'un emplacement verrouillé renvoie `409 state_locked`. Réponse de `DELETE` : `200` avec `{ "trashEntryId": "...", "moved": [...], "movedBytes": 401408 }`, comme `DELETE /games/{gameId}`.

### `GET /games/{gameId}/states/{slot}/thumbnail`

`image/png` si une vignette existe, sinon `404`. NextUI en enregistre une pour les emplacements 0 à 7 (pas pour 8 ni 9) dans `.userdata/shared/.minui/<TAG>/<rom.ext>.<N>.bmp` ; malgré l'extension, le contenu est un PNG, capture plein écran (1024×768 sur Brick) (code NextUI `ma_menu.c:1617, 1713-1714`). Le pak vérifie la signature PNG avant de servir le fichier. Taille servie (telle quelle ou réduite) : décision ouverte, voir [m0/questions.md](m0/questions.md).

### `PUT /games/{gameId}/states/{slot}/lock`

Copie le state dans la zone des verrous et enregistre son SHA-256. Réponse `200` avec l'emplacement mis à jour.

### `DELETE /games/{gameId}/states/{slot}/lock`

Retire le verrou. La copie verrouillée est effacée si elle est identique au state ; sinon (state absent ou différent) elle part dans la corbeille. Le state lui-même ne change pas. Réponse `200` avec l'emplacement mis à jour.

### `POST /games/{gameId}/states/{slot}/restore`

Remplace le state par la copie verrouillée (le state actuel part d'abord dans la corbeille). Réponse `200` avec l'emplacement mis à jour.

### `GET /games/{gameId}/resume-history`

```json
{ "entries": [ { "id": "rh_20261118T165500Z", "capturedAt": "2026-11-18T16:55:00Z", "sizeBytes": 401408 } ] }
```

Les 5 dernières versions distinctes du state de reprise, archivées par le pak à chaque lancement.

### `POST /games/{gameId}/resume-history/{entryId}/restore`

Remplace le state de reprise par cette version (la version actuelle est d'abord archivée). Réponse `200` avec l'historique mis à jour (même forme que `GET /resume-history`).

---

## Cheats

Le format `.cht` est celui de RetroArch, géré comme dans Cheat Manager (clés inconnues conservées, fichier précédent gardé en `.cht.bak` ; l'ancien `.bak` passe par la corbeille avant d'être remplacé).

### `GET /games/{gameId}/cheats`

```json
{
  "cheats": [
    { "index": 0, "description": "Infinite money", "code": "82025490+FFFF", "enabled": false, "extra": { "handler": "0" } }
  ],
  "etag": "\"sha256-...\""
}
```

Les clés inconnues d'un cheat sont transmises dans `extra` **sans** le préfixe d'index du fichier (`handler` pour `cheat0_handler`) ; le pak ajoute le bon préfixe à l'écriture, selon la position du cheat. *(M5.)*

### `PUT /games/{gameId}/cheats`

Corps : `{ "cheats": [ ... ] }` (liste complète, dans l'ordre) avec l'en-tête `If-Match: <etag>`. Si le fichier a changé entre-temps : `412 Precondition Failed` (code `conflict`). Une liste vide supprime le fichier `.cht` (avec `.bak`).

### `POST /games/{gameId}/cheats/import`

```json
{ "source": { "type": "url", "url": "https://..." }, "mode": "merge" }
```

- `source.type` : `url` (téléchargé par la console, 5 Mio maximum, synchrone), `sd` (`sdFileId` venant de `GET /sd/files`), `inline` (`text` : contenu du `.cht`, 1 Mio maximum), `upload` (`uploadId` d'un envoi terminé avec `target.kind = "staging"`, connu seulement pendant la session du pak), `staging` (`stagingId` renvoyé par la fin de l'envoi : à préférer, il survit à un redémarrage du pak).
- `mode` : `merge` (ajoute à la suite) ou `replace`.
- Réponse : `{ "imported": 15, "total": 27 }`. `422 unsupported_format` si aucun cheat n'a été trouvé.

### `GET /sd/files?type=cht|patch`

Liste les fichiers utilisables de la carte SD : `.cht` sous `Cheats/` pour `type=cht`, patchs (`.ips`, `.bps`, `.ups`) sous `Roms/` et sous le dossier `ReminisLink/patches/` pour `type=patch`.

```json
{ "files": [ { "id": "...", "path": "Cheats/GBA/Sky Garden Quest.gba.cht", "sizeBytes": 2048 } ] }
```

---

## Transferts

### `POST /uploads`

```json
{
  "target": { "kind": "rom", "systemId": "...", "folderId": null, "fileName": "Sky Garden Quest DX.gba" },
  "sizeBytes": 33554432,
  "sha256": "9f86d08...",
  "onConflict": "fail"
}
```

`target.kind` :

| `kind` | Champs | Destination |
|---|---|---|
| `rom` | `systemId`, `folderId` (optionnel), `fileName` | `Roms/<système>/[<dossier>/]<fileName>` |
| `save` | `gameId` | La save du jeu (l'ancienne est sauvegardée) |
| `state` | `gameId`, `slot` | Un emplacement de state (refusé si verrouillé) |
| `staging` | `fileName` | Zone temporaire ReminisLink, pour un patch ou un `.cht` à importer ensuite (supprimée après 24 h) |

`onConflict` : `fail` (par défaut, `409 conflict`), `rename` (ajoute ` (2)`, ` (3)`...), `overwrite` (l'ancien fichier part dans la corbeille). Ne s'applique qu'aux cibles `rom` et `staging` : pour `save` et `state`, le fichier existant est toujours remplacé, après avoir été mis à la corbeille (M4 ; M5 y ajoute la copie de sauvegarde `before-replace`).

Limites (M4) : au plus 32 envois ouverts et 100 tâches en file ou en cours par console ; au-delà, `429 too_many_attempts` avec `Retry-After`. Une ROM (`target.kind = "rom"`) doit porter un nom que la bibliothèque liste comme jeu, sinon `400 invalid_name` dès la création. Les noms commençant par `.`, contenant des caractères de mise en forme Unicode ou réservés sous Windows (`CON`, `NUL`, `COM1`…) sont refusés (`invalid_name`).

Réponse `201` :

```json
{ "uploadId": "up_01J...", "chunkSize": 4194304, "offset": 0, "expiresAt": "2026-11-19T17:00:00Z" }
```

### `GET /uploads/{uploadId}`

`{ "uploadId": "...", "offset": 12582912, "sizeBytes": 33554432 }`

### `PUT /uploads/{uploadId}`

En-têtes : `Upload-Offset`, `Content-Length`, `X-RL-Content-SHA256: UNSIGNED`. Corps : le morceau. Réponse `204` avec `Upload-Offset`.

### `POST /uploads/{uploadId}/complete`

Réponse `200` selon la cible : `{ "gameId": "..." }` (`rom`), `{ "backup": {...} }` (`save`), `{ "slot": {...} }` (`state`) ou `{ "stagingId": "..." }` (`staging`).

### `POST /downloads`

```json
{
  "url": "https://example.org/hack.bps",
  "target": { "kind": "staging" },
  "sha256": null,
  "onConflict": "rename"
}
```

Même `target` que pour `/uploads` (le nom de fichier est déduit de `Content-Disposition` ou de l'URL s'il est absent). Schémas `http` et `https` uniquement, 5 redirections maximum, 4 Gio maximum. Réponse `202` : `{ "jobId": "job_..." }`.

### `POST /games/{gameId}/patch`

```json
{
  "patch": { "type": "staging", "stagingId": "..." },
  "output": { "mode": "new-file", "fileName": "Sky Garden Quest (FR).gba" }
}
```

- `patch.type` : `staging` (envoi ou téléchargement préalable), `sd` (`sdFileId`).
- `output.mode` : `new-file` (par défaut, crée une nouvelle ROM à côté de l'originale) ou `replace` (remplace la ROM, l'originale part dans la corbeille).
- Formats : IPS (dont l'extension EOF de troncature), BPS (vérification des CRC32 source, cible et patch), UPS (vérification des CRC32).
- Réponse `202` : `{ "jobId": "job_..." }`. Le résultat de la tâche contient `gameId` de la ROM produite.

### `GET /jobs`, `GET /jobs/{jobId}`, `DELETE /jobs/{jobId}`

Format décrit en [03 §8](03-protocole.md#8-tâches-opérations-longues-côté-console). `DELETE` annule une tâche en attente ou en cours (le fichier partiel est supprimé) ; réponse `200` avec la tâche dans son nouvel état. Une tâche annulée pendant l'installation de son fichier peut finir `succeeded` (le fichier est en place, `result` l'indique).
