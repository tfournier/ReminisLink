# 06 — App iOS

## 1. Cible

- iPhone, iOS 17 minimum. iPad plus tard.
- SwiftUI, Observation (`@Observable`), Swift Concurrency. Aucune dépendance tierce sans accord.
- Langues : anglais et français en v1 (String Catalog `Localizable.xcstrings`).
- Identifiants : domaine du produit `reminis.link`, décision du 19/11/2026, définitifs : app **`link.reminis.app`**, extension `link.reminis.app.share`, groupe d'apps `group.link.reminis.app`, groupe de Trousseau `<Team ID>.link.reminis.app.shared`. Le préfixe ne rattache pas l'app à une entreprise ; le nom du vendeur affiché sur l'App Store est celui du compte qui publie. L'équipe de signature reste locale (`ios/Config/Local.xcconfig`, ignoré par Git).

## 2. Structure

| Cible | Contenu |
|---|---|
| `ReminisLinkKit` (Swift package) | `Discovery` (NWBrowser), `Pairing`, `Signer` (HMAC, CryptoKit), `APIClient`, `Models` (Codable), `Transfers` (envois reprenables), `KeychainStore`, `DeviceStore` |
| `ReminisLink` (app) | Écrans, navigation, état de l'app |
| `ReminisLinkShare` | Extension de partage |

`ReminisLinkKit` NE DOIT PAS dépendre de SwiftUI. Il DOIT passer les vecteurs de test de `protocol/test-vectors/`.

## 3. Info.plist et capacités

| Clé | Valeur |
|---|---|
| `NSLocalNetworkUsageDescription` | « ReminisLink se connecte à ta console sur ton réseau Wi-Fi. » |
| `NSBonjourServices` | `["_reminislink._tcp"]` |
| `NSCameraUsageDescription` | « Pour scanner le QR code affiché par ta console. » |
| `NSAppTransportSecurity` → `NSAllowsLocalNetworking` | `true` (HTTP vers le réseau local) |
| URL scheme | `reminislink` (lien d'appairage du QR code) |
| App Groups | `group.link.reminis.app` (app ↔ extension) |
| Keychain Sharing | Groupe partagé avec l'extension |
| iCloud (Documents) | Conteneur `iCloud.$(RL_APP_BUNDLE_ID)` (`iCloud.link.reminis.app`) pour la sync des saves (M9, §8) ; `NSUbiquitousContainers` le montre comme dossier « ReminisLink » dans iCloud Drive (Xcode ne développe pas les variables dans les clés d'Info.plist : la clé est la macro `RL_ICLOUD_CONTAINER_KEY`, remplacée au prétraitement de l'Info.plist, `INFOPLIST_PREPROCESS`) |
| `UIFileSharingEnabled`, `LSSupportsOpeningDocumentsInPlace` | `true` : sans iCloud, les saves synchronisées sont dans `Documents/`, visible dans Fichiers > Sur mon iPhone > ReminisLink |

Pas d'entitlement multicast : Bonjour via `NWBrowser` n'en a pas besoin.

## 4. Écrans

```
Consoles ─┬─ Ajouter une console (scan QR / PIN)
          └─ Console ouverte : barre d'onglets (M11)
             ├─ Console : tableau de bord (état), sync des saves ─ Saves sur cet iPhone ─ Système ─ Jeu ─ Versions (M9)
             ├─ Bibliothèque : tous les jeux (recherche, filtres) ─ Jeu ─┬─ Save
             │   └─ (Parcourir par console) ─ Système ─ (Dossier) ─ Jeu  ├─ States
             │                                                          ├─ Cheats
             │                                                          ├─ Patch
             │                                                          └─ Supprimer
             ├─ Transferts
             └─ Corbeille
Ajouter (fichier / URL) : feuille ouverte depuis Bibliothèque, Transferts ou l'extension de partage
Réglages de l'app ─ Saves sur cet iPhone ─ Console ─ …
```

**Navigation (M11).** La liste des consoles reste la racine. Ouvrir une console affiche une barre d'onglets **Console**, **Bibliothèque**, **Transferts**, **Corbeille**, chacun avec sa propre pile de navigation ; le bouton **Consoles** en haut à gauche du premier écran de chaque onglet revient à la liste. Les liens internes (fin d'appairage, fin d'un envoi → onglet Transferts, nouvelle ROM d'un patch → onglet Bibliothèque puis le jeu) ouvrent l'onglet voulu. Le bandeau lecture seule (M10) est affiché sur tous les onglets.

### 4.1 Consoles

- Liste des consoles appairées : nom, modèle, état (`En ligne` si vue par Bonjour, sinon `Hors ligne`).
- Bouton **Ajouter une console**.
- Si aucune console : écran d'accueil qui explique en 3 étapes (installer ReminisLink depuis le Pak Store, l'ouvrir, scanner le QR code).

### 4.2 Appairage

- Scanner intégré (VisionKit `DataScannerViewController`) qui reconnaît les liens `reminislink://pair`.
- Le même lien ouvert depuis l'appareil photo de l'iPhone lance directement l'appairage.
- Si la console du lien (ou du PIN) est **déjà appairée**, l'app demande d'abord confirmation (« Cette console est déjà appairée. Refaire l'appairage ? ») ; le nouvel appairage remplace la clé. *(Décision du 18/11/2026.)*
- La console est toujours trouvée par Bonjour avec l'`id` du lien ; sinon, erreur « Console introuvable » (06 §5). L'IP du QR code n'est jamais utilisée (03 §3.1).
- Une fois `POST /pair` envoyé, annuler ne fait que fermer l'écran : l'échange va à son terme et la clé est enregistrée si la preuve de la console est valide, pour ne jamais désynchroniser la clé avec le pak.
- Saisie du PIN en secours, avec un avertissement : « Moins sûr que le QR code. À éviter sur un Wi-Fi public. »
- Étapes visibles : Recherche de la console → Appairage → Terminé. Champ pour renommer la console côté app.

### 4.3 Tableau de bord

Onglet **Console** (M11). Aucun ajout de fichier depuis cet onglet : l'ajout reste dans Bibliothèque et Transferts ; la corbeille a son onglet. Nom, modèle, OS et version, version du pak, IP, batterie, stockage (barre de remplissage, avec la part occupée par la corbeille), appareils connectés (champ `connectedClients` de `GET /device`). Actions : **Se désappairer**. L'onglet **Corbeille** liste les entrées avec taille et date d'expiration (suppression d'une entrée, **Vider la corbeille** avec confirmation). Section **Synchronisation des saves** (M9, §8) : bouton **Synchroniser les saves**, progression avec **Annuler**, compte rendu de la dernière sync (nouvelles versions, saves vérifiées, saves en échec avec leur message), lien **Saves sur cet iPhone**, et l'emplacement (iCloud Drive, ou avertissement « iCloud Drive n'est pas disponible » quand les saves restent sur l'iPhone).

### 4.4 Bibliothèque

- **À plat (M11)** : `GET /games`, tous les jeux de tous les systèmes dans une seule liste : jaquette, nom, console (et dossier), taille, pastilles (save, nombre de states, cadenas si un state est verrouillé, nombre de cheats). Recherche sur le nom et le nom de fichier (sans tenir compte de la casse ni des accents). Menu **Filtrer et trier** : console (les systèmes de même `label` sont fusionnés, comme NextUI les affiche : GBA et MGBA donnent « Game Boy Advance »), avec une save, avec des states, avec des cheats, tri (nom, date, taille), **Effacer les filtres**, et **Parcourir par console**. Bouton **+** : la feuille Ajouter (désactivé en lecture seule).
- **Par système** : systèmes avec nombre de jeux, puis jeux du système et sous-dossiers, recherche dans le dossier, tri. Utilisé quand `truncated` est vrai (plus de 20 000 jeux, avec un message) ou quand le pak n'a pas la route (`404`/`405`, pak antérieur à M11), et depuis **Parcourir par console**.
- Glisser vers la gauche sur un jeu : **Supprimer**.

### 4.5 Fiche d'un jeu

- En-tête : jaquette, nom, système, taille totale. **Toucher la jaquette** (M11) ouvre le choix : **Photothèque**, **Fichiers**, **Retirer l'image** (si le jeu en a une, avec confirmation) ; l'image est réduite à 1024 px au plus et envoyée en PNG, l'ancienne part dans la corbeille de la console. Désactivé en lecture seule (M10).
- **Fichiers** (M11) : chaque fichier de `GET /games/{gameId}` (rôle, nom = dernier composant de `path`, taille) avec un bouton **Télécharger** : `GET /games/{gameId}/files/{fileId}` (jamais un chemin), écrit sur disque au fil de l'eau (téléchargement `URLSession` signé, progression, annulation ; une ROM peut faire des centaines de Mo), puis la feuille de partage iOS (**Enregistrer dans Fichiers**, AirDrop…), comme pour les saves et les states ; la copie locale est supprimée à la fermeture de la feuille. Permis en lecture seule. Un `404` (le fichier n'est plus dans la liste du jeu) recharge la fiche avec « Ce fichier a changé sur la console. La liste des fichiers a été rechargée : réessaie. » Un téléchargement à la fois ; quitter l'écran l'annule.
- **Save** : télécharger (partage iOS), remplacer (depuis Fichiers), copies de sauvegarde (créer avec un libellé, restaurer, supprimer, télécharger).
- **States** : un rang par emplacement (vignette, date, taille, cadenas). Actions : télécharger, verrouiller / déverrouiller, restaurer, supprimer. Section **Historique de reprise**. Texte d'aide sur le fonctionnement des verrous (voir [05 §5.2](05-console-pak.md#52-verrous-de-states)).
- **Cheats** : liste avec interrupteurs, ajout, modification (description, code, activé), suppression, réordonnancement, import (URL, Fichiers, fichier de la carte SD ; fusion ou remplacement).
- **Patch** : choisir le patch (Fichiers, URL, carte SD), choisir la sortie (nouveau fichier avec un nom proposé, ou remplacer), suivre la tâche, puis ouvrir la nouvelle ROM.
- **Supprimer** : feuille de confirmation qui liste exactement ce qui sera supprimé et indique que tout part dans la corbeille de la console pendant au moins 30 jours, et que l'espace ne sera libéré qu'en vidant la corbeille. Option « Garder la save et les states ». Après la suppression, un bouton **Vider la corbeille maintenant** (avec confirmation) permet de libérer l'espace tout de suite.

### 4.6 Ajouter

- **Depuis Fichiers** : sélection multiple, choix du système (proposé d'après l'extension), puis envoi.
- **Depuis une URL** : champ URL (collage automatique proposé si le presse-papiers contient un lien), type (ROM, patch, cheats), système, puis tâche côté console.
- L'app NE DOIT proposer aucune source de ROMs.

### 4.7 Transferts

Liste des envois et des tâches : progression, vitesse, temps restant, annulation, reprise. Un envoi interrompu (app en arrière-plan, Wi-Fi coupé) reprend à l'offset confirmé par la console.

### 4.8 Extension de partage

- Accepte : fichiers (ROMs, `.sav`, `.ips`, `.bps`, `.ups`, `.cht`) et URL.
- Enregistre la demande dans le groupe d'apps, puis ouvre l'app sur l'écran **Ajouter**, préremplie. L'extension n'envoie rien elle-même (limites de mémoire et de durée des extensions).

### 4.9 Réglages de l'app

Langue (suivre le système par défaut), consoles appairées (supprimer), à propos (versions, licence, lien du dépôt). **Synchroniser les saves à la connexion** (activé par défaut, M9) et **Saves sur cet iPhone** (toutes les consoles). Écran ouvert par la roue dentée de la liste des consoles.

## 5. Erreurs à présenter

| Situation | Détection | Message et action |
|---|---|---|
| Accès au réseau local refusé | `NWBrowser` en état `.waiting` avec une erreur de politique (`kDNSServiceErr_PolicyDenied`) | « ReminisLink n'a pas accès au réseau local. » Bouton **Ouvrir les Réglages** |
| Console introuvable | Aucun résultat Bonjour avec le bon `id` après 8 s | « Console introuvable. Vérifie que ReminisLink est ouvert sur la console, que l'iPhone et la console sont sur le même Wi-Fi (pas un réseau invité), et désactive ton VPN. » Bouton **Réessayer** |
| Client inconnu | `401 unknown_client` (y compris un iPhone oublié depuis Réglages > Appareils de la console, M10) | « Cette console ne reconnaît plus ton iPhone. » Bouton **Refaire l'appairage** |
| Horloge décalée | `401 clock_skew` | Correction automatique et nouvel essai ; message seulement si ça échoue encore |
| Espace insuffisant | `507` | « Espace insuffisant sur la carte SD : il manque X Go. » |
| Pak fermé pendant un transfert | Erreur réseau | « La console ne répond plus. » Le transfert passe en pause et reprendra |
| Pak fermé (M11) | `503 shutting_down` sur n'importe quelle requête de la connexion (écran, transfert, bail), ou échec de connexion alors que le service Bonjour de la console a disparu (le navigateur Bonjour tourne depuis au moins 3 s) | Retour à la liste des consoles avec « La console a quitté ReminisLink » ; les envois en cours passent en pause (jamais perdus) et reprennent à la prochaine connexion. Aucune reconnexion automatique avant que l'utilisateur rouvre la console. Une coupure Wi-Fi seule (service toujours annoncé) garde l'écran d'erreur habituel |
| Lecture seule (M10) | `423 read_only` (`details.writer`) sur une écriture | « Lecture seule — <appareil> a la main. Rien n’a été modifié. Prends la main pour faire des modifications depuis cet iPhone. » L’app passe en lecture seule (§9) |
| Déconnecté par la console (M10) | `401 disconnected` | « Déconnecté par la console. » Bouton **Se reconnecter** (§9) |

## 6. Stockage local

- Trousseau : pour chaque console, `deviceId`, `clientId`, clé (accessibilité `afterFirstUnlockThisDeviceOnly`, pas de synchronisation iCloud).
- `UserDefaults` du groupe d'apps : nom affiché des consoles, préférences.
- Cache : jaquettes (par `gameId` + `ETag`), dans `Caches/`.
- Aucune donnée envoyée hors du réseau local, sauf les copies des saves dans l'iCloud Drive de l'utilisateur (§8), jamais chez l'éditeur. Pas d'analytics.

## 7. Transferts en arrière-plan

En v1, les envois se font au premier plan, avec `UIApplication.beginBackgroundTask` pour terminer le morceau en cours si l'app passe en arrière-plan. Les envois reprennent au retour. Les sessions `URLSession` d'arrière-plan sont à étudier plus tard (incompatibles avec la signature par requête sans adaptation).

## 8. Synchronisation des saves vers l'iPhone (M9)

Décisions : [09, M9](09-feuille-de-route.md). Les saves seulement (avec leur `.rtc`), pas les states.

- **Déclenchement** : bouton du tableau de bord, et automatiquement à chaque connexion à une console (réglage de l'app, activé par défaut). Jamais deux syncs en même temps pour une console (même si la connexion est refaite) ; une sync peut être annulée (les versions déjà copiées restent). Une connexion refaite pendant une sync lance la sienne quand celle-ci se termine, et l'échec d'une requête d'une ancienne connexion ne touche pas la nouvelle. Une tâche d'arrière-plan (`beginBackgroundTask`) la laisse finir sa save en cours ; à expiration elle est annulée.
- **Comparaison** : `GET /saves` (délai d'inactivité de 180 s pour cette seule route : le pak ne répond qu'après avoir haché toutes les saves la première fois), puis pour chaque save, le `sha256` de la save et celui de son `.rtc` sont comparés à la version la plus récente de ce jeu (clé : `gameId`, jamais `modifiedAt`, qui vient de l'horloge de la console). Seules les saves différentes sont téléchargées (`GET /games/{gameId}/save`, et `GET /games/{gameId}/save/rtc` si `rtc` n'est pas `null` ; un `404` sur le `.rtc` donne une version sans horloge). Le SHA-256 des octets reçus DOIT être celui de la liste : sinon rien n'est gardé, le jeu est marqué en échec (« La save a changé sur la console pendant la synchronisation, ou est arrivée abîmée ») et la sync suivante réessaie. Avec `truncated`, l'app ne déduit rien d'une save absente (elle ne supprime jamais rien de toute façon).
- **Erreurs** : une erreur sur un jeu est notée et la sync continue ; la perte de la console (erreur réseau, `401 unknown_client`, `503`) l'arrête.
- **Stockage** : dossier `Documents/` du conteneur iCloud de l'app, montré comme « ReminisLink » dans iCloud Drive ; sans compte iCloud, avec iCloud Drive désactivé pour l'app, ou sans l'entitlement (simulateur sans équipe), `Documents/` de l'app (Fichiers > Sur mon iPhone > ReminisLink), avec l'avertissement du tableau de bord. L'emplacement est choisi à chaque sync ; les copies faites sans iCloud restent sur l'iPhone (pas de migration).
- **Arborescence** : `<console>/<système>/<jeu>/<AAAA-MM-JJ HH.mm.ss>/<save>` (+ `.rtc`), date de la sync à l'heure de l'iPhone. Console : nom à la création du dossier ; système : `<label> (<tag>)` de `GET /systems` (GBA et MGBA ont le même label), à défaut le dossier de `path` ; jeu : `gameName`. Noms rendus sûrs (pas de `/ \ : * ? " < > |`, pas de point initial, 100 caractères au plus), doublons suffixés ` (2)`. Les dossiers sont retrouvés par les identifiants de leurs fichiers cachés `.console.json` (`deviceId`) et `.game.json` (`gameId`), jamais par leur nom : renommer une console ou deux jeux du même nom ne mélangent pas les historiques. Chaque version a son `.version.json` (numéro de publication `sequence`, SHA-256, tailles, dates).
- **Écritures** : par `NSFileCoordinator` ; une version est assemblée hors du dossier (fichiers synchronisés sur disque), puis déplacée en un seul renommage. Au plus 10 versions par save, rangées par numéro de publication, jamais par date (l'horloge de l'iPhone peut reculer) : la plus ancienne publiée est supprimée seulement une fois la nouvelle en place, jamais la nouvelle ; une rotation interrompue (11 dossiers) est terminée à la sync suivante, même sans changement ; un dossier sans `.version.json` lisible (ajouté par l'utilisateur) n'est jamais compté ni supprimé. Rien d'autre n'est jamais supprimé par l'app.
- **Écran Saves sur cet iPhone** : jeux par système (nombre de versions, date de la dernière), puis versions d'un jeu (date, nom, taille, « Avec son fichier d'horloge »). Actions : **Restaurer sur la console…** et **Partager** (la save et son `.rtc`).
- **Restauration** : depuis le coffre qui a affiché la version (même si iCloud a été activé ou désactivé depuis), après vérification du SHA-256 de la copie relue (sinon rien n'est envoyé), et après confirmation, `PUT /games/{gameId}/save` (au-delà de 64 Mio, la file d'envoi avec `target.kind = "save"`) ; la console garde d'abord sa save (`before-replace`). Seule la save est restaurée : le contrat n'a pas de route pour écrire un `.rtc`, le fichier d'horloge de la console est gardé tel quel, et la confirmation le dit. La save restaurée est une nouvelle version à la sync suivante.

## 9. Un seul appareil en écriture (M10)

Décisions : [09, M10](09-feuille-de-route.md). Contrat : section « Single writer (M10) » de `protocol/openapi.yaml`.

- **Bail** : après la connexion, l’app lit le détenteur dans `writer` de `GET /device`. Tant qu’elle est au premier plan, ou qu’un de ses envois ou une de ses tâches est en cours, elle envoie `PUT /writer` **sans** `force` environ toutes les 20 s : cela renouvelle le bail si cet iPhone l’a, le prend s’il est libre, et ne le prend jamais à un autre appareil (`423 read_only`, l’app passe alors en lecture seule). Ouvrir l’app ne prend donc jamais la main à un autre appareil. En passant en arrière-plan sans transfert en cours, l’app envoie `DELETE /writer` une fois et s’arrête, après la réponse de toute demande `PUT /writer` déjà envoyée (les demandes de bail passent une par une, jamais annulées) ; avec un transfert en cours, elle renouvelle jusqu’à sa fin puis relâche. Un pak sans la route (`404`) est traité comme sans bail (écritures permises).
- **Bandeau** : en lecture seule, un bandeau au-dessus des écrans de la console (et des feuilles Ajouter et Patch) affiche « Lecture seule — <appareil> a la main », ou « Lecture seule — <appareil> a pris la main » quand cet iPhone avait la main, avec « Consulter et télécharger restent possibles. Les modifications sont désactivées sur cet iPhone. » et le bouton **Prendre la main**.
- **Prendre la main** : après confirmation (« Prendre la main ? — <appareil> passe en lecture seule. Ses transferts en cours se mettent en pause. »), `PUT /writer` avec `{"force": true}`.
- **Actions désactivées en lecture seule** : ajouter (fichiers ou lien), supprimer un jeu, vider la corbeille ou en supprimer une entrée, remplacer la save, créer, restaurer ou supprimer une copie de sauvegarde, verrouiller, déverrouiller, restaurer ou supprimer un state, restaurer une version de l’historique de reprise, modifier, réordonner, enregistrer ou importer des cheats, appliquer un patch, changer ou retirer l’image, **Restaurer sur la console…** (Saves sur cet iPhone), reprendre, renvoyer ou annuler un envoi, annuler une tâche de la console (aussi depuis l’écran Patch). Une annulation n’est définitive qu’une fois confirmée par la console : en attendant (« Annulation… »), l’envoi reste dans la liste (non effaçable), l’écran Patch ne se ferme pas et la copie locale est gardée ; refusée (`423`), l’envoi passe en pause et reprendra. Sans réponse nette (connexion perdue, erreur de la console ; seuls un `2xx` ou un `404` confirment), l’envoi reste aussi, avec sa copie locale : « L’annulation n’a pas atteint la console », pour reprendre ou annuler à nouveau. Restent permis : la navigation et les lectures, les téléchargements vers l’iPhone (save, copies, states, partage) et la sync des saves (M9, lectures seules).
- **Après un `423`** (quelle que soit la requête, y compris d’un envoi ou d’une tâche) : l’app passe en lecture seule et l’écran affiche le message du §5. Un envoi refusé passe **en pause** (« En pause : <appareil> a la main. Le transfert reprendra quand cet iPhone reprendra la main. »), jamais perdu ; il reprend à l’offset confirmé dès que cet iPhone retrouve la main (prise de main ou bail libre au renouvellement suivant). Il en va de même pour l’envoi d’un fichier de cheats (Importer > Fichiers) et d’un patch (écran Patch) : ils attendent, en pause, avec leur copie locale, puis continuent. Un `423` répondant à une requête envoyée avant une prise de main ne change rien.
- **Déconnecté par la console** (`401 disconnected`) : la connexion s’arrête, le tableau de bord affiche « Déconnecté par la console. » et **Se reconnecter**. `POST /clients/me/connect` n’est envoyé que par la tentative de connexion lancée par ce bouton, ou par l’ouverture de cette console depuis la liste des consoles (et seulement si le pak répond `401 disconnected`) ; la permission ne vaut que pour cette tentative, quelle qu’en soit l’issue ; jamais par une reconnexion automatique (nouvel essai, résolution Bonjour, tâches d’arrière-plan, sync à la connexion). Les envois refusés ainsi passent en pause.
- **Oublié par la console** : `401 unknown_client`, comme au §5 (**Refaire l’appairage**).
