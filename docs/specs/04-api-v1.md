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
| GET / POST | `/trash/{entryId}/restore` | Aperçu / restauration d'une entrée de la corbeille (M18) |
| GET | `/saves` | Toutes les saves de la console, avec SHA-256 (sync, M9) |
| GET / PUT | `/games/{gameId}/save` | Télécharger / remplacer la save |
| GET | `/games/{gameId}/save/rtc` | Télécharger le fichier d'horloge de la save (M9) |
| GET / POST | `/games/{gameId}/save/backups` | Lister / créer des copies de la save |
| POST | `/games/{gameId}/save/backups/{backupId}/restore` | Restaurer une copie |
| GET / DELETE | `/games/{gameId}/save/backups/{backupId}` | Télécharger / supprimer une copie |
| GET | `/states` | Tous les states de la console, avec SHA-256 (sync, M19) |
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
| POST | `/games/{gameId}/patch` | Appliquer un patch (crée une tâche) |
| GET | `/jobs` | Tâches récentes |
| GET / DELETE | `/jobs/{jobId}` | État / annulation |
| GET | `/paks` | Paks d'outils et d'émulateurs installés (M12) |
| DELETE | `/paks/{pakId}` | Supprimer un pak (vers la corbeille, M12) |
| POST | `/paks/install` | Installer un pak depuis la carte SD (crée une tâche, M12) |
| GET | `/games/{gameId}/archive` | Exporter les données d'un jeu en archive zip (M19) |
| GET | `/archives/{stagingId}` | Vérifier une archive envoyée et prévoir son import (M19) |
| DELETE | `/archives/{stagingId}` | Supprimer une archive envoyée abandonnée (M19) |
| POST | `/archives/{stagingId}/import` | Importer une archive (crée une tâche, M19) |
| GET | `/sd/browse` | Contenu d'un dossier de la carte SD, en lecture (M12) |
| GET | `/sd/browse/{sdEntryId}/content` | Télécharger un fichier de la carte SD (M12) |
| GET | `/screenshots` | Captures d'écran prises sur la console (M22) |
| GET | `/screenshots/{screenshotId}` | Télécharger une capture d'écran (M22) |
| GET | `/screenshots/{screenshotId}/thumbnail` | Aperçu réduit d'une capture (JPEG, M22) |
| POST | `/screenshots/trash` | Mettre des captures à la corbeille (M22) |

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

Compatibilité (M17, [03 §9](03-protocole.md#9-versions)) : `minClientVersion` (`X.Y.Z`) est la plus ancienne version du protocole client acceptée par le pak, la même pour tous les clients (iOS, Android, web) et non une version commerciale d'app : chaque client la compare à sa constante de version de protocole (`1.0.0` aujourd'hui) ; l'app compare aussi `pakVersion` (le tag sans le `v`, par exemple `1.0.1` ou `1.1.0-beta.1` ; `dev` ou une empreinte de commit pour un build de développement, toujours accepté) à sa propre `minPakVersion`. Suffixe ignoré dans les deux comparaisons. En cas d'écart, l'app dit quel côté mettre à jour (le pak sur la console, ou l'app), avec un lien, au lieu d'une erreur.

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
| `library` | `{ systemIds }` | Après toute modification de la carte faite par l'API, quel que soit le client : envoi installé (pas un envoi vers la zone d'attente), suppression d'un jeu, jaquette, corbeille, save remplacée, copie de save créée, supprimée ou restaurée, state supprimé, verrouillé, déverrouillé ou restauré, historique de reprise restauré, cheats, et tâche de patch réussie. `systemIds` nomme les systèmes touchés ; **vide** quand ils ne sont pas connus (corbeille) : l'app rafraîchit tout. Au plus un par seconde : les changements entre deux événements sont fusionnés (un « inconnu » l'emporte). |
| `paks` | `{}` (M12) | Après l'installation (tâche `pak-install` réussie), la suppression d'un pak ou sa restauration depuis la corbeille (M18, avec `library`), quel que soit le client : l'app relit `GET /paks`. Au plus un par seconde (fusionnés). Un changement de pak n'envoie pas `library`, sauf la restauration. |
| `screenshots` | `{}` (M22) | Après la mise à la corbeille de captures (`POST /screenshots/trash`) ou la restauration de l'une d'elles depuis la corbeille (avec `library`), quel que soit le client : l'app relit `GET /screenshots`. Au plus un par seconde (fusionnés). La mise à la corbeille de captures n'envoie pas `library`. |
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

- **Écritures sérialisées** : le pak exécute une à une les opérations qui modifient la carte SD : suppressions, remplacements, restaurations, copies de save, verrous de states, cheats (un import attend son tour une fois sa source lue), jaquette, corbeille, mise à la corbeille de captures d'écran (M22), et l'étape finale d'un envoi (`POST /uploads/{uploadId}/complete`). La création et l'annulation d'un envoi, ses morceaux (`PUT /uploads/{uploadId}`) et l'annulation d'une tâche ne touchent que la zone d'attente du pak et n'attendent jamais. Une tâche (patch, installation de pak) n'attend son tour que pour l'étape qui installe son fichier, sans délai maximal ni échec pour cette raison.
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

- La jaquette est trouvée comme dans `GET /games/{gameId}` (même règle de casse, NFC et NFD distincts, nom enregistré sur la carte) et ouverte confinée sous `Roms/`, dans le dossier du système, sans lien symbolique à aucun niveau ; une jaquette refusée est traitée comme absente (`404`).
- Une jaquette illisible (erreur d'E/S, accès refusé par le système) reçoit `500 internal`, jamais `404` : l'app garde sa copie (elle ne l'oublie que sur un vrai `404`).
- `ETag` jusqu'à 8 Mio : SHA-256 du contenu (`"sha256-…"`, 16 premiers octets en hexadécimal). Le pak garde en mémoire l'`ETag` déjà calculé d'une version du fichier (chemin, taille, date) pour répondre à une revalidation sans relire la jaquette. Ce cache repose sur deux faits : pendant que le pak tourne, NextUI ne tourne pas et le pak est le seul à écrire sur la carte ; toute écriture du pak tient le verrou d'écriture (« une écriture à la fois »). Le cache est vidé dès qu'une écriture commence, il n'est consulté que si aucune écriture n'a commencé entre le début de la requête et l'ouverture du fichier, et il n'est jamais rempli avec un `ETag` calculé pendant une écriture. Un changement de la jaquette par le pak reçoit donc toujours un nouvel `ETag` `sha256-`, même à taille et date identiques. Hypothèse résiduelle : une modification faite par autre chose que le pak pendant qu'il tourne (shell, carte échangée), à taille et date identiques, n'est vue qu'au redémarrage du pak (le cache n'est pas conservé).
- Au-delà de 8 Mio (l'app n'envoie jamais une telle jaquette) : `ETag` fait de la taille et de la date (`"mt-…"`), pas du contenu ; un nouveau fichier de même taille et même date garde le même `ETag`, quel que soit l'auteur du changement.

### `PUT /games/{gameId}/art` et `DELETE /games/{gameId}/art` (M8)

Définir ou retirer la jaquette d'un jeu (décision du 19/11/2026). Corps de `PUT` : un PNG (`Content-Type: image/png`, 4 Mio au plus, signature PNG et dimensions vérifiées, au plus 1024 × 1024) ; l'app convertit et réduit l'image choisie (Photos ou Fichiers) avant l'envoi. Le pak l'enregistre de façon atomique à l'emplacement que NextUI lit (`.media/<nom>.png`, voir 05 §3.1) ; l'ancienne jaquette part d'abord à la corbeille (raison `overwrite`). `DELETE` déplace la jaquette à la corbeille. Réponses : `200` avec le jeu mis à jour (`hasArt`), `404` si le jeu n'existe pas, `413`, `422 unsupported_format` si le corps n'est pas un PNG valide.

### `GET /games/{gameId}/deletion?keepSave=false` (M8)

Aperçu en lecture seule de `DELETE /games/{gameId}` avec le même `keepSave`, calculé par la même sélection que la suppression (source unique) : l'app affiche la **liste exacte** avant de demander confirmation (06). Réponse `200` : `{ "moved": [DeletionItem], "kept": [DeletionItem], "movedBytes": int64 }`, où `DeletionItem` = `{ "name", "role", "size" }`. Les dossiers sont développés en fichiers, récursivement. `name` est informatif, jamais absolu : relatif au dossier du système pour les fichiers de `Roms/`, relatif à la carte SD pour les autres fichiers du jeu (`Saves/GBA/<rom>.sav`), `ReminisLink/...` pour les copies du pak (verrous, historique de reprise). `role` : un rôle de `GET /games/{gameId}` (`rom`, `save`, `rtc`, `state`, `cheats`, `art`) ou `other` (annexes des states, `.cht.bak`, fichiers d'un jeu dossier hors playlist, copies du pak). `moved` : ce qui partirait à la corbeille, copies du pak comprises ; `kept` : ce qui reste volontairement en place (disques encore listés par une autre playlist et pistes de leur `.cue` ; avec `keepSave=true`, save, `.rtc`, states et annexes, verrous et historique). `movedBytes` : taille de tous les fichiers de `moved`. Erreurs : celles de `GET /games/{gameId}`.

### `DELETE /games/{gameId}?keepSave=false`

1. **Déplace** dans la corbeille ReminisLink (`trash/<horodatage>/`, en conservant le chemin d'origine, voir [05](05-console-pak.md)) : la ROM (ou le `.m3u` et son dossier), la save et son fichier d'horloge (`.rtc`), les states, les cheats et la jaquette, tous dans la **même** entrée de corbeille. Rien n'est effacé définitivement par cette route : c'est un renommage sur la même carte SD, donc instantané.
2. Déplace aussi dans cette entrée de corbeille les verrous et l'historique de reprise du jeu (copies internes de ReminisLink, non listées).
3. Avec `keepSave=true`, la save, son `.rtc` et les states restent en place, **ainsi que les verrous et l'historique de reprise**, qui les protègent. *(Décision du 18/11/2026.)*

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

Une entrée regroupe tout ce qu'une même opération a mis à la corbeille. `reason` : `delete-game`, `delete-state`, `overwrite`, `replace-rom`, `lock-restore`, `restore`, `delete-backup` (suppression d'une copie de save, copies automatiques au-delà de 10, entrées d'historique au-delà de 5), `delete-pak` et `replace-pak` (M12), `import-archive` (M19), `delete-screenshot` (captures d'écran, M22 ; libellé : le nom du fichier, ou `<n> screenshots` pour plusieurs, que l'app traduit).

### `DELETE /trash` et `DELETE /trash/{entryId}`

Suppression **définitive**, seule route qui efface réellement des données de l'utilisateur. Exige l'en-tête `X-RL-Confirm: empty-trash` (sinon `400 invalid_request`), en plus de la signature. Réponse : `{ "freedBytes": 17377096 }`.

### `GET` et `POST /trash/{entryId}/restore` (M18)

Remettre une entrée de la corbeille à sa place. Seul l'identifiant opaque de l'entrée vient du réseau : le pak lit les emplacements d'origine dans l'entrée elle-même (`entry.json`), jamais dans la requête, et vérifie qu'ils restent sur la carte SD.

- **`GET`** (aperçu, lecture seule, n'attend jamais le verrou d'écriture) : `{ "entryId", "reason", "label", "conflict", "items": [{ "name", "size", "conflict", "freeName", "replaceName" }], "restoredBytes", "freeNameUnavailable", "replaceable", "replaceDisplaced", "replaceKeptAs" }`. `items` : les fichiers de l'utilisateur (dossiers développés), `name` relatif à la carte SD ; les copies de ReminisLink de l'entrée (verrous, historique de reprise) reviennent aussi mais ne sont pas listées. `conflict` : l'emplacement est pris. `freeName` : où le fichier irait avec `onConflict: rename` (`name` sans conflit ; `null` quand aucun nom libre n'existe, `freeNameUnavailable` dit pourquoi : `all-taken`, tous les ` (N)` jusqu'à 999 pris, ou `path-blocked`, un dossier du chemin est un fichier). `replaceable` : `onConflict: replace` est possible **maintenant** (calculé sur ce que le remplacement libérera réellement) ; sinon `replaceUnavailable` dit pourquoi (`not-a-save`, `game-missing`, `save-format`, `place-blocked` : un dossier ou un autre fichier occupe la place de la save, ou le dossier des saves est atteint par un lien, `backup-taken` : un fichier du nom de la copie est déjà parmi les copies de secours) ; `replaceDisplaced` : les fichiers actuels qu'il écarterait vraiment (vide quand le jeu n'a plus de save) ; `replaceKeptAs` : où ils sont gardés (`trash` ou `backup` ; `null` quand rien n'est écarté) ; `replaceName` : où va chaque fichier. Une entrée dont les fichiers ne correspondent pas à son journal (un fichier qu'il ne liste pas, un lien) répond `500` et n'est jamais modifiée. Calculé par le même code que la restauration ; un autre client peut changer la carte entre les deux (risque accepté : rien n'est jamais écrasé).
- **`POST`**, corps facultatif `{ "onConflict": "fail" | "rename" | "replace" }` (`fail` par défaut ; un corps présent est un objet JSON, `null` et `"onConflict": null` sont refusés en `400`). Chaque élément de l'entrée revient à sa place par un renommage sur la même carte : un jeu supprimé revient avec sa save, son `.rtc`, ses states, ses cheats, sa jaquette, ses verrous et son historique de reprise ; un pak supprimé ou remplacé revient dans le dossier des paks. Réponse `200` : `{ "restored": [noms relatifs à la carte], "restoredBytes", "renamed" }` ; l'entrée quitte `GET /trash`.
- **Jamais d'écrasement** : un emplacement est pris quand un fichier ou un dossier du même nom y existe, **casse ignorée** (exFAT), ou qu'un élément du chemin n'est pas un dossier. Avec `fail` : `409 conflict`, `details.conflicts` (emplacements pris, relatifs à la carte), rien ne change. Avec `rename` : tous les éléments reviennent sous le **même** nom libre, ` (N)` (N à partir de 2) inséré après le nom du jeu, casse ignorée comme NextUI associe un jeu à ses données (`Game.gba` et `GAME.gba.sav` donnent `Game (2).gba` et `GAME (2).gba.sav`), avant ses extensions (`Clock (2).pak`) ; une save seule suit le nom du jeu (`Game (2).gba.sav`). La playlist d'un jeu dossier est renommée d'après son dossier (`Game (2)/Game (2).m3u`, ou `.cue`), son contenu ne change pas. Sans nom libre : `409` avec `details.freeNameUnavailable`. L'élément revient par un renommage qui ne remplace jamais (`renameat2` `RENAME_NOREPLACE` sous Linux, sinon lien exclusif pour un fichier ou dossier créé exclusivement ; à défaut, sur exFAT, contrôle puis renommage, les écritures du pak étant sérialisées).
- **`replace`** (M18, demandé par la personne le 21/11/2026) : seulement pour une entrée qui ne contient que la save (et son `.rtc`) d'un jeu encore présent, reconnue par les règles de l'adaptateur (NextUI : `Saves/<TAG>/<fichier ROM>.sav` ; Knulli : `saves/<système>/<nom sans extension>.srm`, avec un `.rtc` du même nom ; casse selon le dossier, M20), ou la copie de secours d'une telle save (entrée `delete-backup` : une sauvegarde remplacée) ; sinon `422 unsupported_format`, `details.reason` = `not-a-save`, `game-missing` ou `save-format`. Save du dossier des saves : la save et le `.rtc` actuels du jeu (casse ignorée, règles de l'adaptateur) partent d'abord dans une nouvelle entrée de corbeille (raison `restore`, `replacedEntryId`), puis la save revient sous le nom exact que le jeu utilise ; journalisé (l'entrée écartée note la restauration qui l'a écartée : si celle-ci n'a jamais commencé, le lancement suivant remet la save actuelle). À la reprise, la destination d'un élément `replace` doit être dans le même dossier, avec la même extension et le même nom de jeu (casse ignorée) : jamais la save ou l'horloge d'un autre jeu. Copie de secours : la copie revient d'abord parmi les copies de secours du jeu, puis elle est restaurée comme par `POST /games/{gameId}/save/backups/{backupId}/restore` : **la save actuelle est gardée en copie de secours `before-restore`** (`replacedKeptAs: backup` ; décision de la personne du 21/11/2026 : elle n'est pas mise à la corbeille à la place de cette copie ; quand la copie a un `.rtc`, le couple remplacé passe en plus par une entrée de corbeille `restore`, qui sert de journal au remplacement du couple) ; si cette seconde étape échoue, la copie reste parmi les copies du jeu (`500`). Réponse : `replacedEntryId`, `replaced` (fichiers écartés), `replacedKeptAs`.
- **Pak** : un dossier qui revient dans un dossier des paks passe les vérifications d'une installation (`launch.sh` à sa racine, ni lien ni fichier spécial, limites de `POST /paks/install`) : refusé, `422 unsupported_format`, il reste dans la corbeille.
- **Tout ou rien, journalisé** : l'entrée passe à l'état `returning` avec la règle et la destination de chaque élément avant le premier renommage ; tout échec remet dans l'entrée ce qui était déjà revenu. Frontière : une requête qui échoue avant le premier renommage (même si son journal a atteint la carte) ne restaure jamais rien ; au lancement suivant, une entrée `returning` dont aucun élément n'est revenu est annulée (redevient `done`, la save écartée par `replace` revient). Après un renommage, la coupure est terminée au lancement suivant : chaque élément va à sa destination si elle est valide pour la règle (place d'origine, nom libre ` (N)` du même dossier, nom de la save du jeu pour `replace` ; jamais vérifiée sur le journal seul), passe les mêmes contrôles que la requête (paks) et est libre ; le reste reste dans l'entrée. Seule une absence (`ENOENT`) prouve qu'un élément est revenu (ou n'est jamais parti, pour un déplacement vers la corbeille interrompu) ; toute autre erreur garde l'entrée telle quelle, et aucune entrée qui peut encore contenir un fichier n'est supprimée. Les noms se comparent selon la règle de casse de chaque dossier (M20 : sans casse sur exFAT, exactement sur ext4, où `game.srm` n'est pas un conflit pour `Game.srm`) ; à la reprise, la destination est recalculée (place d'origine résolue sur la carte, nom libre, ou save du jeu donnée par l'adaptateur pour `replace`) et un dossier est revérifié en entier (aucun lien dedans). Le renommage de la playlist d'un jeu dossier est une étape de la reprise : le journal reste (`returning`) tant qu'il n'a pas réussi, et un dossier déjà revenu sous son nom libre retrouve sa playlist renommée au lancement suivant. Un lien ou un fichier spécial n'importe où dans un dossier de l'entrée la rend endommagée (`500`). L'entrée n'est supprimée que si son dossier ne contient plus aucun fichier ; sinon elle redevient `done` avec ce qui reste. Journal et éléments lus et écrits par une racine `os.Root` de la carte, liens refusés à chaque niveau (fichier temporaire du journal créé en exclusif). Une entrée `returning` n'est ni listée, ni supprimée, ni purgée : la purge des 30 jours, `DELETE /trash` et `DELETE /trash/{entryId}` ne tournent jamais pendant une restauration (même verrou).
- **Captures d'écran** (M22) : une entrée `delete-screenshot` revient dans le dossier des captures par la même règle (aucune règle propre : les fichiers reprennent leur place d'origine) ; avec `rename`, ` (N)` est inséré avant l'extension (`Jeu.2026-11-21-18-30-00 (2).png`), le même pour toutes les captures de l'entrée ; `replace` répond `422` (`not-a-save`).
- **Écriture** sous le verrou par action (`429 device_busy` si une autre écriture tourne) ; événement `library` (`systemIds` vide), `paks` si un pak est revenu, `screenshots` si une capture est revenue dans le dossier des captures (M22). `404` : entrée inconnue (restaurée, supprimée ou purgée entre-temps).

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

Fichier d'horloge (`.rtc`, v1.0.1) : l'app n'envoie qu'une save, le `.rtc` actuel **reste en place** (même règle pour l'envoi `target.kind = "save"` et le téléchargement côté console vers une save). Il est copié avec l'ancienne save dans la copie `Avant remplacement` : il n'est jamais perdu sans copie. *Pourquoi :* la route ne reçoit pas de `.rtc` ; l'effacer ferait perdre l'horloge du jeu sans rien apporter, et le remplacer n'est pas possible.

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

Toute copie de save (manuelle ou automatique `before-*`) contient aussi le fichier d'horloge du jeu (`.rtc`) s'il existe (05 §5.1). `sizeBytes` est la taille de la save seule.

### `POST /games/{gameId}/save/backups/{backupId}/restore`

Copie d'abord la save actuelle (`before-restore`, avec son `.rtc` s'il existe), puis remplace la save par la copie choisie. Réponse `200` : `{ "backup": {...} }`, la copie de sécurité créée, ou `null` s'il n'y avait pas de save.

Fichier d'horloge (`.rtc`, v1.0.1) :
- La copie a un `.rtc` : il est restauré avec la save (vérifié par son SHA-256 comme la save). Depuis la revue M18, la save et le `.rtc` sont installés en **un seul remplacement journalisé** de la corbeille, même quand ni la save ni le `.rtc` ne sont en place (journal d'installations seules, jamais listé, retiré une fois terminé ou terminé/retiré au lancement suivant) : le couple actuel (déjà copié dans `before-restore`) part dans une entrée de corbeille (raison `restore`), le couple restauré prend sa place, et une coupure de courant entre les deux renommages est terminée (ou annulée) comme un couple au lancement suivant, jamais la save d'un couple avec l'horloge de l'autre.
- Rien d'une copie n'est lu à travers un lien : le dossier des copies du jeu et ses fichiers sont vérifiés (données de ReminisLink) avant toute lecture.
- La limite des copies automatiques ne déplace jamais à la corbeille la copie en cours de restauration (la copie `before-restore` créée par la restauration ne la pousse pas dehors).
- La copie n'a pas de `.rtc` (faite quand le jeu n'en avait pas) : seule la save est restaurée, le `.rtc` actuel **reste en place** (il est dans la copie `before-restore`). *Pourquoi :* l'effacer ne rendrait pas l'horloge de l'époque (elle n'a jamais été copiée) et ferait repartir l'horloge du jeu de zéro ; le garder est sans perte, et la copie de sécurité permet de revenir en arrière.
- Une copie de `.rtc` abîmée : `409 conflict`, rien n'est changé.

### `GET` / `DELETE /games/{gameId}/save/backups/{backupId}`

Télécharger le fichier de la copie (la save seule, sans son `.rtc`) ; supprimer la copie (`204`) : la save, le `.rtc` et les métadonnées de la copie partent ensemble à la corbeille (`delete-backup`).

---

## States

### `GET /states` (M19)

Lecture seule, pour la synchronisation des états vers le coffre iCloud Drive (06 §8). Tous les states de tous les jeux, en une requête, avec taille, date et SHA-256 (et ceux de leur vignette PNG, `null` sinon), comme `GET /saves` pour les saves : mêmes jeux, même résolution que `GET /games/{gameId}/states` (dossier de states du cœur, fichier nommé d'après la ROM, le `.m3u` ou le `.cue`, sans tenir compte de la casse). Un seul fichier par emplacement, `<nom>.st<slot>` avec le numéro en décimal sans zéro initial, comme NextUI l'écrit (`.st01` n'est pas l'emplacement 1) : le fichier que sert `GET /games/{gameId}/states/{slot}` et qu'exportent les archives. Ni copies verrouillées ni historique de reprise. Hachage gardé en mémoire tant que le fichier (appareil et inode), sa taille, sa date et, quand la plateforme en a une, sa date de changement d'état sont inchangés, et seulement s'il a été modifié au moins 3 s avant le calcul (NextUI ne tourne pas pendant le pak, qui remplace un state par renommage). Liens et fichiers hors de leur dossier ignorés. Une liste à la fois ; 20 000 states au plus (`truncated`), l'app ne déduit rien d'un state absent. `path` est informatif, `modifiedAt` vient de l'horloge de la console : comparer `sha256`.

```json
{ "states": [ { "gameId": "...", "systemId": "...", "gameName": "Sky Garden Quest", "fileName": "Sky Garden Quest.gba",
    "slot": 0, "path": ".userdata/shared/GBA-gpsp/Sky Garden Quest.gba.st0", "sizeBytes": 401408,
    "modifiedAt": "...", "sha256": "...", "thumbnail": { "sizeBytes": 50211, "sha256": "..." } } ],
  "truncated": false, "generatedAt": "..." }
```

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
{ "source": { "type": "staging", "stagingId": "st_..." }, "mode": "merge" }
```

- `source.type` : `sd` (`sdFileId` venant de `GET /sd/files`), `inline` (`text` : contenu du `.cht`, 1 Mio maximum), `upload` (`uploadId` d'un envoi terminé avec `target.kind = "staging"`, connu seulement pendant la session du pak), `staging` (`stagingId` renvoyé par la fin de l'envoi : à préférer, il survit à un redémarrage du pak).
- `mode` : `merge` (ajoute à la suite) ou `replace`.
- Réponse : `{ "imported": 15, "total": 27 }`. `422 unsupported_format` si aucun cheat n'a été trouvé.
- La source `url` (fichier téléchargé par la console) est retirée le 20/11/2026 et écartée, décision de la personne.

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

### `POST /downloads` (retiré le 20/11/2026 et écarté, décision de la personne)

Le pak ne télécharge plus rien : la route n'existe plus (`404`). L'app envoie un fichier téléchargé sur l'iPhone par `POST /uploads`.

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

## Archives d'un jeu (M19)

Les données d'un jeu dans un zip, pour les garder sur l'iPhone ou les passer à une autre console. Le format est décrit par `GameArchiveManifest` dans `protocol/openapi.yaml` : un `manifest.json` (format `reminislink-game-archive`, version 1, `createdAt`, `source` : console, OS, plateforme, version du pak, tag et libellé du système, nom du jeu, `fileName`, `romName`, `kind`) liste chaque autre fichier avec son **rôle** (`save`, `rtc`, `state`, `state-thumbnail`, `cheats`, `art`, `rom`), son emplacement (`slot`, states et vignettes), sa taille et son SHA-256. Les chemins des entrées (`save/…`, `states/<slot>/…`, `rom/…`) sont informatifs : à l'import, chaque fichier est placé d'après son rôle, sous le nom que la console cible utilise pour le jeu cible, jamais d'après son chemin (règle 2).

### `GET /games/{gameId}/archive?rom=false`

Construit le zip à partir des fichiers actuels du jeu (même résolution que `GET /games/{gameId}`) : save, `.rtc`, chaque state et sa vignette, cheats, jaquette ; avec `rom=true` la ROM aussi (jeux d'un seul fichier, sinon `422 unsupported_format`). Ni copies verrouillées, ni historique de reprise, ni copies de secours. Lecture : n'attend jamais une écriture ; fichiers ouverts confinés (aucun lien) ; un fichier refusé, disparu ou qu'un import refuserait (trop gros, image qui n'est pas un PNG, cheats illisibles) est laissé de côté, choisi avant le statut : rien d'importable, `404 not_found`. Un fichier de plus de 1 Mio que `deflate` comprimerait plus de 200 fois (un state plein de zéros) est stocké sans compression, pour que l'archive passe le contrôle de l'import. Réponse en flux (`application/zip`, `Content-Disposition: attachment; filename="<jeu>.reminislink.zip"`, sans `Content-Length`), le manifeste en dernier.

### `GET /archives/{stagingId}`

Vérifie l'archive d'une entrée de la zone d'attente (envoi `target.kind = "staging"`) et dit ce que ferait l'import vers la même cible, calculé par le même code : pour chaque fichier, `create`, `replace` (le fichier actuel part d'abord à la corbeille) ou `skip` avec une raison (`game-exists`, `locked`, `unsupported-format`, `no-state-folder`, `no-state`, `no-target`). Cible en paramètres : `gameId` (les données vont à ce jeu, la ROM est ignorée), ou `systemId` et `folderId` facultatif (un nouveau jeu est fait de la ROM de l'archive, qui doit en contenir une ; son nom, `source.fileName` nettoyé comme un envoi, doit être un jeu que le système liste (Knulli : une de ses extensions) et libre dans le dossier (le système de fichiers est interrogé : APFS trouve une graphie NFD sous son nom NFC), et aucun fichier qu'utiliserait le nouveau jeu, que l'archive le contienne ou non (save, horloge, états et vignettes de tous ses emplacements, jaquette, calculés par les règles de l'OS cible), ne doit appartenir à un autre jeu d'un système du même tag (NextUI : `A/Zelda.gba` et `B/Zelda.gba` ; Knulli : `A/Zelda.gba` et `B/Zelda.zip` partagent `Zelda.srm`), sinon `409 conflict` avec `details.existing` et `details.gameId`, que l'app propose comme cible). Sans cible, le pak en propose une : un jeu d'un système du même tag dont les données portent le même nom de ROM (le nom exact d'abord ; une autre casse seulement là où le système de fichiers l'ignore, jamais sur ext4), sinon, si l'archive contient la ROM, le dossier racine du premier système de ce tag ; `target: null` si rien ne convient.

**Contrôles** (les mêmes à l'import) : répertoire central borné avant lecture (4 Mio), 512 entrées au plus, refusées dès les enregistrements de fin ; disposition physique lue par le pak lui-même avant tout contenu : rien avant la première entrée, entre deux entrées ni entre le répertoire et ses enregistrements de fin, chaque en-tête local et ses données dans le fichier et avant le répertoire, aucune entrée qui partage des octets avec une autre, noms, méthodes et tailles identiques entre en-têtes locaux et répertoire central (ZIP64 compris) ; champs obligatoires du manifeste présents et non nuls ; chaque entrée : ni chemin absolu, ni `..`, `.` ou composant vide, ni barre oblique inverse, NUL ou caractère de contrôle, UTF-8 valide, composants de 255 octets et chemins de 1024 octets au plus, ni lien symbolique ni autre chose qu'un fichier ou un dossier, pas de chiffrement, `store` ou `deflate` seulement, pas deux entrées qui ne diffèrent que par la casse (exFAT), pas d'entrée de plus de 1 Mio comprimée plus de 200 fois. Le manifeste (1 Mio au plus) doit lister exactement les fichiers de l'archive (`__MACOSX/` et dossiers ignorés), chacun une fois, avec un rôle connu, un `slot` de 0 à 99 pour les states et vignettes seulement, la taille de son entrée et un SHA-256 ; une seule save, `.rtc`, liste de cheats, jaquette et ROM, un state et une vignette par emplacement. Tailles : ROM 4 Gio, jaquette 4 Mio, cheats 1 Mio, autre fichier 256 Mio, 5 Gio en tout ; vignettes et jaquette commencent par la signature PNG ; cheats lisibles. Une version supérieure à 1 est refusée (archive d'un ReminisLink plus récent). Refus : `422 unsupported_format` (message en anglais).

### `POST /archives/{stagingId}/import`

Corps `{ "target": { "gameId": "..." } }` ou `{ "target": { "systemId": "...", "folderId": null } }`. Après les contrôles de la requête et de l'archive, crée une tâche `archive-import` (`202`). La tâche extrait les fichiers à importer dans la zone de ReminisLink (jamais à côté du jeu), chacun vérifié contre la taille et le SHA-256 du manifeste, après contrôle de l'espace libre (`507`). Puis, sous le verrou d'écriture (le client est revérifié), elle recalcule la destination de chaque fichier par rôle, avec les noms de cette console (save et `.rtc` nommés d'après la ROM, states dans le dossier du cœur du système, vignettes, cheats, jaquette), en retrouvant un fichier existant sans tenir compte de la casse ; copie la save actuelle (`before-import`, avec son `.rtc`) ; met tout fichier remplacé (et la vignette d'un state remplacé) dans une seule entrée de corbeille (raison `import-archive`, libellé : le nom du jeu) ; renomme les nouveaux fichiers en place, en un remplacement journalisé (M8), aussi quand rien n'est remplacé (le journal, vide, est alors retiré à la fin et jamais listé) : un échec remet tout en place, une coupure est terminée ou annulée au démarrage suivant. La copie `before-import` est faite dès que la save ou son `.rtc` est remplacé. Les dossiers de destination sont créés de façon confinée (jamais à travers un lien). Rien d'autre n'est supprimé ; un state verrouillé n'est jamais remplacé ; un rôle que l'OS cible ne garde pas (cheats sur Knulli) est ignoré (`unsupported-format`) et le reste importé ; une save qu'aucun jeu n'utilise (sa ROM a été supprimée) remplacée par un nouveau jeu est copiée d'abord par son chemin. Tous les dossiers créés sont rendus durables dans leur parent avant le retrait du journal ; une installation ne remplace jamais un fichier existant (vérifié juste avant chaque renommage). La zone d'attente et le dossier des envois ne sont jamais écrits, lus ni vidés à travers un lien, ni repris au démarrage (un envoi dont le `.part` est un lien est abandonné) ; un nouveau dossier `st_…` est rendu durable dans son parent avant la réponse de la finalisation. Nouveau jeu dont le nom est pris entre-temps : la tâche échoue `409 conflict`. L'entrée de la zone d'attente est supprimée quand la tâche réussit (un échec de suppression est journalisé ; la purge de 24 h la retire).

### `DELETE /archives/{stagingId}`

L'app abandonne un import (aperçu fermé, archive refusée) : l'entrée de la zone d'attente est supprimée tout de suite plutôt qu'à la purge de 24 h, pour qu'une grosse archive n'encombre pas la carte. Zone d'attente seulement (pas de verrou d'écriture). `204` ; `404` inconnue ; `409 conflict` si une tâche d'import l'utilise. Résultat : `gameId`, `imported`, `skipped` (rôle, emplacement, raison), `trashEntryId`, `saveBackupId`. Un événement `library` suit.

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

- **Source** : une entrée du navigateur SD, fichier zip ou dossier dont le nom finit par `.pak` (copié tel quel). Les autres sources passent par le même circuit : cible `pak` de `POST /uploads` (fichier de l'iPhone ; un dossier `.pak` choisi sur l'iPhone est zippé par l'app avant l'envoi, `NSFileCoordinator` option `forUploading`). L'installation depuis une URL (cible `pak` de `POST /downloads`) est retirée le 20/11/2026 et écartée, décision de la personne.
- **`kind`** : `tool` (`Tools/<plateforme>/`), `emu` (`Emus/<plateforme>/`) ou `auto` (par défaut) : dossier de l'archive (`Tools/` ou `Emus/`), sinon emplacement du dossier source sur la carte, sinon `type` de son `pak.json` (`TOOL`, `EMU`), sinon `tool`.
- **`onConflict`** : `fail` (par défaut : la tâche échoue en `409 conflict`, `details.existing`) ou `replace` (l'ancien pak part à la corbeille, raison `replace-pak`). L'app envoie `replace` après confirmation. Dans la cible `pak` d'envoi, la même règle s'écrit `onConflict: overwrite` (énumération commune des envois).
- Réponse `202` : `{ "jobId": "..." }` (tâche `pak-install`, résultat `{ "pakId": "..." }`). Refus immédiats : `400`, `404` (entrée absente), `422 unsupported_format` (ni fichier ni dossier `.pak`). Échecs de la tâche : `422 unsupported_format` (avec un message en anglais), `409 conflict` (pak existant avec `fail`, ou ReminisLink lui-même avec `details.self: true`), `507 insufficient_storage`.
- **Archives acceptées** (zip) : `<Nom>.pak/…`, ou `Tools|Emus/<plateforme>/<Nom>.pak/…` (type déduit ; les entrées d'une autre plateforme sont ignorées). Un seul pak par archive, avec `launch.sh` directement dans son dossier (casse ignorée, comme sur la carte exFAT). Les fichiers à la racine de l'archive et le dossier `__MACOSX/` sont ignorés ; toute autre entrée hors du dossier du pak refuse l'archive. **Archive « à plat »** (style Pak Store) : si un fichier `launch.sh` est à la racine de l'archive, toutes ses entrées (sauf `__MACOSX/`) forment le pak, nommé d'après le `name` de son `pak.json` s'il est utilisable, sinon d'après le nom du fichier de l'archive (`fileName` de l'envoi, nom de l'entrée de la carte) sans `.zip` ; `.pak` est ajouté s'il manque et le nom est nettoyé comme tout nom de fichier (`422` s'il ne reste rien d'utilisable).
- **Protections** : refus de l'archive pour un chemin absolu, un composant `..` ou `.`, un composant vide, `\`, un caractère nul ou de contrôle, de l'UTF-8 invalide, un composant de plus de 255 octets ou un chemin de plus de 1024 octets ; un lien symbolique ou toute entrée qui n'est ni un fichier ni un dossier ; deux entrées qui ne diffèrent que par la casse (dossiers compris) ; plus de 10 000 entrées ou un répertoire central de plus de 4 Mio (lus dans les enregistrements de fin avant le répertoire lui-même) ; plus de 1 Gio décompressé (annoncé ou réel) ; une entrée de plus de 1 Mio compressée plus de 200 fois. Rien n'est écrit hors de la zone d'attente avant que tout soit vérifié. Un dossier `.pak` de la carte est copié avec les mêmes limites (aucun lien). Une archive envoyée comme pak fait 1 Gio au plus (vérifié sur la longueur annoncée et à chaque écriture).
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

## Captures d'écran (M22)

Les captures prises sur la console, dans le dossier que donne l'adaptateur d'OS ([05 §3](05-console-pak.md#3-adaptateur-dos) : NextUI `Screenshots/` à la racine de la carte pour toutes les plateformes, Knulli `/userdata/screenshots/`). Seuls les fichiers **directement** dans ce dossier comptent (un sous-dossier n'est pas lu). Identifiant `screenshotId` = `sc_` + `b64u(nom du fichier)`, opaque : le pak le décode avec les mêmes contrôles que les autres identifiants (03 §5 ; un `/`, `..`, un caractère de contrôle, de l'UTF-8 invalide, un nom caché ou une autre extension : `400 invalid_id`) et le résout vers le fichier de ce nom, sans lire le dossier (revue Codex de M22 : coût constant) ; sur exFAT, une autre casse atteint le même fichier (le pak ne produit jamais un tel identifiant), sur ext4 c'est un autre fichier. La mise à la corbeille exige en plus le nom exact listé. Dossier ouvert par une racine `os.Root` de la carte, aucun lien suivi : un lien n'est jamais listé, ouvert ni déplacé, et un dossier des captures atteint par un lien n'a aucune capture (règle 2). Le dossier absent : liste vide.

### `GET /screenshots`

```json
{ "screenshots": [ { "screenshotId": "sc_...", "name": "Sky Garden Quest.2026-11-21-18-30-00.png",
  "takenAt": "2026-11-21T18:30:00Z", "sizeBytes": 183204, "format": "png", "gameName": "Sky Garden Quest",
  "hasThumbnail": true } ], "totalBytes": 183204, "truncated": false }
```

- Fichiers ordinaires d'extension `.png`, `.jpg`, `.jpeg` ou `.bmp` (casse ignorée), non cachés ; `.tmp` et autres fichiers ignorés ; un nom sans identifiant possible (caractère de contrôle, UTF-8 invalide) n'est pas listé. `format` : d'après l'extension (aucun fichier n'est ouvert pour lister).
- `takenAt` : la date écrite dans le nom quand l'OS en met une (NextUI `<jeu>.<AAAA-MM-JJ-HH-MM-SS>.png` ; RetroArch sous Knulli `<ROM>-<AAMMJJ>-<HHMMSS>.png` ; raccourci de Knulli `knulli-screenshot_<L>x<H>_<AAAAMMJJ>_<HH>h<MM>m<SS>s.png` ; scripts `screenshot-…`), lue dans le fuseau de la console, y compris avec le ` (N)` qu'une restauration sous un nom libre insère avant l'extension (le jeu et la date restent lus) ; sinon la date de modification du fichier. Une date qui n'existe pas (30 février, heure sautée au passage à l'heure d'été) n'est pas lue.
- `gameName` : le jeu que donne le nom (NextUI : nom d'affichage, sans extension ni parenthèses finales, ou alias de `map.txt` ; Knulli : nom de la ROM sans extension), `null` sinon. **Informatif** : le nom ne dit pas le système, il n'est jamais résolu en `gameId` (écart assumé avec « jeu associé si le nom le permet » : il ne le permet pas sans ambiguïté).
- `hasThumbnail` : **indicatif**, d'après l'extension et la taille seulement (PNG ou JPEG d'au plus 32 Mio) ; l'aperçu peut quand même répondre `404`. Tri : `takenAt` décroissant, puis nom. 5000 captures au plus (`truncated: true` : les plus récentes) ; le dossier est lu par lots de 256 et seules les 5000 plus récentes sont gardées (mémoire bornée quelle que soit la taille du dossier). Lecture : n'attend jamais une écriture.

### `GET /screenshots/{screenshotId}`

Le fichier entier (`Content-Length`, pas de `Range`), avec son nom dans `Content-Disposition` et le type de son **contenu** (signature : `image/png`, `image/jpeg`, `image/bmp`, sinon `application/octet-stream`). Le descripteur vérifié est celui qui est servi. `404` : absent, lien, hors de la liste.

### `GET /screenshots/{screenshotId}/thumbnail`

Un JPEG (qualité 80) qui tient dans 320 × 320 px (proportions gardées, jamais agrandi), fait par le pak avec les décodeurs PNG et JPEG de la bibliothèque standard de Go (moyenne des pixels couverts). L'en-tête est lu d'abord (les 256 premiers Kio, cadre `SOFn` du JPEG compris) et la mémoire que prendrait le décodage est estimée **avant toute allocation** (revue Codex de M22 : une Brick a environ 1 Go partagé) : pixels (8 octets par pixel pour un PNG 16 bits, double pour un PNG entrelacé), composantes du JPEG et, pour un JPEG progressif, ses coefficients (4 octets par pixel et par composante) ; au-delà de 32 Mio (1920 × 1080 passe dans tous les formats, un JPEG progressif 4:4:4 de 4096 × 4096 ou un PNG de 4096 × 4096 non), comme une image annoncée au-delà de 4096 × 4096 px, un fichier de plus de 32 Mio, un BMP, un contenu illisible ou tronqué : `404 not_found` (l'app affiche un pictogramme). Un seul aperçu décodé à la fois ; les aperçus récents sont gardés en mémoire (512 au plus, 8 Mio), clé : nom, taille et date du fichier ouvert.

### `POST /screenshots/trash`

```json
{ "screenshotIds": ["sc_..."] }
```

- 1 à 500 identifiants (doublons comptés une fois) ; sinon `400 invalid_request`. Tout ou rien : un identifiant illisible → `400 invalid_id` ; une capture absente → `404 not_found` avec `details.missing` (ses identifiants) ; rien n'est déplacé.
- Chaque fichier est revérifié juste avant son déplacement (`store.MoveItem.Identity` : même périphérique et même inode, fichier ordinaire, au plan du déplacement puis juste avant le renommage) : un dossier des captures remplacé, un autre fichier du même nom ou un dossier du même nom ne sont jamais déplacés à sa place (`404`, rien ne bouge).
- **Échec partiel** : une erreur de la carte pendant les déplacements répond `500` ; ce qui a bougé revient, mais un fichier qui ne peut pas revenir reste dans une entrée de corbeille (listée, restaurable) ; un événement `screenshots` est envoyé dans ce cas aussi.
- Les captures partent dans **une** entrée de corbeille (raison `delete-screenshot`, libellé : le nom du fichier, ou `<n> screenshots`), réponse `TrashMoveResult` comme `DELETE /games/{gameId}`. Restauration par `POST /trash/{entryId}/restore` (M18), sans règle propre. Les aperçus en mémoire de ces noms sont oubliés.
- Écriture sous le verrou par action (`429 device_busy`) ; événement `screenshots`, pas `library` ; bandeau « Captures d'écran mises à la corbeille » sur l'écran du pak.

