# M0 — Analyser les relevés de la console et mettre à jour les specs

## Contexte

La personne a suivi la procédure de `docs/specs/m0/README.md` sur une TrimUI Brick sous NextUI. Dans `docs/specs/m0/` : `survey-1-before.txt` (avant de jouer), `survey-2-after.txt` (après une save interne, des states dans les emplacements 1 et 2, puis une extinction en jeu) et `notes.md` (jeu de test utilisé, débits, veille, informations affichées par NextUI).

## Ta mission

Remplacer chaque mention **[À VÉRIFIER M0]** des spécifications par un fait tiré des relevés, ou par une question précise si les relevés ne suffisent pas.

## Étapes

1. Lis `docs/specs/05-console-pak.md` (surtout §2, §3.1, §5.2, §5.3, §6), `docs/specs/04-api-v1.md` (states, `/device`) et `docs/specs/03-protocole.md`.
2. Lis `docs/specs/m0/README.md` (pour savoir ce qui a été fait à chaque étape), `notes.md` et les deux relevés. Compare les deux relevés (sections « Recently modified », « Save states », « Slot 9 », « Saves layout ») en t'aidant du nom du jeu de test indiqué dans `notes.md` pour identifier : le dossier et le nommage des states, l'emplacement de reprise, une éventuelle vignette, le nommage des saves.
3. Pour chaque **[À VÉRIFIER M0]** dans `docs/specs/` :
   - si le relevé tranche : remplace par la valeur, et cite la ligne du relevé qui le prouve (nom du fichier et section) ;
   - sinon : laisse la mention et ajoute une question précise dans `docs/specs/m0/questions.md` (ce qu'il faut faire sur la console pour trancher).
4. Mets à jour le tableau de `05-console-pak.md` §3.1 (colonne Statut : « Confirmé (M0) »).
5. Crée la carte SD factice `testdata/sdcard/` fidèle aux relevés (structure de dossiers et noms de fichiers ; contenu factice de quelques octets, aucune ROM réelle). Inclure : 3 systèmes dont deux pour la même console avec des tags différents, un sous-dossier, un jeu multi-disques s'il en existe sur la console, des noms avec accents, espaces, parenthèses et un emoji, des saves, des states (dont l'emplacement de reprise), des `.cht`, des jaquettes.
6. Si le débit Wi-Fi mesuré ou le comportement en veille change une décision (TLS, prévention de la veille), note-le dans `docs/specs/m0/questions.md` sans modifier l'architecture toi-même.

## Contraintes

- Ne modifie que `docs/specs/` et `testdata/sdcard/`. Pas de code.
- Ne devine jamais un chemin : si un relevé ne le montre pas, c'est une question.

## Fait quand

- `grep -rn "À VÉRIFIER M0" docs/specs/` ne renvoie plus que des points listés dans `docs/specs/m0/questions.md`.
- `testdata/sdcard/` existe et correspond aux relevés.
