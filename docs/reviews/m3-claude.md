# Revues M3 — agents Claude neufs

Revues non croisées, faites en complément de la revue GPT-OSS (de faible qualité).

## Revue complète (`7e636d20`) — à corriger, aucun bloquant
- Annuler ne stoppait pas un appairage en cours (clé enregistrée malgré l'annulation, repli sur l'IP du QR après annulation).
- Un lien d'appairage pour une console déjà appairée remplaçait sa clé sans confirmation ; un faux pak sur une IP privée pouvait ainsi écraser la clé d'une console éteinte.
- Une connexion annulée laissait la session en échec mis en cache.
- `notPaired` sans action (entrée fantôme après restauration sur un nouvel iPhone).
- Suggestions : connexion partagée, jaquettes « best effort », délai croissant du navigateur Bonjour, lien strict (user/port/fragment), `clientName` en scalaires, « Desktop » localisé, démo réservée au Debug.
- Vérifié conforme : Trousseau, `h`/`p` jamais persistés, preuve de la console, nouvel essai unique après `clock_skew`, `encodeQuery`, aucun journal sensible, identification par `id`, modèles conformes au contrat, `304` de la jaquette, projet Xcode, localisation, accessibilité.

## Revue ciblée des corrections (`4977d9fe`) — à corriger, aucun bloquant
- Annuler pendant `POST /pair` jetait une clé déjà enregistrée par le pak (désynchronisation en ré-appairage).
- L'annulation d'un appelant annulait la connexion partagée de tous.
- `startPairing` ne stoppait pas explicitement le flux précédent.
- Suggestions : `start()` idempotent, erreur du Trousseau distinguée de « pas de clé », garde PIN, `RL_RESET_STORE` en Debug, états de la découverte, test UI de la confirmation.

## Aller-retour 2 (`419d9f11`)
Corrections relues par l'agent principal (échange `/pair` non annulable avec l'écran, connexion partagée comptant ses appelants). `swift test` 81 tests, `xcodebuild test` 18 tests app + 2 tests UI démo, build Release sans code démo. Aucun point restant.
