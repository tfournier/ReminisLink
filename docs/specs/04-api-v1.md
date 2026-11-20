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
| GET | `/events` | Flux d'événements de la console (Server-Sent Events, M14) |
| GET | `/systems` | Liste des systèmes |
| GET | `/systems/{systemId}/games` | Contenu d'un système (dossiers et jeux) |
| GET | `/games` | Tous les jeux de tous les systèmes, à plat (M11) |
| GET | `/games/{gameId}` | Détail d'un jeu et de ses fichiers |
| GET | `/games/{gameId}/files/{fileId}` | Télécharger un fichier du jeu (M11) |
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
| GET | `/paks` | Paks d'outils et d'émulateurs installés (M12) |
| DELETE | `/paks/{pakId}` | Supprimer un pak (vers la corbeille, M12) |
| POST | `/paks/install` | Installer un pak depuis la carte SD (crée une tâche, M12) |
| GET | `/sd/browse` | Contenu d'un dossier de la carte SD, en lecture (M12) |
| GET | `/sd/browse/{sdEntryId}/content` | Télécharger un fichier de la carte SD (M12) |

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
  "apiVersion": 2,
  "minClientVersion": "1.0.0",
  "serverTime": "2026-11-18T17:00:00Z",
  "pairingOpen": true
}
```

`apiVersion` vaut `2` depuis M13 (TLS épinglé, appairage v2, signature RL2-ED25519). Une app appairée à un pak plus ancien (RL1) qui lit `2` propose de réappairer : le pak a effacé les appairages RL1. Elle peut lire `/hello` sans épinglage dans ce seul but ([03 §1](03-protocole.md#1-transport)). `writerHeld` (M10) est retiré avec le bail d'écriture.

Aucune autre information (pas de liste de jeux, pas de clients). Limitée à 10 requêtes par seconde par IP (au-delà : `429 too_many_attempts`).

### `POST /pair` (non signée)

Voir [03 §3](03-protocole.md#3-appairage) (appairage v2, M13). Corps : `clientId`, `clientName` (1 à 64 caractères, saisi par l'utilisateur à l'appairage), `platform` (`apple`, `android`, `web` ou `other`), `clientPublicKey` (clé publique Ed25519, 32 octets en base64url), `clientNonce`, `proof`. Plus de champ `method` ni de PIN. `clientId` et `deviceId` : UUID en minuscules. Réponse : `deviceId`, `deviceName`, `deviceNonce`, `proof`.

### `DELETE /clients/me`

Supprime le client appelant de la console. Réponse `204`. L'app oublie ensuite cette console ; sa propre clé reste dans le Trousseau (elle peut servir à d'autres consoles et aux autres appareils Apple du compte).

### `POST /clients/me/connect` (M10)

Lève la déconnexion décidée depuis la console (Réglages > Appareils, [05 §7.2](05-console-pak.md#72-réglages-y)) pour le client appelant. Réponse `204`, sans effet si le client n'était pas déconnecté. L'app ne l'appelle que sur une action de l'utilisateur (ouverture de cette console, bouton « Se reconnecter »), jamais lors d'une reconnexion automatique. C'est la seule route signée permise à un client déconnecté. Un appel dont le pak a chargé la clé avant une nouvelle déconnexion par la console ne lève pas celle-ci (`401 disconnected`). Le pak ne peut pas savoir quand l'app a envoyé la requête : un appel dont les en-têtes arrivent après la déconnexion la lève, comme une nouvelle action de l'utilisateur.

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
  ]
}
```

`writer` (M10) est retiré en M13 avec le bail d'écriture.

`connectedClients` : les clients appairés dont une requête signée a été acceptée dans les 30 dernières secondes, ou qui ont un flux d'événements ouvert (`GET /events`, M14 : connectés tant que le flux est ouvert, `lastSeenAt` vaut alors l'heure courante ; à la fermeture de leur dernier flux, les 30 s partent de ce moment ; une déconnexion ou un oubli depuis la console les retire aussitôt) (même règle que l'état « Connecté » de l'écran de la console, [05 §7.1](05-console-pak.md#71-écran-principal)), le client appelant compris. Tenu **en mémoire** par le pak, sans écriture sur la carte SD ; vide au lancement. *(Ajouté le 18/11/2026.)*

`ssid`, `battery` et `osVersion` PEUVENT être `null` si l'information n'est pas disponible. Sources sur NextUI ([05 §6](05-console-pak.md#6-informations-de-la-console)) : `osVersion` = `.system/version.txt` (chaîne de release telle que l'affiche NextUI, ex. `NextUI-20261013-039a06de` : date de build et hash court ; jamais le numéro `v6.x.y` ; décision D4 du 19/11/2026) ; `battery` = `axp2202-battery/capacity` et `axp2202-usb/online` + `time_to_full_now` ; `ssid` = `wpa_supplicant` (`null` si `wpa_state` ≠ `COMPLETED`).

### `GET /events` (M14)

Flux **Server-Sent Events** (`Content-Type: text/event-stream`, UTF-8, `Cache-Control: no-store`). Route signée comme les autres, de lecture : la signature n'est vérifiée qu'à l'ouverture, elle n'attend jamais une écriture en cours, un client déconnecté depuis la console reçoit `401 disconnected`. Le flux reste ouvert tant que le pak tourne.

```text
id: 1
event: hello
data: {"serverTime":"2026-11-20T08:00:00Z","keepAliveSeconds":15}

: keep-alive

id: 2
event: library
data: {"systemIds":["R2FtZSBCb3kgQWR2YW5jZSAoR0JBKQ"]}

id: 3
event: shutdown
data: {"reason":"quit"}
```

Chaque événement : `id: <n>` (croissant dans le flux, à partir de 1), `event: <type>`, une ligne `data: <JSON>`, puis une ligne vide. `Last-Event-ID` est ignoré : après une reconnexion, l'app recharge ce qu'elle affiche. Les types inconnus doivent être ignorés.

| Type | `data` | Quand |
|---|---|---|
| `hello` | `{ serverTime, keepAliveSeconds }` | Premier événement, à l'ouverture |
| `library` | `{ systemIds }` | Après toute modification de la carte faite par l'API, quel que soit le client : envoi installé (pas un envoi vers la zone d'attente), suppression d'un jeu, jaquette, corbeille, save remplacée, copie de save créée, supprimée ou restaurée, state supprimé, verrouillé, déverrouillé ou restauré, historique de reprise restauré, cheats, et tâche de téléchargement (hors zone d'attente) ou de patch réussie. `systemIds` nomme les systèmes touchés ; **vide** quand ils ne sont pas connus (corbeille) : l'app rafraîchit tout. Au plus un par seconde : les changements entre deux événements sont fusionnés (un « inconnu » l'emporte). |
| `paks` | `{}` (M12) | Après l'installation (tâche `pak-install`, ou tâche `download` de cible `pak`, réussie) ou la suppression d'un pak, quel que soit le client : l'app relit `GET /paks`. Au plus un par seconde (fusionnés). Un changement de pak n'envoie pas `library`. |
| `job` | un `Job` ([03 §8](03-protocole.md#8-tâches-opérations-longues-côté-console)) | À chaque changement d'état d'une tâche (ou de `cancelRequested`), aussitôt ; pendant qu'elle avance, au plus une fois par seconde par tâche (le dernier état l'emporte). Tâches de tous les clients. |
| `device` | `{ battery, connectedClients, storage }` (mêmes champs que `GET /device`) | Quand la batterie (pourcentage ou charge), l'ensemble des clients connectés (pas leur `lastSeenAt`) ou l'espace libre (d'au moins 1 Mio, le journal le change sans cesse) change ; lu toutes les 5 s, et seulement tant qu'au moins un flux est ouvert ; un client qui devient connecté ou est révoqué (déconnecté, oublié, désappairé) déclenche aussi l'événement, sans attendre le relevé. Au plus un toutes les 5 s par flux, comptées depuis l'écriture du précédent. L'événement est construit au moment où il est écrit : batterie et espace du dernier relevé, `connectedClients` de cet instant (un client retiré par une révocation n'y revient jamais). |
| `shutdown` | `{ reason }` : `quit` (l'utilisateur quitte ReminisLink sur la console) ou `stopped` (le système arrête le pak : extinction, signal) | Au début de la fermeture du pak, avant l'arrêt du serveur HTTP ; puis le flux se termine. L'adieu Bonjour n'attend ni les flux ni les requêtes en cours. |

- **Signe de vie** : un commentaire `: keep-alive` au moins toutes les 15 s. L'app considère 35 s sans aucun octet comme une connexion perdue.
- **Client lent** : chaque écriture (événement ou signe de vie) a un délai de 60 s, comme les téléchargements de fichiers ; un client qui ne lit plus est coupé sans gêner les autres. Les événements sont mis en file par flux (bornée) : un flux trop en retard est fermé, l'app se reconnecte et recharge.
- **Fin du flux** : à la fermeture du pak (après `shutdown`), et sans événement quand le client est déconnecté ou oublié depuis la console, se désappaire (`DELETE /clients/me`) ou est appairé de nouveau : plus rien n'est écrit dès que l'action est faite, et une écriture en cours est interrompue (connexion coupée). Le flux garde sa place dans les limites jusqu'à ce qu'il soit vraiment fermé ; une révocation pendant la fermeture du pak l'emporte sur l'envoi des derniers événements et de `shutdown`.
- **Fermeture du pak** : les événements en attente de la fin de leur intervalle (un `library` fusionné, l'avancement d'une tâche) sont envoyés aussitôt, avant `shutdown`, sans attendre la fin de leur seconde. Les dernières écritures de tous les flux (événements restants et `shutdown`) doivent finir dans la seconde qui suit le début de la fermeture ; un client qui ne lit plus est coupé sans retarder l'arrêt.
- **Limites** : 4 flux ouverts par client, 16 en tout ; au-delà, `429 too_many_attempts` avec `Retry-After` (5 s). Pendant la fermeture : `503 shutting_down`.
- **Présence** : un client qui a au moins un flux ouvert compte comme connecté (`connectedClients` de `GET /device` et des événements `device`, état « Connecté » de la console), quel que soit le temps écoulé depuis sa dernière requête ; à la fermeture de son dernier flux, la règle des 30 s s'applique à partir de ce moment. Une révocation (déconnexion, oubli, désappairage) le retire aussitôt.
- **Pas d'événement `device` initial** : le premier relevé après l'ouverture sert de référence ; l'app lit `GET /device` après `hello` pour avoir l'état de départ, puis suit les événements `device`.

---

## Écritures une à une (M13)

Décision du 19/11/2026, qui remplace le bail d'écriture de M10 (retirés : bail, renouvellement, `GET` / `PUT` / `DELETE /writer`, `writerHeld`, `Device.writer`, `423 read_only`). Plusieurs clients appairés peuvent agir en même temps ; aucun n'est en lecture seule.

- **Écritures sérialisées** : le pak exécute une à une les opérations qui modifient la carte SD : suppressions, remplacements, restaurations, copies de save, verrous de states, cheats (un import attend son tour une fois sa source lue), jaquette, corbeille, et l'étape finale d'un envoi (`POST /uploads/{uploadId}/complete`). La création et l'annulation d'un envoi, ses morceaux (`PUT /uploads/{uploadId}`) et l'annulation d'une tâche ne touchent que la zone d'attente du pak et n'attendent jamais. Une tâche (téléchargement par la console, patch) n'attend son tour que pour l'étape qui installe son fichier, sans délai maximal ni échec pour cette raison.
- **Attente** : une écriture qui arrive pendant une autre attend qu'elle finisse ; l'attente commence une fois le corps reçu et vérifié (le pak ne garde jamais le verrou pendant la lecture d'un corps). Après **10 s** d'attente sans obtenir son tour, la requête reçoit **`429 device_busy`** (message `Device busy`) avec `Retry-After`, sans rien avoir modifié ; l'app réessaie. Un pak M13 répondait `429 too_many_attempts` avec le même message (code dédié ajouté ensuite) : l'app reconnaît aussi cette réponse.
- **Révocation pendant l'attente** (revue du 20/11/2026) : une fois le verrou obtenu, juste avant de modifier la carte, le pak vérifie de nouveau que le client n'a pas été déconnecté, oublié, désappairé ou réappairé pendant l'attente ; sinon la requête reçoit `401 disconnected` ou `401 unknown_client` et rien n'est modifié (vaut aussi pour l'étape finale d'un envoi et pour un import de cheats).
- **Tâches d'un client révoqué** : quand la console déconnecte ou oublie un client (ou qu'il se désappaire ou se réappaire), ses tâches en attente ou en cours sont annulées (`cancelled`) ; une tâche dont l'installation a déjà commencé va jusqu'au bout. Une tâche est liée dès sa création au client et à l'état d'accès de la requête qui l'a lancée : son étape d'installation, une fois le verrou obtenu, vérifie de nouveau cet état et se termine `cancelled` sans rien installer s'il n'est plus valable ; une tâche lancée après une reconnexion n'est jamais annulée par une révocation antérieure.
- **Lectures** : jamais bloquées par une écriture.
- **Risque accepté** (07 §3) : entre l'aperçu d'une suppression (`GET /games/{gameId}/deletion`) et la suppression, un autre client peut changer les fichiers du jeu ; tout fichier supprimé ou remplacé reste 30 jours dans la corbeille.

### Déconnecter et oublier depuis la console

- **Déconnecter** (Réglages > Appareils) : le client disparaît de `connectedClients`, et toutes ses requêtes signées reçoivent **`401 disconnected`** (après la vérification de la signature et la réservation du nonce) jusqu'à ce qu'il appelle `POST /clients/me/connect`. État tenu en mémoire seulement : la fermeture du pak le lève. Un nouvel appairage du même client depuis l'écran d'appairage de la console le lève aussi.
- **Oublier** : l'appairage est supprimé de `clients.json` (écriture atomique) ; ses requêtes reçoivent ensuite `401 unknown_client`, et sa déconnexion est oubliée. **Tout oublier** supprime tous les appairages.

### Erreurs

| Code | HTTP | Sens |
|---|---|---|
| `device_busy` | 429 | `Device busy` : une écriture a attendu 10 s celle en cours (`Retry-After`). Rien n'a été modifié. Un pak M13 envoyait `too_many_attempts` avec ce message. |
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

### `GET /games` (M11)

Tous les jeux de tous les systèmes de `GET /systems` (avec ou sans émulateur), sous-dossiers compris, en une seule liste à plat pour la bibliothèque de l'app (recherche et filtres faits par l'app).

```json
{
  "games": [
    {
      "id": "R2FtZSBCb3kgQWR2YW5jZSAoR0JBKS9IYWNrcy9Ta3kgR2FyZGVuIFF1ZXN0IERYLmdiYQ",
      "systemId": "R2FtZSBCb3kgQWR2YW5jZSAoR0JBKQ",
      "folderId": "R2FtZSBCb3kgQWR2YW5jZSAoR0JBKS9IYWNrcw",
      "folderPath": "Hacks",
      "name": "Sky Garden Quest DX",
      "fileName": "Sky Garden Quest DX.gba",
      "kind": "file",
      "sizeBytes": 16777216,
      "modifiedAt": "2026-10-02T09:12:00Z",
      "hasSave": true,
      "stateCount": 3,
      "lockedStateCount": 1,
      "cheatCount": 12,
      "hasArt": true
    }
  ],
  "truncated": false,
  "generatedAt": "2026-11-19T21:00:00Z"
}
```

- Chaque jeu a les champs de `GET /systems/{systemId}/games`, avec les mêmes règles (ce qui est un jeu, disques d'un `.m3u`, fichiers ignorés), plus `systemId`, `folderId` (l'identifiant que `GET /systems/{systemId}/games` donne au dossier qui contient le jeu, `null` à la racine du système) et `folderPath` (informatif : noms des dossiers sous le système joints par `/`, `null` à la racine).
- Profondeur des sous-dossiers : celle de `gameCount` (la somme des `gameCount` de `GET /systems` est le nombre de jeux).
- Tri par dossier du système puis par chemin, insensible à la casse.
- 20 000 jeux au plus : au-delà, `truncated` vaut `true` (les 20 000 premiers dans cet ordre) et l'app revient à la navigation par système.
- Coût : chaque dossier et chaque dossier de données d'un système (saves, states, cheats) est lu une seule fois. La requête s'arrête si le client se déconnecte.
- Route de lecture : n'attend jamais une écriture.

### `GET /games/{gameId}`

```json
{
  "id": "...",
  "systemId": "...",
  "name": "Sky Garden Quest",
  "fileName": "Sky Garden Quest.gba",
  "kind": "file",
  "files": [
    { "fileId": "...", "role": "rom",   "path": "Roms/Game Boy Advance (GBA)/Sky Garden Quest.gba", "sizeBytes": 16777216, "modifiedAt": "..." },
    { "fileId": "...", "role": "save",  "path": "Saves/GBA/Sky Garden Quest.gba.sav", "sizeBytes": 131072, "modifiedAt": "..." },
    { "fileId": "...", "role": "state", "slot": 0, "path": "...", "sizeBytes": 401408, "modifiedAt": "..." },
    { "fileId": "...", "role": "cheats","path": "Cheats/GBA/Sky Garden Quest.gba.cht", "sizeBytes": 2048, "modifiedAt": "..." },
    { "fileId": "...", "role": "art",   "path": "Roms/Game Boy Advance (GBA)/.media/Sky Garden Quest.png", "sizeBytes": 50211, "modifiedAt": "..." }
  ],
  "totalBytes": 17377096
}
```

`path` est **informatif** (affiché à l'utilisateur, relatif à la racine de la carte SD). L'app ne DOIT jamais le renvoyer au serveur.

`fileId` (M11) identifie le fichier pour `GET /games/{gameId}/files/{fileId}`. Opaque pour l'app ; il reste le même tant que le fichier garde son chemin. Le pak le calcule comme le base64url (sans remplissage) du chemin relatif à la racine de la carte SD, mais ne le décode **jamais** en chemin : il ne sert qu'à être comparé aux `fileId` de la liste courante du jeu.

### `GET /games/{gameId}/files/{fileId}` (M11)

Télécharge un fichier de `files` (ROM, disque, playlist, save, fichier d'horloge, state, cheats, jaquette) pour que l'utilisateur en garde une copie sur l'iPhone.

- Le pak recalcule la liste des fichiers du jeu (même code que `GET /games/{gameId}`) et ne sert que l'entrée dont le `fileId` est **exactement** celui demandé. Un `fileId` absent de cette liste (inconnu, d'un autre jeu, fichier supprimé ou renommé depuis, base64url fabriqué d'un fichier existant hors du jeu) : `404 not_found`. Un `fileId` qui n'est pas un identifiant opaque (caractères hors base64url) : `400 invalid_id`.
- Confinement (règle 2) : le fichier est ouvert à travers `os.Root` du dossier de tête de son rôle (`Roms/`, `Saves/`, `Cheats/`, dossier des states), sans lien symbolique à aucun composant du chemin sous ce dossier, et doit être sous le dossier le plus étroit de son rôle (le dossier du système pour une ROM ou une jaquette, `Saves/<TAG>`, `Cheats/<TAG>`). Un lien, un chemin qui passe par un lien ou un fichier hors de ces dossiers : `404 not_found` (journalisé, sans chemin absolu).
- Réponse `200` : le fichier entier, `Content-Type: application/octet-stream`, `Content-Length`, `Content-Disposition: attachment; filename="<nom>"` (même forme que le téléchargement de la save, avec `filename*` pour un nom non ASCII). Pas de `Range` ni de requête conditionnelle dans cette version.
- Envoi en flux à mémoire constante (disques PS1 de plusieurs centaines de Mo) ; le serveur HTTP du pak n'a pas de `WriteTimeout` : un envoi lent n'est pas coupé tant que des octets passent ; le fichier est écrit par morceaux de 32 Kio au plus, chacun avec un délai de 60 s : un client qui accepte moins de 32 Kio en 60 s est coupé (comme pour les autres téléchargements de fichiers).
- Route de lecture : n'attend jamais une écriture.

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
| `pak` | `pakKind` (`tool`, `emu`, `auto` par défaut), `fileName` (nom de l'archive : affiché sur la console, et nom de repli d'une archive « à plat » sans `name` utilisable dans son `pak.json`) | Archive zip d'un pak, installée par une tâche `pak-install` (M12, voir `POST /paks/install`) ; 1 Gio au plus (`413 too_large`) |

`onConflict` : `fail` (par défaut, `409 conflict`), `rename` (ajoute ` (2)`, ` (3)`...), `overwrite` (l'ancien fichier part dans la corbeille). Ne s'applique qu'aux cibles `rom`, `staging` et `pak` (pour `pak`, `fail` ou `overwrite` seulement ; le nom du pak n'est connu qu'à la lecture de l'archive, le conflit est donc vérifié par la tâche d'installation) : pour `save` et `state`, le fichier existant est toujours remplacé, après avoir été mis à la corbeille (M4 ; M5 y ajoute la copie de sauvegarde `before-replace`).

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

Réponse `200` selon la cible : `{ "gameId": "..." }` (`rom`), `{ "backup": {...} }` (`save`), `{ "slot": {...} }` (`state`), `{ "stagingId": "..." }` (`staging`) ou `{ "jobId": "..." }` (`pak`, M12 : l'archive passe dans la zone d'attente, l'envoi est terminé, et une tâche `pak-install` l'installe ; extraire jusqu'à 1 Gio peut durer plus longtemps qu'une requête). Le résultat de la tâche est `{ "pakId": "..." }`.

### `POST /downloads`

```json
{
  "url": "https://example.org/hack.bps",
  "target": { "kind": "staging" },
  "sha256": null,
  "onConflict": "rename"
}
```

Même `target` que pour `/uploads` (le nom de fichier est déduit de `Content-Disposition` ou de l'URL s'il est absent). Schémas `http` et `https` uniquement, 5 redirections maximum, 4 Gio maximum. Réponse `202` : `{ "jobId": "job_..." }`. Cible `pak` (M12) : la tâche (`download`) télécharge l'archive (1 Gio au plus) puis l'installe comme `POST /paks/install` ; résultat `{ "pakId": "..." }`.

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

---

## Paks (M12)

Paks d'outils (`Tools/<plateforme>/*.pak`) et d'émulateurs (`Emus/<plateforme>/*.pak`) de la plateforme de la console. Les paks système de `.system/` ne sont pas concernés. Tout appareil appairé peut installer un pak (07 §3) : ReminisLink ne juge pas son contenu, seules restent les protections de la carte.

### `GET /paks`

```json
{ "paks": [ { "pakId": "pk_...", "name": "ReminisLink", "kind": "tool", "path": "Tools/tg5040/ReminisLink.pak",
  "sizeBytes": 9437184, "modifiedAt": "2026-11-20T08:00:00Z", "version": "v1.0.0", "self": true } ] }
```

- Dossiers dont le nom finit par `.pak` (casse ignorée), hors dossiers cachés et liens. `name` : nom du dossier sans `.pak`. `sizeBytes` : somme des fichiers du dossier. `modifiedAt` : date du dossier. `version` : champ `version` de son `pak.json`, `null` sinon.
- `self` : le pak de ReminisLink lui-même (dossier de l'exécutable qui tourne), qui ne peut être ni supprimé ni remplacé pendant qu'il tourne.
- Tri : outils d'abord, puis nom sans tenir compte de la casse.

### `DELETE /paks/{pakId}`

Déplace le dossier entier du pak vers la corbeille (raison `delete-pak`, libellé : nom du pak), après confirmation dans l'app. Réponse `200` : `TrashMoveResult`, comme `DELETE /games/{gameId}`. ReminisLink lui-même : `409 conflict` (`details.self: true`). Écriture sous le verrou par action (M13) ; événement `paks` ensuite.

### `POST /paks/install`

```json
{ "sdEntryId": "se_...", "kind": "auto", "onConflict": "replace" }
```

- **Source** : une entrée du navigateur SD, fichier zip ou dossier dont le nom finit par `.pak` (copié tel quel). Les autres sources passent par le même circuit : cible `pak` de `POST /uploads` (fichier de l'iPhone ; un dossier `.pak` choisi sur l'iPhone est zippé par l'app avant l'envoi, `NSFileCoordinator` option `forUploading`) et de `POST /downloads` (URL).
- **`kind`** : `tool` (`Tools/<plateforme>/`), `emu` (`Emus/<plateforme>/`) ou `auto` (par défaut) : dossier de l'archive (`Tools/` ou `Emus/`), sinon emplacement du dossier source sur la carte, sinon `type` de son `pak.json` (`TOOL`, `EMU`), sinon `tool`.
- **`onConflict`** : `fail` (par défaut : la tâche échoue en `409 conflict`, `details.existing`) ou `replace` (l'ancien pak part à la corbeille, raison `replace-pak`). L'app envoie `replace` après confirmation. Dans les cibles `pak` d'envoi et de téléchargement, la même règle s'écrit `onConflict: overwrite` (énumération commune des envois).
- Réponse `202` : `{ "jobId": "..." }` (tâche `pak-install`, résultat `{ "pakId": "..." }`). Refus immédiats : `400`, `404` (entrée absente), `422 unsupported_format` (ni fichier ni dossier `.pak`). Échecs de la tâche : `422 unsupported_format` (avec un message en anglais), `409 conflict` (pak existant avec `fail`, ou ReminisLink lui-même avec `details.self: true`), `507 insufficient_storage`.
- **Archives acceptées** (zip) : `<Nom>.pak/…`, ou `Tools|Emus/<plateforme>/<Nom>.pak/…` (type déduit ; les entrées d'une autre plateforme sont ignorées). Un seul pak par archive, avec `launch.sh` directement dans son dossier (casse ignorée, comme sur la carte exFAT). Les fichiers à la racine de l'archive et le dossier `__MACOSX/` sont ignorés ; toute autre entrée hors du dossier du pak refuse l'archive. **Archive « à plat »** (style Pak Store) : si un fichier `launch.sh` est à la racine de l'archive, toutes ses entrées (sauf `__MACOSX/`) forment le pak, nommé d'après le `name` de son `pak.json` s'il est utilisable, sinon d'après le nom du fichier de l'archive (`fileName` de l'envoi, nom déduit ou donné du téléchargement, nom de l'entrée de la carte) sans `.zip` ; `.pak` est ajouté s'il manque et le nom est nettoyé comme tout nom de fichier (`422` s'il ne reste rien d'utilisable).
- **Protections** : refus de l'archive pour un chemin absolu, un composant `..` ou `.`, un composant vide, `\`, un caractère nul ou de contrôle, de l'UTF-8 invalide, un composant de plus de 255 octets ou un chemin de plus de 1024 octets ; un lien symbolique ou toute entrée qui n'est ni un fichier ni un dossier ; deux entrées qui ne diffèrent que par la casse (dossiers compris) ; plus de 10 000 entrées ou un répertoire central de plus de 4 Mio (lus dans les enregistrements de fin avant le répertoire lui-même) ; plus de 1 Gio décompressé (annoncé ou réel) ; une entrée de plus de 1 Mio compressée plus de 200 fois. Rien n'est écrit hors de la zone d'attente avant que tout soit vérifié. Un dossier `.pak` de la carte est copié avec les mêmes limites (aucun lien). Une archive envoyée ou téléchargée comme pak fait 1 Gio au plus (vérifié sur la longueur annoncée et à chaque écriture).
- **Installation** : extraction (ou copie) dans la zone d'attente, vérification, puis sous le verrou d'écriture (la tâche revérifie que son client n'a pas été révoqué) : l'ancien pak du même nom (casse ignorée) part à la corbeille et le dossier préparé est renommé à sa place, en un remplacement journalisé (comme une ROM écrasée, M8) : chaque dossier est synchronisé, tout échec remet l'ancien pak et fait échouer la tâche, et une coupure de courant est terminée (nouveau pak en place) ou annulée (ancien pak remis) au lancement suivant, jamais ni l'un ni l'autre. Le dossier des paks n'est jamais atteint par un lien. Événement `paks` ensuite.

## Navigateur de la carte SD (M12)

Consultation seule en M12. Identifiants `sdEntryId` opaques, résolus par le pak à chaque requête : rien hors de la carte, aucun lien suivi (les liens ne sont pas listés ; un identifiant qui passe par un lien est refusé), données de ReminisLink cachées et inaccessibles (`404`) : son dossier (`.userdata/shared/ReminisLink/` : clés des clients, clé TLS, corbeille, zone d'attente, copies) et ses journaux. Les fichiers cachés (`.` en tête) sont listés.

### `GET /sd/browse?folderId=<sdEntryId>`

```json
{ "folderId": "se_...", "path": "Tools", "parentId": null,
  "entries": [ { "sdEntryId": "se_...", "name": "tg5040", "kind": "folder", "sizeBytes": null, "modifiedAt": "2026-11-18T17:00:00Z" } ],
  "truncated": false }
```

- Sans `folderId` : la racine de la carte (`folderId` et `parentId` à `null`). `parentId` est le `folderId` qui ouvre le dossier parent, `null` quand c'est la racine.
- Dossiers d'abord, puis fichiers, par nom sans tenir compte de la casse ; 5000 entrées au plus (`truncated: true` au-delà).
- `404` : identifiant qui n'est pas (ou plus) un dossier, ou données de ReminisLink ; `400 invalid_id` : identifiant illisible ou hors de la carte.

### `GET /sd/browse/{sdEntryId}/content`

Le fichier entier, comme `GET /games/{gameId}/files/{fileId}` (`Content-Disposition`, `Content-Length`, pas de `Range`). Dossier, lien, chemin passant par un lien, fichier absent ou données de ReminisLink : `404 not_found`.
