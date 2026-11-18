# M0 — Relevés sur la console (procédure réduite)

Les chemins, noms de fichiers et sources d'information de NextUI ont été établis le 19/11/2026 par l'analyse du code source de NextUI et du firmware d'origine de la Brick : [source-analysis.md](source-analysis.md). Les specs sont à jour. Il ne reste sur la console que les points que le code ne peut pas trancher, listés dans [questions.md](questions.md) :

1. veille et bouton d'alimentation pendant un pak gabagool, maintien du Wi-Fi ;
2. coexistence du répondeur mDNS du pak avec `avahi-daemon` en Wi-Fi réel (déjà validée en conteneur) ;
3. RTC et NTP : l'heure tient-elle console éteinte, `ntpd` tourne-t-il ;
4. `axp2202-battery/status` : existe-t-il, avec quelles valeurs ;
5. séparateur des lignes de `.system/version.txt` ;
6. confirmation rapide : dossier `.userdata/shared/<TAG>-<cœur>/` avec `.st0` et `.st9`, vignette `.minui/<TAG>/*.0.bmp` (signature PNG, 1024×768), valeurs de `minuisettings.txt`.

Ce jalon est **fait par une personne**, console en main : environ 20 minutes, plus une attente de quelques heures console éteinte (étape 5). Ensuite, un agent relit les résultats et met à jour les specs et `questions.md`.

## Ce qu'il faut à la fin

Dans ce dossier `docs/specs/m0/` :

| Fichier | Contenu | Étape |
|---|---|---|
| `survey-1-before.txt` | Sortie du script, après la partie, sur batterie, Wi-Fi actif | 2 |
| `survey-2-after.txt` | Sortie du script, après plusieurs heures éteinte, Wi-Fi coupé, sur secteur | 5 |
| `notes.md` | Le modèle rempli | 2 à 6 |

## Matériel

- La TrimUI Brick sous NextUI, chargée, Wi-Fi activé et connecté au même réseau que le Mac.
- Un accès shell à la console : le pak **Dropbear Server (SSH)** (Pak Store, catégorie File Management) pour les étapes avec Wi-Fi, et le pak **Terminal** pour l'étape 5 (Wi-Fi coupé).
- Un jeu Game Boy ou GBA à toi, déjà sur la carte.
- Le pak **Cheat Manager** (ou un autre pak gabagool) installé.
- Le Mac, avec le dépôt ReminisLink.

## Étape 1 — Copier le script sur la console

- **Par SSH** (Dropbear lancé, IP affichée par le pak) :
  ```sh
  scp scripts/verify-device.sh root@<IP-de-la-console>:/mnt/SDCARD/verify-device.sh
  ```
- **Par la carte SD** : éteins la console, copie `scripts/verify-device.sh` à la racine de la carte, remets-la.

Le script ne fait que lire : il n'écrit rien sur la carte, hors de sa propre sortie.

## Étape 2 — Jouer, puis premier relevé

1. Lance ton jeu de test. Dans le menu de NextUI en jeu, **enregistre un state dans l'emplacement 1**.
2. Joue 30 secondes, puis **éteins la console en jeu** (appui long sur le bouton d'alimentation) : c'est ce qui écrit le state de reprise (`.st9`).
3. Rallume la console (elle relance le jeu : quitte-le par le menu), débranche le chargeur, lance Dropbear, puis :
   ```sh
   sh /mnt/SDCARD/verify-device.sh > /mnt/SDCARD/survey-1-before.txt 2>&1
   ```
4. Note dans `notes.md` l'heure exacte (celle du Mac) au moment du relevé, le dossier du système et le nom du fichier ROM.
5. Récupère le fichier : `scp root@<IP>:/mnt/SDCARD/survey-1-before.txt docs/specs/m0/`.

## Étape 3 — Veille et bouton d'alimentation pendant un pak

1. Sur le Mac : `ping <IP-de-la-console>`, laissé ouvert.
2. Ouvre **Cheat Manager** et ne touche plus à rien pendant **10 minutes**. Note si l'écran s'éteint, si la console se met en veille, et si le ping continue (pertes, latence qui monte).
3. Toujours dans Cheat Manager : **appui court** sur le bouton d'alimentation. Que se passe-t-il (rien, écran éteint, veille, retour au menu) ? Le ping continue-t-il ?
4. Puis **appui long** (environ 3 secondes, pas plus). Même question. Si la console s'est éteinte ou mise en veille, note au réveil le délai avant la reprise du ping et si l'IP a changé.

## Étape 4 — Annonce Bonjour à côté d'avahi (dès qu'une version du pak est installable)

1. Installe le pak ReminisLink et lance-le.
2. Sur le Mac : `dns-sd -B _reminislink._tcp` doit afficher la console, puis `dns-sd -L "<nom de la console>" _reminislink._tcp` son TXT.
3. Sur l'iPhone : l'app doit trouver la console.
4. Le relevé de l'étape 2 dit déjà si avahi tourne (section « mDNS / avahi »).

Si aucune version du pak n'est encore installable, saute cette étape et reviens-y plus tard.

## Étape 5 — Horloge (RTC) console éteinte

1. Dans les réglages de NextUI, **désactive le Wi-Fi** (pour que NTP ne corrige pas l'heure au redémarrage).
2. Éteins la console par le menu. Note l'heure du Mac.
3. Attends **au moins 3 heures**.
4. Branche le chargeur, rallume la console, ouvre le pak **Terminal**, puis :
   ```sh
   sh /mnt/SDCARD/verify-device.sh > /mnt/SDCARD/survey-2-after.txt 2>&1
   ```
   Note aussitôt l'heure du Mac.
5. Réactive le Wi-Fi, récupère le fichier par SSH ou par la carte SD.

## Étape 6 — Informations visibles dans NextUI

Dans `notes.md` : la version de NextUI affichée dans les réglages, le niveau de batterie affiché au moment de chaque relevé, et si l'option « Network time sync » est activée.

## Optionnel — Débit Wi-Fi

Utile pour la décision TLS ([07](../07-securite.md)), pas pour les chemins. Sur le Mac, dans un dossier temporaire : `mkfile 512m test-512.bin && python3 -m http.server 8000` ; sur la console : `time wget -O /mnt/SDCARD/rl-wifitest.bin http://<IP-du-Mac>:8000/test-512.bin && rm /mnt/SDCARD/rl-wifitest.bin`. Débit = 512 / secondes (Mo/s).

## Étape 7 — Nettoyage

```sh
rm /mnt/SDCARD/verify-device.sh /mnt/SDCARD/survey-*.txt
```

Arrête Dropbear si tu ne t'en sers plus.

## Ensuite

1. Commite le contenu de `docs/specs/m0/`.
2. Lance l'agent d'analyse (ou laisse l'orchestrateur le faire) : il confronte les relevés aux specs et à `questions.md`.
3. Tranche les « Décisions à prendre » de [questions.md](questions.md).
