# Revue indépendante d'une pull request

Tu relis une pull request de ReminisLink que tu n'as pas écrite. Ton rôle est de trouver les problèmes, pas de réécrire le code.

## Préparation

1. Lis `CLAUDE.md`, `docs/specs/README.md` et les specs citées dans le prompt du jalon concerné (`prompts/<jalon>.md`).
2. Lis le diff complet de la branche par rapport à `main` (`git diff main...HEAD`) et la description de la PR.
3. Lance les suites de tests (`make lint test`, `make contract`, et `swift test` / `xcodebuild test` si l'iOS est touché). Note tout échec.

## Points à vérifier, par ordre d'importance

1. **Perte de données** : chaque suppression, remplacement, restauration ou patch fait-il une copie avant ? Les écritures sont-elles atomiques (`.tmp`, `fsync`, renommage) ? Que se passe-t-il si la console s'éteint au milieu ?
2. **Sécurité** : toutes les routes non publiques passent-elles par l'auth ? Les chemins venant du réseau sont-ils résolus et contenus sous leur racine ? Comparaisons en temps constant ? Rien de secret dans les journaux ? Limites de taille en place ?
3. **Conformité au contrat** : noms de champs, codes HTTP et codes d'erreur identiques à `protocol/openapi.yaml` et à la spec ? Les vecteurs de test passent-ils des deux côtés ?
4. **Hypothèses NextUI** : un chemin ou un nommage est-il codé en dur hors de l'adaptateur ? Les points `TODO(M0)` sont-ils isolés et signalés ?
5. **Tests** : chaque comportement de la spec a-t-il un test, y compris les cas d'erreur ? Les tests dépendent-ils du réseau, de l'heure réelle ou d'une console ?
6. **Concurrence** : accès concurrents à l'état partagé (tâches, envois, cache de nonces, fichiers JSON) protégés ? Courses possibles entre deux requêtes sur le même jeu ?
7. **Expérience utilisateur** (iOS et écrans de la console) : textes traduits, messages d'erreur utiles, confirmations avant les actions destructives, accessibilité.
8. **Lisibilité** : conventions de `CLAUDE.md` et de Cheat Manager respectées, pas de dépendance non autorisée, pas de code mort.

## Rapport

En français, sous cette forme :

```
Verdict : prêt à fusionner / à corriger / à reprendre

Bloquant
- [fichier:ligne] Problème. Scénario concret qui le déclenche. Correction suggérée.

À corriger
- ...

Suggestions
- ...

Tests lancés : commandes et résultats.
```

Un point n'est **bloquant** que si tu peux décrire un scénario concret où il cause une perte de données, une faille, un écart au contrat ou un test qui échoue. N'invente pas de problème pour remplir une section.
