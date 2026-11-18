# 05 — Le pak (console)

## 1. Installation et lancement

- Pak de type `TOOL`, affiché **ReminisLink** dans le menu Outils de NextUI.
- Emplacement : `/Tools/<platform>/ReminisLink.pak/` (ex. `/Tools/tg5040/ReminisLink.pak/launch.sh`).
- Archive de publication : `ReminisLink.pak.zip`, publiée par la CI sur un tag `vX.Y.Z`, lue par le Pak Store (même chaîne que Cheat Manager : `pak.json`, Makefile, workflow).
- `launch.sh` : comme celui de Cheat Manager (bibliothèques embarquées dans `resources/lib`, journal d'erreurs dans `$LOGS_PATH/ReminisLink.stderr.txt`, `sync` à la fin).

## 2. Cycle de vie

1. **Démarrage** : lecture de la configuration, création de l'identifiant de console au premier lancement.
2. **Contrôle Wi-Fi** : une adresse IPv4 non loopback doit exister sur l'interface sans fil (`wlan0` sur tg5040 **[À VÉRIFIER M0]**). Sinon : écran « Pas de Wi-Fi. Active le Wi-Fi dans les réglages de NextUI. » et sortie avec B.
3. **Maintenance** (avant d'ouvrir le serveur, en affichant « Préparation… ») :
   - archivage des states de reprise qui ont changé (§5.3) ;
   - contrôle des verrous et restauration automatique (§5.2) ;
   - purge de la corbeille (entrées de plus de 30 jours d'âge crédité, voir §4), des envois expirés et des fichiers `staging` (> 24 h).
4. **Serveur** : écoute sur `0.0.0.0:47821` (ou le premier port libre parmi les 9 suivants), puis annonce Bonjour.
5. **Écran principal** (§7).
6. **Fermeture** (B, ou signal SIGTERM/SIGINT) : refus des nouvelles requêtes (`503 shutting_down`), attente des écritures en cours (10 s maximum), retrait de l'annonce, `sync`, sortie.

Le pak NE DOIT PAS laisser de processus en arrière-plan après sa fermeture.

**Mise en veille** : vérifier au jalon M0 si NextUI met la console en veille pendant qu'un pak tourne, et ce que deviennent le Wi-Fi et le serveur **[À VÉRIFIER M0]**. Si nécessaire, le pak empêche la veille tant qu'un transfert est en cours.

## 3. Adaptateur d'OS

Toute connaissance de l'organisation des fichiers passe par une interface. Le reste du code ne construit jamais un chemin NextUI lui-même.

```go
type Adapter interface {
    Name() string                               // "nextui"
    Roots() Roots                               // SD, Roms, Saves, States, Cheats, Userdata
    Systems() ([]System, error)                 // dossiers de Roms/ avec un tag
    GameFiles(g Game) (GameFiles, error)        // ROM, save, states, cheats, art
    SavePath(g Game) string
    StatePath(g Game, slot int) string
    StateThumbnailPath(g Game, slot int) string // "" si non géré
    ResumeSlot() (slot int, ok bool)
    CheatPath(g Game) string
    ArtPath(g Game) string
    DeviceInfo() (DeviceInfo, error)            // modèle, plateforme, version OS, batterie
}
```

### 3.1 Organisation NextUI

Variables d'environnement avec valeurs par défaut, comme Cheat Manager : `SDCARD_PATH` (`/mnt/SDCARD`), `ROMS_PATH`, `SAVES_PATH`, `CHEATS_PATH`, `USERDATA_PATH` (`/mnt/SDCARD/.userdata/<platform>`), `PLATFORM` (`tg5040`), `LOGS_PATH`.

| Élément | Chemin | Statut |
|---|---|---|
| Système | `Roms/<Nom> (<TAG>)/` | Confirmé (Cheat Manager) |
| ROM | `Roms/<Système>/[sous-dossiers/]<rom>.<ext>` | Confirmé |
| Multi-disques | `Roms/<Système>/<Jeu>/<Jeu>.m3u` | Convention MinUI **[À VÉRIFIER M0]** |
| Cheats | `Cheats/<TAG>/<fichier ROM avec extension>.cht` | Confirmé (Cheat Manager) |
| Jaquette | `Roms/<Système>/.media/<nom ROM sans extension>.png` | Confirmé (guides NextUI) |
| Save | `Saves/<TAG>/<fichier ROM avec extension>.sav` | Convention MinUI **[À VÉRIFIER M0]** |
| States | `.userdata/shared/.minui/<TAG>/<fichier ROM>.st<N>` | Convention MinUI **[À VÉRIFIER M0]** : dossier, nommage, nombre d'emplacements |
| Vignette de state | ? | **[À VÉRIFIER M0]** |
| State de reprise | Emplacement 9 ? | **[À VÉRIFIER M0]** |
| Version NextUI | ? (fichier de version sur la carte SD) | **[À VÉRIFIER M0]** |
| Batterie | `/sys/class/power_supply/*/capacity`, `status` | **[À VÉRIFIER M0]** |

Le script `scripts/verify-device.sh` (lecture seule) relève toutes ces informations sur une vraie console. Son résultat, joint au jalon M0, fixe les valeurs définitives de ce tableau. Une fois fixées, mettre ce tableau à jour.

**Cas particuliers à gérer** : un même tag utilisé par deux dossiers (ex. `Game Boy Advance (GBA)` et `Game Boy Advance (MGBA)` : chacun a son propre tag, donc ses propres dossiers de saves) ; noms avec accents, parenthèses, crochets, emojis ; jeux dans des sous-dossiers ; casse différente entre la ROM et ses fichiers (exFAT est insensible à la casse, la comparaison DOIT l'être aussi).

## 4. Données de ReminisLink sur la carte SD

Racine : `/.userdata/shared/ReminisLink/` (partagée entre plateformes, comme les saves).

```
ReminisLink/
├── config.json            # deviceId, deviceName, port préféré, langue
├── clients.json           # clients appairés : clientId, nom, clé (b64u), dates
├── backups/
│   └── <TAG>/<fichier ROM>/
│       ├── saves/<backupId>.sav + <backupId>.json   # métadonnées : libellé, raison, date, sha256
│       └── resume/<entryId>.st + <entryId>.json
├── locks/
│   └── <TAG>/<fichier ROM>/slot<N>.st + slot<N>.json  # sha256, date de verrouillage
├── trash/
│   └── <trashEntryId>/
│       ├── entry.json     # raison, libellé, date, liste des fichiers d'origine, taille
│       └── <chemin d'origine relatif à la SD>
├── uploads/               # fichiers temporaires des envois et téléchargements
├── staging/               # patchs et .cht en attente d'utilisation
└── patches/               # patchs déposés par l'utilisateur (listés par /sd/files)
```

- `clients.json` contient les clés en clair : sur exFAT il n'y a pas de permissions. Quelqu'un qui a la carte SD en main a déjà accès à tout, ce risque est accepté (voir [07](07-securite.md)).
- **Corbeille** : tout fichier de l'utilisateur supprimé ou remplacé par ReminisLink y est *déplacé* (renommage, même carte SD), jamais effacé directement. Seules la purge automatique (> 30 jours d'âge crédité) et les routes `DELETE /trash` effacent définitivement.
- **Âge crédité** (décision du 18/11/2026) : l'horloge de la console n'est pas fiable (pile, NTP). Le pak tient une horloge propre, cumulée et enregistrée dans `config.json` : pendant qu'il tourne, elle avance selon l'horloge monotone du système (enregistrée régulièrement et à la fermeture) ; à chaque lancement, elle est créditée du temps écoulé depuis la dernière fermeture selon l'horloge murale, **borné entre 0 et 7 jours**. Une entrée de corbeille note la valeur de cette horloge à sa création ; elle est purgée quand l'écart atteint 30 jours. Une horloge murale fausse ne peut donc jamais vieillir une entrée de plus de 7 jours d'un coup ; au pire, la purge est retardée. Un arrêt brutal fait perdre au plus le temps depuis le dernier enregistrement (dans le sens prudent).
- Toute écriture de fichier JSON est atomique (`.tmp`, `fsync`, renommage).
- Les dossiers `uploads/`, `staging/` et `trash/` sont sur la même carte SD que les destinations : le renommage final est atomique.

## 5. Fonctions métier

### 5.1 Sauvegardes de save

- Identifiant : `bk_<AAAAMMJJTHHMMSSZ>_<4 hex>`.
- Pas de limite automatique sur les copies `manual`. Les copies automatiques (`before-*`) sont limitées à 10 par jeu ; au-delà, les plus anciennes sont supprimées.

### 5.2 Verrous de states

Le pak ne tourne jamais en même temps qu'un jeu : il ne peut donc pas *empêcher* l'émulateur d'écraser un state. Le verrou fonctionne ainsi :

1. **Verrouiller** : copie du state dans `locks/`, avec son SHA-256.
2. **À chaque lancement du pak** : pour chaque verrou, comparer le state actuel à la copie.
   - Identique : rien à faire (`intact`).
   - Différent : le state actuel (écrasé en jeu) part dans la corbeille, la copie verrouillée est restaurée (`restored`, affiché dans l'app jusqu'à la consultation suivante).
   - Absent : la copie verrouillée est restaurée (`restored`).
3. **Déverrouiller** : suppression de la copie et du verrou.

L'app DOIT expliquer cette limite à l'utilisateur : « Le state est restauré à la prochaine ouverture de ReminisLink sur la console s'il a été écrasé en jeu. »

### 5.3 Historique du state de reprise

À chaque lancement, pour chaque jeu qui a un state de reprise **[emplacement À VÉRIFIER M0]** : si son SHA-256 diffère de la dernière entrée archivée, une copie est ajoutée à `backups/.../resume/`. On garde les 5 dernières entrées.

Pour que le lancement reste rapide, le pak compare d'abord la date de modification et la taille avec la dernière entrée, et ne calcule le SHA-256 que si elles ont changé.

### 5.4 Cheats

Réutiliser l'analyseur et l'écriture de Cheat Manager : clés inconnues conservées, fichier précédent gardé en `.cht.bak`, même convention de nommage.

### 5.5 Patchs

Implémentation en Go pur, sans dépendance :

- **IPS** : enregistrements classiques et RLE, marqueur `EOF`, extension de troncature de 3 octets après `EOF`.
- **BPS** : actions `SourceRead`, `TargetRead`, `SourceCopy`, `TargetCopy` ; vérification des CRC32 source, cible et patch (`422 patch_mismatch` si la source ne correspond pas).
- **UPS** : XOR par blocs, vérification des CRC32 source et cible.

L'application se fait vers un fichier temporaire, puis vérification (pour BPS et UPS), puis renommage. La ROM d'origine n'est jamais modifiée sur place.

### 5.6 Téléchargements

- Client HTTP standard de Go, avec le magasin de certificats racine utilisé par Cheat Manager pour son import par URL (via gabagool/certifiable) **[À VÉRIFIER : même mécanisme]**.
- Délai de connexion 15 s, délai d'inactivité 60 s, reprise automatique avec `Range` (3 tentatives) si le serveur la supporte.
- Le nom de fichier final est nettoyé (voir [03 §5](03-protocole.md#5-identifiants)).

## 6. Informations de la console

| Champ | Source |
|---|---|
| `model` | Variable `DEVICE` exportée par NextUI (`brick`, `smartpro`...) |
| `platform` | Variable `PLATFORM` |
| `osVersion` | **[À VÉRIFIER M0]** |
| `battery` | `/sys/class/power_supply/...` **[À VÉRIFIER M0]** |
| `storage` | `statfs` sur `SDCARD_PATH` |
| `network.ipv4` | Interfaces réseau (`net.Interfaces`) |
| `network.ssid` | **[À VÉRIFIER M0]**, sinon `null` |

## 7. Écrans

Interface gabagool, thème NextUI, mêmes principes que Cheat Manager. Toutes les chaînes passent par la traduction (anglais et français en v1).

### 7.1 Écran principal

```
┌──────────────────────────────────────┐
│ ReminisLink                   ● Prêt │
│                                      │
│          ██████████████              │
│          ██ QR code   ██             │
│          ██████████████              │
│                                      │
│  Scanne avec l'app ReminisLink       │
│  Code : 482 913   (expire dans 9 min)│
│  Brick de Thomas · 192.168.1.42      │
│                                      │
│  Appareils connectés : iPhone de T.  │
│  ▸ Envoi : Sky Garden Quest DX.gba  62 % │
│                                      │
│ A Masquer le QR   Y Réglages   B Quitter │
└──────────────────────────────────────┘
```

- L'état en haut à droite : `Prêt`, `Connecté` (une requête signée depuis moins de 30 s), `Transfert en cours`.
- La liste des transferts et tâches en cours avec leur progression.
- Un bandeau de notification bref quand une action modifie la carte SD (« Save restaurée : Sky Garden Quest »).

### 7.2 Réglages (Y)

- **Nom de la console** (clavier gabagool), utilisé dans Bonjour.
- **Appareils appairés** : liste, avec suppression d'un appareil.
- **Oublier tous les appareils** (confirmation).
- **Langue**.
- **Version et journal** : version du pak, chemin du journal.

### 7.3 Quitter (B)

Si un transfert ou une tâche est en cours : demande de confirmation (« Un transfert est en cours. Quitter quand même ? »).

## 8. Journal

`$LOGS_PATH/ReminisLink.txt` via `log/slog` : démarrage, adresse et port, appairages (sans secrets), chaque requête (méthode, route, code, durée, client), chaque opération destructive avec le chemin de la sauvegarde créée. Jamais de clé, de secret, de PIN ni de signature dans le journal.

Protection de la carte SD (jalon M2) : un appareil non appairé ne doit pas pouvoir remplir le journal.
- Les lignes qu'une requête **non acceptée par la vérification de signature** peut provoquer (requêtes refusées, routes non signées `/hello` et `/pair`, redirections du routeur, erreurs du serveur HTTP) sont échantillonnées (quelques lignes par seconde) ; les lignes perdues sont comptées par type dans une ligne récapitulative.
- Les valeurs venant du réseau sont bornées (chemin 512 octets, méthode 16 octets) ou validées (`clientId`) avant d'être journalisées ; en-têtes limités à 16 Kio.
- Au lancement, un journal de plus de 5 Mio est renommé en `ReminisLink.old.txt` (l'ancien est remplacé) ; au-delà de 20 Mio pendant une session, seules les lignes des requêtes acceptées et les erreurs du pak sont encore écrites.

## 9. Mode desktop

`reminislink --desktop --sd ./testdata/sdcard` lance le pak sur Mac ou Linux, avec la même UI dans une fenêtre et une carte SD factice. `--headless` lance seulement le serveur et la découverte, sans UI ni SDL. Ces deux modes permettent de développer et tester l'app iOS sans console (voir [08](08-tests-qualite.md)).
