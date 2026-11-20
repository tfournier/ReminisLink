# Checklist de tests sur console

À dérouler sur une vraie TrimUI Brick sous NextUI, à chaque jalon qui touche la console (voir [08 §5](../08-tests-qualite.md#5-tests-sur-console-manuels-à-chaque-jalon)). Copier ce fichier en `console-<jalon>-<date>.md`, cocher, noter les mesures et les écarts.

Jalon : …… Date : …… Version du pak : …… Version de NextUI : ……

| # | Scénario | À partir de | Résultat | Notes / mesures |
|---|---|---|---|---|
| 1 | Installation du pak à la main (`/Tools/tg5040/ReminisLink.pak/`) et lancement depuis le menu Outils | M7 (M2 à M6 : binaire lancé en SSH) | ☐ | |
| 2 | Sans Wi-Fi : message d'erreur et sortie propre avec B | M7 | ☐ | |
| 3 | Appairage par QR code (appareil photo de l'iPhone et scanner de l'app), puis par PIN | M3 | ☐ | |
| 4 | Navigation dans une vraie bibliothèque : systèmes, dossiers, fiche de jeu ; temps de réponse de la liste d'un gros système | M3 | ☐ | ms : |
| 5 | Envoi d'une ROM de 512 Mo : durée ; coupure du Wi-Fi au milieu, reprise jusqu'au bout | M4 | ☐ | durée : |
| 6 | Envoi d'un patch depuis l'iPhone (Fichiers ou menu de partage), application BPS, lancement du jeu patché dans NextUI | M6 | ☐ | |
| 7 | Save : copie de sauvegarde, modification en jeu, restauration, vérification en jeu | M5 | ☐ | |
| 8 | State verrouillé, écrasé en jeu, restauré à l'ouverture suivante du pak ; l'app affiche « Restauré » | M5 | ☐ | |
| 9 | Suppression d'un jeu ; contenu de la corbeille (`/.userdata/shared/ReminisLink/trash/`) ; vidage de la corbeille | M4 | ☐ | |
| 10 | Fermeture du pak (B) pendant un transfert : confirmation, puis reprise du transfert à la réouverture | M4 (confirmation : M7) | ☐ | |

## Contrôles transverses

- [ ] Le journal `$LOGS_PATH/ReminisLink.txt` ne contient ni secret d'appairage, ni PIN, ni clé, ni signature.
- [ ] Après la fermeture du pak, aucun processus ReminisLink ne reste lancé (`ps`).
- [ ] La console n'a pas été mise en veille pendant un transfert long (décision prise après M0).
