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

- Côté Go : `/uploads`, `/downloads`, `/jobs`, `DELETE /games/{id}`, corbeille (`/trash`), purge.
- Côté iOS : écran Ajouter, Transferts, reprise, extension de partage, suppression.

**Fait quand** : ajout d'une ROM depuis Fichiers et depuis une URL, reprise après coupure, suppression avec corbeille, testés en automatique (headless) et sur console.

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

## M11 — Ergonomie de l'app (proposé, non lancé)

Suggestions de la personne du 19/11/2026, après essai de M10. À préciser et valider avant de lancer.

- **Image du jeu** : toucher l'image pour la changer (au lieu de la section Image).
- **Télécharger chaque fichier d'un jeu** vers l'iPhone (ROM, cheats, states…) ; probablement une route de téléchargement par fichier, identifiants opaques résolus par le pak (contrat d'abord).
- **Bibliothèque à plat** : tous les jeux sans regroupement par console, avec recherche et filtres (console, etc.) ; peut demander une liste de tous les jeux en une requête.
- **Barre d'onglets** quand une console est ouverte : Console, Bibliothèque, Transferts, Corbeille.
- **Plus d'envoi depuis la vue Console** (l'ajout reste dans Bibliothèque et Transferts).
- **Retour à la liste des consoles quand le pak se ferme** : quand on quitte ReminisLink sur la console (`503 shutting_down`, annonce Bonjour retirée), l'app revient à la liste des consoles avec un message bref (« La console a quitté ReminisLink »), au lieu de rester sur un écran en erreur ; les envois en cours restent en pause.

## Après la v1 (idées, non engagées)

- Plateformes `tg5050`, `h700`, `my355`, puis adaptateur Knulli.
- iPad, Android.
- TLS en option.
- Gestionnaire de fichiers générique, lancement d'un jeu, Pak Store, mises à jour.
- Export / import des données d'un jeu en archive ; synchronisation des states (M9 ne couvre que les saves).
- Restauration depuis la corbeille.
- **Mise à jour du pak ReminisLink depuis l'app** (proposée par la personne le 19/11/2026, après la v1.0.0) : l'app récupère la dernière version publiée sur GitHub et l'installe sur la console. S'appuie sur l'installation de paks de M12 ; comme ReminisLink ne peut pas se remplacer pendant qu'il tourne, la nouvelle version est déposée à part et appliquée au lancement suivant (par `launch.sh`), l'ancienne passant à la corbeille. Vérifier l'archive avant de l'appliquer (somme publiée avec la version, ou signature).
- **Interface web servie par le pak** (proposée par la personne le 19/11/2026) : les actions de l'app depuis un navigateur (ordinateur, Android) à `http://<console>:47821/`, en réutilisant l'API ; signature dans le navigateur (WebCrypto), appairage par PIN (la caméra exige HTTPS), à réconcilier avec 07 §4 (« aucun fichier statique »).

## M12 — Gestion des apps (paks) (décidé, après M13)

Demande et décisions de la personne du 19/11/2026.

- **Périmètre** : paks d'outils (`Tools/<plateforme>/*.pak`) et d'émulateurs (`Emus/<plateforme>/*.pak`), chacun à sa place (détectée à l'installation, modifiable dans l'app).
- **Qui** : tout appareil appairé qui a la main en écriture (M10). Nouvelle menace acceptée dans 07 §3 : un appareil appairé peut installer du code exécuté sur la console (tous les appareils appairés sont de confiance).
- **Sources** : URL (téléchargement par le pak, comme M4), fichier de l'iPhone (envoi reprenable, Fichiers ou extension de partage), fichier déjà sur la carte SD, n'importe où, choisi dans un sélecteur de fichiers de l'app (liste fournie par le pak). Formats : zip, ou dossier `.pak`.
- **Contenu non vérifié** : comme pour une ROM corrompue, ReminisLink ne juge pas la qualité d'un pak (le Pak Store ne le fait pas non plus). Seules restent les protections de la carte : aucun chemin qui sort du dossier d'installation (zip-slip), aucun lien, une taille maximale, un seul dossier `<Nom>.pak` reconnu par archive.
- **Remplacement et suppression** : l'ancien pak passe à la corbeille (récupérable 30 jours) avant un remplacement ; suppression vers la corbeille avec confirmation. ReminisLink ne peut ni se supprimer ni se remplacer pendant qu'il tourne.
- **Navigateur de la carte SD** (demande de la personne du 19/11/2026), ouvert en touchant la section Stockage de l'app et réutilisé comme sélecteur de pak : **consultation seule en M12** (dossiers, noms, tailles, dates, téléchargement d'un fichier vers l'iPhone). Identifiants opaques résolus par le pak, rien hors de la carte, aucun lien suivi ; les données de ReminisLink (clés des appareils, journaux internes) sont cachées. Cible plus tard : gestionnaire complet (suppression vers la corbeille, renommer, déplacer, créer un dossier, envoyer un fichier), avec la main en écriture et des dossiers protégés.
- **Pas de remplacement du Pak Store** : ni catalogue ni mises à jour automatiques (pour l'instant).

## M13 — Appairage inspiré de HomeKit (décidé, après M11, avant M12)

Décisions de la personne du 19/11/2026 : s'inspirer du protocole des accessoires HomeKit sans le suivre à la lettre. Demande un nouvel appairage de l'iPhone (changement de contrat et des vecteurs de test partagés).

- **Plusieurs appareils, deux mécanismes** (décision de la personne du 19/11/2026, qui remplace « un seul appareil appairé ») :
  - **Trousseau iCloud** : la clé privée de l'app est un élément synchronisé du trousseau ; l'iPhone, l'iPad et le Mac d'un même compte Apple partagent la même identité et n'appairent qu'une fois (pas de Secure Enclave, dont les clés ne se synchronisent pas).
  - **Plusieurs appairages** : chaque autre appareil (autre compte Apple, plus tard Android, interface web) a sa propre clé, appairée par le QR code de la console (Réglages > Appairer un nouvel appareil, comme en M8). La console garde la liste des appareils avec Déconnecter et Oublier (M10). Pas de rôles.
- **Verrou par action à la place du bail de M10** (décision du 19/11/2026) : le pak exécute les écritures une à une (suppression, remplacement, restauration, patch, cheats, image, étape finale d'un envoi) ; les morceaux d'un envoi vont en zone d'attente sans verrou. Si une écriture est en cours, la suivante attend qu'elle finisse (quelques secondes) au lieu d'être refusée ; « occupé » seulement si l'attente dépasse une limite. Retirés : bail, renouvellement, `PUT/GET/DELETE /writer`, `423 read_only`, bandeau « Lecture seule » et « Prendre la main ». Gardés : Déconnecter, Oublier, `401 disconnected`. Le risque résiduel « aperçu puis suppression » de 07 §3 revient tel qu'accepté le 19/11/2026.
- La compatibilité iPad et Mac de l'app (écrans, cible Mac) est un jalon à part.
- **Clés asymétriques Ed25519** à la place de la clé HMAC partagée : chaque requête est signée par la clé privée de l'iPhone (Keychain) ; le pak ne garde sur la carte que la clé publique de l'iPhone, inutilisable pour signer (corrige la copie de `clients.json` depuis une carte SD retirée de la console). Le pak a sa propre paire de clés, dont l'iPhone garde la clé publique.
- **Pas de PIN en M13** (décision de la personne du 19/11/2026) : appairage par QR code seulement ; le PIN (secours si l'appareil photo est abîmé) reviendra plus tard, protégé par un PAKE ou par un échange X25519 confirmé par le PIN (choix à faire alors). L'écran d'appairage de la console n'affiche plus de PIN.
- **Chiffrement par TLS épinglé** : le pak génère un certificat (ECDSA P-256) ; son empreinte est transmise de façon authentifiée à l'appairage (dans le QR code) et épinglée par l'app. TLS 1.3 de la bibliothèque standard Go (ChaCha20-Poly1305 choisi automatiquement si le processeur n'a pas d'instructions AES) ; débit à mesurer sur la console.
- 07 mis à jour : menace « copie de la carte SD » ajoutée, risques « PIN cassable hors ligne » et « contenu en clair » retirés.

