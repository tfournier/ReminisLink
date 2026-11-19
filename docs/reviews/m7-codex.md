# Revue M7 — Codex (ChatGPT, compte de la personne)

Date : 2026-11-19. Revue croisée en lecture seule (`codex exec -s read-only`, Codex CLI 0.170.0) sur `main` @ `5cb60ebf`, périmètre du jalon. Les tests n'ont pas pu tourner dans le bac à sable de Codex : constats par lecture du code. Chaque point est vérifié par l'agent principal et l'agent du jalon M8 avant correction.

Verdict : **à corriger**

### Bloquant

- [`internal/ui/run_sdl.go:239`](internal/ui/run_sdl.go#L239) — Le QR d’appairage est écrit en PNG dans le répertoire temporaire choisi par `os.MkdirTemp`. En mode desktop, ce répertoire peut se trouver sur le disque. Si le pak reçoit `SIGKILL` pendant que le QR est affiché, le fichier contenant le secret reste sur le disque, en violation de l’exigence « secrets jamais écrits ». Rendre le QR en mémoire, y compris en mode desktop.
- [`internal/transfer/download.go:251`](internal/transfer/download.go#L251) — Le journal peut contenir le jeton d’une URL de téléchargement. Lorsqu’un serveur renvoie une redirection mal formée, `net/http` inclut la valeur brute de `Location` dans l’erreur interne. `downloadErrorText` retire l’enveloppe `url.Error`, puis conserve ce message. Si `Location` contient une URL signée, son jeton peut donc apparaître dans le journal de reprise et dans l’erreur de la tâche. Journaliser des motifs d’erreur prédéfinis et tester le masquage sur une redirection mal formée contenant un jeton fictif.
- [`pak/launch.sh:20`](pak/launch.sh#L20) — Un `SIGTERM` envoyé au PID du lanceur ne déclenche pas l’arrêt du pak. Le shell attend le binaire enfant sans lui relayer le signal. Un superviseur qui arrête seulement `launch.sh` peut ainsi laisser le serveur et son annonce Bonjour actifs. Faire du binaire le processus du lanceur avec `exec` en conservant la synchronisation côté Go, ou relayer le signal et attendre la fin du binaire. Tester le signal sur le PID du lanceur.

### À corriger

- [`ios/README.md:58`](ios/README.md#L58) — La procédure App Store Connect indique encore `<prefix>.reminislink`, alors que l’archive porte `link.reminis.app`. La personne qui suit cette étape cherchera un autre identifiant que celui enregistré à l’étape précédente. Indiquer l’identifiant issu de `RL_APP_BUNDLE_ID`.
- [`internal/app/network.go:46`](internal/app/network.go#L46) — Une erreur de réannonce Bonjour au réveil laisse la console absente de la découverte tant que son IP reste stable. Le code mémorise la nouvelle IP avant l’annonce et les vérifications suivantes s’arrêtent sur `!changed`. Réessayer tant que le Wi-Fi est disponible et que l’annonce manque.
- [`Makefile:134`](Makefile#L134) — Le ZIP omet le fichier `LICENSE` ajouté après M7. Son README embarqué pointe désormais vers ce fichier, ce qui produit un lien cassé dans l’archive distribuée. Embarquer `LICENSE` à côté du README.

### Suggestions

- [`Makefile:147`](Makefile#L147) — Documenter l’essai de publication sur un dépôt de test. Sur l’arbre relu, un tag `v1.0.0` posé directement échoue faute d’entrée de changelog. La cible `make vX.Y.Z` prépare cette entrée, puis pousse le commit et le tag vers `origin` et déclenche une publication.
- [`.github/workflows/ci.yml:91`](.github/workflows/ci.yml#L91) — Ajouter une archive iOS non signée au job macOS pour vérifier le chemin Release avant TestFlight.
- [`Makefile:128`](Makefile#L128) — Le contrôle D1 confirme que `certifiable` est lié au binaire. Compléter ce contrôle par un téléchargement HTTPS avec le pak ARM64 sur la Brick, sans magasin de certificats système.

### Checklist console avant le premier tag public

À exécuter sur une vraie TrimUI Brick sous NextUI, selon [`08 §5`](docs/specs/08-tests-qualite.md) et la [checklist console](docs/specs/checklists/console.md).

1. Installer manuellement le ZIP dans `/Tools/tg5040/ReminisLink.pak/` et lancer ReminisLink depuis le menu Outils.
2. Lancer sans Wi-Fi, vérifier le message et quitter proprement avec B.
3. Appairer par QR avec l’appareil photo de l’iPhone et le scanner de l’app, puis tester le PIN.
4. Parcourir une vraie bibliothèque et mesurer le temps d’affichage de la liste d’un gros système.
5. Envoyer une ROM de 512 Mo, mesurer la durée, couper le Wi-Fi à mi-parcours et vérifier la reprise jusqu’au bout.
6. Télécharger un patch depuis une URL, appliquer le BPS et lancer le jeu patché dans NextUI.
7. Sauvegarder une save, la modifier en jeu, la restaurer et vérifier le résultat en jeu.
8. Verrouiller un state, l’écraser en jeu, rouvrir le pak et vérifier la restauration ainsi que l’indication « Restauré » dans l’app.
9. Supprimer un jeu, vérifier les fichiers présents dans la corbeille, puis vérifier son vidage.
10. Appuyer sur B pendant un transfert, vérifier la confirmation, quitter et reprendre le transfert à la réouverture.

Contrôler aussi l’absence de secret, PIN, clé et signature dans les journaux, ainsi que l’absence de processus résiduel après la sortie. Vérifier le comportement réel du bouton d’alimentation et la reprise Wi-Fi selon la décision M0.

### Tests lancés

| Commande | Résultat |
|---|---|
| `make -k lint test` | Formatage OK. `go vet` et `go test` bloqués par les permissions sur le cache et le répertoire temporaire Go. |
| `make contract` | Bloqué à l’accès au socket Docker, avant Redocly et la vérification Go des vecteurs. |
| `scripts/sync-test-vectors.sh --check` | OK. Les copies Swift des vecteurs sont à jour. |
| `swift test --scratch-path /tmp/reminislink-review-codex-swift` | Bloqué avant compilation par `permissionDenied` du compilateur Swift. |
| `xcodebuild test` et `xcodebuild archive` sans signature, avec répertoires de sortie sous `/tmp` | Bloqués avant compilation par les permissions Xcode. CoreSimulator était également inaccessible pour les tests. |
| `sh -n pak/launch.sh`, `sh -n scripts/setup-toolchain.sh`, `plutil -lint` sur les options d’export et les deux manifestes de confidentialité | OK. |
| `make check-version TAG=v1.0.0` | Refus attendu sur cet arbre. `pak.json` contient seulement l’entrée `Unreleased`. |
| `python3 -B site/tools/check_site.py` | OK. Sept pages et treize cibles internes vérifiées. |

`git diff main...HEAD` est vide parce que le `HEAD` détaché est un ancêtre du `main` local. J’ai utilisé `ec8fa31c^1..5c43c819` pour identifier le périmètre et vérifié les fichiers dans leur état actuel. Les commits supplémentaires de `main` spécifient M8 sans modifier le code M7. La description GitHub de la PR était inaccessible via `gh`, qui demandait une authentification. Les messages de fusion étaient disponibles localement. La génération du ZIP, la publication sur tag, l’archive iOS et les essais sur Brick restent à valider.