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

- **TLS épinglé** : chaque console a son client OkHttp, dérivé d'un client commun, avec un `X509TrustManager` qui n'accepte que le certificat dont l'empreinte SPKI (`k` du QR code) correspond, comparée en temps constant ; jamais de confiance système, pas de vérification du nom d'hôte (l'adresse est une IP). Le `CertificatePinner` d'OkHttp ne convient pas : il s'applique après la vérification système, qu'un certificat auto-signé ne passe pas. Une empreinte différente donne `IdentityChanged` et la requête n'est jamais envoyée. Sans empreinte, tout serveur est refusé. Pas de trafic en clair (`usesCleartextTraffic="false"`) et **aucune redirection suivie** (`followRedirects` et `followSslRedirects` désactivés sur chaque client épinglé, dans `:core`) : une redirection ne peut ni sortir du TLS épinglé avec les en-têtes `X-RL-*`, ni réutiliser une signature pour un autre chemin.
- **Signature** : un intercepteur OkHttp signe les requêtes marquées, avec le chemin et la requête **tels qu'envoyés** (`encodedPath`, `encodedQuery`) et le SHA-256 du corps ; un nouveau nonce à chaque tentative. Les requêtes sont construites avec `Signer.encodeQuery` (forme canonique), qui refuse une clé en double comme le pak ; `UNSIGNED` n'est accepté que pour `PUT /api/v1/uploads/{uploadId}`. La canonicalisation elle-même reste indépendante de ces règles (les vecteurs contiennent une clé en double). En-tête informatif `X-RL-Platform: android` ; `platform: android` dans `POST /pair`.
- **Version du protocole client** : `minClientVersion` de `/hello` est comparé à une version du protocole client (M17), commune aux apps, et non à la version de l'app. L'app Android annonce `ApiClient.CLIENT_PROTOCOL_VERSION` (`1.0.0`, comme `clientProtocolVersion` côté iOS), indépendante de son `versionName` (`0.1.0`).

### Clé de signature

Contrairement à iOS (identité partagée par le trousseau iCloud), **chaque appareil Android a sa propre identité** (`clientId` UUID en minuscules et clé Ed25519) et s'appaire une fois par console. Pas de sauvegarde Android (`allowBackup="false"`, règles d'extraction vides) : la clé ne peut pas être restaurée ailleurs.

1. **API 33 et plus** : paire Ed25519 générée dans l'**Android Keystore** (`ECGenParameterSpec("ed25519")`, signature `Ed25519`), jamais exportable. À la création, une signature d'essai est vérifiée avec Tink ; si le Keystore ne prend pas Ed25519 en charge (certains appareils), on passe au cas 2.
2. **Sinon** : graine Ed25519 de 32 octets tirée au hasard, signée avec Tink (`subtle.Ed25519Sign`, RFC 8032), et gardée **chiffrée** (AES-256-GCM) par une clé AES non exportable de l'Android Keystore. *(Choix du 21/11/2026 : équivalent à un « keyset » Tink chiffré par le Keystore, sans le format de keyset de Tink, dont les signatures portent un préfixe de 5 octets et dont la clé publique brute est plus difficile à extraire.)* La graine déchiffrée ne reste qu'en mémoire.

L'identité n'est utilisée qu'une fois enregistrée (`commit()` vérifié ; en cas d'échec, la clé du Keystore est supprimée et l'appairage échoue au lieu de partir avec une identité perdue au redémarrage). L'émulateur API 35 utilise le cas 1 (`IdentityTest` l'exige sur un émulateur API 33+).

## 5. Découverte

- `NsdManager` (mDNS de la plateforme) cherche `_reminislink._tcp`, résout chaque service un par un (avant l'API 34, NSD refuse les résolutions en parallèle), lit `id` dans le TXT et garde l'adresse IPv4. Une console est identifiée par son `id`, jamais par son nom ni son adresse.
- Comme sur iOS (06 §4.2) : recherche par `id` pendant **3 s**, puis repli sur `h`:`p` du lien d'appairage, et pour une console appairée sur la dernière adresse validée (mise à jour à chaque `/hello` réussi), toujours en TLS épinglé.
- Une annonce mDNS n'est pas authentifiée : l'adresse annoncée puis l'adresse de repli sont essayées **l'une après l'autre avec la même empreinte** (`Locator.connectFirst`). Un mauvais certificat, une autre console ou une adresse injoignable font passer à la suivante ; si aucune n'est validée, l'erreur d'identité l'emporte. Une fausse annonce ne peut donc ni se faire accepter ni cacher la vraie console.
- *En ligne* sur la liste des consoles : seulement après un `/hello` validé avec l'empreinte à l'adresse annoncée ; l'état disparaît quand le service mDNS disparaît ou quand la liste n'est plus affichée.
- Le nom et le modèle affichés viennent de `/hello`, rapprochés par `deviceId`, jamais du nom Bonjour.

## 6. Écrans (premier incrément)

Icône : l'icône de l'app iOS (`ios/Design/AppIcon/icon.svg`) en icône adaptative (fond en dégradé, premier plan vectoriel réduit à 80 % pour la zone sûre, version monochrome pour les icônes à thème).


- **Consoles** : liste des consoles appairées (nom, modèle, *En ligne* si vue par mDNS pendant que la liste est affichée, sinon *Hors ligne*), **Oublier** avec confirmation (retire la console de ce téléphone seulement ; pour la retirer aussi de la console : Réglages > Appareils sur la console). Sans console : les 3 étapes et **Ajouter une console**.
- **Ajouter une console** : nom affiché sur la console (`clientName`, le modèle de l'appareil par défaut), **Scanner le QR code**, et **coller un lien d'appairage** (repli sans caméra).
- **Lien ouvert depuis une autre app** (`reminislink://pair`) : une autre app peut fournir un lien vers une console qu'elle contrôle. L'app affiche d'abord « Appairer avec cette console ? » avec le nom et l'adresse lus dans le lien ; **rien n'est envoyé sur le réseau avant la confirmation**. Un lien rouvert depuis les apps récentes ou après une recréation de l'activité n'est pas rejoué.
- **Secret du QR code** : le lien n'entre jamais dans les arguments de navigation ni dans l'état sauvegardé (pas de `rememberSaveable` pour le champ de collage). Il reste en mémoire (`PendingLinks`) derrière un jeton opaque, retiré à la fin de l'appairage ou quand l'écran est quitté ; après un redémarrage de l'app, il faut scanner de nouveau.
- **Scanner** : CameraX (aperçu et analyse) et ZXing core sur le plan de luminance ; seuls les liens `reminislink://pair` sont pris ; un lien `v=1` ou plus récent affiche le même message que sur iOS.
- **Appairage** : Recherche de la console → Appairage → Terminé ; refus si `pairingOpen` est faux ; confirmation si la console est déjà appairée ; la preuve de la console est vérifiée avant d'enregistrer quoi que ce soit. Jusqu'à l'envoi de `POST /pair`, le travail appartient à l'écran : le quitter annule tout (recherche, `/hello`). À partir de `POST /pair`, l'échange passe dans la portée de l'application : il va à son terme et l'appairage est enregistré même si l'écran est fermé. Un réappairage remplace l'entrée de la console en une seule écriture atomique.
- **Console** (tableau de bord, `GET /device`) : modèle, système et version, version du pak, IP, batterie, stockage (barre), appareils connectés, **Bibliothèque**, **Oublier**.
- **Bibliothèque** (`GET /games`, `GET /systems` pour les noms) : liste plate, recherche insensible à la casse et aux accents (tous les mots), système, dossier, sauvegarde, nombre d'états et de codes de triche ; message si la liste est tronquée.
- **Jeu** (`GET /games/{gameId}`) : fichier, taille, sauvegarde, nombre d'états et de fichiers de codes de triche, liste des fichiers avec leur rôle.

Pas encore : transferts, sauvegardes, états, codes de triche, patchs, apps, corbeille, mode démo, événements (SSE), désappairage côté console (`DELETE /clients/me`).

## 7. Tests

| Niveau | Contenu | Commande |
|---|---|---|
| Unitaires `:core` | Tous les vecteurs partagés (signature, requêtes invalides, appairage, liens, empreinte TLS, identifiants), lien d'appairage, magasin de consoles, `Locator`, recherche ; MockWebServer en TLS avec un certificat P-256 auto-signé : bonne empreinte acceptée, mauvaise refusée sans requête reçue, certificat signé par une autorité refusé même quand le client de base fait confiance à cette autorité, redirections HTTPS → HTTP et HTTPS → autre chemin non suivies, clé en double et `UNSIGNED` hors envoi refusés, repli après une fausse annonce (`connectFirst`), en-têtes de signature vérifiés, nouveau nonce par requête, correction d'horloge, preuve de la console | `make android-test` |
| Instrumentés | `IdentityTest` (Keystore exigé sur un émulateur API 33+), `SmokeTest` (Compose : écran d'accueil, lien collé refusé), `ExternalLinkTest` (lien d'une autre app : aucune connexion avant confirmation ni après Annuler ; quitter l'écran après confirmation coupe la connexion en cours) | émulateur API 35 : `./gradlew connectedDebugAndroidTest` |
| De bout en bout | `PakIntegrationTest` contre le vrai pak headless : appairage par le lien collé, tableau de bord, bibliothèque, fiche d'un jeu. L'émulateur joint le Mac à `10.0.2.2` ; mDNS ne traverse pas l'émulateur, l'app se replie donc sur l'adresse du lien après 3 s | `android/scripts/pak-e2e.sh` (lance `make headless`, réécrit `h=` en `10.0.2.2`, arrête le pak par PID ; la sortie du pak, qui contient le lien et le QR code, va dans un fichier 0600 d'un dossier temporaire 0700 supprimé à la sortie, et tout diagnostic affiché est expurgé du lien et du QR code) |

La CI (job `android`, ubuntu, JDK 21) lance les tests unitaires, construit l'APK de debug et compile les tests instrumentés ; les tests instrumentés et de bout en bout tournent sur le Mac.
