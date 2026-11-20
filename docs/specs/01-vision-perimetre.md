# 01 — Vision et périmètre

## Le problème

Sur une console comme la TrimUI Brick sous NextUI, gérer ses jeux est pénible :

- ajouter une ROM, un patch ou un fichier de cheats demande de sortir la carte SD, de passer par un PC, ou de taper des URL avec le clavier virtuel ;
- les outils existants ne gèrent que les jeux qu'ils ont eux-mêmes installés (Grout avec RomM) ;
- rien ne permet de sauvegarder une save, de protéger un state contre l'écrasement, ou de supprimer un jeu **avec** toutes ses données ;
- appliquer un patch de romhack ou de traduction impose de passer par un ordinateur.

## La solution

Une app iPhone qui pilote la console sur le Wi-Fi local, sur le modèle de l'app mobile du Flipper Zero. L'iPhone apporte ce qui manque à la console : un vrai clavier, le navigateur, l'app Fichiers, le partage depuis n'importe quelle app.

Le mode d'emploi tient en une phrase : **on ouvre ReminisLink sur la console, on gère depuis l'iPhone, on ferme, on joue.**

## Utilisateurs

- **Principal** : joueur rétro avec une console Linux (NextUI d'abord), à l'aise avec les paks, qui veut gagner du temps.
- **Secondaire** : joueur moins technique qui ne veut pas manipuler la carte SD.
- Langues : français et anglais dès la v1. Les 7 langues de Cheat Manager ensuite.

## Périmètre v1

| Domaine | Fonctions |
|---|---|
| **Console** | IP, nom, modèle, OS et version, batterie (niveau, en charge), stockage (total, libre), version du pak |
| **Bibliothèque** | Lister les systèmes et les jeux (y compris installés à la main), avec pour chacun : taille, save, nombre de states, nombre de cheats, jaquette |
| **Ajout de jeux** | Depuis l'iPhone (Fichiers, partage) |
| **Suppression** | Supprimer un jeu et toutes ses données (save, states, cheats, jaquette), avec sauvegarde préalable de la save et des states |
| **Saves** | Télécharger sur l'iPhone, remplacer, créer une copie de sauvegarde nommée, restaurer une copie, supprimer une copie |
| **States** | Lister les emplacements (date, taille, vignette si disponible), télécharger, supprimer, verrouiller / déverrouiller, restaurer ; historique du state de reprise |
| **Cheats** | Lister, ajouter, modifier, activer / désactiver, supprimer ; importer depuis l'iPhone ou un fichier `.cht` de la carte SD (fusion ou remplacement) |
| **Patchs** | Appliquer un patch IPS, BPS ou UPS venant de l'iPhone ou de la carte SD ; résultat dans un nouveau fichier (par défaut) ou à la place de la ROM (avec sauvegarde) |

## Hors périmètre v1

Téléchargement depuis un lien par la console (retiré le 20/11/2026 et écarté, décision de la personne : l'utilisateur télécharge sur l'iPhone puis envoie depuis Fichiers), lancer un jeu ou un outil, contrôle à distance et recopie d'écran, Pak Store, mise à jour du firmware, gestionnaire de fichiers générique, synchronisation entre consoles, Bluetooth, app Android, format xdelta et patchs PPF.

Ces points sont repris, ou non, après la v1 (voir [09](09-feuille-de-route.md)).

## Plateformes

| Composant | v1 | Ensuite |
|---|---|---|
| Console | NextUI sur `tg5040` (TrimUI Brick, Brick Pro, Smart Pro) | `tg5050`, `h700`, `my355` (comme Cheat Manager), puis Knulli |
| Téléphone | iOS 18+ (iPhone) | iPad, Android |

## Principes

1. **Aucune perte de données.** Toute opération destructive est précédée d'une sauvegarde automatique.
2. **Local uniquement.** Aucun serveur, aucun compte, aucune donnée envoyée à un service tiers ou à l'éditeur. Seule exception, à partir de la fonction Sync (M9) : l'app peut copier les saves dans l'iCloud Drive **de l'utilisateur** (son propre stockage Apple), désactivable.
3. **Aucune source de jeux.** L'app ne propose, ne liste et ne suggère aucune source de ROMs. L'utilisateur apporte ses propres fichiers.
4. **Simple pour l'utilisateur.** Appairage en un scan de QR code, aucune IP à taper.
5. **Portable.** Le cœur ne dépend pas de NextUI ; les spécificités de l'OS sont isolées dans un adaptateur.

## Nom et identité

- Nom affiché : **ReminisLink** (toujours en entier, jamais « Reminis » seul).
- Identifiants techniques : `reminislink` (dépôt, Bonjour `_reminislink._tcp`, *bundle ID*).
- Nom du pak sur la console : **ReminisLink**, comme l'app.
- Domaine : `reminis.link` (commandé le 19/11/2026).
