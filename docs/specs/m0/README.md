# M0 — Relevés sur la console

But : remplacer toutes les mentions **[À VÉRIFIER M0]** des spécifications par des faits observés sur une vraie console. Ce jalon est **fait par une personne**, avec la console en main. Compter environ 45 minutes.

Ensuite, un agent (prompt `prompts/m0-analyse-releves.md`) lit les résultats déposés ici, met à jour les specs et crée la carte SD factice des tests.

## Ce qu'il faut à la fin

Dans ce dossier `docs/specs/m0/` :

| Fichier | Contenu | Étape |
|---|---|---|
| `survey-1-before.txt` | Sortie du script, avant de jouer | 2 |
| `survey-2-after.txt` | Sortie du script, après avoir joué | 4 |
| `notes.md` | Le modèle rempli (débit, veille, observations) | 3, 5, 6, 7 |
| `screenshots/` (optionnel) | Photos d'écran utiles (menu des states, etc.) | — |

`questions.md` est rempli par l'agent d'analyse, pas par toi.

## Matériel

- La TrimUI Brick sous NextUI, chargée, avec le Wi-Fi activé et connecté au même réseau que le Mac.
- Un accès shell à la console : le pak **Dropbear Server (SSH)** (Pak Store, catégorie File Management) ou le pak **Terminal**.
- Un jeu Game Boy ou GBA à toi, déjà sur la carte, qui gère les saves internes.
- Le Mac, avec le dépôt ReminisLink.

## Étape 1 — Copier le script sur la console

Au choix :

- **Par SSH** (Dropbear lancé sur la console, IP affichée par le pak) :
  ```sh
  scp scripts/verify-device.sh root@<IP-de-la-console>:/mnt/SDCARD/verify-device.sh
  ```
- **Par la carte SD** : éteins la console, copie `scripts/verify-device.sh` à la racine de la carte, remets-la.

Les identifiants SSH sont ceux indiqués dans le README du pak Dropbear.

## Étape 2 — Premier relevé (avant de jouer)

Sur la console (SSH ou Terminal) :

```sh
sh /mnt/SDCARD/verify-device.sh > /mnt/SDCARD/survey-1-before.txt 2>&1
```

Le script ne fait que lire. Récupère le fichier sur le Mac (`scp root@<IP>:/mnt/SDCARD/survey-1-before.txt docs/specs/m0/`) ou par la carte SD.

## Étape 3 — Débit Wi-Fi et carte SD

Objectif : savoir combien de temps prend l'envoi d'une ROM de 512 Mo, et si c'est le Wi-Fi ou la carte SD qui limite.

1. **Écriture sur la carte SD seule** (sur la console) :
   ```sh
   time dd if=/dev/zero of=/mnt/SDCARD/rl-sdtest.bin bs=1M count=256 conv=fsync
   rm /mnt/SDCARD/rl-sdtest.bin
   ```
   Note la durée → débit = 256 / secondes (Mo/s).

2. **Téléchargement Mac → console par le Wi-Fi** :
   - sur le Mac, dans un dossier temporaire :
     ```sh
     mkfile 512m test-512.bin        # ou : dd if=/dev/urandom of=test-512.bin bs=1m count=512
     python3 -m http.server 8000
     ```
   - sur la console :
     ```sh
     time wget -O /mnt/SDCARD/rl-wifitest.bin http://<IP-du-Mac>:8000/test-512.bin
     rm /mnt/SDCARD/rl-wifitest.bin
     ```
   Note la durée → débit = 512 / secondes (Mo/s). Fais-le deux fois, la console près de la box puis à l'endroit où tu joues d'habitude.

3. Si `iperf3` existe sur la console (le script l'indique dans « Tools available »), mesure aussi le débit réseau pur : `iperf3 -s` sur le Mac, `iperf3 -c <IP-du-Mac> -t 10` sur la console.

Reporte les résultats dans `notes.md`.

## Étape 4 — Jouer, puis second relevé

Cette étape révèle où NextUI range les saves et les states, comment il les nomme, et quel emplacement sert à la reprise automatique.

1. Lance ton jeu de test. Joue jusqu'à pouvoir **sauvegarder dans le jeu** (save interne), et fais-le.
2. Dans le menu de NextUI en jeu : **enregistre un state dans l'emplacement 1**, puis un autre dans l'**emplacement 2**. Si le menu affiche une vignette de l'état, note-le.
3. Quitte le jeu par le menu (pas en éteignant).
4. Relance le même jeu, joue 30 secondes, puis **éteins la console en jeu** avec le bouton d'alimentation (c'est ce qui déclenche la sauvegarde de reprise).
5. Rallume la console, relance l'accès shell, puis :
   ```sh
   sh /mnt/SDCARD/verify-device.sh > /mnt/SDCARD/survey-2-after.txt 2>&1
   ```
6. Récupère `survey-2-after.txt` dans `docs/specs/m0/`.

Dans `notes.md`, indique le nom exact du fichier ROM utilisé et le dossier du système, pour que l'agent retrouve ses fichiers dans les relevés.

## Étape 5 — Veille et Wi-Fi

Objectif : savoir si le pak peut rester joignable pendant un long transfert.

1. Sur le Mac : `ping <IP-de-la-console>` dans un terminal, laissé ouvert.
2. **Veille automatique pendant un pak** : ouvre un pak d'outil (par exemple Cheat Manager) et ne touche plus à rien. Note après combien de temps l'écran s'éteint ou la console se met en veille, et si le ping continue.
3. **Veille manuelle** : appuie brièvement sur le bouton d'alimentation pour mettre en veille. Le ping s'arrête-t-il ? Au réveil, combien de secondes avant qu'il reprenne ? L'IP a-t-elle changé ?
4. Regarde dans les réglages de NextUI s'il existe un délai de mise en veille ou d'extinction de l'écran, et sa valeur.

Reporte tout dans `notes.md`.

## Étape 6 — Informations visibles dans NextUI

Dans `notes.md` : la version de NextUI affichée dans les réglages, le nom du réseau Wi-Fi tel qu'affiché, le niveau de batterie affiché au moment du second relevé (pour le comparer à la valeur lue par le script).

## Étape 7 — Nettoyage

```sh
rm /mnt/SDCARD/verify-device.sh /mnt/SDCARD/survey-*.txt
```

Arrête le pak Dropbear si tu ne t'en sers plus.

## Ensuite

1. Commite le contenu de `docs/specs/m0/`.
2. Lance l'agent d'analyse (`prompts/m0-analyse-releves.md`), ou laisse l'orchestrateur le faire.
3. Réponds aux questions qu'il aura listées dans `questions.md`, si les relevés n'ont pas tout tranché.
