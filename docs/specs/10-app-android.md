# 10 — App Android (M21)

L'app iOS ([06](06-app-ios.md)) reste la référence fonctionnelle : l'app Android parle la même API ([04](04-api-v1.md), `protocol/openapi.yaml`) avec le même protocole ([03](03-protocole.md)) et passe les mêmes vecteurs de test. Ce document ne décrit que ce qui est propre à Android. Statut : premier incrément (21/11/2026), lecture seule.

## 1. Cible et publication

- Android 8.0 (API 26) minimum, cible API 35 (`targetSdk 35`), compilé avec l'API 36 (exigée par les versions d'AndroidX utilisées). minSdk 26 : `java.time`, NSD et AES-GCM de l'Android Keystore disponibles partout.
- Kotlin, Jetpack Compose (Material 3, couleurs dynamiques à partir d'Android 12), coroutines.
- Identifiant d'application : **`link.reminis.app`** (le même que l'app iOS).
- Langues : anglais et français dès le départ (`res/values`, `res/values-fr`), tutoiement et vocabulaire de l'app iOS (« sauvegardes », « états », « codes de triche », « patch »), espace insécable avant « : ? ! % ».
- **Aucune dépendance Google propriétaire** (ni Google Play Services, ni ML Kit, ni Firebase) : l'app DOIT pouvoir être publiée sur F-Droid. Publication prévue : F-Droid et APK des releases GitHub d'abord, Google Play ensuite ([09](09-feuille-de-route.md), M21). Le bloc de métadonnées de dépendances signé par Google est retiré de l'APK (`dependenciesInfo`).

## 2. Dépendances

Approuvées par la personne le 21/11/2026, et seulement celles-ci : bibliothèque standard Kotlin, AndroidX (Compose, Activity, Lifecycle, Navigation-Compose, CameraX, DataStore si besoin), kotlinx.coroutines, kotlinx.serialization, OkHttp (et okhttp-sse si utile), ZXing core (lecture du QR code), Google Tink (Ed25519, Java pur, sans Play Services), JUnit, AndroidX Test, Compose UI Test, MockWebServer pour les tests. Toute autre dépendance est proposée à la personne avant d'être ajoutée. Les versions sont dans `android/gradle/libs.versions.toml`.

Précisions : `okhttp-tls` (module d'OkHttp) sert seulement aux tests, pour créer un certificat auto-signé P-256 ; `material-icons-core` (AndroidX Compose) donne les icônes de base. DataStore n'est pas utilisé (fichiers et `SharedPreferences` suffisent).

## 3. Structure

| Module | Contenu |
|---|---|
| `android/core` (Kotlin/JVM pur) | Modèles du contrat (kotlinx.serialization, champs inconnus ignorés, énumérations ouvertes gardées en chaînes), `Signer` (chaîne canonique RL2-ED25519), `Ed25519Key` (Tink), `Pairing` / `PairingAttempt` (preuves v2), `PairingLink`, `TlsPin` et `PinnedTrustManager`, `ApiClient` (OkHttp, intercepteur de signature, correction d'horloge après `401 clock_skew`), `ConsoleStore`, `Locator`, `LibrarySearch` |
| `android/app` | Écrans Compose, `IdentityStore` (Keystore), `Discovery` (NSD), navigation |

`:core` NE DOIT PAS dépendre d'Android : il se teste avec JUnit sur la JVM. Ses tests lisent les vecteurs **sur place** dans `protocol/test-vectors/` (propriété système `reminislink.vectors` posée par Gradle), sans copie : pas d'équivalent de `scripts/sync-test-vectors.sh` côté Android.

## 4. Réseau et sécurité

- **TLS épinglé** : chaque console a son client OkHttp, dérivé d'un client commun, avec un `X509TrustManager` qui n'accepte que le certificat dont l'empreinte SPKI (`k` du QR code) correspond, comparée en temps constant ; jamais de confiance système, pas de vérification du nom d'hôte (l'adresse est une IP). Le `CertificatePinner` d'OkHttp ne convient pas : il s'applique après la vérification système, qu'un certificat auto-signé ne passe pas. Une empreinte différente donne `IdentityChanged` et la requête n'est jamais envoyée. Sans empreinte, tout serveur est refusé. Pas de trafic en clair (`usesCleartextTraffic="false"`).
- **Signature** : un intercepteur OkHttp signe les requêtes marquées, avec le chemin et la requête **tels qu'envoyés** (`encodedPath`, `encodedQuery`) et le SHA-256 du corps ; un nouveau nonce à chaque tentative. Les requêtes sont construites avec `Signer.encodeQuery` (forme canonique). En-tête informatif `X-RL-Platform: android` ; `platform: android` dans `POST /pair`.
- **Version du client** : `minClientVersion` de `/hello` est une seule valeur pour toutes les apps. L'app Android compare `CLIENT_PROTOCOL_VERSION` (`1.0.0`, le niveau de l'app iOS qu'elle égale), et non son `versionName` (`0.1.0`). *À trancher par la personne : une valeur par plateforme dans `/hello`, ou une numérotation commune.*

### Clé de signature

Contrairement à iOS (identité partagée par le trousseau iCloud), **chaque appareil Android a sa propre identité** (`clientId` UUID en minuscules et clé Ed25519) et s'appaire une fois par console. Pas de sauvegarde Android (`allowBackup="false"`, règles d'extraction vides) : la clé ne peut pas être restaurée ailleurs.

1. **API 33 et plus** : paire Ed25519 générée dans l'**Android Keystore** (`ECGenParameterSpec("ed25519")`, signature `Ed25519`), jamais exportable. À la création, une signature d'essai est vérifiée avec Tink ; si le Keystore ne prend pas Ed25519 en charge (certains appareils), on passe au cas 2.
2. **Sinon** : graine Ed25519 de 32 octets tirée au hasard, signée avec Tink (`subtle.Ed25519Sign`, RFC 8032), et gardée **chiffrée** (AES-256-GCM) par une clé AES non exportable de l'Android Keystore. *(Choix du 21/11/2026 : équivalent à un « keyset » Tink chiffré par le Keystore, sans le format de keyset de Tink, dont les signatures portent un préfixe de 5 octets et dont la clé publique brute est plus difficile à extraire.)* La graine déchiffrée ne reste qu'en mémoire.

L'émulateur API 35 utilise le cas 1 (vérifié par le test instrumenté `IdentityTest`).

## 5. Découverte

- `NsdManager` (mDNS de la plateforme) cherche `_reminislink._tcp`, résout chaque service un par un (avant l'API 34, NSD refuse les résolutions en parallèle), lit `id` dans le TXT et garde l'adresse IPv4. Une console est identifiée par son `id`, jamais par son nom ni son adresse.
- Comme sur iOS (06 §4.2) : recherche par `id` pendant **3 s**, puis repli sur `h`:`p` du lien d'appairage, et pour une console appairée sur la dernière adresse validée (mise à jour à chaque `/hello` réussi), toujours en TLS épinglé.
- Le nom et le modèle affichés viennent de `/hello`, rapprochés par `deviceId`, jamais du nom Bonjour.

## 6. Écrans (premier incrément)

- **Consoles** : liste des consoles appairées (nom, modèle, *En ligne* si vue par mDNS pendant que la liste est affichée, sinon *Hors ligne*), **Oublier** avec confirmation (retire la console de ce téléphone seulement ; pour la retirer aussi de la console : Réglages > Appareils sur la console). Sans console : les 3 étapes et **Ajouter une console**.
- **Ajouter une console** : nom affiché sur la console (`clientName`, le modèle de l'appareil par défaut), **Scanner le QR code**, et **coller un lien d'appairage** (repli sans caméra). Le lien `reminislink://pair` ouvert depuis une autre app lance aussi l'appairage.
- **Scanner** : CameraX (aperçu et analyse) et ZXing core sur le plan de luminance ; seuls les liens `reminislink://pair` sont pris ; un lien `v=1` ou plus récent affiche le même message que sur iOS.
- **Appairage** : Recherche de la console → Appairage → Terminé ; refus si `pairingOpen` est faux ; confirmation si la console est déjà appairée ; la preuve de la console est vérifiée avant d'enregistrer quoi que ce soit. L'échange tourne dans la portée de l'application : quitter l'écran ne l'interrompt pas une fois `POST /pair` envoyé.
- **Console** (tableau de bord, `GET /device`) : modèle, système et version, version du pak, IP, batterie, stockage (barre), appareils connectés, **Bibliothèque**, **Oublier**.
- **Bibliothèque** (`GET /games`, `GET /systems` pour les noms) : liste plate, recherche insensible à la casse et aux accents (tous les mots), système, dossier, sauvegarde, nombre d'états et de codes de triche ; message si la liste est tronquée.
- **Jeu** (`GET /games/{gameId}`) : fichier, taille, sauvegarde, nombre d'états et de fichiers de codes de triche, liste des fichiers avec leur rôle.

Pas encore : transferts, sauvegardes, états, codes de triche, patchs, apps, corbeille, mode démo, événements (SSE), désappairage côté console (`DELETE /clients/me`).

## 7. Tests

| Niveau | Contenu | Commande |
|---|---|---|
| Unitaires `:core` | Tous les vecteurs partagés (signature, requêtes invalides, appairage, liens, empreinte TLS, identifiants), lien d'appairage, magasin de consoles, `Locator`, recherche ; MockWebServer en TLS avec un certificat P-256 auto-signé : bonne empreinte acceptée, mauvaise refusée sans requête reçue, certificat signé par une autorité refusé, en-têtes de signature vérifiés, nouveau nonce par requête, correction d'horloge, preuve de la console | `make android-test` |
| Instrumentés | `IdentityTest` (Keystore), `SmokeTest` (Compose : écran d'accueil, lien collé refusé) | émulateur API 35 : `./gradlew connectedDebugAndroidTest` |
| De bout en bout | `PakIntegrationTest` contre le vrai pak headless : appairage par le lien collé, tableau de bord, bibliothèque, fiche d'un jeu. L'émulateur joint le Mac à `10.0.2.2` ; mDNS ne traverse pas l'émulateur, l'app se replie donc sur l'adresse du lien après 3 s | `android/scripts/pak-e2e.sh` (lance `make headless`, réécrit `h=` en `10.0.2.2`, arrête le pak par PID) |

La CI (job `android`, ubuntu, JDK 21) lance les tests unitaires et construit l'APK de debug ; les tests instrumentés et de bout en bout tournent sur le Mac.
