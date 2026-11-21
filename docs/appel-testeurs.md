# Appel aux testeurs : ReminisLink sur d'autres consoles

ReminisLink fonctionne aujourd'hui sur la **TrimUI Brick avec NextUI**. Une version expérimentale tourne aussi, en théorie, sur :

- **NextUI** : TrimUI Smart Pro S (`tg5050`), Anbernic RG28XX, RG34XX, RG35XX Plus/H/Pro/SP, RG40XX H/V, RG CubeXX (`h700`), Miyoo Flip (`my355`) ;
- **Knulli** (par exemple sur une Anbernic H700 ou une TrimUI).

Nous n'avons pas ces consoles. Le pak a été construit et testé sans elles : organisation de la carte reconstituée à partir du code source de chaque firmware, serveur lancé sous émulation dans le système de chaque console. L'écran, les boutons et le Wi-Fi n'ont jamais été essayés. **Ces consoles ne sont donc pas prises en charge** tant qu'une personne qui en possède une ne les a pas validées.

## Ce qu'il faut

- Une des consoles ci-dessus, avec NextUI ou Knulli à jour, et une carte SD **dont vous avez une copie** (ReminisLink garde une copie de tout ce qu'il remplace ou supprime, mais c'est une version de test).
- Un iPhone avec l'app ReminisLink (ou l'app Android de test), sur le même Wi-Fi que la console.

## Récupérer le pak

- **NextUI** (`tg5050`, `h700`, `my355`) : le même `ReminisLink.pak.zip` que pour la Brick. La version expérimentale est publiée comme **pré-version** (`vX.Y.Z-beta.N`) sur https://github.com/tfournier/ReminisLink/releases : le Pak Store ne propose pas les pré-versions, il faut l'installer à la main (ci-dessous).
- **Knulli** : `ReminisLink.knulli.zip` n'est pas encore publié dans les versions. Il est construit à chaque passage de l'intégration continue : ouvrez https://github.com/tfournier/ReminisLink/actions/workflows/ci.yml, choisissez le dernier passage réussi sur la branche `main`, puis, en bas de la page, l'artefact **reminislink-knulli** (il faut être connecté à GitHub ; GitHub le livre dans un zip qui contient `ReminisLink.knulli.zip` ; les artefacts expirent au bout de 90 jours). Ou demandez-le dans un ticket.

## Installer

- **NextUI** : créez le dossier `/Tools/<plateforme>/ReminisLink.pak/` sur la carte (`<plateforme>` : `tg5050`, `h700` ou `my355`) et décompressez `ReminisLink.pak.zip` dedans, pour obtenir `/Tools/<plateforme>/ReminisLink.pak/launch.sh`. Sur Mac, le Finder décompresse le zip dans un nouveau dossier `ReminisLink.pak` : copiez ce dossier tel quel dans `/Tools/<plateforme>/`. Si vous obtenez `ReminisLink.pak/ReminisLink.pak/launch.sh`, remontez le dossier intérieur d'un niveau. Ouvrez **Outils > ReminisLink**.
- **Knulli** : décompressez `ReminisLink.knulli.zip` dans `/userdata/roms/tools/` (vous obtenez `ReminisLink.sh` et un dossier caché `.ReminisLink`). Mettez à jour la liste des jeux, puis ouvrez **Outils > ReminisLink**.

## À essayer (dans l'ordre)

1. Le pak s'ouvre : le QR code s'affiche, les boutons répondent (A, Y, B), B ferme le pak.
2. L'app trouve la console et s'appaire avec le QR code.
3. La bibliothèque montre vos systèmes et vos jeux (jaquettes comprises) ; le tableau de bord montre la batterie et le Wi-Fi.
4. Envoyez un petit jeu depuis l'app, puis lancez-le sur la console.
5. Faites une copie de secours d'une save, jouez, sauvegardez en jeu, restaurez la copie : la partie revient.
6. Verrouillez un state, écrasez-le en jeu, rouvrez le pak : il revient.
7. Supprimez un jeu de test : il part dans la corbeille de ReminisLink.

## Nous faire un retour

Ouvrez un ticket sur https://github.com/tfournier/ReminisLink/issues avec :

- la console, le firmware et sa version (NextUI : écran des réglages ; Knulli : menu Système) ;
- ce qui marche et ce qui ne marche pas, dans la liste ci-dessus ;
- les journaux du pak : `ReminisLink.txt` et `ReminisLink.stderr.txt` dans `/.userdata/<plateforme>/logs/` (NextUI) ou `/userdata/system/logs/` (Knulli). Ils ne contiennent ni le secret d'appairage ni clé privée ; relisez-les quand même avant de les joindre (le nom de la console et celui de votre réseau Wi-Fi peuvent y figurer).

Merci ! Chaque console sera annoncée comme prise en charge après la validation d'au moins une personne qui la possède.
