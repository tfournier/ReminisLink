# M4 — Transferts et suppression (Go + Swift)

## À lire

`docs/specs/03-protocole.md` §7 et §8, `docs/specs/04-api-v1.md` (sections Transferts et `DELETE /games/{gameId}`), `docs/specs/05-console-pak.md` §4, §5.6, `docs/specs/06-app-ios.md` §4.5 (Supprimer), §4.6, §4.7, §4.8, §7, `docs/specs/07-securite.md`.

## Côté Go

1. `internal/store` : zones `uploads/`, `staging/`, `trash/` ; corbeille horodatée qui conserve le chemin d'origine ; purge au lancement (corbeille > 30 jours, envois expirés > 24 h, staging > 24 h).
2. `internal/transfer` :
   - envois reprenables (`POST /uploads`, `GET`/`PUT`/`DELETE /uploads/{id}`, `POST /uploads/{id}/complete`) : contrôle de l'espace libre, SHA-256 incrémental, refus d'un offset incorrect, `fsync` puis renommage, règles `onConflict` (`fail`, `rename` → ` (2)`, `overwrite` → corbeille) ; cibles `rom`, `save`, `state`, `staging` (pour `save` et `state`, appelle des fonctions de `internal/saves` / `internal/states` minimales : sauvegarde de l'ancien fichier dans la corbeille ; les copies de sauvegarde nommées viennent en M5) ;
   - gestionnaire de tâches : file d'une tâche à la fois, états, progression, annulation, conservation 1 h ;
   - `POST /downloads` : client HTTP (certificats racine comme l'import par URL de Cheat Manager), délais, `Range` pour reprendre, 5 redirections, 4 Gio maximum, nom de fichier déduit et nettoyé.
3. `DELETE /games/{gameId}?keepSave=` : collecte des fichiers via l'adaptateur, **déplacement** de tous les fichiers du jeu dans une entrée de corbeille (`entry.json` + chemins d'origine), suppression des verrous et de l'historique de reprise, réponse avec `trashEntryId`, `moved`, `movedBytes`. Jeux `m3u` et `folder-m3u` gérés. Routes `GET /trash`, `DELETE /trash` et `DELETE /trash/{entryId}` (en-tête `X-RL-Confirm: empty-trash` obligatoire).
4. Tests : envoi complet, reprise au milieu, mauvais offset, mauvais SHA-256, espace insuffisant (système de fichiers simulé ou seuil injectable), conflits, téléchargement depuis un `httptest.Server` (avec et sans `Content-Length`, avec redirection, avec coupure puis reprise `Range`), annulation, suppression d'un jeu simple et d'un jeu multi-disques, purge.

## Côté iOS

1. `ReminisLinkKit/Transfers` : envoi par morceaux (taille donnée par la console), calcul du SHA-256 en flux (CryptoKit, sans charger le fichier en mémoire), reprise à l'offset de `GET /uploads/{id}`, file d'envois, progression, vitesse, annulation ; `beginBackgroundTask` pour finir le morceau en cours.
2. Suivi des tâches (`/jobs/{id}` toutes les secondes tant que l'écran est visible).
3. Écrans : **Corbeille** (depuis le tableau de bord : entrées, taille, vider avec confirmation), **Ajouter** (Fichiers en sélection multiple avec système proposé d'après l'extension ; URL avec collage proposé ; type ROM / patch / cheats), **Transferts**, **Supprimer** (feuille qui liste ce qui sera supprimé et l'option « Garder la save et les states »).
4. **Extension de partage** : accepte fichiers et URL, écrit la demande dans le groupe d'apps, ouvre l'app sur **Ajouter** préremplie. Elle n'envoie rien elle-même.
5. Tests : reprise d'envoi (client factice qui coupe au milieu), SHA-256 en flux sur un fichier de 100 Mio sans dépasser 50 Mio de mémoire, parcours XCUITest « ajouter un fichier » contre le pak en mode headless.

## Fait quand

- Toutes les suites passent.
- Contre `make headless` : ajout d'un fichier de 200 Mio depuis le simulateur, coupure du pak au milieu, relance, reprise jusqu'au bout avec le bon SHA-256 ; ajout depuis une URL servie localement ; suppression d'un jeu avec contenu de la corbeille vérifié.
