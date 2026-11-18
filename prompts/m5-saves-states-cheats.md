# M5 — Saves, states, verrous, cheats (Go + Swift)

## À lire

`docs/specs/04-api-v1.md` (sections Saves, States, Cheats, `/sd/files`), `docs/specs/05-console-pak.md` §2 (maintenance), §4, §5.1 à §5.4, `docs/specs/06-app-ios.md` §4.5. Le paquet `internal/cheats` de `../nextui-cheat-manager`.

## Côté Go

1. **Saves** (`internal/saves`) : téléchargement, remplacement (`PUT`, 64 Mio maximum, sinon `/uploads`), copies de sauvegarde (`bk_<horodatage>_<4 hex>`, métadonnées JSON : libellé, raison, date, SHA-256), création, restauration (copie `before-restore` d'abord), téléchargement et suppression d'une copie, limite de 10 copies automatiques par jeu. Raccorde la cible `save` de `/uploads` (M4) à ces fonctions.
2. **States** (`internal/states`) : liste des emplacements via l'adaptateur (taille, date, vignette si gérée, verrou), téléchargement, suppression (corbeille ; `409 state_locked` si verrouillé), vignette.
3. **Verrous** : verrouiller (copie + SHA-256 dans `locks/`), déverrouiller, restaurer ; à chaque lancement, contrôle de tous les verrous selon `05 §5.2` (intact, restauré avec l'état écrasé mis à la corbeille, absent). L'état `restored` est conservé jusqu'au prochain `GET /states` qui le renvoie, puis repasse à `intact`.
4. **Historique de reprise** : archivage au lancement selon `05 §5.3` (comparaison date + taille d'abord, SHA-256 ensuite), 5 entrées, routes de liste et de restauration.
5. **Cheats** : copie de `internal/cheats` de Cheat Manager (mention d'origine) ; `GET` avec `etag`, `PUT` avec `If-Match` (`412` si conflit), import `url` / `sd` / `inline` / `upload` en `merge` ou `replace` ; `GET /sd/files?type=cht`.
6. **Maintenance au lancement** : raccorde verrous et historique de reprise à la séquence de démarrage (avant l'ouverture du serveur), avec un temps maximum raisonnable et un journal de ce qui a été restauré.
7. Tests pour chaque route et chaque cas de `05 §5.2` (avec une horloge et un système de fichiers de test), les clés `.cht` inconnues conservées, le `.bak`, le conflit d'`etag`.

## Côté iOS

1. Fiche de jeu, section **Save** : télécharger (feuille de partage iOS), remplacer depuis Fichiers (via `/uploads` si > 64 Mio), copies (créer avec libellé, restaurer avec confirmation, télécharger, supprimer).
2. Section **States** : un rang par emplacement (vignette, date, taille, cadenas, badge « Restauré » si `lockStatus = restored`), actions verrouiller / déverrouiller / restaurer / télécharger / supprimer, texte d'aide sur les verrous (`05 §5.2`), section **Historique de reprise**.
3. Section **Cheats** : liste avec interrupteurs, ajout, édition, suppression, réordonnancement (glisser), enregistrement avec `If-Match` et gestion du conflit (« La liste a changé sur la console. Recharger ? »), import (URL, Fichiers, carte SD ; fusion ou remplacement).
4. Tests unitaires (modèles, logique de conflit) et un parcours XCUITest par section contre le pak en mode headless.

## Fait quand

- Toutes les suites passent.
- Contre `make headless` : un state verrouillé, modifié à la main dans la carte SD factice, est restauré au relancement du pak et l'app affiche « Restauré ».
- Sur console (checklist `08 §5`, scénarios 7 et 8) : à faire par la personne ; liste-les dans ton rapport.
