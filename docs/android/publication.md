# Publishing the Android app

What the owner does to publish ReminisLink for Android (milestone M21,
docs/specs/09-feuille-de-route.md): GitHub releases and F-Droid first,
Google Play next. Nothing in this repository publishes anything by itself
except the release workflow, on a tag the owner pushes. The app has no
proprietary Google dependency (no Play Services, ML Kit or Firebase), so the
same build goes everywhere.

The release procedure (version, tag, CI) is in [android/README.md](../../android/README.md#release).

## 1. Upload key (once)

One key signs every APK of the GitHub releases (and is the "upload key" of
Google Play). Keep it outside the repository, with a backup: an app signed
with another key cannot update an installed one.

```sh
keytool -genkeypair -v -keystore ~/reminislink-upload.jks \
  -alias reminislink -keyalg RSA -keysize 4096 -validity 10000 \
  -dname "CN=ReminisLink, O=Thomas Fournier"
```

`keytool` asks for the keystore password and the key password (use two
strong passwords, kept in a password manager). Then:

- **Local signed builds**: put the four values in `~/.gradle/gradle.properties`
  (never in the repository):

  ```properties
  reminislink.keystore=/Users/<you>/reminislink-upload.jks
  reminislink.keystorePassword=...
  reminislink.keyAlias=reminislink
  reminislink.keyPassword=...
  ```

  or export `RL_ANDROID_KEYSTORE`, `RL_ANDROID_KEYSTORE_PASSWORD`,
  `RL_ANDROID_KEY_ALIAS`, `RL_ANDROID_KEY_PASSWORD`. `make android-release-apk`
  then writes a signed `android/app/build/outputs/apk/release/app-release.apk`
  (unsigned `app-release-unsigned.apk` without them).

- **GitHub secrets** (repository Settings > Secrets and variables > Actions):

  | Secret | Value |
  |---|---|
  | `ANDROID_KEYSTORE_BASE64` | `base64 -i ~/reminislink-upload.jks` (one line) |
  | `ANDROID_KEYSTORE_PASSWORD` | the keystore password |
  | `ANDROID_KEY_ALIAS` | `reminislink` |
  | `ANDROID_KEY_PASSWORD` | the key password |

  Without them the release workflow still runs, but signs the APK with a
  throwaway debug key and names it `ReminisLink-android-<version>-debug-signed.apk`,
  with a warning in the release notes: such an APK must not be installed
  for daily use (it cannot be updated by a properly signed one).

- The certificate's SHA-256 (for F-Droid's reproducible builds and to check
  a downloaded APK):

  ```sh
  keytool -list -v -keystore ~/reminislink-upload.jks -alias reminislink | grep SHA256
  ```

## 2. GitHub release

1. Write the notes of the version, English and French:
   `android/app/fastlane/metadata/android/en-US/changelogs/<versionCode>.txt`
   and `fr-FR/...` (500 characters at most; `scripts/android-version-code.sh X.Y.Z`
   prints the versionCode).
2. `make android-vX.Y.Z` (or `android-vX.Y.Z-beta.N`) on `main`: writes
   `android/version.properties`, commits, tags and pushes.
3. The workflow *Android release* builds and publishes the release:
   never "Latest" (the NextUI Pak Store reads the latest release, which stays
   the pak's), "Pre-release" for a `-beta.N` / `-rc.N` tag.

## 3. F-Droid

F-Droid builds the app itself from the tag, with its own key, or publishes
the developer-signed APK when its build is identical (reproducible builds).

1. Fork <https://gitlab.com/fdroid/fdroiddata>, add
   [`link.reminis.app.yml`](link.reminis.app.yml) as `metadata/link.reminis.app.yml`
   (check the version, versionCode and commit of the first tag), and open a
   merge request ("New app: ReminisLink"). The F-Droid CI runs `fdroid lint`
   and a test build.
2. The listing (title, descriptions, changelogs, icon, screenshots) is read
   from `android/app/fastlane/metadata/android/` at each tag: nothing to copy.
   fdroidserver takes a fastlane folder outside the repository root only
   when its parent is the recipe's `subdir` (`android/app`): keep both in
   step if the module moves.
3. New versions are found by the tag pattern `android-vX.Y.Z` (pre-release
   tags are ignored) and `android/version.properties`.

What F-Droid checks, and the state here:

| Check | State |
|---|---|
| Free licence, source available | MIT, GitHub |
| No proprietary dependency | AndroidX, Kotlin, kotlinx (coroutines, serialization), OkHttp, ZXing core, Tink (pure Java part): all Apache 2.0; no Play Services, Firebase, ML Kit |
| No network at build time but the Maven repositories | Gradle fetches from Google Maven and Maven Central only; the build reads `protocol/test-vectors/` for the tests only |
| Binaries in the source | `android/gradle/wrapper/gradle-wrapper.jar` (deleted by the recipe, F-Droid uses its own Gradle); made-up disc images in `testdata/` (pak tests, ignored by the recipe) |
| Dependency metadata blob | removed (`dependenciesInfo`), and no VCS info in the APK (`vcsInfo.include = false`) |
| Anti-features | none expected: no tracking, no ads, no non-free network service (the app only talks to the user's own handheld on the local network) |
| Signing | F-Droid's key by default; with `Binaries` and `AllowedAPKSigningKeys`, the release key if the builds are identical |

Possible remarks from the reviewers:

- **Reproducibility**: the GitHub APK is built on Ubuntu with JDK 21 and
  build-tools 35.0.0, F-Droid's with its own JDK 17 and SDK. Two local builds
  of the same commit give the same unsigned APK; a match with F-Droid's build
  is only known once it runs (start without `Binaries` if it does not match:
  F-Droid then signs with its own key, and its APK and GitHub's cannot update
  each other).
- **"Games" category with ROM handling**: the description says the app
  provides no game and no source of games; the user brings their own files.
- **Trademarks**: product names appear only to say which handhelds work.

## 4. Google Play

Play needs a developer account (the owner's) and Play App Signing: Google
keeps the app signing key, and the key of section 1 is only the **upload
key** (if it is lost, Google can reset it; the app signing key is never at
risk). F-Droid and GitHub APKs are signed by other keys: Play installs
cannot update them, and the other way round.

Checklist:

1. **Create the app**: name ReminisLink, default language English (United
   States), app, free.
2. **App signing**: accept Play App Signing (Google-generated key). Upload
   the first release as an **AAB**: `cd android && ./gradlew :app:bundleRelease`
   (signed with the upload key through the same properties as above), in
   `android/app/build/outputs/bundle/release/app-release.aab`. Start with the
   internal testing track.
3. **Store listing**: title, short and full description from
   `android/app/fastlane/metadata/android/<locale>/` (English and French; Play
   shows `<b>`, `<i>` and line breaks, not `<p>`/`<ul>`: paste the text and
   replace the list items with "• " lines), icon `images/icon.png` (512 x
   512), screenshots `images/phoneScreenshots/` (made from the demo mode by
   `android/scripts/make-screenshots.sh`), feature graphic
   `images/featureGraphic.png` (1024 x 500, no alpha, no text: the icon on
   the gradient of the app icon; the same file in `en-US` and `fr-FR`, also
   the name F-Droid reads). Category: Tools. Contact email and website
   <https://reminis.link>.
4. **Privacy policy**: <https://reminis.link/privacy/>. It covers the
   Android app (docs/privacy.md and `site/`, sections "On your Android
   device" and "Copies of your saves and save states (Android)"): the key
   in the Android Keystore, the pairings and transfers kept on the phone, no
   backup (`allowBackup` off), the synced saves (app storage or a folder the
   user picks), camera for the QR code, the notification of transfers.
   Check that the published page matches before submitting.
5. **Data safety**. Read the current definitions first: *Provide information
   for Google Play's Data safety section*,
   <https://support.google.com/googleplay/android-developer/answer/10787469>
   (sections "Data collection" and "Data sharing", with their lists of
   exceptions). There is **no general exemption** for data sent to a device
   the user owns: Google defines *collection* as transmitting data from the
   app off the user's device, and then lists narrow cases that need not be
   disclosed (for example data processed only ephemerally); check whether
   one of them, as worded on the day of the submission, really applies.

   What leaves the phone, and where it goes:

   | Data | Where | Kept there | Developer or third party? |
   |---|---|---|---|
   | Files the user picks or shares (games, saves, states, cheats, patches, box art, pak archives) | the user's own handheld, on the local network, TLS pinned to its certificate | yes, on its SD card (that is the point) | never |
   | Pairing: a random client id (UUID), the public key, the client name (device model by default, editable), the platform (`android`) | the handheld | yes, in its list of devices | never |
   | Signed requests (time, nonce) | the handheld | the nonce for a few minutes (replay protection) | never |
   | Synced saves and states, with their small manifests (`SyncManager`) | by default the app's private storage (`filesDir/SyncedSaves`, does not leave the phone); or a folder the user picks with the system folder picker (SAF, `SafVaultStore`), on the phone or at a cloud storage provider offered by the picker | yes, 10 versions per save and per state slot; a user-picked folder survives uninstallation | never by the app; a cloud provider only if the user picks one of its folders, under that provider's terms |

   Nothing goes to the developer, to Google or to any other service, and the
   app has no analytics, crash reporting or advertising SDK. The only data
   that can leave the phone other than to the handheld is the copies of the
   saves, and only when the user picks a cloud provider's folder for them:
   the app writes to the folder through Android's Storage Access Framework,
   and the provider, chosen by the user, uploads it. The app never opens a
   connection to that provider. Treat this as a user-initiated transfer to
   a destination the user chose (check the wording of the "Data sharing"
   exceptions on the day of the submission); it changes neither answer set
   below, but the privacy policy says it (section "Copies of your saves and
   save states (Android)").

   Two honest answer sets:

   - **Cautious (recommended unless an exception clearly applies)**: *Does
     your app collect or share any of the required user data types?* Yes.
     Collected, **not shared**: *Files and docs* (the files the user sends
     to the handheld) and *Device or other IDs* (the pairing identifier and
     client name). For each: processed ephemerally: No (stored on the
     handheld); purpose: *App functionality*. *Files and docs* is
     **optional**: a user can pair, browse the library and download saves
     without ever sending a file, and a file is sent only when the user
     picks or shares it (Play's "users can choose whether this data is
     collected"). *Device or other IDs* is **required**: pairing sends the
     identifier and name, and nothing works without a pairing. *Is all of the user data
     collected by your app encrypted in transit?* Yes. *Do you provide a
     way for users to request that their data be deleted?* The data is on
     the user's own handheld, not with the developer: deleting files and
     forgetting the phone are done on the handheld or from the app (trash,
     Forget, Settings > Devices on the handheld); answer according to the
     form's wording and explain this in the privacy policy.
   - **"No data collected"**: only if, after reading the page above, the
     owner concludes that transfers to a device the user controls, never
     reaching the developer or a third party, fall outside Google's
     definition or one of its listed exceptions. Say so in the privacy
     policy in the same words. If in doubt, take the cautious set:
     declaring more than needed is not a policy violation, declaring less is.

   Either way, *shared*: No (no data goes to a third party; a transfer the
   user starts to their own device is not sharing by Google's definition of
   a third party, but check the "Data sharing" exceptions too).
6. **Content rating** (IARC questionnaire): category "Utility, productivity,
   communication or other"; no violence, sexuality, gambling, user-generated
   content shared with others, location sharing or purchases: rating
   "Everyone" / PEGI 3 expected.
7. **Target audience and content**: 18 and over (or 13+); not designed for
   children; no ads (*Contains ads: No*).
8. **App access** (for the reviewers): "All functionality is available
   without special access" is wrong (pairing needs a handheld): choose
   *All or some functionality is restricted* and give these instructions:

   > ReminisLink is the companion of a retro handheld running the free
   > ReminisLink tool. To review without the hardware, tap "Try Without a
   > Console" on the first screen: a simulated console lets you try every
   > screen (library, saves, states, cheats, patches, apps, transfers, trash)
   > with made-up games. Nothing leaves the phone; "Quit" in its banner
   > returns to the first screen. Pairing with a real handheld (QR code)
   > needs the device. No account or login is needed.

9. **Permissions declarations**: camera (QR code, asked when scanning),
   notifications (transfers), foreground service *data sync* (Play asks
   for a declaration of `FOREGROUND_SERVICE_DATA_SYNC`: "uploads and
   downloads started by the user between the phone and their handheld,
   shown in a notification with progress, stopped when the queue is empty";
   a short video of a transfer may be requested).
10. **Government apps, financial features, health, news**: No.
11. **Release**: internal testing, then closed testing (a new personal
    account must run a closed test with at least 12 testers for 14 days
    before production), then production.
