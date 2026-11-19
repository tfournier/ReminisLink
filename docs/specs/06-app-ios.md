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

Pas d'entitlement multicast : Bonjour via `NWBrowser` n'en a pas besoin.

## 4. Écrans

```
Consoles ─┬─ Ajouter une console (scan QR / PIN)
          └─ Console ─┬─ Tableau de bord (état)
                      ├─ Bibliothèque ─ Système ─ (Dossier) ─ Jeu ─┬─ Save
                      │                                            ├─ States
                      │                                            ├─ Cheats
                      │                                            ├─ Patch
                      │                                            └─ Supprimer
                      ├─ Ajouter (fichier / URL)
                      ├─ Transferts
                      └─ Réglages de la console
Réglages de l'app
```

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

Nom, modèle, OS et version, version du pak, IP, batterie, stockage (barre de remplissage, avec la part occupée par la corbeille), appareils connectés (champ `connectedClients` de `GET /device`). Actions : **Corbeille** (liste des entrées avec taille et date d'expiration, suppression d'une entrée, **Vider la corbeille** avec confirmation), **Se désappairer**.

### 4.4 Bibliothèque

- Systèmes avec nombre de jeux, puis jeux du système : jaquette, nom, taille, pastilles (save, nombre de states, cadenas si un state est verrouillé, nombre de cheats).
- Recherche dans le système, tri (nom, date, taille).
- Glisser vers la gauche sur un jeu : **Supprimer**.

### 4.5 Fiche d'un jeu

- En-tête : jaquette, nom, système, taille totale, liste des fichiers (chemins informatifs).
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

Langue (suivre le système par défaut), consoles appairées (supprimer), à propos (versions, licence, lien du dépôt).

## 5. Erreurs à présenter

| Situation | Détection | Message et action |
|---|---|---|
| Accès au réseau local refusé | `NWBrowser` en état `.waiting` avec une erreur de politique (`kDNSServiceErr_PolicyDenied`) | « ReminisLink n'a pas accès au réseau local. » Bouton **Ouvrir les Réglages** |
| Console introuvable | Aucun résultat Bonjour avec le bon `id` après 8 s | « Console introuvable. Vérifie que ReminisLink est ouvert sur la console, que l'iPhone et la console sont sur le même Wi-Fi (pas un réseau invité), et désactive ton VPN. » Bouton **Réessayer** |
| Client inconnu | `401 unknown_client` | « Cette console ne reconnaît plus ton iPhone. » Bouton **Refaire l'appairage** |
| Horloge décalée | `401 clock_skew` | Correction automatique et nouvel essai ; message seulement si ça échoue encore |
| Espace insuffisant | `507` | « Espace insuffisant sur la carte SD : il manque X Go. » |
| Pak fermé pendant un transfert | Erreur réseau | « La console ne répond plus. » Le transfert passe en pause et reprendra |

## 6. Stockage local

- Trousseau : pour chaque console, `deviceId`, `clientId`, clé (accessibilité `afterFirstUnlockThisDeviceOnly`, pas de synchronisation iCloud).
- `UserDefaults` du groupe d'apps : nom affiché des consoles, préférences.
- Cache : jaquettes (par `gameId` + `ETag`), dans `Caches/`.
- Aucune donnée envoyée hors du réseau local. Pas d'analytics.

## 7. Transferts en arrière-plan

En v1, les envois se font au premier plan, avec `UIApplication.beginBackgroundTask` pour terminer le morceau en cours si l'app passe en arrière-plan. Les envois reprennent au retour. Les sessions `URLSession` d'arrière-plan sont à étudier plus tard (incompatibles avec la signature par requête sans adaptation).
