# 04 — API v1

Toutes les routes commencent par `/api/v1`. Sauf mention contraire, elles sont **signées** (voir [03 §4](03-protocole.md#4-authentification-des-requêtes)) et répondent en JSON. Ce document est la base de `protocol/openapi.yaml`, qui reste la référence une fois écrit.

## Sommaire

| Méthode | Route | Rôle |
|---|---|---|
| GET | `/hello` | Présentation de la console (**non signée**) |
| POST | `/pair` | Appairage (**non signée**) |
| DELETE | `/clients/me` | Se désappairer |
| GET | `/device` | État de la console |
| GET | `/systems` | Liste des systèmes |
| GET | `/systems/{systemId}/games` | Contenu d'un système (dossiers et jeux) |
| GET | `/games/{gameId}` | Détail d'un jeu et de ses fichiers |
| GET | `/games/{gameId}/art` | Jaquette (PNG) |
| DELETE | `/games/{gameId}` | Supprimer un jeu et ses données (vers la corbeille) |
| GET | `/trash` | Contenu et taille de la corbeille |
| DELETE | `/trash` | Vider la corbeille |
| DELETE | `/trash/{entryId}` | Supprimer définitivement une entrée de la corbeille |
| GET / PUT | `/games/{gameId}/save` | Télécharger / remplacer la save |
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
  "osVersion": "6.14.0",
  "pakVersion": "1.0.0",
  "apiVersion": 1,
  "minClientVersion": "1.0.0",
  "serverTime": "2026-11-18T17:00:00Z",
  "pairingOpen": true
}
```

Aucune autre information (pas de liste de jeux, pas de clients). Limitée à 10 requêtes par seconde par IP (au-delà : `429 too_many_attempts`).

### `POST /pair` (non signée)

Voir [03 §3](03-protocole.md#3-appairage). `method` : `qr` ou `pin`. `clientName` : 1 à 64 caractères. `clientId` et `deviceId` : UUID en minuscules.

### `DELETE /clients/me`

Supprime le client appelant de la console. Réponse `204`. L'app supprime ensuite la clé de son Trousseau.

### `GET /device`

```json
{
  "deviceId": "7f3a...",
  "deviceName": "Brick de Thomas",
  "model": "brick",
  "platform": "tg5040",
  "os": "nextui",
  "osVersion": "6.14.0",
  "pakVersion": "1.0.0",
  "network": { "ipv4": "192.168.1.42", "ssid": "MaBox" },
  "battery": { "percent": 78, "charging": false },
  "storage": [
    { "id": "sd", "label": "SD", "totalBytes": 127865454592, "freeBytes": 40265318400 }
  ],
  "uptimeSeconds": 512,
  "connectedClients": [
    { "clientName": "iPhone de Thomas", "lastSeenAt": "2026-11-18T17:00:00Z" }
  ]
}
```

`connectedClients` : les clients appairés dont une requête signée a été acceptée dans les 30 dernières secondes (même règle que l'état « Connecté » de l'écran de la console, [05 §7.1](05-console-pak.md#71-écran-principal)), le client appelant compris. Tenu **en mémoire** par le pak, sans écriture sur la carte SD ; vide au lancement. *(Ajouté le 18/11/2026.)*

`ssid`, `battery` et `osVersion` PEUVENT être `null` si l'information n'est pas disponible **[À VÉRIFIER M0 : sources sur NextUI]**.

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

- `kind` : `file` (ROM simple), `m3u` (fichier `.m3u`), `folder-m3u` (dossier contenant un `.m3u` du même nom, jeu multi-disques).
- Les fichiers ignorés sont ceux de Cheat Manager (`.txt`, `.png`, `.sav`, `.cht`, `.bak`...) et les fichiers cachés.
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

### `DELETE /games/{gameId}?keepSave=false`

1. **Déplace** dans la corbeille ReminisLink (`trash/<horodatage>/`, en conservant le chemin d'origine, voir [05](05-console-pak.md)) : la ROM (ou le `.m3u` et son dossier), la save, les states, les cheats et la jaquette. Rien n'est effacé définitivement par cette route : c'est un renommage sur la même carte SD, donc instantané.
2. Supprime les verrous et l'historique de reprise du jeu (copies internes de ReminisLink, sans valeur une fois le jeu supprimé).
3. Avec `keepSave=true`, la save et les states restent en place.

Réponse :

```json
{
  "trashEntryId": "tr_20261118T170000Z_a1b2",
  "moved": ["Roms/...", "Saves/...", "Cheats/..."],
  "movedBytes": 17377096
}
```

L'espace n'est libéré qu'à la purge de la corbeille : automatiquement après 30 jours (au lancement du pak), ou tout de suite avec `DELETE /trash` ou `DELETE /trash/{entryId}`. L'app le dit à l'utilisateur.

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

Une entrée regroupe tout ce qu'une même opération a mis à la corbeille. `reason` : `delete-game`, `delete-state`, `overwrite`, `replace-rom`, `lock-restore`, `restore`.

### `DELETE /trash` et `DELETE /trash/{entryId}`

Suppression **définitive**, seule route qui efface réellement des données de l'utilisateur. Exige l'en-tête `X-RL-Confirm: empty-trash` (sinon `400 invalid_request`), en plus de la signature. Réponse : `{ "freedBytes": 17377096 }`.

La restauration depuis la corbeille n'est pas dans la v1 (voir [09](09-feuille-de-route.md)) ; les fichiers restent récupérables à la main dans `/.userdata/shared/ReminisLink/trash/`.

---

## Saves

### `GET /games/{gameId}/save`

`application/octet-stream`, `Content-Disposition: attachment; filename="<nom>"`. `404` si pas de save.

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

- `lockStatus` : `intact` (identique à la copie verrouillée), `restored` (écrasé en jeu, restauré automatiquement au lancement du pak), `missing` (le state a disparu, la copie verrouillée existe).
- `resumeSlot` : numéro de l'emplacement de reprise automatique **[À VÉRIFIER M0]**, ou `null`.

### `GET` / `DELETE /games/{gameId}/states/{slot}`

Télécharger ; supprimer (le state est déplacé dans la corbeille). La suppression d'un emplacement verrouillé renvoie `409 state_locked`. Réponse de `DELETE` : `200` avec `{ "trashEntryId": "...", "moved": [...], "movedBytes": 401408 }`, comme `DELETE /games/{gameId}`.

### `GET /games/{gameId}/states/{slot}/thumbnail`

`image/png` si NextUI enregistre une capture avec le state **[À VÉRIFIER M0]**, sinon `404`.

### `PUT /games/{gameId}/states/{slot}/lock`

Copie le state dans la zone des verrous et enregistre son SHA-256. Réponse `200` avec l'emplacement mis à jour.

### `DELETE /games/{gameId}/states/{slot}/lock`

Retire le verrou et supprime la copie verrouillée. Le state lui-même ne change pas. Réponse `200` avec l'emplacement mis à jour.

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

Le format `.cht` est celui de RetroArch, géré comme dans Cheat Manager (clés inconnues conservées, fichier précédent gardé en `.cht.bak`).

### `GET /games/{gameId}/cheats`

```json
{
  "cheats": [
    { "index": 0, "description": "Infinite money", "code": "82025490+FFFF", "enabled": false, "extra": { "cheat0_handler": "0" } }
  ],
  "etag": "\"sha256-...\""
}
```

### `PUT /games/{gameId}/cheats`

Corps : `{ "cheats": [ ... ] }` (liste complète, dans l'ordre) avec l'en-tête `If-Match: <etag>`. Si le fichier a changé entre-temps : `412 Precondition Failed` (code `conflict`). Une liste vide supprime le fichier `.cht` (avec `.bak`).

### `POST /games/{gameId}/cheats/import`

```json
{ "source": { "type": "url", "url": "https://..." }, "mode": "merge" }
```

- `source.type` : `url` (téléchargé par la console, 5 Mio maximum, synchrone), `sd` (`sdFileId` venant de `GET /sd/files`), `inline` (`text` : contenu du `.cht`, 1 Mio maximum), `upload` (`uploadId` d'un envoi terminé avec `target.kind = "staging"`).
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

`onConflict` : `fail` (par défaut, `409 conflict`), `rename` (ajoute ` (2)`, ` (3)`...), `overwrite` (l'ancien fichier part dans la corbeille).

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

Format décrit en [03 §8](03-protocole.md#8-tâches-opérations-longues-côté-console). `DELETE` annule une tâche en attente ou en cours (le fichier partiel est supprimé) ; réponse `200` avec la tâche annulée.
