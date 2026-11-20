# ReminisLink — Privacy Policy / Politique de confidentialité

[English](#en) · [Français](#fr)

Published version / Version publiée : <https://reminis.link/privacy/> ·
<https://reminis.link/fr/privacy/>. Keep this file and `site/privacy/`,
`site/fr/privacy/` identical / Garder ce fichier et les pages du site
identiques.

Last updated / Dernière mise à jour : 20 November 2026 / 20 novembre 2026

---

<a id="en"></a>

## English

ReminisLink is an iPhone app that manages the games of your retro handheld
over your local Wi-Fi network. It is built so that your data never leaves
your network, except what you keep in your own iCloud account: the copies of
your saves in iCloud Drive and the pairing of your handhelds in iCloud
Keychain.

### What we collect

**Nothing.** ReminisLink has no account, no server, no analytics, no
advertising, no tracking and no third-party SDK. The developer receives no
data about you or about how you use the app.

### What stays on your iPhone

- **The preferences of the app**, and the list of handhelds paired with an
  older version that must be paired again, in the storage of the app.
- **A cache of box art** shown in the library, which iOS may clear at any
  time.
- **Temporary copies of the files you send** (from Files or the share sheet),
  kept only until the transfer ends.
- **The files you download** from your handheld (a file of a game, a file of
  the SD card), saved only where you choose (Files, AirDrop, another app).

Deleting the app removes its preferences, its cache and the temporary copies.
It does not remove the copies of your saves in iCloud Drive (see below), and
iOS may keep the pairing in the Keychain after the app is deleted: unpair
your handhelds in the app first if you want it removed.

### Pairing of your handhelds (iCloud Keychain)

To pair, the app creates its own identity: an identifier and a private
signing key. For each paired handheld, it keeps the identifier, name and
model of the handheld, the name you gave it, the fingerprint of its
certificate and its last address on your local network, and the name you
chose to show on your handhelds.

All of this is kept in the iOS Keychain, readable only by ReminisLink and its
share extension, and synced by **iCloud Keychain** to your other Apple
devices signed in with the same Apple Account, so that they share one
pairing. iCloud Keychain is Apple’s service, end-to-end encrypted, under your
Apple account and Apple’s privacy policy. If iCloud Keychain is off, these
items stay on this device. The developer never receives them.

Unpairing a handheld in the app removes the pairing from the handheld and
from all these devices. The identity itself is kept, so that your other
pairings keep working.

### What goes to your handheld

ReminisLink talks only to the handhelds you paired, on your local network:
found with Bonjour, then over HTTPS (encrypted) on that network. The app
accepts only the certificate whose fingerprint was in the QR code you
scanned, and signs every request with its private key. Of each paired device,
the handheld keeps on its SD card only its name, its type and its public key,
which cannot be used to sign.

- The files you choose to send (games, saves, patches, cheat files, game
  images, apps for the handheld).
- When pairing, the name you chose (by default the model of the device, such
  as “iPhone”), the identifier and public key of the app, and the platform
  (Apple); with each request, the type of device (iPhone, iPad or Mac). The
  handheld shows the name and the type in its list of paired devices. You can
  remove it there at any time.
- **Links you type or share**: the app sends the link to your handheld, and
  the handheld downloads the file itself (a game, a patch, a cheat file or an
  app for the handheld), from its own connection. The site you link to sees
  the handheld, not the app.

### Copies of your saves (iCloud Drive)

ReminisLink can copy the saves of your handhelds (with their clock files) to
your iPhone: with the Sync Saves button of a handheld, and automatically when
it connects (you can turn this off in the settings of the app). Up to 10
versions of each save are kept.

The copies go to **your own iCloud Drive**, in the ReminisLink folder of the
Files app, where you can see, move or delete them. iCloud is Apple’s service,
under your Apple account and Apple’s privacy policy: the developer has no
server and never receives these copies. If iCloud Drive is off, the copies
stay on your iPhone (Files > On My iPhone > ReminisLink) and are deleted with
the app.

### Permissions

- **Local network**: to find your handheld and talk to it.
- **Camera**: only to scan the QR code shown by your handheld. No picture is
  taken or saved.

The app does not ask for access to your photo library: when you choose a
picture for a game, the iOS picker gives it only the picture you pick.

### Children

ReminisLink collects no data from anyone, children included.

### This website

reminis.link uses no cookies, no analytics, no trackers and loads nothing
from other sites. It is hosted on GitHub Pages, which may keep technical
logs of visits (such as IP addresses) for security, under GitHub’s own
privacy statement.

### Changes

Any change to this policy will be published on this page, with a new date.

### Contact

Open an issue on the repository of the project:
<https://github.com/tfournier/ReminisLink/issues>.

---

<a id="fr"></a>

## Français

ReminisLink est une app iPhone qui gère les jeux de ta console rétro par ton
réseau Wi-Fi local. Elle est conçue pour que tes données ne quittent jamais
ton réseau, sauf ce que tu gardes dans ton propre compte iCloud : les copies
de tes saves dans iCloud Drive et l’appairage de tes consoles dans le
trousseau iCloud.

### Ce que nous collectons

**Rien.** ReminisLink n’a ni compte, ni serveur, ni analytics, ni publicité,
ni pistage, ni SDK tiers. Le développeur ne reçoit aucune donnée sur toi ni
sur ton utilisation de l’app.

### Ce qui reste sur ton iPhone

- **Les préférences de l’app**, et la liste des consoles appairées avec une
  ancienne version qui doivent être appairées de nouveau, dans le stockage de
  l’app.
- **Un cache des jaquettes** affichées dans la bibliothèque, qu’iOS peut
  vider à tout moment.
- **Des copies temporaires des fichiers que tu envoies** (depuis Fichiers ou
  le menu de partage), gardées seulement jusqu’à la fin du transfert.
- **Les fichiers que tu télécharges** depuis ta console (un fichier d’un jeu,
  un fichier de la carte SD), enregistrés seulement là où tu le choisis
  (Fichiers, AirDrop, une autre app).

Supprimer l’app efface ses préférences, son cache et les copies temporaires.
Cela n’efface pas les copies de tes saves dans iCloud Drive (voir plus bas),
et iOS peut garder l’appairage dans le trousseau après la suppression de
l’app : désappaire d’abord tes consoles dans l’app si tu veux qu’il
disparaisse.

### Appairage de tes consoles (trousseau iCloud)

Pour s’appairer, l’app crée sa propre identité : un identifiant et une clé
privée de signature. Pour chaque console appairée, elle garde l’identifiant,
le nom et le modèle de la console, le nom que tu lui as donné, l’empreinte de
son certificat et sa dernière adresse sur ton réseau local, ainsi que le nom
que tu as choisi d’afficher sur tes consoles.

Tout cela est gardé dans le trousseau iOS, lisible seulement par ReminisLink
et son extension de partage, et synchronisé par le **trousseau iCloud** avec
tes autres appareils Apple connectés au même compte Apple, pour qu’ils
partagent un seul appairage. Le trousseau iCloud est le service d’Apple,
chiffré de bout en bout, sous ton compte Apple et la politique de
confidentialité d’Apple. Si le trousseau iCloud est désactivé, ces éléments
restent sur cet appareil. Le développeur ne les reçoit jamais.

Se désappairer d’une console dans l’app retire l’appairage de la console et
de tous ces appareils. L’identité elle-même est gardée, pour que tes autres
appairages continuent de fonctionner.

### Ce qui va vers ta console

ReminisLink ne communique qu’avec les consoles que tu as appairées, sur ton
réseau local : trouvées par Bonjour, puis en HTTPS (chiffré) sur ce réseau.
L’app n’accepte que le certificat dont l’empreinte figurait dans le QR code
scanné, et signe chaque requête avec sa clé privée. De chaque appareil
appairé, la console ne garde sur sa carte SD que son nom, son type et sa clé
publique, qui ne permet pas de signer.

- Les fichiers que tu choisis d’envoyer (jeux, saves, patchs, fichiers de
  cheats, images de jeux, apps pour la console).
- À l’appairage, le nom que tu as choisi (par défaut le modèle de l’appareil,
  par exemple « iPhone »), l’identifiant et la clé publique de l’app, et la
  plateforme (Apple) ; avec chaque requête, le type d’appareil (iPhone, iPad
  ou Mac). La console affiche le nom et le type dans sa liste des appareils
  appairés. Tu peux l’en retirer à tout moment.
- **Les liens que tu saisis ou partages** : l’app envoie le lien à ta
  console, qui télécharge elle-même le fichier (un jeu, un patch, un fichier
  de cheats ou une app pour la console), avec sa propre connexion. Le site du
  lien voit la console, pas l’app.

### Copies de tes saves (iCloud Drive)

ReminisLink peut copier les saves de tes consoles (avec leurs fichiers
d’horloge) sur ton iPhone : avec le bouton Synchroniser les saves d’une
console, et automatiquement à chaque connexion (désactivable dans les
réglages de l’app). Jusqu’à 10 versions de chaque save sont gardées.

Les copies vont dans **ton propre iCloud Drive**, dans le dossier ReminisLink
de l’app Fichiers, où tu peux les voir, les déplacer ou les supprimer. iCloud
est le service d’Apple, sous ton compte Apple et la politique de
confidentialité d’Apple : le développeur n’a aucun serveur et ne reçoit
jamais ces copies. Si iCloud Drive est désactivé, les copies restent sur ton
iPhone (Fichiers > Sur mon iPhone > ReminisLink) et sont supprimées avec
l’app.

### Autorisations

- **Réseau local** : pour trouver ta console et communiquer avec elle.
- **Appareil photo** : uniquement pour scanner le QR code affiché par ta
  console. Aucune photo n’est prise ni enregistrée.

L’app ne demande pas l’accès à ta photothèque : quand tu choisis une image
pour un jeu, le sélecteur d’iOS ne lui donne que l’image choisie.

### Enfants

ReminisLink ne collecte aucune donnée, sur personne, enfants compris.

### Ce site

reminis.link n’utilise ni cookie, ni mesure d’audience, ni traqueur, et ne
charge rien depuis d’autres sites. Il est hébergé par GitHub Pages, qui peut
garder des journaux techniques des visites (comme les adresses IP) pour sa
sécurité, selon la déclaration de confidentialité de GitHub.

### Modifications

Toute modification de cette politique sera publiée sur cette page, avec une
nouvelle date.

### Contact

Ouvre une issue sur le dépôt du projet :
<https://github.com/tfournier/ReminisLink/issues>.
