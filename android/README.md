# ReminisLink for Android

The Android app of ReminisLink (milestone M21): Kotlin, Jetpack Compose,
Android 8.0 (API 26) and later. Its specification is
[docs/specs/10-app-android.md](../docs/specs/10-app-android.md) (French); the
API contract is [protocol/openapi.yaml](../protocol/openapi.yaml).

| Module | Content |
|---|---|
| `core/` | Pure Kotlin/JVM: protocol models, signing, pairing, pinned TLS client, transfers, event stream (SSE), the demo mode's fake pak |
| `app/` | The Compose app, Android Keystore identity, discovery (NSD), transfer service |
| `app/fastlane/metadata/android/` | Store listing (F-Droid, Google Play): texts, changelogs, icon, screenshots |
| `scripts/` | `pak-e2e.sh` (tests against the real pak), `make-screenshots.sh` (store screenshots from the demo mode) |

No proprietary Google dependency: the app builds and runs without Play
Services, so it can be published on F-Droid.

## Build and test

Needs a JDK 17+ (21 recommended) and the Android SDK (`ANDROID_HOME`, or
`android/local.properties`).

```sh
make android-test          # :core and :app unit tests (shared test vectors read in place)
make android-build         # debug APK
make android-release-apk   # release APK (R8), signed if the release key is configured, unsigned otherwise
cd android && ./gradlew connectedDebugAndroidTest   # instrumented tests (emulator API 35)
android/scripts/pak-e2e.sh                          # against the real headless pak (emulator running)
cd android && ./gradlew connectedMinifiedAndroidTest -Preminislink.testBuildType=minified \
  -Pandroid.testInstrumentationRunnerArguments.class=link.reminis.app.ReleaseSmokeTest   # smoke test of the R8 build
android/scripts/make-screenshots.sh                 # store screenshots, en-US and fr-FR (emulator running)
```

The demo mode ("Try Without a Console") runs the whole app against a fake
pak in the process, without any network: useful to try a screen without a
handheld.

## Release

Versions follow SemVer, numbered on their own (CLAUDE.md rule 6): tags
`android-vX.Y.Z`, pre-releases `android-vX.Y.Z-beta.N` / `-rc.N` (the number
of the release they prepare). The version lives in `version.properties`
(`versionName`, and `versionCode` derived from it by
`scripts/android-version-code.sh`: `X*1000000 + Y*10000 + Z*100 + 99`, a
`beta.N` ending in `N`, an `rc.N` in `50 + N`, so a pre-release always comes
before its release; the build refuses a mismatch). F-Droid reads both values
from that file.

1. Pick the number from the changes since the last Android release.
2. Write the notes, English and French, in
   `app/fastlane/metadata/android/en-US/changelogs/<versionCode>.txt` and
   `fr-FR/changelogs/<versionCode>.txt` (500 characters at most), and commit them.
3. On `main`, clean tree: `make android-vX.Y.Z` (or `make android-vX.Y.Z-beta.N`).
   It writes `version.properties`, commits, tags and pushes.
4. The workflow `.github/workflows/android-release.yml` checks the tag
   against `version.properties` and the changelogs, runs the unit tests,
   builds the release APK and attaches it to a GitHub release that is never
   marked "Latest" (the NextUI Pak Store reads the latest release, which
   stays the pak's); a `-beta.N` / `-rc.N` tag makes a pre-release. Android
   tags never start the pak's workflow (`ci.yml`, tags `v*`) and pak tags
   never start this one (checked by `make release-checks`).

Signing: the release key is read from environment variables
(`RL_ANDROID_KEYSTORE`, `RL_ANDROID_KEYSTORE_PASSWORD`, `RL_ANDROID_KEY_ALIAS`,
`RL_ANDROID_KEY_PASSWORD`) or Gradle properties in `~/.gradle/gradle.properties`
(`reminislink.keystore`, `reminislink.keystorePassword`, `reminislink.keyAlias`,
`reminislink.keyPassword`); never from the repository. In the CI, from the
secrets `ANDROID_KEYSTORE_BASE64`, `ANDROID_KEYSTORE_PASSWORD`,
`ANDROID_KEY_ALIAS` and `ANDROID_KEY_PASSWORD`; without them the APK is
signed with a debug key and named `…-debug-signed.apk`. Creating the key,
F-Droid and Google Play: [docs/android/publication.md](../docs/android/publication.md).

R8 shrinks and optimizes the release build (`app/proguard-rules.pro`); keep
`app/build/outputs/mapping/release/mapping.txt` of each release (the CI
uploads it as an artifact) to read crash stack traces.
