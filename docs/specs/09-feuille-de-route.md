# 09 — Feuille de route

Chaque jalon a un prompt dédié dans `prompts/`. Un jalon est terminé quand tous ses critères sont remplis et que la CI passe.

## M0 — Relevés sur la console (humain)

But : remplacer toutes les mentions **[À VÉRIFIER M0]** par des faits.

- Fait le 19/11/2026 : analyse du code source de NextUI et du firmware d'origine ([m0/source-analysis.md](m0/source-analysis.md)), tableau de [05 §3.1](05-console-pak.md#31-organisation-nextui) mis à jour.
- Reste : la procédure réduite de [m0/README.md](m0/README.md) sur la console (veille, mDNS en Wi-Fi, RTC, batterie, version, confirmation rapide des chemins), puis les points de [m0/questions.md](m0/questions.md).

**Fait quand** : plus aucune mention **[À VÉRIFIER M0]** dans les spécifications.

## M1 — Contrat et cryptographie

- `protocol/openapi.yaml` complet pour l'API v1.
- `protocol/test-vectors/` (appairage, signature, identifiants).
- Go : `internal/auth` (appairage, dérivation, signature, cache de nonces, stockage des clients).
- Swift : `ReminisLinkKit` avec `Signer` et `Pairing`.

**Fait quand** : les deux suites passent les mêmes vecteurs ; `openapi.yaml` est valide.

## M2 — Serveur du pak (lecture)

- Squelette `cmd/reminislink` (`--desktop`, `--headless`), serveur, middlewares, erreurs.
- Annonce Bonjour, `/hello`, `/pair`, `/device`, `/systems`, `/systems/{id}/games`, `/games/{id}`, `/games/{id}/art`.
- Adaptateur NextUI (lecture), carte SD factice, tests d'intégration.

**Fait quand** : `make headless` sert une bibliothèque factice ; toutes ces routes ont des tests.

## M3 — App iOS (base)

- Projet Xcode, `ReminisLinkKit` (découverte, client API), écrans Consoles, Appairage (scanner + lien + PIN), Tableau de bord, Bibliothèque, Fiche de jeu (lecture seule).
- Gestion des erreurs du tableau de [06 §5](06-app-ios.md#5-erreurs-à-présenter).

**Fait quand** : sur simulateur, l'app trouve le pak lancé en `--headless`, s'appaire et parcourt la bibliothèque.

## M4 — Transferts et suppression

- Côté Go : `/uploads`, `/downloads`, `/jobs`, `DELETE /games/{id}`, corbeille (`/trash`), purge.
- Côté iOS : écran Ajouter, Transferts, reprise, extension de partage, suppression.

**Fait quand** : ajout d'une ROM depuis Fichiers et depuis une URL, reprise après coupure, suppression avec corbeille, testés en automatique (headless) et sur console.

## M5 — Saves, states, cheats

- Go : routes saves, states, verrous, historique de reprise, cheats (code repris de Cheat Manager), maintenance au lancement.
- iOS : sections Save, States, Cheats de la fiche de jeu.

**Fait quand** : tous les scénarios 7 et 8 de la checklist console passent.

## M6 — Patchs

- Go : `internal/patch` (IPS, BPS, UPS), route `/games/{id}/patch`, `/sd/files`.
- iOS : section Patch.

**Fait quand** : les tests de patchs passent (dont les cas d'erreur) et un jeu patché se lance sur la console.

## M7 — Interface console, publication

- Écrans gabagool du pak (principal, réglages, quitter), traductions EN/FR.
- `pak.json`, Makefile, CI, `ReminisLink.pak.zip`, README, captures d'écran.
- App : icône, textes App Store, politique de confidentialité, TestFlight.

**Fait quand** : installation depuis l'archive publiée sur une Brick neuve, checklist console complète, build TestFlight distribué.

## Après la v1 (idées, non engagées)

- Plateformes `tg5050`, `h700`, `my355`, puis adaptateur Knulli.
- iPad, Android.
- TLS en option.
- Gestionnaire de fichiers générique, lancement d'un jeu, Pak Store, mises à jour.
- Export / import des données d'un jeu en archive, synchronisation via l'iPhone.
- Restauration depuis la corbeille.
