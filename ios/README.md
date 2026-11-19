# ReminisLink for iOS

| Path | Content |
|---|---|
| `ReminisLink.xcodeproj` | App, share extension and UI tests (hand-written project, see below) |
| `Config/Config.xcconfig` | Identifier prefix, team and versions shared by every target |
| `Config/*-Info.plist`, `Config/*.entitlements` | Info.plist keys (launch screen included) and capabilities of docs/specs/06-app-ios.md §3 |
| `Config/ExportOptions.plist` | Options of `xcodebuild -exportArchive` for App Store Connect (see "Release") |
| `ReminisLink/` | SwiftUI app: `App/` (state, pairing, connections, errors), `Views/`, `Resources/` (String Catalogs EN + FR, assets, privacy manifest) |
| `ReminisLinkShare/` | Share extension: copies shared files and links into the app group inbox, then opens the app on Add (it never uploads) |
| `Design/` | SVG sources of the app icon and of the launch screen mark, and `make-icons.sh` that renders them into the asset catalog |
| `AppStore/` | Texts of the App Store page (English and French) and notes for App Review |
| `ReminisLinkUITests/` | XCUITests of the main paths (pairing, add a file, delete, save, states, cheats, patch) |
| `ReminisLinkKit/` | Swift package: `ReminisLinkKit` (signing, pairing, API client, discovery, stores, `Transfers/`: resumable uploads, streamed SHA-256, queue, share inbox; `CheatEditor`) and `ReminisLinkDemo` (fake pak, M4, M5 and M6 routes included) |

## Build and test

```sh
cd ios/ReminisLinkKit && swift test
cd ios && xcodebuild test -scheme ReminisLink -destination 'platform=iOS Simulator,name=iPhone 16'
```

No Apple Developer account is needed for the simulator. To run on a device,
set `DEVELOPMENT_TEAM` in `Config/Local.xcconfig` (see "Release" below).

## Release (TestFlight and App Store)

The identifiers are fixed in `Config/Config.xcconfig` (app `link.reminis.app`,
from the product domain reminis.link); the signing team is not in the
repository. Nothing is ever uploaded by a script of the repository.

### 1. Identifiers and team

Find your Team ID (developer.apple.com > Account > Membership details) and
put it in `Config/Local.xcconfig` (ignored by Git, included last by
`Config.xcconfig`):

```
DEVELOPMENT_TEAM = ABCDE12345
```

The identifiers are the app `link.reminis.app`, the extension
`link.reminis.app.share`, the app group `group.link.reminis.app` and the
Keychain group `<Team ID>.link.reminis.app.shared`. Never change them once
the app is published: a new identifier is a new app for the App Store, and
installed copies lose their pairings. The seller name shown on the App Store
is the one of the account that publishes the app, not the identifier.

Signing is automatic. Open the project in Xcode once, select the
`ReminisLink` and `ReminisLinkShare` targets in *Signing & Capabilities*
and check that Xcode registered the two App IDs and the App Group without
error (it may ask to sign in with the Apple Developer account in *Settings >
Accounts*).

### 2. App Store Connect record

In App Store Connect, *Apps > + > New App*: platform iOS, name ReminisLink,
primary language, the bundle id `<prefix>.reminislink`, any SKU. Fill the
page with the texts of `AppStore/` (see `AppStore/README.md`), the URL of
the privacy policy (step 5) and "Data Not Collected" for App Privacy.

### 3. Versions

`MARKETING_VERSION` (shown on the App Store, `1.0`) and
`CURRENT_PROJECT_VERSION` (build number, `1`) are in `Config/Config.xcconfig`
and shared by the app and the extension. Increase the build number for every
upload, the version for every release.

### 4. Archive and upload

In Xcode: destination *Any iOS Device (arm64)*, *Product > Archive*, then in
the Organizer *Distribute App > App Store Connect > Upload*. The build shows
in TestFlight after Apple's processing; add testers there.

Or from the command line (the upload is a separate, explicit step):

```sh
cd ios
xcodebuild archive -scheme ReminisLink -destination 'generic/platform=iOS' \
  -archivePath build/ReminisLink.xcarchive -allowProvisioningUpdates
xcodebuild -exportArchive -archivePath build/ReminisLink.xcarchive \
  -exportOptionsPlist Config/ExportOptions.plist -exportPath build/export \
  -allowProvisioningUpdates
# build/export/ReminisLink.ipa: upload it with the Transporter app, or set
# "destination" to "upload" in a copy of ExportOptions.plist.
```

Check that the app archives without an account or signing (what the CI
can do):

```sh
cd ios
xcodebuild archive -scheme ReminisLink -destination 'generic/platform=iOS' \
  -archivePath build/ReminisLink.xcarchive CODE_SIGNING_ALLOWED=NO
```

At the first upload, App Store Connect asks about encryption: the app uses
HMAC-SHA256 and HKDF (CryptoKit) to authenticate its requests to the
console, and sends them over plain HTTP on the local network. Answer the
questions (and, once the answer is known, add
`ITSAppUsesNonExemptEncryption` to `Config/ReminisLink-Info.plist` so they
are not asked again).

### 5. Privacy policy

The privacy policy (`docs/privacy.md`, English and French) is published on
the website of the project, built from `site/` by GitHub Pages (setup and
DNS in `site/README.md`). In App Store Connect:

- Privacy Policy URL: `https://reminis.link/privacy/`
- Support URL: `https://reminis.link/support/`

Check that both open over HTTPS before submitting.

### 6. App Review

The demo mode exists in Debug builds only: a reviewer cannot use the app
without a console. Attach a video of the whole flow and complete
`AppStore/review_notes.txt` in *App Review Information > Notes*.

### App icon and launch screen

The icon (two links and a pixel trail, no brand) is drawn in
`Design/AppIcon/*.svg`: light, dark and tinted appearances, and the launch
screen mark. After a change, run `Design/make-icons.sh` (AppKit renders the
SVG, nothing to install) and commit the SVG and the PNG files together. The
launch screen is the `UILaunchScreen` key of `Config/ReminisLink-Info.plist`
(colour `LaunchBackground`, image `LaunchLogo`).

## The Xcode project

`project.pbxproj` is written by hand on purpose (no XcodeGen or Tuist to
install). The source folders are file-system synchronized groups: adding or
removing a file under `ReminisLink/`, `ReminisLinkShare/` or
`ReminisLinkUITests/` needs no project change. Build settings common to all
targets are in `Config/Config.xcconfig`.

## Demo mode (no console, no network)

Launch with the environment variable `RL_DEMO=1` (already in the scheme,
disabled: tick it in *Edit Scheme > Run > Arguments*) or the argument
`-RLDemo YES`. The app then talks to the fake pak of `ReminisLinkDemo`
through a `URLProtocol`, keeps its keys in memory and sees one "Demo Brick".
Pair it with *Add a Console > Pair with the Demo Console*, or open its link:

```sh
xcrun simctl openurl booted 'reminislink://pair?id=5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0&n=Demo%20Brick&s=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8&v=1'
```

The fake pak checks signatures like the real one; its game names are made up.
Its PlayStation system has no emulator (`emulatorInstalled: false`): the
library shows it with "Emulator not installed on the console", and it can
still be managed. A pak that does not send the field is taken as having the
emulator.

## Against the pak on the Mac (`make headless`)

1. At the repository root, run `make headless` (milestone M2). It announces
   `_reminislink._tcp` with Bonjour and prints the pairing link.
2. Run the `ReminisLink` scheme on the simulator **without** `RL_DEMO`. The
   simulator uses the Mac's network: the app finds the pak with Bonjour.
3. Open the pairing link printed by the pak:
   `xcrun simctl openurl booted '<link>'`.

The UI test against the real pak is skipped unless the link is given
(`TEST_RUNNER_` variables are passed to the test runner):

```sh
TEST_RUNNER_RL_UITEST_PAIR_URL='reminislink://pair?...' \
  xcodebuild test -scheme ReminisLink -destination 'platform=iOS Simulator,name=iPhone 16'
```

It launches the app with `RL_RESET_STORE=1`, which forgets the consoles
paired before. The pak is found with Bonjour only (the `h`/`p` of the link
are ignored), so it must be announced on the Mac's network.

The same link also enables the "add a file" test against the pak
(`AddFileUITests`): the app generates a file at launch
(`RL_UITEST_SHARE_FILE=<name>:<bytes>`, Debug only) and receives it as the
share extension would, then uploads it, checks it in the library, deletes
it and empties the trash. `TEST_RUNNER_RL_UITEST_SHARE_BYTES` sets its size
(default 8 MiB):

```sh
TEST_RUNNER_RL_UITEST_PAIR_URL='reminislink://pair?...' TEST_RUNNER_RL_UITEST_SHARE_BYTES=209715200 \
  xcodebuild test -scheme ReminisLink -destination 'platform=iOS Simulator,name=iPhone 16' \
  -only-testing:ReminisLinkUITests/AddFileUITests
```

## Transfers

- Uploads (`POST /uploads`, `PUT /uploads/{id}` signed `UNSIGNED`, chunk
  streamed from a scratch file, `complete`) resume at the offset of
  `GET /uploads/{id}` or of a `409 conflict` (the pak keeps uploads across
  its restarts); an upload the pak purged is created again, unless
  `complete` was already sent: then the outcome is reported as unknown and
  the file is never sent again without the user. Each request is signed
  with a new nonce.
- A save (`.sav`, `.srm`) sent from the Add screen goes to the game chosen
  for it through `/uploads` (`target.kind = "save"`); the pak copies the
  current save first (`before-replace`).
- Files chosen in Files are copied into `Application Support/Transfers`
  (deleted when the upload ends, and at launch) so they can be resumed.
- When the app goes to the background, the queue is suspended: the running
  upload finishes its chunk under `beginBackgroundTask` and pauses, and no
  other upload or hashing starts. It resumes when the app is active again.
- `swift test` measures the memory of a streamed SHA-256 over 100 MiB
  (`phys_footprint` peak minus baseline, under 50 MiB).

## Saves, states and cheats (milestone M5)

- **Save**: download (iOS share sheet; the file is written under
  `tmp/Exports`, deleted when the sheet closes and at launch), replace from
  Files (`PUT /games/{id}/save` up to 64 MiB, streamed from a copy of the
  file and signed with its SHA-256, never `UNSIGNED`; above 64 MiB, the
  transfer queue with `target.kind = "save"`), backup copies (create with
  a label of at most 80 characters, restore, download, delete).
- **States**: slots 0 to 7 are shown 1 to 8 as on NextUI, slot 8 (hidden
  by NextUI) is not listed, slot 9 (`resumeSlot`) is shown with the resume
  history. Thumbnails (full-screen PNG) are reduced to the row size.
  `restored` is reported once by the pak; the badge stays while the
  screen is open.
- **Cheats**: edits stay on the iPhone until saved; the whole list is sent
  with `If-Match`. A `412` never overwrites: the app offers to reload,
  which discards the local edits. Unknown keys (`extra`) are sent back as
  received. Import from a link (downloaded by the console), from Files
  (uploaded to the staging area, then imported with its `uploadId`) or from
  a `.cht` of the SD card (`/sd/files?type=cht`).

The demo has two hooks for UI tests (Debug only): its first `PUT /cheats`
meets a change made on the console with `RL_UITEST_CHEATS_CONFLICT=1`, and
its first locked slot is reported `restored` once.

### UI tests against the pak

A pairing link works **once**: every test against the pak needs a new
one. Show a new QR code on the pak (or restart it) between tests and pass
each link in its own variable:

| Test | Variable |
|---|---|
| `SaveUITests/testBackupCopiesAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_SAVE` |
| `StatesUITests/testLocksAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_STATES` |
| `StatesUITests/testRestoredBadgeAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_RESTORED` |
| `CheatsUITests/testCheatsAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_CHEATS` |
| `PatchUITests/testPatchAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_PATCH` |

They work on "Starlight Courier" (GBA) of `testdata/sdcard`. Run one at a
time with `-only-testing:ReminisLinkUITests/<Class>/<test>`.

Acceptance of M5 (a locked state overwritten by hand is restored at the
next start and shown "Restored"):

1. `make headless`; note the SD card folder it prints (`$sd`).
2. Run `testLocksAgainstHeadlessPak` with a link: it locks slot 1
   (shown "Slot 2") of Starlight Courier and leaves it locked.
3. Stop the pak, change the state by hand:
   `echo changed > "$sd/.userdata/shared/GBA-gpsp/Starlight Courier.gba.st1"`
   (the states folder of the fake card today; `<TAG>-<core>/` once the pak
   follows docs/specs/05 §3.1), then start
   the pak again **on the same card**: `build/reminislink-host --headless --sd "$sd"`.
4. Run `testRestoredBadgeAgainstHeadlessPak` with the new link: slot 2
   shows "Restored".

## Patches (milestone M6)

- The **Patch** section of the game sheet opens the Patch screen. The patch
  comes from Files (sent to the staging area with a resumable upload, then
  applied by its `stagingId`), from a link (downloaded by the console to the
  staging area, `POST /downloads`, then applied by the `stagingId` of the
  job's result) or from the SD card (`GET /sd/files?type=patch`). A staged
  patch is not sent again when the user tries another output.
- Output: a new file, named `<name> (patched).<ext>` by default (editable;
  like the pak, the app adds `.<extension of the ROM>` when the name does
  not end with it, shows the final name and sends it; the console adds
  ` (2)` if it is taken), or **replace** the game after a confirmation: the
  original ROM goes to the trash of the console, the save, states and
  cheats stay.
- `POST /games/{gameId}/patch` starts a job, followed every second while
  the screen is visible and the app is active; it also shows on the
  Transfers screen, so the screen can be closed. A new game can then be
  opened. `patch_mismatch` and `unsupported_format` have their own texts.
  A `source-mismatch` shows the expected ROM and the one on the console
  (sizes, CRC32) when the pak sends them; `already-patched`, `409` (the ROM
  changed) and `413` have their own texts; an IPS result says the source
  could not be verified (`verified: false`).
- Cancellation never leads to a patch. The final state is always the one
  the console reports (a running job stays `running` with
  `cancelRequested` until it stops): a download cancelled too late ends
  "downloaded, nothing applied" (kept for an explicit new try), a patch
  cancelled too late shows its result. Polling stops during a
  cancellation; a polling error is shown with Try Again.
- Multi-disc games (`m3u`, `folder-m3u`) and `.zip`/`.7z` ROMs are not
  offered: a patch applies to a single, extracted ROM file.
- A shared `.ips`, `.bps` or `.ups` (alone) opens the Patch screen directly:
  the console is chosen if there are several (locked once something was
  sent to it), then the game. Closing the screen cancels an upload left
  paused on the console.

The fake pak judges a patch by its header and, for BPS and UPS, by the
source size it declares, compared with the size of the game. Its SD card
has a matching BPS for Sky Garden Quest, a UPS for another ROM, an IPS and
a file that is not a patch. UI tests make a BPS at launch and share it
(Debug only): `RL_UITEST_SHARE_PATCH=<name>:<source size>:<source CRC32,
hex>:<target text>`.

`testPatchAgainstHeadlessPak` works on Starlight Courier of
`testdata/sdcard` (10 bytes, CRC32 `4afa16bd`). It launches the app three
times (the link is used by the first launch only): a BPS for another
source is refused, a matching BPS makes `UITest Patched <n>.gba` (opened),
and a BPS made for that new file replaces it.
