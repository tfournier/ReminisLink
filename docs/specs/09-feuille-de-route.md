# 09 — Feuille de route

Chaque jalon a un prompt dédié dans `prompts/`. Un jalon est terminé quand tous ses critères sont remplis et que la CI passe.

## M0 — Relevés sur la console (humain)

But : remplacer toutes les mentions **[À VÉRIFIER M0]** par des faits.

- Fait le 19/11/2026 : analyse du code source de NextUI et du firmware d'origine ([m0/source-analysis.md](m0/source-analysis.md)), tableau de [05 §3.1](05-console-pak.md#31-organisation-nextui) mis à jour.
- Reste : la procédure réduite de [m0/README.md](m0/README.md) sur la console (veille, mDNS en Wi-Fi, RTC, batterie, version, confirmation rapide des chemins), puis les points de [m0/questions.md](m0/questions.md).

**Fait quand** : plus aucune mention **[À VÉRIFIER M0]** dans les spécifications.

## M1 — Contrat et cryptographie

- `protocol/openapi.yaml` complet pour l'API v1.
- `protocol/test-vectors/` (appairage, signature, identifiants).
- Go : `internal/auth` (appairage, dérivation, signature, cache de nonces, stockage des clients).
- Swift : `ReminisLinkKit` avec `Signer` et `Pairing`.

**Fait quand** : les deux suites passent les mêmes vecteurs ; `openapi.yaml` est valide.

## M2 — Serveur du pak (lecture)

- Squelette `cmd/reminislink` (`--desktop`, `--headless`), serveur, middlewares, erreurs.
- Annonce Bonjour, `/hello`, `/pair`, `/device`, `/systems`, `/systems/{id}/games`, `/games/{id}`, `/games/{id}/art`.
- Adaptateur NextUI (lecture), carte SD factice, tests d'intégration.

**Fait quand** : `make headless` sert une bibliothèque factice ; toutes ces routes ont des tests.

## M3 — App iOS (base)

- Projet Xcode, `ReminisLinkKit` (découverte, client API), écrans Consoles, Appairage (scanner + lien + PIN), Tableau de bord, Bibliothèque, Fiche de jeu (lecture seule).
- Gestion des erreurs du tableau de [06 §5](06-app-ios.md#5-erreurs-à-présenter).

**Fait quand** : sur simulateur, l'app trouve le pak lancé en `--headless`, s'appaire et parcourt la bibliothèque.

## M4 — Transferts et suppression

- Côté Go : `/uploads`, `/downloads` (**retiré de la v1** le 20/11/2026, voir « Après la v1 »), `/jobs`, `DELETE /games/{id}`, corbeille (`/trash`), purge.
- Côté iOS : écran Ajouter, Transferts, reprise, extension de partage, suppression.

**Fait quand** : ajout d'une ROM depuis Fichiers (et depuis une URL, retiré de la v1 le 20/11/2026), reprise après coupure, suppression avec corbeille, testés en automatique (headless) et sur console.

## M5 — Saves, states, cheats

- Go : routes saves, states, verrous, historique de reprise, cheats (code repris de Cheat Manager), maintenance au lancement.
- iOS : sections Save, States, Cheats de la fiche de jeu.

**Fait quand** : tous les scénarios 7 et 8 de la checklist console passent.

## M6 — Patchs

- Go : `internal/patch` (IPS, BPS, UPS), route `/games/{id}/patch`, `/sd/files`.
- iOS : section Patch.

**Fait quand** : les tests de patchs passent (dont les cas d'erreur) et un jeu patché se lance sur la console.

## M7 — Interface console, publication

- Écrans gabagool du pak (principal, réglages, quitter), traductions EN/FR.
- `pak.json`, Makefile, CI, `ReminisLink.pak.zip`, README, captures d'écran.
- App : icône, textes App Store, politique de confidentialité, TestFlight.

**Fait quand** : installation depuis l'archive publiée sur une Brick neuve, checklist console complète, build TestFlight distribué.

## M8 — Retours de test et revue croisée

- Corrections des revues croisées Codex de M3 à M7 (`docs/reviews/m*-codex.md`).
- Appairage seulement à la demande (05 §7), image d'un jeu (04 `PUT`/`DELETE /games/{gameId}/art`), noms en NFC (03 §5), pastille d'état, progression d'envoi fluide, captures avec un nom neutre.

**Fait quand** : toutes les suites passent, parcours réels contre `make headless`, revue croisée des corrections, pak et app de test fournis à la personne.

## M9 — Sync des saves vers l'iPhone (iCloud Drive)

Décisions du 19/11/2026 (personne) :
- **Contenu** : les saves seulement (avec leur `.rtc`), pas les states.
- **Déclenchement** : bouton « Sync » au tableau de bord, et synchronisation automatique à chaque connexion à la console (désactivable dans les réglages de l'app).
- **Stockage** : iCloud Drive de l'utilisateur, dossier « ReminisLink » visible dans Fichiers (`<console>/<système>/<jeu>/`), avec jusqu'à 10 versions par save ; repli sur le stockage local de l'app si iCloud est désactivé.
- **Pak** : nouvelle route `GET /saves` (liste de toutes les saves avec chemin informatif, taille, date et SHA-256, en une requête) pour ne télécharger que ce qui a changé ; téléchargement par la route existante `GET /games/{gameId}/save`.
- **Restauration** : depuis une version sur l'iPhone vers la console, par `PUT /games/{gameId}/save` (le pak garde sa copie `before-replace`).
- Politique de confidentialité et 06 mises à jour (données copiées dans l'iCloud de l'utilisateur, jamais chez l'éditeur) ; capacité iCloud (conteneur) sur l'App ID.

**Fait quand** : sync complète puis incrémentale testée contre `make headless`, restauration d'une version, comportement sans iCloud, textes et politique à jour.

## M10 — Un seul appareil en écriture (verrou façon KVM)

Décision du 19/11/2026 (personne) : quand plusieurs iPhones sont connectés, **un seul a la main en écriture**, les autres sont en lecture seule ; la console peut déconnecter ou oublier un appareil. Remplace le risque résiduel « deux clients pendant une suppression » (07 §3).

- **Bail d'écriture** tenu en mémoire par le pak : un seul client à la fois. Le premier client qui fait une opération d'écriture le prend s'il est libre. Il est renouvelé par chaque requête signée de ce client et par un renouvellement explicite (environ toutes les 20 s tant que l'app est au premier plan, et tant qu'un envoi ou un téléchargement du client est en cours). Il expire après **60 s** sans signe de vie ; il est libéré à la fermeture du pak et quand l'app passe en arrière-plan sans transfert en cours.
- **Lecture seule** pour les autres clients : toutes les routes de lecture, téléchargements et Sync (M9) restent permis ; toute route qui modifie la carte (envoi, suppression, remplacement, restauration, patch, cheats, image, corbeille, verrous) répond **`423 read_only`** avec le nom de l'appareil qui a la main. Les tâches déjà lancées par l'ancien détenteur se terminent ou s'interrompent proprement (envoi mis en pause, jamais perdu).
- **Prendre la main** depuis l'app, avec confirmation : le bail passe au nouveau client ; l'ancien voit « Lecture seule — <appareil> a pris la main ».
- **Contrat** (écrit le 19/11/2026) : `GET /device` et `GET /writer` indiquent le détenteur (`writer` : nom du client, `you`, `expiresAt`, ou `null`) ; `GET /hello`, non signé, n'indique que `writerHeld` (booléen), pour ne pas exposer le nom d'un iPhone à tout le réseau ; `PUT /writer` (prendre ou renouveler, `force` pour prendre la main) et `DELETE /writer` (relâcher) ; erreur `423 read_only` (`details.writer`) ; déconnexion par la console : `401 disconnected` jusqu'à `POST /clients/me/connect`, appelé seulement sur une action de l'utilisateur.
- **Console** (05 §7) : dans **Réglages > Appareils**, la liste des appareils appairés avec leur état (connecté, a la main) ; **Déconnecter** (retire le bail et refuse ses requêtes jusqu'à sa prochaine connexion volontaire depuis l'app) et **Oublier** (supprime l'appairage, avec confirmation ; l'appareil devra se réappairer).
- **App** : bandeau « Lecture seule » et bouton **Prendre la main** ; actions d'écriture désactivées en lecture seule ; message clair après un `423` ou une déconnexion par la console.
- 07 §3 mis à jour (risque résiduel retiré, nouvelle menace : un client appairé qui prend la main sans arrêt — accepté, car tous les clients appairés sont de confiance).

**Fait quand** : deux clients contre `make headless` (le second en lecture seule, prise de main, expiration du bail, déconnexion et oubli depuis l'interface), tests des deux côtés, revue croisée.

## M11 — Ergonomie de l'app

Suggestions de la personne du 19/11/2026, après essai de M10 ; lancé le 19/11/2026. Contrat : `GET /games` (tous les jeux à plat, 20 000 au plus, `truncated`), `fileId` dans chaque fichier de `GET /games/{gameId}` et `GET /games/{gameId}/files/{fileId}` (fichier entier, sans `Range`, seulement un fichier de la liste courante du jeu).

- **Image du jeu** : toucher l'image pour la changer (au lieu de la section Image).
- **Télécharger chaque fichier d'un jeu** vers l'iPhone (ROM, cheats, states…) ; probablement une route de téléchargement par fichier, identifiants opaques résolus par le pak (contrat d'abord).
- **Bibliothèque à plat** : tous les jeux sans regroupement par console, avec recherche et filtres (console, etc.) ; peut demander une liste de tous les jeux en une requête.
- **Barre d'onglets** quand une console est ouverte : Console, Bibliothèque, Transferts, Corbeille.
- **Plus d'envoi depuis la vue Console** (l'ajout reste dans Bibliothèque et Transferts).
- **Retour à la liste des consoles quand le pak se ferme** : quand on quitte ReminisLink sur la console (`503 shutting_down`, annonce Bonjour retirée), l'app revient à la liste des consoles avec un message bref (« La console a quitté ReminisLink »), au lieu de rester sur un écran en erreur ; les envois en cours restent en pause.

**Fait quand** : chaque point visible dans l'app (démo et pak headless), téléchargement d'une ROM et d'un fichier de cheats vers Fichiers, recherche et filtres dans la bibliothèque à plat, tests des deux côtés, revue croisée.

## M14 — Événements poussés par la console (SSE)

Idée de la personne du 20/11/2026 ; fait avant M13 (ordre : M11 → M14 → M13 → M12). Contrat : `GET /events`, flux Server-Sent Events signé à l'ouverture.

- **Fermeture du pak** : événement `shutdown` puis fin du flux ; l'app revient à la liste des consoles aussitôt (la détection par Bonjour reste en secours).
- **Signe de vie** toutes les 15 s au plus ; 35 s sans rien reçu = connexion perdue (veille, Wi-Fi coupé) : l'app l'affiche comme aujourd'hui une coupure réseau.
- **`library`** après toute modification de la carte par un client (envoi installé, suppression, remplacement, restauration, patch, cheats, image, corbeille) : l'app rafraîchit ce qu'elle affiche.
- **`job`** : avancement des patchs et des installations de paks (et des téléchargements par URL, retirés de la v1 le 20/11/2026) (remplace l'interrogation chaque seconde) ; **`device`** : batterie, appareils connectés, espace libre.
- Le pak ferme le flux d'un client déconnecté ou oublié depuis la console, ou désappairé. 4 flux par client, 16 en tout.

**Fait quand** : la fermeture du pak ramène l'app à la liste en moins de 2 s sans action ; un envoi depuis un second client rafraîchit la bibliothèque du premier ; tests des deux côtés et contre `make headless` ; revue croisée.

## M12 — Gestion des apps (paks) (décidé, après M13)

Demande et décisions de la personne du 19/11/2026.

- **Périmètre** : paks d'outils (`Tools/<plateforme>/*.pak`) et d'émulateurs (`Emus/<plateforme>/*.pak`), chacun à sa place (détectée à l'installation, modifiable dans l'app).
- **Qui** : tout appareil appairé qui a la main en écriture (M10). Nouvelle menace acceptée dans 07 §3 : un appareil appairé peut installer du code exécuté sur la console (tous les appareils appairés sont de confiance).
- **Sources** : URL (téléchargement par le pak, comme M4 ; **retiré de la v1** le 20/11/2026), fichier de l'iPhone (envoi reprenable, Fichiers ou extension de partage), fichier déjà sur la carte SD, n'importe où, choisi dans un sélecteur de fichiers de l'app (liste fournie par le pak). Formats : zip, ou dossier `.pak`.
- **Contenu non vérifié** : comme pour une ROM corrompue, ReminisLink ne juge pas la qualité d'un pak (le Pak Store ne le fait pas non plus). Seules restent les protections de la carte : aucun chemin qui sort du dossier d'installation (zip-slip), aucun lien, une taille maximale, un seul dossier `<Nom>.pak` reconnu par archive.
- **Remplacement et suppression** : l'ancien pak passe à la corbeille (récupérable 30 jours) avant un remplacement ; suppression vers la corbeille avec confirmation. ReminisLink ne peut ni se supprimer ni se remplacer pendant qu'il tourne.
- **Navigateur de la carte SD** (demande de la personne du 19/11/2026), ouvert en touchant la section Stockage de l'app et réutilisé comme sélecteur de pak : **consultation seule en M12** (dossiers, noms, tailles, dates, téléchargement d'un fichier vers l'iPhone). Identifiants opaques résolus par le pak, rien hors de la carte, aucun lien suivi ; les données de ReminisLink (clés des appareils, journaux internes) sont cachées. Cible plus tard : gestionnaire complet (suppression vers la corbeille, renommer, déplacer, créer un dossier, envoyer un fichier), avec la main en écriture et des dossiers protégés.
- **Conception retenue (agent principal, 20/11/2026, à reporter dans `openapi.yaml`, 04, 05, 07)** :
  - `GET /paks` : paks installés de `Tools/<plateforme>/` et `Emus/<plateforme>/` (`pakId` opaque, nom, type `tool`/`emu`, taille, date, version lue dans `pak.json` s'il existe, `self` pour ReminisLink).
  - Installation par les mécanismes existants : cible d'envoi `pak` (`POST /uploads`), cible de téléchargement `pak` (`POST /downloads`, retirée de la v1 le 20/11/2026), et `POST /paks/install` depuis un fichier de la carte (`sdEntryId` du navigateur), avec `kind` (`tool`, `emu`, ou détection) et `onConflict` (`fail` par défaut ; l'app envoie `replace` après confirmation). Un dossier `.pak` choisi sur l'iPhone est zippé par l'app avant l'envoi (`NSFileCoordinator` option `forUploading`) ; sur la carte, un dossier `.pak` s'installe tel quel (copie).
  - Archives acceptées : `<Nom>.pak/…`, ou `Tools|Emus/<plateforme>/<Nom>.pak/…` (type déduit) ; un seul pak par archive, `launch.sh` présent. Protections : aucun chemin absolu ni `..`, aucun lien, 10 000 entrées et 1 Gio décompressé au plus, rapport de compression borné ; décompression dans la zone de préparation puis installation atomique (renommage) ; l'ancien pak va à la corbeille. Refus : `422 unsupported_format`.
  - `DELETE /paks/{pakId}` : vers la corbeille. ReminisLink lui-même (`self`) ne peut être ni supprimé ni remplacé : `409 conflict`.
  - Navigateur SD en lecture : `GET /sd/browse?folderId=` (dossiers et fichiers d'un dossier, `sdEntryId` opaques, taille, date, dossier parent) et `GET /sd/browse/{sdEntryId}/content` (fichier entier). Rien hors de la carte, aucun lien suivi, données de ReminisLink cachées (zone de stockage : clés des clients, clé TLS, corbeille, préparation).
  - Écritures sous le verrou par action de M13 ; nouvel événement `paks` (M14) après une installation ou une suppression.
- **Pas de remplacement du Pak Store** : ni catalogue ni mises à jour automatiques (pour l'instant).

## M13 — Appairage inspiré de HomeKit (décidé, après M11, avant M12)

Décisions de la personne du 19/11/2026 : s'inspirer du protocole des accessoires HomeKit sans le suivre à la lettre. Demande un nouvel appairage de l'iPhone (changement de contrat et des vecteurs de test partagés).

- **Plusieurs appareils, deux mécanismes** (décision de la personne du 19/11/2026, qui remplace « un seul appareil appairé ») :
  - **Trousseau iCloud** : la clé privée de l'app est un élément synchronisé du trousseau ; l'iPhone, l'iPad et le Mac d'un même compte Apple partagent la même identité et n'appairent qu'une fois (pas de Secure Enclave, dont les clés ne se synchronisent pas). Sur la console, cette identité apparaît comme un appareil de type **Apple** : nom saisi à l'appairage (iOS ne donne accès ni au nom ni à l'adresse du compte iCloud, ni au vrai nom de l'appareil), avec les plateformes vues récemment (iPhone, iPad, Mac), envoyées dans un en-tête non secret.
  - **Plusieurs appairages** : chaque autre appareil (autre compte Apple, plus tard Android, interface web) a sa propre clé, appairée par le QR code de la console (Réglages > Appairer un nouvel appareil, comme en M8). La console garde la liste des appareils avec Déconnecter et Oublier (M10). Pas de rôles.
- **Verrou par action à la place du bail de M10** (décision du 19/11/2026) : le pak exécute les écritures une à une (suppression, remplacement, restauration, patch, cheats, image, étape finale d'un envoi) ; les morceaux d'un envoi vont en zone d'attente sans verrou. Si une écriture est en cours, la suivante attend qu'elle finisse (quelques secondes) au lieu d'être refusée ; « occupé » seulement si l'attente dépasse une limite. Retirés : bail, renouvellement, `PUT/GET/DELETE /writer`, `423 read_only`, bandeau « Lecture seule » et « Prendre la main ». Gardés : Déconnecter, Oublier, `401 disconnected`. Le risque résiduel « aperçu puis suppression » de 07 §3 revient tel qu'accepté le 19/11/2026.
- La compatibilité iPad et Mac de l'app (écrans, cible Mac) est un jalon à part.
- **Conception retenue (agent principal, 20/11/2026, à reporter dans `openapi.yaml`, 03, 04, 07 et les vecteurs de test)** :
  - **TLS sur le port du pak** : au premier lancement, le pak crée une clé ECDSA P-256 et un certificat auto-signé (validité longue), gardés dans la zone ReminisLink de la carte ; tout le trafic, `/hello` compris, passe en HTTPS. L'empreinte `pin` = SHA-256 de la clé publique du certificat (SPKI), base64url. L'app n'accepte que ce certificat (épinglage), pas de confiance système.
  - **Lien d'appairage v2** : `reminislink://pair?v=2&id=<deviceId>&n=<nom>&s=<secret>&h=<IPv4>&p=<port>&k=<pin>` (QR seulement, plus de PIN). Un lien `v=1` est refusé avec un message « mets à jour le pak ».
  - **`POST /pair` v2** : `{clientId, clientName, platform (apple|android|web|other), clientPublicKey (Ed25519 brut, base64url), clientNonce, proof}` avec `proof = HMAC-SHA256(s, "RL2-PAIR-CLIENT|" + deviceId + "|" + clientId + "|" + clientPublicKey + "|" + clientNonce)` ; réponse `{deviceId, deviceName, deviceNonce, proof}` avec `proof = HMAC-SHA256(s, "RL2-PAIR-DEVICE|" + deviceId + "|" + clientId + "|" + clientPublicKey + "|" + clientNonce + "|" + deviceNonce)`. Le secret reste à usage unique, 10 min, 5 essais. Aucune clé partagée n'est dérivée.
  - **Signature des requêtes RL2** : même chaîne canonique qu'en RL1 avec la première ligne `RL2-ED25519`, signée par la clé privée Ed25519 de l'app ; `X-RL-Signature` = base64url des 64 octets. Le pak ne garde que la clé publique (`clients.json`). Mêmes contrôles d'horloge et de nonce. En-tête informatif non signé `X-RL-Platform: iphone|ipad|mac|android|web` pour l'affichage sur la console.
  - **Identité Apple partagée** : la clé privée et le `clientId` sont un élément synchronisé du trousseau iCloud (même groupe d'accès), pas dans la Secure Enclave. Les autres appareils s'appairent chacun.
  - **Verrou par action** : retrait de `/writer`, `writerHeld`, `Device.writer`, `423 read_only` ; les écritures sont sérialisées par le pak (attente jusqu'à 10 s, puis `429 too_many_attempts` « Device busy » avec `Retry-After` ; remplacé après M13 par le code dédié `429 device_busy`, l'ancienne réponse restant reconnue par l'app). `401 disconnected` et `POST /clients/me/connect` restent.
  - **Migration** : les clients RL1 (clé HMAC) sont effacés au premier lancement du pak M13 ; l'app détecte un pak M13 (`apiVersion: 2` dans `/hello`) et propose de réappairer.
  - **Vecteurs de test** RL2 (preuves d'appairage, signature Ed25519 déterministe, empreinte SPKI) partagés Go et Swift ; les vecteurs RL1 sont retirés.
- **Clés asymétriques Ed25519** à la place de la clé HMAC partagée : chaque requête est signée par la clé privée de l'iPhone (Keychain) ; le pak ne garde sur la carte que la clé publique de l'iPhone, inutilisable pour signer (corrige la copie de `clients.json` depuis une carte SD retirée de la console). Le pak a sa propre paire de clés, dont l'iPhone garde la clé publique.
- **Pas de PIN en M13** (décision de la personne du 19/11/2026) : appairage par QR code seulement ; le PIN (secours si l'appareil photo est abîmé) reviendra plus tard, protégé par un PAKE ou par un échange X25519 confirmé par le PIN (choix à faire alors). L'écran d'appairage de la console n'affiche plus de PIN.
- **Chiffrement par TLS épinglé** : le pak génère un certificat (ECDSA P-256) ; son empreinte est transmise de façon authentifiée à l'appairage (dans le QR code) et épinglée par l'app. TLS de la bibliothèque standard Go, 1.2 au minimum et 1.3 de préférence (ChaCha20-Poly1305 choisi automatiquement si le processeur n'a pas d'instructions AES) ; débit à mesurer sur la console.
- 07 mis à jour : menace « copie de la carte SD » ajoutée, risques « PIN cassable hors ligne » et « contenu en clair » retirés.

## M17 — CI/CD et versions séparées (décidé le 20/11/2026, après la v1.0.0)

But : publier le pak et l'app iOS depuis le même dépôt, chacun avec sa version, sans manipulation à la main.

- **Tags et releases** : pak `vX.Y.Z` (ce qu'attendent la CI actuelle et le Pak Store), app `ios-vX.Y.Z`. Seule une release du pak est marquée « Latest » sur GitHub (le Pak Store lit la dernière release) ; les releases de l'app sont publiées avec `--latest=false` (ou en simple tag). Les deux numéros évoluent indépendamment.
- **Pré-versions** : un tag avec suffixe (`ios-beta-v1.1.0-beta.1`, `v1.1.0-beta.1`) est une pré-version. Côté app, le préfixe distinct `ios-beta-v` est imposé par Xcode Cloud, dont les conditions de tag ne sont que des préfixes : `ios-v` lance le workflow Release, `ios-beta-v` le workflow Beta, jamais les deux pour un même tag (décision du 2026-11-21). App : envoyée sur TestFlight seulement. Pak : release GitHub marquée « Pre-release » (ignorée par le Pak Store), zip joint pour les testeurs. Le numéro de version de l'app reste `X.Y.Z` (exigé par l'App Store), le suffixe ne sert qu'au tag ; le numéro de build augmente à chaque envoi.
- **Versions normales** : tag sans suffixe. Pak : release « Latest » (comme aujourd'hui). App : build envoyé à App Store Connect, puis soumis à l'examen par le workflow Submit lancé à la main sur le tag une fois le build traité, publication automatique après approbation (réglage « Publier automatiquement » de la version). L'examen d'Apple reste obligatoire.
- **Livraison iOS par Xcode Cloud** (choix de la personne) : workflows configurés dans App Store Connect, déclenchés par les tags `ios-v*` et `ios-beta-v*` ; signature gérée par Apple, aucune clé dans GitHub. La soumission automatique à l'examen demande l'API App Store Connect depuis un script Xcode Cloud (`ci_scripts/`), avec une clé API stockée en variable secrète de Xcode Cloud, créée par la personne. Les tests restent sur GitHub Actions.
- **Compatibilité** : `/hello` annonce `minClientVersion`, la version minimale du **protocole client** (pas une version d'app : la même pour tous les clients, comparée à la constante de protocole du client, `APIClient.clientProtocolVersion` sur iOS, `CLIENT_PROTOCOL_VERSION` sur Android), et l'app compare son `minPakVersion` au `pakVersion` du pak ; en cas d'écart, message clair (« mets à jour le pak » / « mets à jour l'app ») au lieu d'une erreur.
- **Notes de version** : un journal par produit (`pak.json` pour le pak, un fichier pour l'app, repris dans les « Nouveautés » de l'App Store).
- **Documentation** de la procédure de publication (README, ios/README.md).
- **Aucune publication sans tests verts** (décision de la personne du 22/11/2026) : une version du pak, de l'app iOS (bêta TestFlight comprise) ou de l'app Android n'est construite que si les tests passent, en deux couches.
  - **Avant l'étiquette (local)** : `make vX.Y.Z`, `make ios-vX.Y.Z` et `make android-vX.Y.Z` (pré-versions comprises) refusent de poser l'étiquette si la CI GitHub du commit `HEAD` lui-même n'a pas réussi les jobs utiles : `go`, `contract`, `build`, `emulation` pour le pak (les `needs` du job `release`) ; `ios` et `contract` pour iOS ; `android` pour Android. Aucune exception du parent ici. `scripts/require-ci.sh` lit l'API avec `gh`.
  - **Règles communes** : seules comptent les exécutions de `ci.yml` pour un push sur la branche `main` (`/actions/workflows/ci.yml/runs?head_sha=…&branch=main&event=push`, toutes les pages, puis vérification de `head_branch == main` : jamais une exécution lancée par une étiquette, qui pourrait viser un commit jamais poussé sur `main`), puis leurs jobs (toutes les pages). On étiquette toujours un commit déjà poussé sur `main`, donc `make v…` n'est pas bloqué. Il suffit qu'une exécution ait réussi tous les jobs demandés ; seule la conclusion `success` compte (`skipped`, `neutral`, `cancelled`, `timed_out`, `action_required` sont des échecs) et un job déjà en échec l'emporte sur un job encore en cours. Message distinct si la CI est en cours, en échec ou absente.
  - **Pendant la construction** : pak, le job `release` a `needs: [go, contract, build, emulation]` sur l'exécution de l'étiquette ; Android, `android-release.yml` lance les tests JVM (`make android-test` : `:core:test`, `:app:testDebugUnitTest`) et le lint (`make android-lint` : `:app:lintRelease`, aussi ajouté au job `android` de la CI) avant de construire et signer l'APK ; iOS, `ci_post_clone.sh` (workflows Release et Beta) vérifie par l'API publique GitHub, sans jeton, que les jobs `ios` et `contract` ont réussi pour `CI_COMMIT`, sinon le build échoue avant l'archive. Réseau absent, limite de requêtes atteinte (60 par heure et par adresse sans jeton), réponse inattendue : échec avec la raison, jamais de passage silencieux. Le commit `Release app X.Y.Z` de `make ios-vX.Y.Z` ne change que les notes de version et part avec l'étiquette, donc sa propre CI démarre en même temps que Xcode Cloud : pour Xcode Cloud seulement, quand le commit construit n'a pas de résultat terminé et qu'aucun de ses jobs exigés n'a échoué, les résultats de son parent sur `main` (le commit vérifié par `make`) comptent, à condition que le clone local montre que c'est bien ce commit : exactement un parent (lu dans l'objet du commit), présent dans le clone (sinon refus) ; le clone de Xcode Cloud est superficiel (profondeur 1, constaté au premier vrai passage avec `ios-v1.1.0`, commit 5d586ace : refus « does not have exactly one parent »), donc le parent est d'abord récupéré depuis le remote du clone (`git fetch --deepen=1`, puis le parent seul), et les contrôles sont ensuite faits comme sur un clone complet ; si la récupération échoue (pas de réseau, pas d'accès), refus avec la marche à suivre : attendre que la CI du commit de version soit verte sur `main`, puis relancer le build (« Rebuild » dans Xcode Cloud), qui passe alors par le contrôle direct ; message exactement `Release app X.Y.Z` pour la version de l'étiquette ; diff complet (`git diff --no-renames --name-status` parent..commit, jamais l'API) fait seulement de modifications (`M`) de `ios/CHANGELOG.md` et de `ios/AppStore/*/release_notes.txt`, sans ajout, suppression ni renommage. Si un job exigé du commit a échoué, ou s'est terminé autrement qu'en `success`, le build est refusé, même si un autre tourne encore.
  - **Échappatoire** : `RL_SKIP_CI_CHECK=1` (sur la ligne de `make`, ou variable d'environnement du workflow Xcode Cloud) saute la vérification avec un avertissement ; à la main, pour une publication exceptionnelle, jamais par défaut.
  - Testé par `scripts/test-release-scripts.sh` (`make release-checks`) avec un faux `curl`, un faux `gh` et un vrai dépôt Git temporaire : vert, en échec, `skipped`/`neutral`/`cancelled`/`timed_out`/`action_required`, en cours, en file d'attente, job en échec pendant qu'un autre tourne, job absent, aucune exécution, exécution lancée par une étiquette ignorée, plusieurs exécutions, listes paginées (exécutions et jobs) et liste incomplète, commit de notes de version (accepté ; refusé avec un autre message, du code, un renommage, un ajout, une suppression, sans parent, absent du clone, parent rouge ou vert seulement sur une étiquette), réseau absent, limite de requêtes, échappatoire ; les recettes `make vX.Y.Z`, `make ios-vX.Y.Z` et `make android-vX.Y.Z` lancées contre une CI rouge (ni commit, ni étiquette, ni push) et verte ; jobs qui existent dans `ci.yml`.

**Fait quand** : un tag `ios-beta-v1.0.1-beta.1` arrive seul sur TestFlight, un tag `ios-v1.0.1` part seul à l'examen, un tag `v1.0.1` publie le pak en « Latest » et le Pak Store le voit, et une pré-version du pak n'apparaît pas dans le Pak Store.

- Fait le 21/11/2026 (branche `m17-cicd`), en attente des essais réels :
  - **Pak** : `make vX.Y.Z-beta.N` (ou `-rc.N`) pose et pousse le tag sans toucher `pak.json` (le Pak Store le lit dans le dépôt : il ne voit donc pas la pré-version), notes tirées de l'entrée `Unreleased` ; la CI publie un tag à suffixe en « Pre-release » (`--prerelease --latest=false`) et un tag simple en « Latest » (`--latest`). `scripts/pak-release-kind.sh` refuse tout autre tag, `ios-v*` compris ; le filtre de tags `v*` de la CI ne peut pas correspondre à `ios-v*` (vérifié par `scripts/test-release-scripts.sh`, `make release-checks`, lancé par la CI).
  - **App** : `ios/ci_scripts/` pour Xcode Cloud : version `X.Y.Z` tirée de `CI_TAG`, numéro de build de `CI_BUILD_NUMBER`, écrits dans `Config/CI.xcconfig` (ignoré par Git, inclus par `Config.xcconfig`, projet inchangé) ; tag mal formé refusé ; le workflow Release refuse un tag à suffixe (une bêta ne peut pas partir à l'examen), le workflow Beta un tag simple. `make ios-vX.Y.Z` pose le tag `ios-vX.Y.Z`, `make ios-vX.Y.Z-beta.N` le tag `ios-beta-vX.Y.Z-beta.N`. Le workflow Submit se lance à la main sur le tag une fois le build Release traité (Xcode Cloud exige une condition de lancement).
  - **Soumission à l'examen** : impossible depuis le script `ci_post_xcodebuild` du workflow Release (Xcode Cloud n'envoie le build à App Store Connect qu'après ce script, puis Apple le traite). Ce script prépare la version (création, « Nouveautés » EN et FR, publication automatique après approbation) par l'API App Store Connect ; un workflow **Submit** (même tag, démarré à la main seulement, une fois le build du run Release visible dans TestFlight) attend le build issu du run Release de ce tag, toujours trouvé par l'API Xcode Cloud (un numéro de build donné ne fait que choisir parmi les runs Release du tag ; tout autre build est refusé ; jamais le dernier envoi de la version, une bêta ou un envoi manuel portant le même numéro), vérifie qu'il est traité et admissible à l'App Store, le rattache et soumet la version seule (un brouillon de soumission contenant autre chose, relu juste avant la soumission, arrête tout ; personne ne doit modifier ce brouillon pendant que Submit tourne) (`submit_for_review.sh`, client `AppStoreConnect.swift` sans dépendance). Sans clé API, les étapes manuelles sont affichées. Procédure dans `ios/README.md`.
  - **Notes de version de l'app** : `ios/CHANGELOG.md` (EN + FR), recopié dans `ios/AppStore/*/release_notes.txt` (`ci_scripts/release_notes.sh sync`, vérifié par la CI).
  - **Compatibilité** : `minClientVersion` existant gardé comme version minimale du protocole client (commune à tous les clients, comparée à la constante `APIClient.clientProtocolVersion` sur iOS et `CLIENT_PROTOCOL_VERSION` sur Android, pas à la version App Store), `minPakVersion` dans l'app comparé à `pakVersion` ([03 §9](03-protocole.md#9-versions)) ; messages « Mets à jour le pak ReminisLink sur la console » / « Mets à jour l'app ReminisLink » avec lien.
  - Reste à faire par la personne : créer les workflows Xcode Cloud et la clé API, puis vérifier les quatre critères ci-dessus avec de vrais tags.

## M16 — Mode démo (décidé le 20/11/2026, avant M15)

But : essayer toute l'app sans console, pour l'examen App Store (le reviewer n'a pas de Brick), pour découvrir l'app avant d'installer le pak, et pour les captures App Store. Numéroté après M15 mais fait avant, car il compte pour la première soumission.

- Le mode démo (pak simulé `FakePak`, aujourd'hui réservé aux builds de développement) devient disponible dans l'app publiée.
- Écran sans console : bouton secondaire **« Essayer sans console »** à côté de « Ajouter une console ». Pas de fonction cachée (règle App Store 2.3.1).
- Une console fictive « Brick de démo » avec des jeux aux noms inventés et des libellés, tags et extensions de systèmes inventés (aucune marque) ; toutes les actions disponibles, sur des données en mémoire seulement.
- Bandeau « Mode démo » toujours visible, avec **Quitter**. La démo ne touche ni au trousseau, ni à iCloud Drive, ni à la liste des vraies consoles, et n'utilise pas le réseau.
- Textes en anglais et en français ; notes pour l'examen (`ios/AppStore/review_notes.txt`) : « Tap *Try Without a Console* on the first screen ». Politique de confidentialité vérifiée (rien ne sort de l'iPhone en démo).

**Fait quand** : depuis une installation neuve, la démo s'ouvre, chaque fonction marche, la sortie ramène à la liste vide sans trace, et les tests UI couvrent l'entrée et la sortie.

- Fait le 20/11/2026 (branche `m16-demo`) : bouton sur l'écran d'accueil et rangée dans la liste des consoles, bandeau avec Quitter, isolement décrit en [06 §12](06-app-ios.md#12-mode-démo-m16) (données en mémoire, fichiers et préférences dans un dossier et une suite temporaires effacés à la sortie), tests unitaires (réseau, trousseau, iCloud, aucune trace) et tests UI (entrée, une fonction, sortie). Captures App Store et vidéo pour l'examen tirées de la démo (`docs/screenshots/appstore/`).

## M15 — App universelle (décidé le 20/11/2026)

But : une seule app, un seul binaire, sur iPhone, iPad et Mac à puce Apple (même fiche App Store), **sans code propre au Mac** (choix de la personne).

- **iPad** : famille d'appareils iPhone + iPad (`TARGETED_DEVICE_FAMILY = 1,2`), mise en page adaptée aux grands écrans (navigation en colonnes, listes et formulaires à largeur raisonnable), toutes les orientations, Split View et Slide Over, plusieurs tailles de fenêtre (Stage Manager). Pas de `UIRequiresFullScreen`.
- **Mac** : l'app iPad (« Designed for iPad », Mac à puce Apple), même binaire, autorisée dans App Store Connect ; adaptations explicites par les API d'iOS seulement (`isiOSAppOnMac`, prise en charge de VisionKit) : nom par défaut « Mac », scanner par la caméra là où VisionKit n'est pas pris en charge, textes de l'écran Ajouter une console. À vérifier : découverte Bonjour et autorisation réseau local, scan du QR code avec la caméra FaceTime, import et export par Fichiers, extension de partage, trousseau iCloud. Repli si un point bloque : Mac Catalyst, toujours sans code propre au Mac.
- **iPhone Duo** : rapports d'écran différents des autres iPhone et deux écrans (petit écran extérieur fermé, grand écran intérieur ouvert, mi-ouvert). L'app doit s'adapter à chaque posture sans bandes noires ni contenu coupé, et passer d'un écran à l'autre sans perdre l'état (transferts en cours, navigation).
- **Appairage** : rien de nouveau. L'identité et la liste des consoles passent par le trousseau iCloud (M13) : une console appairée sur l'iPhone apparaît sur l'iPad et le Mac ; sinon, scan du QR code avec la caméra de l'appareil.
- **Corrections après l'essai sur Mac (21/11/2026, build TestFlight 1.1.0 (8), branche `m15-mac-fixes`)** : scanner de repli sans VisionKit (caméra par AVFoundation + Vision) et import d'une image du QR code (Photos, Fichiers), textes sans l'app Appareil photo sur Mac ; nom affiché par défaut « Mac » ; relecture du Trousseau au premier plan, toutes les 60 s et sur le marqueur du magasin clé-valeur iCloud ; « appairage pas encore arrivé » au lieu de « ne reconnaît plus cet appareil », reconnexion seule dès que l'identité arrive, sessions qui abandonnent un client signé avec d'anciens identifiants ; règle de convergence de l'identité (pointeur jamais remplacé tant que son identité n'est pas arrivée). Détails : [06 §4.2, §5, §6](06-app-ios.md#6-stockage-local).
- **Tests** : tests UI sur un simulateur iPad et sur le simulateur iPhone Duo (postures fermée, mi-ouverte, ouverte), captures App Store iPad, essai à la main sur Mac.

**Fait quand** : l'app passe ses tests sur iPhone, iPad et iPhone Duo, s'ouvre et fonctionne sur un Mac à puce Apple (appairage, bibliothèque, transfert), et les captures iPad sont prêtes.

- En partie fait le 21/11/2026 (branche `m15-universal`, à relire) : famille iPhone + iPad (app, extension, tests), toutes les orientations sur iPad, pas de `UIRequiresFullScreen` ; onglets en `TabView` `.sidebarAdaptable` (barre en haut ou barre latérale sur iPad et Mac) ; onglet Bibliothèque en un seul `NavigationSplitView` : deux colonnes en largeur normale (liste et jeu côte à côte), une seule pile en largeur compacte ; quand la largeur change, les écrans affichés, leurs feuilles ouvertes et brouillons (modification d'un code de triche non enregistrée, feuille Patch) et un téléchargement en cours de la fiche sont gardés (test UI large → compact → large par un lien de test, pas par un vrai redimensionnement) ; textes de la démo sans nom de produit réel (« la console » au lieu de NextUI) ; contenu à largeur lisible (700 pt au plus) dans les grandes fenêtres ; textes « cet iPhone » déclinés par appareil (« cet appareil » sur iPad et Mac) ; tout selon la classe de taille et la largeur, jamais le modèle. Tests (unitaires et UI) verts sur simulateur iPhone et iPad ; captures App Store iPad 13" EN/FR (`docs/screenshots/appstore/*-ipad-13/`, `ios/AppStore/make-screenshots.sh`). Reste : **iPhone Duo** (aucun simulateur utilisable ici : le type d'appareil demande un runtime plus récent qu'iOS 26.4) et **essai à la main sur Mac** (liste des points et repli Mac Catalyst dans `ios/README.md`).

## Plan après la v1.0.0 (décidé le 21/11/2026)

Ordre : M17, M15, M18, M19, M20, M21. M22 (captures d'écran) proposé le 21/11/2026, après M18 à M20 ; M23 (navigation, fichiers de la carte, renommage d'un jeu) proposé le 22/11/2026, après M22. Puis v1.5 (interface web), puis v2.0.

## M18 — Restaurer depuis la corbeille

- Nouvelle route de restauration d'une entrée de corbeille (contrat d'abord) : les fichiers reviennent à leur place d'origine, sans jamais écraser un fichier présent (conflit → `409` avec le détail, ou restauration sous un nom libre si la personne le choisit). Le `.rtc`, les states, cheats et jaquette d'un jeu supprimé reviennent avec lui.
- App : bouton « Restaurer » sur une entrée de l'onglet Corbeille, avec l'aperçu de ce qui revient ; événement `library` pour les autres appareils.

**Fait quand** : un jeu supprimé, une save remplacée et un pak supprimé reviennent depuis l'app, sans perte ni écrasement, tests et smoke compris.

Réalisé (branche `m18-trash-restore`, à relire) : route `GET`/`POST /trash/{entryId}/restore` (04) — aperçu, `onConflict` `fail` (`409`, `details.conflicts`) ou `rename` (un même nom libre ` (N)` pour tous les fichiers de l'entrée), comparaison sans casse (exFAT), restauration journalisée (état `returning` terminé au lancement suivant), jamais purgée ni supprimée pendant une restauration, dossiers de paks soumis aux vérifications d'une installation (`422`), événements `library` et `paks`. App : feuille **Restaurer** de l'onglet Corbeille (aperçu, conflit, nom libre), faux pak du mode démo. Revue Codex 1 traitée : contenu de l'entrée comparé à son journal (rien n'est jamais effacé avec elle), confinement par `os.Root` et liens refusés (journal compris), destinations du journal revalidées et contrôles de pak rejoués au démarrage, noms libres sans casse et playlist d'un jeu dossier renommée, renommage sans remplacement, frontière d'échec du journal, absence de nom libre dans le contrat, corps invalides refusés, faux pak honnête. Revue Codex 2 traitée : couple save/`.rtc` d'une restauration de copie installé en un remplacement journalisé, destinations `replace` revalidées sur le nom du jeu, `ENOENT` seul et aucune entrée supprimée tant qu'elle peut contenir un fichier, `replaceable` exact (`replaceUnavailable`), copie restaurée protégée de la limite automatique, playlist renommée par la reprise, liens refusés dans les dossiers, démo sans nom libre fictif, texte « sans te demander ». Ajout demandé : `onConflict: replace` (une save, ou sa copie de secours, remplace la sauvegarde actuelle, gardée d'abord). Smoke : `scripts/smoke-trash.sh` (jeu supprimé avec `.rtc`, states, cheats et jaquette ; save remplacée ; pak d'émulateur supprimé ; conflit puis nom libre ; coupures de courant avant et après un renommage et journal altéré, octets vérifiés ; remplacement de la sauvegarde actuelle par une copie de secours et par une save de la corbeille ; `422` pour un jeu).

## M19 — Données d'un jeu en archive, synchronisation des états

- Export : une archive zip d'un jeu (save, `.rtc`, states et leurs vignettes, cheats, jaquette, et la ROM en option) téléchargée sur l'iPhone ; import : la même archive renvoyée vers la même console ou une autre, avec copie de sécurité de tout ce qui serait remplacé.
- Synchronisation des états vers le coffre iCloud Drive (M9 ne couvre que les saves), mêmes règles de versions et de convergence entre appareils.

**Fait quand** : un jeu passe d'une console à l'autre par une archive, et les états d'un jeu sont versionnés dans iCloud Drive comme ses saves.

**Réalisé (21/11/2026, branche `m19-archives`, à relire)** : contrat d'abord (`GET /games/{gameId}/archive`, `GET /archives/{stagingId}`, `POST /archives/{stagingId}/import` avec la tâche `archive-import`, `GET /states`, raison de corbeille `import-archive`, raison de copie `before-import`), détails en [04](04-api-v1.md#archives-dun-jeu-m19). Format : zip avec `manifest.json` (version 1) qui donne le rôle de chaque fichier ; l'import place chaque fichier par rôle, sous les noms de la console cible, jamais par chemin. Pak : paquet `internal/gamearchive` (lecture qui ne fait jamais confiance à l'archive : répertoire central borné, chemins, liens, doublons sans casse, ratio de compression, tailles, manifeste exact, octets vérifiés contre le SHA-256 ; tests d'archives hostiles), export en flux confiné, import en remplacement journalisé (save copiée, remplacés à la corbeille, state verrouillé jamais remplacé), `GET /states` avec cache de hachages. App : export (feuille de partage), import (Fichiers, menu de la Bibliothèque, extension de partage) avec aperçu et choix de la cible, états versionnés dans le coffre à côté des saves (06 §8), restauration d'un état ; mode démo (faux pak : vraies archives zip, `GET /states`). Non fait : restauration de la vignette d'un état (pas de route d'écriture), import vers un sous-dossier choisi dans l'app (le pak l'accepte, l'app propose le dossier racine du système).

## M20 — Autres consoles et Knulli

- Plateformes NextUI `tg5050`, `h700` et `my355` (chemins, `PLATFORM`, binaire et pak par plateforme, `pak.json`), puis un adaptateur pour le firmware **Knulli** (autre arborescence, autre lanceur).
- **Tests sans les consoles** (la personne n'en a pas) : dispositions de carte reconstituées à partir du code source de chaque firmware, binaire de chaque plateforme exécuté dans son rootfs sous émulation (qemu-user, comme pour la Brick en M7), tests d'intégration et smoke sur chaque disposition. Limite assumée : ni écran, ni boutons, ni Wi-Fi réels ; une phase de test par des volontaires possédant ces consoles est prévue avant de les annoncer comme prises en charge.

**Fait quand** : le pak se construit et passe les tests et le smoke pour chaque plateforme et pour Knulli en émulation, et au moins un volontaire a validé chaque console.

**Avancement (21/11/2026, branche `m20-platforms`, à relire)** : plateformes `tg5050`, `h700` et `my355` dans l'adaptateur NextUI (modèle d'après `DEVICE`, batterie de chaque plateforme) et adaptateur Knulli (05 §3.2 et §3.3, sources et hypothèses écrites) ; choix de l'adaptateur par `--os` ou `REMINISLINK_OS` ; lanceur Knulli et `ReminisLink.knulli.zip` ; `libSDL2_ttf` embarquée pour h700 comme le Pak Store. Fait sans console : tests Go, `TestSmokeLayouts` sur les cinq dispositions, `make emulation` vert sur les cinq (rootfs d'origine de la Brick, images de même glibc pour les autres ; le binaire de la version demande glibc 2.17), lanceur Knulli et binaire de la version lancés en headless dans Debian 12 ; job CI `emulation`. Revue Codex 1 traitée : liens symboliques sous la racine refusés pour les saves et les states, noms comparés selon la casse du système de fichiers sur Knulli (deux jeux qui ne diffèrent que par la casse restent séparés, restauration sous le nom exact), lanceurs qui passent `--os`, zips vérifiés et binaire livré lancé par chaque lanceur dans `make emulation` (qui a trouvé que la `libSDL2_gfx` de Debian 11 demande glibc 2.29 : celle de Debian 10 est embarquée pour my355), dossier du pak Knulli caché du navigateur, jaquette partagée gardée. Le dossier racine du zip NextUI est corrigé sur `main` (v1.0.2). Revue Codex 2 traitée : verrous d'une autre casse gardés à la suppression, aucune racine fonctionnelle ni écriture de la zone de données à travers un lien, envoi d'un jeu à côté d'un nom d'une autre casse sur ext4, vignette servie depuis le descripteur vérifié, lanceur tg5040 testé, zip testé avec l'extraction du Pak Store. Écarts et décisions à prendre : (1) un binaire et un zip pour toutes les plateformes, comme le Pak Store, au lieu d'un par plateforme ; (2) `platforms` de `pak.json` reste `tg5040` (revue Codex 2 : le Pak Store lit `pak.json` sur `main` et proposerait le zip v1.0.2, sans les bibliothèques h700 et my355, sur ces consoles) ; les trois plateformes y entrent dans le commit de la première version stable qui contient M20 (v1.1.0), et le Pak Store n'a pas de marque « expérimental » côté `pak.json` ; (3) publier ou non `ReminisLink.knulli.zip` dans les releases : décidé le 23/11/2026, il est joint à chaque version et pré-version du pak à partir de `v1.1.0-beta.1`, à côté de `ReminisLink.pak.zip` (branche `pak-knulli-release-shortcuts`). Reste : l'appel aux testeurs ([appel-testeurs.md](../appel-testeurs.md)) et la validation par au moins un volontaire par console.

## M21 — App Android

- App Android native (Kotlin, Jetpack Compose) qui parle la même API (`protocol/openapi.yaml`, vecteurs de test partagés) : découverte NSD (mDNS), appairage par QR (CameraX), TLS épinglé, signature RL2-ED25519 (clé dans Android Keystore), bibliothèque, transferts, saves, states, cheats, patchs, apps, corbeille, mode démo. Les dépendances tierces seront proposées à la personne avant d'être ajoutées, et **aucune dépendance Google propriétaire** (ni Google Play Services, ni ML Kit ; QR code avec une bibliothèque libre comme ZXing) : l'app doit pouvoir être publiée sur F-Droid comme sur Google Play.
- Publication d'abord sur F-Droid et en APK sur les releases GitHub, puis sur Google Play avec un compte d'organisation (D-U-N-S de la société, pas de test fermé obligatoire), créé seulement quand l'app est prête (les comptes inutilisés sont fermés) ; mention des marques Google Play sur le site.

**Fait quand** : l'app Android passe les vecteurs de test et les mêmes parcours que l'app iOS contre le pak headless et le mode démo.

**Premier incrément (21/11/2026, branche `m21-android`)** : projet `android/` (modules `:core` en Kotlin/JVM pur et `:app` en Compose), dépendances approuvées le 21/11/2026, détails dans [10](10-app-android.md). Fait : tous les vecteurs partagés, TLS épinglé, signature, appairage v2 (QR avec CameraX et ZXing, lien collé, lien ouvert depuis une autre app), découverte NSD avec repli après 3 s, tableau de bord, bibliothèque en lecture seule, fiche d'un jeu ; clé Ed25519 dans l'Android Keystore (API 33+) ou graine Tink chiffrée par le Keystore ; tests unitaires (CI), tests instrumentés sur émulateur API 35 et test de bout en bout contre le pak headless (`android/scripts/pak-e2e.sh`). Incréments suivants proposés : (2) transferts (envois reprenables, téléchargements, ouverture depuis le partage Android) ; (3) sauvegardes et états ; (4) codes de triche et patchs ; (5) apps (paks), carte SD et corbeille ; (6) événements SSE et désappairage ; (7) mode démo ; (8) publication F-Droid et APK des releases. L'équivalent de la synchronisation iCloud (M9) reste à décider (dossier choisi par l'utilisateur via le Storage Access Framework, sans service Google).

## M22 — Captures d'écran de la console (proposé par la personne le 21/11/2026)

- Lister, télécharger et supprimer depuis l'app les captures d'écran prises sur la console (dossier propre à chaque OS, donné par l'adaptateur : NextUI, puis les plateformes de M20 et Knulli ; emplacement exact à relever dans le code source de chaque firmware). Contrat d'abord : routes de liste (date, taille, jeu associé si le nom de fichier le permet, vignette), de téléchargement et de suppression par identifiant opaque ; la suppression passe par la corbeille (règle 1).
- App : un écran « Captures d'écran » (grille de vignettes, partage et enregistrement dans Photos ou Fichiers, sélection multiple pour télécharger ou supprimer), mode démo compris ; plus tard sur Android.

**Fait quand** : les captures prises sur la console s'affichent dans l'app, se téléchargent et se suppriment (vers la corbeille), tests et smoke compris. Ordre : après M18 à M20, qui touchent les mêmes couches (contrat, serveur, app).

**Réalisé (22/11/2026, branche `m22-screenshots`, à relire)** : emplacements relevés dans le code source (05 §3.1 à §3.3) : NextUI `Screenshots/<nom d'affichage>.<AAAA-MM-JJ-HH-MM-SS>.png` à la racine de la carte, identique sur les quatre plateformes (`SDCARD_PATH` constant) ; Knulli `/userdata/screenshots/` (RetroArch `<ROM>-<AAMMJJ>-<HHMMSS>.png`, raccourci `knulli-screenshot_…`). Contrat d'abord : `GET /screenshots`, `GET /screenshots/{screenshotId}`, `GET /screenshots/{screenshotId}/thumbnail` (JPEG 320 × 320 fait avec la bibliothèque standard), `POST /screenshots/trash` (une entrée de corbeille `delete-screenshot`, tout ou rien, restaurée par M18 sans règle propre), événement `screenshots` (04). Pak : `Roots.Screenshots` et lecture des noms par adaptateur, paquet `internal/screenshots` (dossier par `os.Root`, aucun lien, nom exact, images hostiles refusées avant décodage, aperçus en mémoire), route d'écriture sous le verrou, bandeau sur l'écran du pak. App : écran **Captures d'écran** (06 §11 bis : grille selon la largeur, capture entière, partage, Photos, Fichiers, sélection multiple, corbeille), faux pak du mode démo avec des images dessinées. Tests : Go (identifiants hostiles, liens, casse sur ext4, PNG forgé), `scripts/smoke-screenshots.sh` (pak headless et cinq dispositions), `make emulation LAYOUTS="tg5040 knulli"` vert, `swift test`, tests de l'app et UI (mode démo) sur iPhone et iPad. Écart : `gameName` seulement informatif (le nom ne dit pas le système, aucun `gameId`). Android : TODO dans 10.

**Idée notée le 22/11/2026 (contrat, iOS et Android)** : `POST /screenshots/trash` pourrait accepter, pour chaque id, la version attendue (taille et date de modification) que le pak vérifierait au moment du déplacement, pour couvrir le cas improbable d'une capture remplacée sous le même nom entre l'affichage et la confirmation (aujourd'hui l'élément part dans la corbeille, restaurable).

## M23 — Navigation et fichiers de la carte SD (proposé par la personne le 22/11/2026)

- **Barre d'onglets** : retirer Transferts et Corbeille de la barre d'onglets (accessibles depuis l'onglet Console, par exemple), et y mettre **Apps** et **Fichiers** (navigateur de la carte SD). Mise en page universelle (barre du haut ou barre latérale sur iPad et Mac).
- **Fichiers** : le navigateur de la carte SD (aujourd'hui en lecture seule) permet d'**ajouter** un fichier dans un dossier et de **supprimer** un fichier ou un dossier (vers la corbeille, règle 1). Contrat d'abord ; dossiers protégés (`.userdata`, dossier de ReminisLink, système) refusés ; chemins par identifiants opaques uniquement (règle 2).
- **Renommer un jeu** : renomme la ROM et tous ses fichiers liés (save, `.rtc`, états et vignettes, codes de triche, jaquette, playlist d'un jeu dossier) en une opération journalisée, sans jamais écraser un fichier existant ; règles de nommage de chaque OS (NextUI, Knulli).
- **Fiche d'un jeu** : le nombre de codes de triche au lieu de « Oui ».
- Android suit.

**Fait quand** : la nouvelle navigation, l'ajout et la suppression de fichiers et le renommage d'un jeu fonctionnent depuis l'app, tests et smoke compris. Ordre : après M22.

**Réalisé (22/11/2026, branche `m23-navigation-files`, à relire)** : contrat d'abord ([04](04-api-v1.md#écritures-du-navigateur-m23)) : cible d'envoi `sd-file` (jamais de remplacement : `fail` → `409` avec `details.freeName`, ou `rename`), `POST /sd/folders`, `POST /sd/trash` (une entrée de corbeille de raison `delete-file`, tout ou rien, restaurée par M18), `writable` / `deletable` / `protection` dans `GET /sd/browse` (`SdProtection` : `system`, `data`, `root`, `link`), `POST /games/{gameId}/rename`, `cheatCount` dans `GET /games/{gameId}`. Pak : emplacements protégés par OS ([05 §3.4](05-console-pak.md#34-emplacements-protégés-du-navigateur-de-la-carte-m23) : NextUI, toute entrée cachée de la racine en lecture seule ; Knulli, `/userdata/system` ; dossiers principaux jamais à la corbeille ; le pak en cours, le lanceur Knulli et les données de ReminisLink intouchables ; dossiers contenant un lien refusés), écritures sous le verrou d'écriture et le verrou d'installation, événement `library` ; renommage d'un jeu selon les règles de chaque adaptateur (ROM, jeu dossier et sa playlist, save, `.rtc`, states et fichiers annexes, `.cht` et `.cht.bak`, jaquette, verrous, copies et historique de reprise), en un groupe journalisé (`renames/<id>/journal.json`, défait au lancement après une coupure), renommages sans remplacement, changement de casse en deux temps, conflits (`409 details.conflicts`) et disques partagés refusés. App : onglets **Console**, **Bibliothèque**, **Apps**, **Fichiers** (Transferts et Corbeille depuis l'onglet Console, avec compteurs), écritures de l'onglet Fichiers (ajout par la file des transferts, nouveau dossier, corbeille avec confirmation qui nomme les éléments, sélection multiple), **Renommer…** sur la fiche, nombre de codes de triche ; faux pak du mode démo. Tests : Go (identifiants et noms hostiles, liens, emplacements protégés NextUI et Knulli, coupure entre deux renommages, changement de casse, conflits, disques partagés), `scripts/smoke-files.sh` (coupure de courant rejouée sur le vrai binaire, cinq dispositions dans `TestSmokeLayouts`), `make emulation LAYOUTS="tg5040 knulli"` vert, `swift test`, tests unitaires et UI de l'app (mode démo) sur iPhone et iPad. Non fait : ajout depuis l'extension de partage vers un dossier de la carte (l'ajout passe par l'onglet Fichiers) ; renommage d'un fichier quelconque du navigateur (seul un jeu se renomme).

**Raccourcis du pak Shortcuts (note du 22/11/2026).** Le pak tiers Shortcuts (`Helaas/nextui-shortcuts-pak`, `src/device.c` `create_tool_shortcut`) crée pour chaque raccourci d'outil un dossier `Roms/<préfixe><nom> (SHORTCUT)/` (un `.m3u` vers le pak de `Tools/`, `target`, `.shortcut`), lancé par l'émulateur passerelle `Emus/<plateforme>/SHORTCUT.pak` : sans traitement, chacun apparaissait comme un système de la Bibliothèque avec un « jeu ». Ce sont des lanceurs, pas des jeux : l'adaptateur NextUI ignore désormais les dossiers de tag `SHORTCUT` (sûr : le tag nomme l'émulateur passerelle). Raccourcis de jeux (décision du 23/11/2026, branche `pak-knulli-release-shortcuts`) : le `.m3u` d'un raccourci de jeu (`create_rom_shortcut`, `Roms/<nom> (<TAG>)/<nom> (<TAG>).m3u` à côté du fichier caché `.shortcut`) n'est plus listé ni compté comme un jeu, et un dossier qui ne contient que lui n'est plus un système ; format relevé dans le code source du pak Shortcuts ([05](05-console-pak.md) §3.4).

## v1.2.0 (app iOS) — Revoir l'interface sur Mac (demandé par la personne le 22/11/2026)

L'app iPad tourne sur Mac (Apple silicon) depuis la 1.1.0, mais son interface n'a pas été pensée pour le Mac. À revoir écran par écran : barre latérale et barre d'onglets, largeur des listes, barre d'outils, raccourcis clavier, menus, glisser-déposer de fichiers, survol au pointeur.

Premier point, signalé sur le build TestFlight `ios-beta-v1.1.0-beta.3` : changer le style du menu (barre d'onglets ou barre latérale) affiche mal les onglets de la console. La barre d'onglets se place en haut à gauche par-dessus le bouton « Consoles », un panneau vide reste après la fermeture de la barre latérale, et pendant « Connexion à la console… » la barre latérale montre les anciens onglets (Transferts, Corbeille) au lieu de Apps et Fichiers. La correction (branche `fix-ios-tab-style`) part avec cette version, pas avec la 1.1.0.

## v1.5 — Interface web

L'interface web (voir « Interface web servie par le pak » ci-dessous) : web app statique sur GitHub Pages, WebCrypto, découverte façon Synology ou par `reminislink-N.local`, appairage par PIN (PAKE).

## v2.0

Les fonctions de l'app sur l'écran de la console ; le pak en arrière-plan pendant le jeu (revue de la spec sécurité) ; le widget iOS ; l'envoi visible dans la Dynamic Island ; la mise à jour du pak depuis l'app (releases GitHub). Détails ci-dessous.

## Après la v1 (idées, non engagées)

- Plateformes `tg5050`, `h700`, `my355`, puis adaptateur Knulli (devenu M20).
- Android (iPad et Mac : M15).
- TLS en option.
- Gestionnaire de fichiers générique, lancement d'un jeu, Pak Store, mises à jour.
- ~~Export / import des données d'un jeu en archive ; synchronisation des states~~ : M19.
- Restauration depuis la corbeille.
- **Mise à jour du pak ReminisLink depuis l'app** (proposée par la personne le 19/11/2026, après la v1.0.0) : l'app récupère la dernière version publiée sur GitHub et l'installe sur la console. S'appuie sur l'installation de paks de M12 ; comme ReminisLink ne peut pas se remplacer pendant qu'il tourne, la nouvelle version est déposée à part et appliquée au lancement suivant (par `launch.sh`), l'ancienne passant à la corbeille. Vérifier l'archive avant de l'appliquer (somme publiée avec la version, ou signature).
- **Interface web servie par le pak** (proposée par la personne le 19/11/2026) : les actions de l'app depuis un navigateur (ordinateur, Android) à `http://<console>:47821/`, en réutilisant l'API ; signature dans le navigateur (WebCrypto), appairage par PIN (la caméra exige HTTPS), à réconcilier avec 07 §4 (« aucun fichier statique »). Note du 19/11/2026 : appairage du navigateur par PIN, clé WebCrypto non exportable propre au navigateur ; un navigateur ne peut ni épingler un certificat auto-signé ni utiliser une clé WebCrypto comme certificat client TLS (mTLS) : le PIN devra passer par un PAKE et l'authentification du pak se faire au niveau de l'application, à étudier avec le retour du PIN.
  - **Orientation de la personne (19/11/2026)** : web app JS statique hébergée sur GitHub Pages (HTTPS, donc WebCrypto et caméra pour le QR code), qui parle **directement** à la console sur le réseau local, sans serveur ni relais à nous. Pas de Bonjour dans un navigateur : adresse et clés lues dans le QR code. Chrome et Edge autorisent une page HTTPS publique à appeler une adresse locale en HTTP via la permission « Local Network Access » (`fetch` avec `targetAddressSpace`) ; Safari et Firefox non. Repli (autres navigateurs, ou réseau sans Internet) : la même web app servie par le pak en HTTPS auto-signé à `https://<ip>`, avec l'alerte du navigateur et l'empreinte affichée sur l'écran de la console pour vérification. Dans les deux cas, signatures (clé WebCrypto non exportable) et chiffrement au niveau de l'application avec la clé publique de la console lue dans le QR code, pour ne pas dépendre du transport.
  - **Découverte façon find.synology.com** (idée de la personne, 20/11/2026) : la console annonce à un petit annuaire en ligne son identifiant, son adresse locale et son port ; l'annuaire les rend à une page web ouverte depuis la **même adresse IP publique** (même box). La page propose les consoles trouvées puis ouvre leur interface. Demande un petit service (GitHub Pages ne fait que du statique : par exemple une fonction serverless), une option désactivable sur la console et une mention dans la politique de confidentialité (adresse locale et identifiant envoyés à l'annuaire). Sans Internet : saisie de l'adresse IP comme prévu.
  - **Découverte sans annuaire** (idée de la personne, 20/11/2026) : le pak s'annonce aussi sous un nom d'hôte mDNS numéroté, `reminislink-1.local`, `reminislink-2.local`… (premier numéro libre) ; la web app, avec l'autorisation réseau local du navigateur, essaie ces noms dans l'ordre (`GET /hello`, non signé) et liste les consoles qui répondent. Le système résout les noms `.local` (macOS, iOS, Windows 10+, Android récent, Linux avec Avahi). À prévoir : en-têtes CORS sur `/hello` pour l'origine de la web app (et la pré-requête « réseau privé ») ; limite du nombre de noms essayés et délai court par nom ; complète l'annuaire façon Synology, sans service en ligne.
  - Pistes écartées pour l'instant : certificat par console façon Plex (`*.<id>.reminis.link`, service en ligne, limites Let's Encrypt) et relais par notre serveur (tout le trafic passe par Internet).
- **Envoi visible dans la Dynamic Island** (proposé par la personne le 20/11/2026, après la v1.0.0) : une Live Activity (ActivityKit, extension widget) montre l'avancement d'un envoi vers la console, sur l'écran verrouillé et dans la Dynamic Island, à la manière d'AirDrop. À étudier : les envois actuels s'arrêtent quand l'app est suspendue (reprise au retour) ; une Live Activity ne se met à jour que tant que l'app tourne, sauf mises à jour par notifications push, que ReminisLink n'a pas (pas de serveur).
- **ReminisLink en arrière-plan sur la console** (proposé par la personne le 20/11/2026, v1.5 ou v2.0) : le pak resterait disponible pendant qu'un jeu tourne. Remet en cause une règle fondatrice (01, 05 : « le pak ne tourne que quand aucun jeu ne tourne ») : démarrage par NextUI au lancement de la console, coût en batterie, mémoire et CPU pendant le jeu, et surtout écritures concurrentes avec l'émulateur (save ou state en cours d'écriture). Piste : pendant un jeu, lecture seule (bibliothèque, état, jeu en cours) et écritures refusées ou mises en attente jusqu'à la sortie du jeu. Demande une revue de la spec sécurité (07 : service exposé en permanence sur le réseau).
- **Widget iOS** (proposé par la personne le 20/11/2026, v1.5 ou v2.0) : état de la console (allumée ou non, adresse, batterie, jeu ou app en cours). Un widget ne peut ni découvrir la console par Bonjour ni la joindre souvent (budget de rafraîchissement de WidgetKit) : il afficherait le dernier état connu, écrit par l'app dans le groupe d'apps, avec son heure. L'état en direct (jeu en cours) suppose le pak en arrière-plan (idée précédente).
- **Les fonctions de l'app aussi sur la console** (proposé par la personne le 20/11/2026, v2) : gérer depuis l'écran de la console presque tout ce que fait l'app (jeux et corbeille, saves et copies, states et verrous, cheats, patchs avec les fichiers déjà sur la carte, apps), pour qu'un utilisateur n'ait pas à installer plusieurs outils. Faisable sans nouvelle logique : les paquets Go (`library`, `saves`, `states`, `cheats`, `patch`, `paks`, `store`) font déjà le travail, il manque les écrans gabagool. Saisie complète gardée (clavier à l'écran) : corriger une petite erreur dans un cheat ou un nom, ou tout gérer sans téléphone ni ordinateur sous la main (choix de la personne). Points d'attention : écrans lisibles sur 1024 × 768, mêmes garanties (copie avant toute écriture, verrou d'écriture partagé avec les requêtes de l'app, événements pour que l'app se mette à jour). Doublon assumé avec Cheat Manager : lui reste utile pour qui ne veut que les cheats, ReminisLink vise la gestion complète de la console.
