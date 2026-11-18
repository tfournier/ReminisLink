# M6 — Patchs IPS, BPS, UPS (Go + Swift)

## À lire

`docs/specs/05-console-pak.md` §5.5, `docs/specs/04-api-v1.md` (`POST /games/{gameId}/patch`, `/sd/files`), `docs/specs/08-tests-qualite.md` §2 (Patchs), `docs/specs/06-app-ios.md` §4.5 (Patch).

## Côté Go : `internal/patch`

1. Détection du format par l'en-tête (`PATCH` pour IPS, `BPS1`, `UPS1`), pas par l'extension.
2. **IPS** : enregistrements normaux et RLE, marqueur `EOF`, extension de troncature (3 octets après `EOF`), cible pouvant dépasser la taille de la source, limite d'adresse de 16 Mio.
3. **BPS** : lecture des nombres variables, tailles source / cible / métadonnées, actions `SourceRead`, `TargetRead`, `SourceCopy`, `TargetCopy` (décalages relatifs signés) ; vérification des CRC32 source (avant d'écrire), cible et patch.
4. **UPS** : nombres variables, XOR par blocs, tailles, vérification des CRC32 source et cible (y compris application « à l'envers » si la ROM fournie correspond à la cible : refuser avec un message clair plutôt qu'inverser).
5. Écriture en flux vers un fichier temporaire de la zone ReminisLink, sans charger plus que nécessaire en mémoire (la console a 1 Go de RAM ; viser moins de 2 × la taille de la ROM pour BPS, qui a besoin d'un accès aléatoire à la cible), puis vérification, puis renommage.
6. Route `POST /games/{gameId}/patch` (tâche) : `patch.type` `staging` ou `sd` ; `output.mode` `new-file` (nom par défaut : `<nom> (patched).<ext>`, conflit → ` (2)`) ou `replace` (originale à la corbeille, save et states conservés). `422 patch_mismatch` et `422 unsupported_format` selon les cas.
7. `GET /sd/files?type=patch`.
8. Tests : fichiers générés par les tests (encodeurs IPS/BPS/UPS minimaux **dans les tests uniquement**, ou fichiers de référence créés avec Flips et commités, sans contenu commercial) ; application correcte, mauvaise source, patch tronqué, CRC du patch faux, cible plus grande et plus petite que la source, fuzzing de l'analyseur de chaque format.

## Côté iOS

1. Section **Patch** de la fiche de jeu : choisir le patch (Fichiers → envoi `staging` ; URL → téléchargement `staging` ; carte SD → `/sd/files`), choisir la sortie (nouveau fichier avec nom proposé et modifiable, ou remplacer avec avertissement), lancer, suivre la tâche, puis proposer d'ouvrir la fiche de la nouvelle ROM.
2. Messages clairs pour `patch_mismatch` (« Ce patch ne correspond pas à cette ROM. Vérifie la version de la ROM demandée par le patch. ») et `unsupported_format`.
3. Extension de partage : un fichier `.ips`, `.bps` ou `.ups` partagé ouvre directement le parcours Patch (choix du jeu ensuite).

## Fait quand

- Toutes les suites passent, y compris les fuzzers en mode court.
- Contre `make headless` : application d'un BPS de test en nouveau fichier et en remplacement, avec rejet d'une mauvaise source.
- Sur console (par la personne) : un jeu patché se lance dans NextUI.
