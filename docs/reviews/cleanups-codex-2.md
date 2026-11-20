Verdict : à corriger

## Bloquant

- `scripts/run-headless-uitests.sh:112`, `scripts/smoke-events.sh:81`, `scripts/lib/rl2.sh:163` et `:196` — `exec` donne bien au lanceur le PID de `smoke-events.sh`, et le piège `TERM` déclenche le nettoyage quand Bash reprend la main. Mais Bash 3.2 diffère ce piège pendant une substitution de commande en cours. Les appels RL2 d’appairage et de requête utilisent des `curl` sans délai maximal. Si l’un se bloque, `stop_helper` peut attendre indéfiniment après l’échec de `xcodebuild`, avec les clés privées encore sur disque. J’ai vérifié le report du piège sous Bash 3.2 avec une substitution de commande inoffensive. Il faut borner ces appels ou rendre leurs PID directement arrê­tables.

## À corriger

- `scripts/run-headless-uitests.sh:262` et `:294` — Le faux `PASS` du tour 1 est corrigé. Il faut désormais un code de sortie zéro et la ligne `passed` de la méthode demandée. L’attente du second client se fonde encore sur le seul code de sortie. Pour `EVENTS_LIBRARY`, une exécution vide avec `SKIP_BUILD=1`, ou un test sauté avec un code zéro, peut donc attendre environ deux minutes avant d’être déclaré en échec. Appliquer le contrôle XCTest avant le `wait`.
- `scripts/run-headless-uitests.sh:267` et `:270`, `docs/specs/08-tests-qualite.md:36` — La relance est bien limitée à une tentative, mais sa condition reste une heuristique. Une destination invalide satisfait la condition et déclenche une relance inutile. À l’inverse, un renouvellement du lien à l’expiration suffit à la bloquer sans appairage. La sortie du pak fournit un marqueur explicite `Paired with` à `cmd/reminislink/pairing.go:93`. Utiliser ce marqueur si la décision dépend de l’appairage et préciser dans la spec le rôle du code de sortie de `xcodebuild`.
- `scripts/run-headless-uitests.sh:122` et `:133`, `cmd/reminislink/pairing.go:80` — La fuite sous `bash -x` est corrigée dans les deux scripts. Le journal privé du pak contient toutefois le lien en clair **et** le QR code. `KEEP_LOGS=1`, également activé sur échec, conserve les deux. Expurger ces éléments avant de conserver les journaux, y compris lors d’une interruption.

## Suggestions

- `scripts/smoke-events.sh:324` — Le cinquième flux vérifie bien `Retry-After: 5`, conformément au contrat. Si l’en-tête manque, `set -e` et `pipefail` font sortir le script avant le diagnostic `check`. Faire passer une valeur vide à `check` donnerait un échec explicite.
- `docs/specs/08-tests-qualite.md:33` — La ligne **Intégration** est juste. Elle distingue `PinnedHandshakeTests` contre un pak réel avec `RL_TEST_PAK_LINK` des parcours XCUITest via le lanceur. Ajouter `swift test --filter PinnedHandshake` rendrait la commande directement utilisable.

L’échec de `mktemp` ou de `cp -R` est désormais propagé avant l’utilisation de la carte partielle (`scripts/run-headless-uitests.sh:154`, `:208`). Les deux tests Swift couvrent le délai de 9 s face au délai local de 500 ms et la recréation de l’envoi après un refus `device_busy` de `complete` suivi d’un redémarrage du FakePak (`ios/ReminisLinkKit/Tests/ReminisLinkKitTests/TransferTests.swift:224`, `:239`).

Revue en lecture seule. Syntaxe des trois scripts, `--list` sous `bash -x` et transmission des variables via `exec` vérifiés avec Bash 3.2. Les suites Swift et XCUITest et le smoke test complet n’ont pas été exécutés.