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
- **Pré-versions** : un tag avec suffixe (`ios-v1.1.0-beta.1`, `v1.1.0-beta.1`) est une pré-version. App : envoyée sur TestFlight seulement. Pak : release GitHub marquée « Pre-release » (ignorée par le Pak Store), zip joint pour les testeurs. Le numéro de version de l'app reste `X.Y.Z` (exigé par l'App Store), le suffixe ne sert qu'au tag ; le numéro de build augmente à chaque envoi.
- **Versions normales** : tag sans suffixe. Pak : release « Latest » (comme aujourd'hui). App : build envoyé à App Store Connect puis soumis automatiquement à l'examen, publication automatique après approbation (réglage « Publier automatiquement » de la version). L'examen d'Apple reste obligatoire.
- **Livraison iOS par Xcode Cloud** (choix de la personne) : workflows configurés dans App Store Connect, déclenchés par les tags `ios-v*` ; signature gérée par Apple, aucune clé dans GitHub. La soumission automatique à l'examen demande l'API App Store Connect depuis un script Xcode Cloud (`ci_scripts/`), avec une clé API stockée en variable secrète de Xcode Cloud, créée par la personne. Les tests restent sur GitHub Actions.
- **Compatibilité** : `/hello` annonce aussi la version minimale de l'app qu'il accepte et l'app la version minimale du pak qu'elle sait piloter ; en cas d'écart, message clair (« mets à jour le pak » / « mets à jour l'app ») au lieu d'une erreur.
- **Notes de version** : un journal par produit (`pak.json` pour le pak, un fichier pour l'app, repris dans les « Nouveautés » de l'App Store).
- **Documentation** de la procédure de publication (README, ios/README.md).

**Fait quand** : un tag `ios-v1.0.1-beta.1` arrive seul sur TestFlight, un tag `ios-v1.0.1` part seul à l'examen, un tag `v1.0.1` publie le pak en « Latest » et le Pak Store le voit, et une pré-version du pak n'apparaît pas dans le Pak Store.

- Fait le 21/11/2026 (branche `m17-cicd`), en attente des essais réels :
  - **Pak** : `make vX.Y.Z-beta.N` (ou `-rc.N`) pose et pousse le tag sans toucher `pak.json` (le Pak Store le lit dans le dépôt : il ne voit donc pas la pré-version), notes tirées de l'entrée `Unreleased` ; la CI publie un tag à suffixe en « Pre-release » (`--prerelease --latest=false`) et un tag simple en « Latest » (`--latest`). `scripts/pak-release-kind.sh` refuse tout autre tag, `ios-v*` compris ; le filtre de tags `v*` de la CI ne peut pas correspondre à `ios-v*` (vérifié par `scripts/test-release-scripts.sh`, `make release-checks`, lancé par la CI).
  - **App** : `ios/ci_scripts/` pour Xcode Cloud : version `X.Y.Z` tirée de `CI_TAG`, numéro de build de `CI_BUILD_NUMBER`, écrits dans `Config/CI.xcconfig` (ignoré par Git, inclus par `Config.xcconfig`, projet inchangé) ; tag mal formé refusé ; le workflow Release refuse un tag à suffixe (une bêta ne peut pas partir à l'examen), le workflow Beta un tag simple. `make ios-vX.Y.Z[-beta.N]` pose le tag.
  - **Soumission à l'examen** : impossible depuis le script `ci_post_xcodebuild` du workflow Release (Xcode Cloud n'envoie le build à App Store Connect qu'après ce script, puis Apple le traite). Ce script prépare la version (création, « Nouveautés » EN et FR, publication automatique après approbation) par l'API App Store Connect ; un workflow **Submit** (même tag, démarré à la main ou automatiquement) attend le build issu du run Release de ce tag (API Xcode Cloud, ou numéro de build donné ; jamais le dernier envoi de la version, une bêta portant le même numéro), vérifie qu'il est traité et admissible à l'App Store, le rattache et soumet la version seule (un brouillon de soumission contenant autre chose arrête tout) (`submit_for_review.sh`, client `AppStoreConnect.swift` sans dépendance). Sans clé API, les étapes manuelles sont affichées. Procédure dans `ios/README.md`.
  - **Notes de version de l'app** : `ios/CHANGELOG.md` (EN + FR), recopié dans `ios/AppStore/*/release_notes.txt` (`ci_scripts/release_notes.sh sync`, vérifié par la CI).
  - **Compatibilité** : `minClientVersion` existant gardé comme version minimale du protocole client (commune à tous les clients, comparée à une constante `clientProtocolVersion`, pas à la version App Store), `minPakVersion` dans l'app comparé à `pakVersion` ([03 §9](03-protocole.md#9-versions)) ; messages « Mets à jour le pak ReminisLink sur la console » / « Mets à jour l'app ReminisLink » avec lien.
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
- **Mac** : l'app iPad telle quelle (« Designed for iPad », Mac à puce Apple), autorisée dans App Store Connect. À vérifier : découverte Bonjour et autorisation réseau local, scan du QR code avec la caméra FaceTime, import et export par Fichiers, extension de partage, trousseau iCloud. Repli si un point bloque : Mac Catalyst, toujours sans code propre au Mac.
- **iPhone Duo** : rapports d'écran différents des autres iPhone et deux écrans (petit écran extérieur fermé, grand écran intérieur ouvert, mi-ouvert). L'app doit s'adapter à chaque posture sans bandes noires ni contenu coupé, et passer d'un écran à l'autre sans perdre l'état (transferts en cours, navigation).
- **Appairage** : rien de nouveau. L'identité et la liste des consoles passent par le trousseau iCloud (M13) : une console appairée sur l'iPhone apparaît sur l'iPad et le Mac ; sinon, scan du QR code avec la caméra de l'appareil.
- **Tests** : tests UI sur un simulateur iPad et sur le simulateur iPhone Duo (postures fermée, mi-ouverte, ouverte), captures App Store iPad, essai à la main sur Mac.

**Fait quand** : l'app passe ses tests sur iPhone, iPad et iPhone Duo, s'ouvre et fonctionne sur un Mac à puce Apple (appairage, bibliothèque, transfert), et les captures iPad sont prêtes.

## Plan après la v1.0.0 (décidé le 21/11/2026)

Ordre : M17, M15, M18, M19, M20, M21. Puis v1.5 (interface web), puis v2.0.

## M18 — Restaurer depuis la corbeille

- Nouvelle route de restauration d'une entrée de corbeille (contrat d'abord) : les fichiers reviennent à leur place d'origine, sans jamais écraser un fichier présent (conflit → `409` avec le détail, ou restauration sous un nom libre si la personne le choisit). Le `.rtc`, les states, cheats et jaquette d'un jeu supprimé reviennent avec lui.
- App : bouton « Restaurer » sur une entrée de l'onglet Corbeille, avec l'aperçu de ce qui revient ; événement `library` pour les autres appareils.

**Fait quand** : un jeu supprimé, une save remplacée et un pak supprimé reviennent depuis l'app, sans perte ni écrasement, tests et smoke compris.

## M19 — Données d'un jeu en archive, synchronisation des états

- Export : une archive zip d'un jeu (save, `.rtc`, states et leurs vignettes, cheats, jaquette, et la ROM en option) téléchargée sur l'iPhone ; import : la même archive renvoyée vers la même console ou une autre, avec copie de sécurité de tout ce qui serait remplacé.
- Synchronisation des états vers le coffre iCloud Drive (M9 ne couvre que les saves), mêmes règles de versions et de convergence entre appareils.

**Fait quand** : un jeu passe d'une console à l'autre par une archive, et les états d'un jeu sont versionnés dans iCloud Drive comme ses saves.

## M20 — Autres consoles et Knulli

- Plateformes NextUI `tg5050`, `h700` et `my355` (chemins, `PLATFORM`, binaire et pak par plateforme, `pak.json`), puis un adaptateur pour le firmware **Knulli** (autre arborescence, autre lanceur).
- **Tests sans les consoles** (la personne n'en a pas) : dispositions de carte reconstituées à partir du code source de chaque firmware, binaire de chaque plateforme exécuté dans son rootfs sous émulation (qemu-user, comme pour la Brick en M7), tests d'intégration et smoke sur chaque disposition. Limite assumée : ni écran, ni boutons, ni Wi-Fi réels ; une phase de test par des volontaires possédant ces consoles est prévue avant de les annoncer comme prises en charge.

**Fait quand** : le pak se construit et passe les tests et le smoke pour chaque plateforme et pour Knulli en émulation, et au moins un volontaire a validé chaque console.

## M21 — App Android

- App Android native (Kotlin, Jetpack Compose) qui parle la même API (`protocol/openapi.yaml`, vecteurs de test partagés) : découverte NSD (mDNS), appairage par QR (CameraX), TLS épinglé, signature RL2-ED25519 (clé dans Android Keystore), bibliothèque, transferts, saves, states, cheats, patchs, apps, corbeille, mode démo. Les dépendances tierces seront proposées à la personne avant d'être ajoutées, et **aucune dépendance Google propriétaire** (ni Google Play Services, ni ML Kit ; QR code avec une bibliothèque libre comme ZXing) : l'app doit pouvoir être publiée sur F-Droid comme sur Google Play.
- Publication d'abord sur F-Droid et en APK sur les releases GitHub, puis sur Google Play avec un compte d'organisation (D-U-N-S de la société, pas de test fermé obligatoire), créé seulement quand l'app est prête (les comptes inutilisés sont fermés) ; mention des marques Google Play sur le site.

**Fait quand** : l'app Android passe les vecteurs de test et les mêmes parcours que l'app iOS contre le pak headless et le mode démo.

## v1.5 — Interface web

L'interface web (voir « Interface web servie par le pak » ci-dessous) : web app statique sur GitHub Pages, WebCrypto, découverte façon Synology ou par `reminislink-N.local`, appairage par PIN (PAKE).

## v2.0

Les fonctions de l'app sur l'écran de la console ; le pak en arrière-plan pendant le jeu (revue de la spec sécurité) ; le widget iOS ; l'envoi visible dans la Dynamic Island ; la mise à jour du pak depuis l'app (releases GitHub). Détails ci-dessous.

## Après la v1 (idées, non engagées)

- Plateformes `tg5050`, `h700`, `my355`, puis adaptateur Knulli.
- Android (iPad et Mac : M15).
- TLS en option.
- Gestionnaire de fichiers générique, lancement d'un jeu, Pak Store, mises à jour.
- Export / import des données d'un jeu en archive ; synchronisation des states (M9 ne couvre que les saves).
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
