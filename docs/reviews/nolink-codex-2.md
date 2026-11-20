Verdict : à corriger

## Bloquant

- `ios/ReminisLinkKit/Sources/ReminisLinkKit/Transfers/ShareAttachments.swift:52,80` — Le cas du tour 1 (`public.url` + `public.plain-text` pour un lien) est corrigé, mais le filtre rejette la pièce jointe sur ses seuls types déclarés. Un fournisseur qui expose un vrai `.cht` en `public.plain-text` et propose aussi `public.url` perd son fichier avant toute lecture. Le test du partage mixte utilise un fournisseur distinct pour le `.cht` et ne couvre pas ce cas (`ios/ReminisLinkKit/Tests/ReminisLinkKitTests/ShareAttachmentsTests.swift:50`).
- `ios/ReminisLinkKit/Sources/ReminisLinkKit/Transfers/ShareAttachments.swift:105,114` — Un lien partagé uniquement comme texte passe dans la boîte de réception si sa représentation est en UTF‑16 ou dépasse 4 096 octets. Dans les deux cas, le filtre traite la représentation comme un fichier. La spec demande d’écarter une pièce jointe qui ne contient qu’une URL (`docs/specs/06-app-ios.md:110`).

## À corriger

- `CLAUDE.md:33` — « device-side downloads » reste à retirer. Le parcours d’installation de la spec et le scénario BPS de la checklist ont bien été corrigés (`docs/specs/06-app-ios.md:42`, `docs/specs/checklists/console.md:14`).
- `ios/AppStore/en-US/description.txt:24`, `ios/AppStore/fr-FR/description.txt:24` — La phrase mentionne désormais la purge au lancement, mais généralise le passage en corbeille à tous les fichiers remplacés ou patchés. Un patch vers un nouveau fichier laisse la ROM source en place, et une sauvegarde remplacée est conservée dans les copies de secours (`internal/transfer/patch.go:413`, `internal/saves/saves_test.go:165`).
- `ios/AppStore/en-US/description.txt:13`, `ios/AppStore/fr-FR/description.txt:13` — La nouvelle formulation promet que le verrouillage empêche le jeu d’écraser un état. Le pak restaure la copie verrouillée au lancement si l’état a été écrasé ou supprimé (`internal/states/states.go:1178`).

## Suggestions

- `ios/Config/ReminisLinkShare-Info.plist:11`, `ios/ReminisLinkKit/Tests/ReminisLinkKitTests/ShareAttachmentsTests.swift:43` — Sur iPhone, vérifier que l’extension apparaît pour un partage mixte fichier + lien, puis que le fichier arrive dans Ajouter malgré une pièce jointe illisible. Le test ajouté couvre la poursuite après erreur au niveau du kit.
- `ios/AppStore/review_notes.txt:5` — Le lien vidéo reste à renseigner avant soumission. Montrer l’ajout depuis Fichiers.

Le test de migration couvre bien la ROM encore proposée, sa prise en charge et la purge de l’ancienne demande (`ios/ReminisLinkKit/Tests/ReminisLinkKitTests/TransferQueueTests.swift:556`). Les deux descriptions et les notes d’examen disent bien que l’app liste les jeux de la carte SD et n’en fournit aucun (`ios/AppStore/en-US/description.txt:4`, `ios/AppStore/fr-FR/description.txt:4`, `ios/AppStore/review_notes.txt:3`). Le passage sur les envois inactifs est identique pour chaque langue entre le Markdown et le site, avec les deux déclencheurs de purge (`docs/privacy.md:127`, `docs/privacy.md:288`, `site/privacy/index.html:80`, `site/fr/privacy/index.html:80`).

Les champs App Store modifiés respectent les limites Apple. En EN/FR, les descriptions font 2 448/2 739 caractères, les textes promotionnels 142/141 caractères et les mots-clés 94/95 octets. Les notes font 973 octets. [Limites App Store Connect](https://developer.apple.com/help/app-store-connect/reference/app-information/platform-version-information)

Côté Swift Concurrency, la lecture statique ne révèle pas de franchissement d’acteur avec `NSItemProvider`. `nonisolated(nonsending)` conserve l’acteur appelant et les callbacks du SDK sont annotés `Sendable` (`ios/ReminisLinkKit/Sources/ReminisLinkKit/Transfers/ShareAttachments.swift:27,78,84,93`). Revue en lecture seule. `git diff --check` passe. Compilation, suites Swift et essai sur iPhone non exécutés.