# M7 iOS — vérification par l'agent principal

Pas de revue indépendante dédiée (jalon surtout de publication : icône, textes App Store, confidentialité, configuration, relecture des chaînes ; code limité à l'affichage de D4/D6).

Vérifié : `swift test` 193 tests, `xcodebuild test` 59 tests app + tests UI, build Release et archive non signée sans avertissement de build, aucune marque ni source de jeux dans les textes App Store, icône originale (validée par la personne). Identifiants passés à `link.reminis.app` (décision de la personne, domaine `reminis.link`) ; l'équipe de signature reste hors du dépôt (`Config/Local.xcconfig`).
