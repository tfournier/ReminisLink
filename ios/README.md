# ReminisLink for iOS

| Path | Content |
|---|---|
| `ReminisLink.xcodeproj` | App, share extension and UI tests (hand-written project, see below) |
| `Config/Config.xcconfig` | Identifier prefix, team and versions shared by every target |
| `Config/*-Info.plist`, `Config/*.entitlements` | Info.plist keys (launch screen included) and capabilities of docs/specs/06-app-ios.md §3 |
| `Config/ExportOptions.plist` | Options of `xcodebuild -exportArchive` for App Store Connect (see "Release") |
| `ReminisLink/` | SwiftUI app: `App/` (state, pairing, connections, errors), `Views/`, `Resources/` (String Catalogs EN + FR, assets, privacy manifest) |
| `ReminisLinkShare/` | Share extension: copies shared files into the app group inbox, then opens the app on Add (it never uploads; web links are not accepted) |
| `Design/` | SVG sources of the app icon and of the launch screen mark, and `make-icons.sh` that renders them into the asset catalog |
| `AppStore/` | Texts of the App Store page (English and French) and notes for App Review |
| `ReminisLinkUITests/` | XCUITests of the main paths (pairing, add a file, delete, save, states, cheats, patch, game art, save sync, tabs, flat library, file download, console closing, apps and SD browser) |
| `ReminisLinkKit/` | Swift package: `ReminisLinkKit` (signing, pairing, API client, discovery, stores, `Events/`: the event stream of milestone M14 (SSE parser, typed events, liveness and reconnection), `Transfers/`: resumable uploads, streamed SHA-256, queue, share inbox; `CheatEditor`; `Sync/`: save vault and save sync) and `ReminisLinkDemo` (fake pak, M4 to M14 routes included, M12 paks and SD browser: `GET /events` with keep-alives, `library`/`job`/`device`/`shutdown` events and a mode without the route) |

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
Keychain group `<Team ID>.link.reminis.app.shared`, and the iCloud
container `iCloud.link.reminis.app` (`RL_ICLOUD_CONTAINER`). Never change
them once the app is published: a new identifier is a new app for the App Store, and
installed copies lose their pairings. The seller name shown on the App Store
is the one of the account that publishes the app, not the identifier.

Signing is automatic. Open the project in Xcode once, select the
`ReminisLink` and `ReminisLinkShare` targets in *Signing & Capabilities*
and check that Xcode registered the two App IDs and the App Group without
error (it may ask to sign in with the Apple Developer account in *Settings >
Accounts*).

**iCloud (milestone M9, save sync).** The app's entitlements ask for iCloud
Documents with the container `iCloud.link.reminis.app`. Nothing is
registered by the repository: once, on developer.apple.com (*Certificates,
Identifiers & Profiles*):

1. *Identifiers > + > iCloud Containers*: create `iCloud.link.reminis.app`
   (description "ReminisLink").
2. *Identifiers >* the App ID `link.reminis.app` *> Capabilities*: tick
   *iCloud* (iCloud Documents, no CloudKit needed), *Edit* and assign the
   container `iCloud.link.reminis.app`. Save; automatic signing makes the
   provisioning profiles again.
3. Or let Xcode do it: *Signing & Capabilities* of `ReminisLink` shows
   *iCloud* with *iCloud Documents* and the container (from
   `Config/ReminisLink.entitlements`); use the refresh button next to the
   container if it is shown in red.

The share extension needs nothing. Xcode does not expand build settings in
Info.plist keys: the key of `NSUbiquitousContainers` in
`Config/ReminisLink-Info.plist` is the macro `RL_ICLOUD_CONTAINER_KEY`,
replaced by `RL_ICLOUD_CONTAINER` when the Info.plist is preprocessed
(`INFOPLIST_PREPROCESS`), so a local `RL_APP_BUNDLE_ID` (e.g.
`link.reminis.app.dev` in `Local.xcconfig`) gets its own container
everywhere; a unit test checks it.
The folder shows in iCloud Drive as "ReminisLink" once the app wrote a
first file there; iOS may take a while to show it after a change of
`NSUbiquitousContainers` (increase `CURRENT_PROJECT_VERSION`). Without the
capability (simulator builds without a team, no iCloud account, iCloud
Drive off for the app), the saves go to the app's `Documents` folder
(Files > On My iPhone > ReminisLink) and the dashboard says so.

### 2. App Store Connect record

In App Store Connect, *Apps > + > New App*: platform iOS, name ReminisLink,
primary language, the bundle id of `RL_APP_BUNDLE_ID` (`link.reminis.app`
with the default prefix of `Config/Config.xcconfig`), any SKU. Fill the
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

At the first upload, App Store Connect asks about encryption: the app signs
its requests to the console with Ed25519 and checks the pairing proofs with
HMAC-SHA256 (CryptoKit), and sends them over HTTPS on the local network
(TLS of the system's `URLSession`, with the console's self-signed
certificate pinned, milestone M13). Answer the
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

The reviewer has no console: the published app has a demo mode (milestone
M16, *Try Without a Console* on the first screen, see "Demo mode" below).
Paste `AppStore/review_notes.txt` in *App Review Information > Notes*; the
App Store screenshots and a review video made from the demo are in
`docs/screenshots/appstore/`. The demo video does not show pairing with a
real handheld: for that part Apple asks for a video filmed with a physical
Apple device (to be made with the hardware, not in the repository).

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

## Demo mode (no console, no server)

Milestone M16: *Try Without a Console* (welcome screen, or the last row of
the list of consoles) opens a "Demo Brick" in every build, App Store
included (`App/DemoMode.swift`, docs/specs/06-app-ios.md §12). It talks to
the fake pak of `ReminisLinkDemo` through `FakeURLProtocol`, and
`OfflineURLProtocol` fails any other request without going out; its keys
stay in memory, its synced saves, art cache and file copies in a temporary
folder and its settings in a `UserDefaults` suite, both named by a new UUID
per session. Downloads never reach the share sheet (an alert confirms them).
*Quit* in its banner returns at once; the session then stops and awaits its
work (`WorkTracker` counts file copies and downloads), erases its own
folder and suite and is freed (a crashed session is erased at the next
launch). While it runs, the real app pauses Bonjour, its uploads and its
save syncs. The real list of consoles is never touched. Box arts and state
thumbnails are drawn by `DemoArtwork` (no real art). `DemoModeAppTests`
and `DemoModeUITests` cover it; the App Store screenshots are taken with
`-RLScreenshots YES` (Debug only), which hides the banner.

For UI tests, launch with the environment variable `RL_DEMO=1` (already in the scheme,
disabled: tick it in *Edit Scheme > Run > Arguments*) or the argument
`-RLDemo YES` (Debug builds only). The whole app then runs on the same
demo, without the banner and not paired yet, and sees one "Demo Brick".
Pair it with *Add a Console > Pair with the Demo Console*, or open its link:

```sh
xcrun simctl openurl booted 'reminislink://pair?id=5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0&n=Demo%20Brick&s=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8&v=1'
```

The fake pak checks signatures like the real one; its game names, system
labels, folder tags and ROM extensions are made up (no trademark: they appear
in App Store screenshots), in the NextUI folder style: `Handheld Advance (HA)`
(`.hab`), `01) Pocket Classic (PCL)` (`.pcr`), `Disc Console (DCN)` (a
`.m3u` folder). No real NextUI tag uses them, so Add suggests these systems
from the demo library (`DemoLibrary.tagsByExtension`), not from the real
extension table. Its disc system has no emulator (`emulatorInstalled: false`): the
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

Since M13 the pak serves HTTPS only and prints a pairing link v2
(`reminislink://pair?v=2&id=…&n=…&s=…&h=…&p=…&k=<pin>`): `k` is the pin of
its certificate (b64u of the SHA-256 of its SubjectPublicKeyInfo), which
the app pins for every request. A link `v=1` (pak older than M13) is
refused with "update ReminisLink on the console". The identity of the app
(client id and Ed25519 private key) and the paired consoles are
synchronizable Keychain items (iCloud Keychain); on the simulator they
stay on the simulator.

The UI test against the real pak is skipped unless the link is given
(`TEST_RUNNER_` variables are passed to the test runner):

```sh
TEST_RUNNER_RL_UITEST_PAIR_URL='reminislink://pair?...' \
  xcodebuild test -scheme ReminisLink -destination 'platform=iOS Simulator,name=iPhone 16'
```

It launches the app with `RL_RESET_STORE=1`, which forgets the consoles
paired before (and the name typed for the identity). The pak is found with
Bonjour, or, when Bonjour does not find it within 3 s, at the `h`/`p` of the
link (M13: the pin `k` authenticates it). Every request then goes over TLS
pinned on `k`: these tests are the end-to-end check of the pinning (the
fake pak of the demo serves no TLS).

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
  received. Import from Files (uploaded to the staging area, then imported
  with its `uploadId`) or from a `.cht` of the SD card
  (`/sd/files?type=cht`). The console never downloads a link (removed from
  v1).

The demo has two hooks for UI tests (Debug only): its first `PUT /cheats`
meets a change made on the console with `RL_UITEST_CHEATS_CONFLICT=1`, and
its first locked slot is reported `restored` once.

### UI tests against the pak

A pairing link works **once**: every test against the pak needs a new
one. Show a new QR code on the pak between tests and pass each link in its
own variable.

`scripts/run-headless-uitests.sh [NAME...]` does it for you: it runs the
tests of the table below one at a time, each against its own headless pak
(fresh copy of `testdata/sdcard`, `--pairing-open`), passes the link in
the test's variable, and plays the runner's part of `RESTORED`, `CLOSING`,
`EVENTS_QUIT` and `EVENTS_LIBRARY` (`--list` for the names;
`DESTINATION`, `DERIVED_DATA`, `SKIP_BUILD=1`, `KEEP_LOGS=1`).

Since M8, pairing is on demand: once a client is paired, restarting the
pak on the same card shows no link (pairing opens from Settings > Pair a
new device on the console). Start it with `--pairing-open` to print a new
link at each start: `make headless HEADLESS_ARGS=--pairing-open`, or
`build/reminislink-host --headless --sd "$sd" --pairing-open` on the same
card.

| Test | Variable |
|---|---|
| `SaveUITests/testBackupCopiesAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_SAVE` |
| `StatesUITests/testLocksAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_STATES` |
| `StatesUITests/testRestoredBadgeAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_RESTORED` |
| `CheatsUITests/testCheatsAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_CHEATS` |
| `PatchUITests/testPatchAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_PATCH` |
| `ArtUITests/testChangeThenRemoveTheImageAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_ART` |
| `SyncUITests/testSyncAndRestoreAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_SYNC` (pak of milestone M9: `GET /saves`) |
| `ErgonomicsUITests/testLibraryAndDownloadAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_LIBRARY` (milestone M11; see below) |
| `ErgonomicsUITests/testTheConsoleClosingAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_CLOSING` (milestone M11; quit the pak during the test, see below) |
| `EventsUITests/testPakQuitReturnsToListAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_EVENTS_QUIT` (milestone M14; SIGTERM to the pak during the test, see below) |
| `EventsUITests/testLibraryRefreshFromAnotherClientAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_EVENTS_LIBRARY` (milestone M14; a second client deletes a game, see below) |
| `PaksUITests/testInstallAndDeleteAgainstHeadlessPak` | `TEST_RUNNER_RL_UITEST_PAIR_URL_PAKS` (milestone M12; see "Apps and SD browser") |

The M10 write lease and its UI test (`WriteAccessUITests`, with
`scripts/smoke-writer.sh` in hold mode) are gone since M13.
`scripts/smoke-writes.sh` replaces `scripts/smoke-writer.sh`: it checks the
pak's side of M13 (TLS pin, pairing v2, RL2-ED25519, two clients writing
at the same time) with curl and openssl.

The two M14 tests (event stream `GET /events`) need a runner. Start a
fresh pak per test with `--pairing-open > <pak output>`, and pass, besides
the link, `TEST_RUNNER_RL_UITEST_EVENTS_READY_FILE=<path>` (created by the
test once it waits: idle on the Console tab, or the Library tab showing
10 games) and `TEST_RUNNER_RL_UITEST_EVENTS_TRIGGER_FILE=<path>` (the
runner writes the epoch time of its action there, `date +%s` or with
milliseconds; the test then asserts a delay under 3 s and prints
`RL_EVENTS_DELAY`). Paths on the Mac work: the simulator shares its file
system. Once the ready file exists:

- `EVENTS_QUIT`: write the time to the trigger file and `kill -TERM` the
  pak (the console's Quit needs the UI); the list of consoles with "The
  console closed ReminisLink" must show with no action.
- `EVENTS_LIBRARY`: after the app paired, run
  `UI_DELETE=1 PAK_LOG=<pak output> READY_FILE=<path> TRIGGER_FILE=<path> scripts/smoke-events.sh`:
  it pairs a second client with the latest link and deletes "Lantern
  Keeper [b1]" (GBA); the library must show 9 games without pull-to-refresh.

Without the two files, the tests still check the screens but do not
measure the delay. `scripts/smoke-events.sh` alone checks the stream with
two curl clients (hello, keep-alive, `library`, `job`, the 4-stream limit,
`shutdown` then EOF on SIGTERM).

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
   the pak again **on the same card**:
   `build/reminislink-host --headless --sd "$sd" --pairing-open`.
4. Run `testRestoredBadgeAgainstHeadlessPak` with the new link: slot 2
   shows "Restored".

## Patches (milestone M6)

- The **Patch** section of the game sheet opens the Patch screen. The patch
  comes from Files (sent to the staging area with a resumable upload, then
  applied by its `stagingId`) or from the SD card
  (`GET /sd/files?type=patch`). A staged
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
  `cancelRequested` until it stops): a patch cancelled too late shows its
  result. Polling stops during a
  cancellation; a polling error is shown with Try Again.
- Multi-disc games (`m3u`, `folder-m3u`) and `.zip`/`.7z` ROMs are not
  offered: a patch applies to a single, extracted ROM file.
- A shared `.ips`, `.bps` or `.ups` (alone) opens the Patch screen directly:
  the console is chosen if there are several (locked once something was
  sent to it), then the game. Closing the screen cancels an upload left
  paused on the console.

The fake pak judges a patch by its header and, for BPS and UPS, by the
source size it declares, compared with the size of the game. Its SD card
has a matching BPS for Thistledown Isles, a UPS for another ROM, an IPS and
a file that is not a patch. UI tests make a BPS at launch and share it
(Debug only): `RL_UITEST_SHARE_PATCH=<name>:<source size>:<source CRC32,
hex>:<target text>`.

`testPatchAgainstHeadlessPak` works on Starlight Courier of
`testdata/sdcard` (10 bytes, CRC32 `4afa16bd`). It launches the app three
times (the link is used by the first launch only): a BPS for another
source is refused, a matching BPS makes `UITest Patched <n>.gba` (opened),
and a BPS made for that new file replaces it.

## Save sync (milestone M9)

- **When**: the *Sync Saves* button of the dashboard, and at each
  connection to a console (*Settings > Sync Saves When Connecting*, on by
  default; the gear of the Consoles screen). One sync at a time per
  console; it can be cancelled (versions already copied stay).
- **What**: `GET /saves`, then only the saves whose SHA-256 (or the SHA-256
  of their `.rtc`) differ from their newest version are downloaded
  (`GET /games/{gameId}/save`, `GET /games/{gameId}/save/rtc`). The bytes
  received must have the listed SHA-256, or nothing is kept for that game
  ("changed during the sync"). A failure on one game does not stop the
  others; losing the console does.
- **Where** (`SaveVaultLocator`): the `Documents` folder of the iCloud
  container ("ReminisLink" in iCloud Drive), else the app's `Documents`
  (Files > On My iPhone > ReminisLink). Layout:
  `<console>/<label (tag)>/<game>/<yyyy-MM-dd HH.mm.ss>/<save>` and its
  `.rtc`; hidden `.console.json`, `.game.json` and `.version.json` hold the
  identifiers and SHA-256. Writes go through `NSFileCoordinator`; a version
  is assembled outside the vault and moved in with one rename. At most 10
  versions per save, ordered by their publication `sequence` (never by
  date: the iPhone clock can go back): the oldest published is deleted
  once the new one is in place, never the new one, and an unfinished
  rotation is finished at the next sync. Nothing else is ever deleted.
- `GET /saves` waits up to 180 s (the session's 15 s for the other
  routes): the pak answers once every save is hashed.
- **Restore**: *Saves on This iPhone* (dashboard or settings) > game >
  version > *Restore to the Console…*: read from the vault the version was
  shown from, checked against its SHA-256 (nothing is sent otherwise), then
  `PUT /games/{gameId}/save` (the
  transfer queue above 64 MiB), after a confirmation. The console keeps a
  `before-replace` copy. Only the save is restored: there is no route to
  write a `.rtc`, the console keeps its own clock file. *Share* sends the
  save and its `.rtc` to the share sheet.

The demo keeps its synced saves in a temporary folder emptied at each
launch, as without iCloud. UI tests (Debug only):
`RL_UITEST_SAVE_CHANGES_ON_LISTING=<n>` makes the `n`-th `GET /saves` of
the fake pak find Thistledown Isles played on the console (a new save).

`testSyncAndRestoreAgainstHeadlessPak` needs a pak built from the M9 pak
branch. It syncs on connection, syncs again ("Up to date") and restores the
newest version of Starlight Courier. The synced saves of the simulator stay
in the app's `Documents` between runs (the first sync of a later run may
find nothing new).

## Several devices, writes one at a time (milestones M10, M13)

M13 removed the M10 write lease (`WriterLease`, `/writer`, `423 read_only`,
the read-only banner, **Take Over**): several paired devices may write; the
pak runs the writes one at a time.

- **Device busy**: a write that waited 10 s for the one in progress gets
  `429 device_busy` with `Retry-After`; nothing was changed
  (`ServerError.isDeviceBusy`, which also recognizes the answer of an M13
  pak: `429 too_many_attempts` with the message `Device busy`). The end of an upload
  (`complete`) is sent again by itself after `Retry-After` (and that
  refusal never makes its outcome unknown); a user action shows "The
  console is busy with another change. Nothing was changed. Try again in N
  seconds."
- **Disconnected by the console** (`401 disconnected`): the dashboard shows
  the message with **Reconnect**. `POST /clients/me/connect` is only sent
  by the connection that follows that tap, or opening the console from the
  list (and only if the pak answers `401 disconnected`); automatic
  reconnections never send it. `401 unknown_client` (forgotten by the
  console) still offers **Pair Again**. The permission belongs to that one
  connection attempt, whatever its outcome. Every `401 disconnected`, from
  any screen or transfer, reaches the session through the `errorObserver`
  of `APIClient`.
- Uploads refused with `401 disconnected` are paused (never lost) and
  resume after the reconnection; the cheat file upload (Import > Files) and
  the patch upload wait the same way, with their local copy. A cancel is
  final only once the console confirms it: meanwhile the upload is
  `cancelling` (kept in the list, not clearable; the Patch sheet cannot
  close; the local copy stays). Refused, it pauses. Only a `2xx` (or a
  `404` before `complete`) confirms a cancel; without a definite answer
  (lost connection, `5xx`) the upload and its copy stay, failed with
  "cancel not confirmed", to resume or cancel again.

The fake pak implements `disconnect(clientId:)`, `forget(clientId:)` and
`busyNextWrites(_:retryAfter:)`. UI tests (Debug only):
`RL_UITEST_BUSY_WRITES=<n>` makes the next n writes of the demo console
busy (`DeviceBusyUITests`), and `RL_UITEST_RL1_CONSOLE=1` lists the demo
console as paired before M13, to pair again (`PairingUITests`).

## Pairing v2 and TLS pinning (milestone M13)

- **Identity** (`ClientIdentity`, `DeviceStore`): a `clientId` and an
  Ed25519 key pair (CryptoKit `Curve25519.Signing`), stored as **one**
  synchronizable Keychain item per `clientId` (`identity.<clientId>`; a
  pointer item `identity` names the current one, for new pairings, with the
  name shown on the consoles)
  (`kSecAttrSynchronizable`, `kSecAttrAccessibleAfterFirstUnlock`, the
  shared access group `RL_KEYCHAIN_GROUP`, service `ReminisLink.pairing`):
  the iPhone, iPad and Mac of one Apple account share it and pair once. No
  Secure Enclave (its keys do not synchronize). Each paired console is
  another synchronizable item (`deviceId`, the `clientId` it was paired
  with, certificate pin, last validated address, names, model); each console
  signs with its own pairing's identity, so two devices that create an
  identity each before the Keychain converges keep both pairings working.
  Identity items are never deleted automatically (a key may arrive before
  the record that refers to it). The list
  is read again each time the app becomes active.
- **Last validated address**: the address the pairing reached (the QR
  code's when Bonjour was silent), updated at each successful connection,
  is tried when Bonjour does not find the console within 3 s (pinned TLS).
- **Pairing** (`PairingLink`, `Pairing`, `PairingFlow`): QR code only, no
  PIN. Bonjour first (3 s), then the `h`:`p` of the link, both over TLS
  pinned on `k`; `POST /pair` with `platform: apple`, the public key and
  the HMAC proof made with the QR secret; the device proof is checked
  before anything is stored. The name shown on the console is typed on the
  Add a Console screen (default: the device model, "iPhone").
- **Signing** (`Signer`): the canonical string of RL1 with the first line
  `RL2-ED25519`, signed with the private key; CryptoKit signatures are
  randomized, so the tests verify the vectors' signatures and ours with the
  public key. `X-RL-Platform` (`iphone`, `ipad`, `mac`) on every request.
- **TLS** (`TLSPin`, `PinnedTrust`, `PakTaskDelegate`): every request is
  `https://`; the delegate of each task answers the server trust challenge
  with the SHA-256 of the leaf certificate's SPKI (X9.63 point from
  `SecKeyCopyExternalRepresentation` behind the fixed P-256 header),
  compared with the stored pin; the system trust is never evaluated. A
  mismatch (or no pin) cancels the connection before the request is sent
  and throws `APIError.identityChanged` ("This console's identity changed
  … pair it again"). The session has no delegate of its own, so URLSession
  hands its server trust challenges to the task delegate; downloads with
  progress and the event stream use the same delegate.
- **Migration**: at the first M13 launch the RL1 keys (Keychain item of
  this device only, `ReminisLink.devices`) are deleted; their consoles stay
  listed with **Pair Again** until paired again or forgotten.
- Tests: `swift test` runs the shared vectors (`pairing.json` with the
  pairing links, `signing.json`, `tls.json`: SPKI and pin from the
  certificate), the Keychain queries through an injected `KeychainAPI`, and
  the pinning on the certificate of `tls.json` (`TLSPinTests`); the demo
  and the app tests use the fake pak over `URLProtocol` (no TLS; its link
  carries a fake pin that is stored but never checked). Against a running
  pak, `RL_TEST_PAK_LINK='<link>' swift test --filter PinnedHandshake`
  checks the handshake itself: the pin of the link is accepted, another pin
  or no pin gives `identityChanged` for `/hello`, the event stream, a
  download with progress, an upload chunk and a JSON request. With
  `RL_TEST_PAK_LOG=<pak output>` it also checks that none of them reached
  the pak: each carries a unique marker in its path that must never appear
  in the log, a control request with the right pin and its own marker must
  appear, and the pak must log the refused TLS handshakes (nothing is
  paired, the link stays usable).

## App ergonomics (milestone M11)

- **Tabs**: the list of consoles is the root. Opening a console shows a tab
  bar (Console, Library, Transfers, Trash), each tab with its own
  `NavigationStack`; **Consoles** (top left of each tab's first screen) goes
  back to the list. `AppModel.path` stays one path for the code that opens
  screens (`[.console(id), .library(id), .game(…)]` opens the Library tab
  and pushes the game; `.transfers(id)` and `.trash(id)` select their tab;
  no second route is the Console tab). Nothing is added from the Console
  tab: **+** is in the Library tab, and in Transfers.
- **Flat library**: `GET /games` (every game, at most 20 000) and
  `GET /systems` (the labels). Search (name, file name) and filters
  (console, save, states, cheats) and sort run on the iPhone
  (`LibraryFiltering`, ReminisLinkKit); systems with the same label are one
  console in the filter, as NextUI shows them; their rows add the tag
  ("Game Boy Advance (MGBA)") so two games of the same name differ. `truncated`, or a pak
  without the route (`404`/`405`), falls back to browsing per system, also
  reachable from the menu (**Browse by Console**).
- **Game sheet**: tapping the image changes it (Photos, Files, Remove
  Image). Each file of `GET /games/{gameId}` has a
  download button: `GET /games/{gameId}/files/{fileId}` (the opaque
  `fileId`, never a path), streamed to disk by a signed `URLSession`
  download task with progress and cancel, then the share sheet (Save to
  Files, AirDrop…). A `404` reloads the sheet, then says "This file changed
  on the console"; a game that is gone loses its rows ("This game is no
  longer on the console"). A pak older than M11 sends
  no `fileId` (decoded as nil): the sheet works, without download buttons.
- **Console closed**: `503 shutting_down` seen by any request of the
  connection (screen, transfer), or a connection failure
  while the console's Bonjour service is gone (the browser has run for at
  least 3 s, `PakClosure`), ends the connection without an error screen:
  back to the list with "The console closed ReminisLink", uploads paused
  (`TransferQueue.pauseForClosedConsole`; uploads added meanwhile start
  paused), no automatic reconnection until the user opens the console
  again (`ConsoleSession.connect` refuses every non-explicit attempt, and
  the end of an Add does not reopen it); the next connection resumes them.
  A `503` during the opening (`/hello`, `/device`), or a connection failure
  there once the announcement is gone, does the same. The image of a game
  that is gone has no action. A
  Wi-Fi glitch alone (service still announced) keeps today's error screen.
  The user does not have to do anything: when the browser sees the open
  console's service withdrawn (the pak's mDNS goodbye as it quits,
  `Discovery.onDevicesRemoved`), the session probes `GET /hello` once (3 s,
  no retry, `ConsoleSession.checkStillOpen`): `503 shutting_down` or no
  connection is the closing above, an answer means a Bonjour glitch and the
  console stays open.
- **Console name**: the list shows the name the console reports
  (`deviceName` of `/hello` or `/device`, matched by `deviceId`); a console
  paired before M11 takes it at the first report.

The fake pak serves `GET /games` (`allGamesSupported`, `allGamesLimit` to
test the fallback), a `fileId` per file (base64url of `f_<role>[slot]`) and
`GET /games/{gameId}/files/{fileId}` (made-up content of exactly the listed
size, so demo games stay small; a `.cht` built from its cheats, listed with
its size). `allGamesSupported = false` is a pak older than M11 (no
`GET /games`, no `fileId`, no file downloads). It can quit (`availability`: `503
shutting_down`, then gone), and the demo withdraws its Bonjour service
meanwhile. UI tests (Debug only): `RL_UITEST_PAK_CLOSES_ON="GET /games"`
makes it quit on that request and open again 5 s later
(`ErgonomicsUITests`).

Against the real pak (a fresh copy of `testdata/sdcard`, `--pairing-open`):
`testLibraryAndDownloadAgainstHeadlessPak` checks the flat list of its 10
games (no per-system browsing), finds Tiny Trial (GBA `Hacks/Deeper`) with
the search, downloads its ROM up to the share sheet, then the `.cht` of
Pixel Garden 🌱. `testTheConsoleClosingAgainstHeadlessPak` pairs, stays on
the dashboard and waits (90 s at most) for the list of consoles with "The
console closed ReminisLink": end the pak meanwhile with `kill -TERM <pid>`
(SIGTERM or Ctrl-C), a few seconds after the pairing. On the API side,
`scripts/smoke-library.sh` checks `GET /games`, the `fileId`s and the
downloads with curl (SHA-256 against the SD card).

## Apps and SD browser (milestone M12)

- **Apps** (Console tab > Apps, `PaksView`, `PakManager`): `GET /paks`
  grouped Tools / Emulators (`PakList.grouped`), name, version, size;
  ReminisLink (`self`) is tagged "This app" and has no delete action.
  Delete (swipe or context menu) asks first, then `DELETE /paks/{pakId}`
  (to the trash), and the list and the trash are read again. A `paks`
  event (M14) or a stream `hello` reloads the list once it was shown; a
  pak older than M12 (`404`) shows "Update ReminisLink on the console".
- **Install** (`+`): Files (a `.zip`, or a `.pak` folder zipped by
  `PakArchiver` with `NSFileCoordinator` `.forUploading` into
  `<Name>.pak.zip`) or the SD card (the browser as a picker:
  only zips and `.pak` folders can be chosen). An options sheet keeps
  `auto` unless the user picks Tool or Emulator. A file goes through the
  transfer queue (target `pak`, `ownsFile: false`: `PakManager` keeps the
  copy until the end, to send it again on a replacement); its `complete`
  returns a `jobId`, followed like the `pak-install` job of
  `POST /paks/install` (SD), by events or polling
  (the Apps screen polls every second while visible, as Transfers).
- **Conflict**: a job failed with `409 conflict` and `details.existing`
  asks "Replace <name>? The current version goes to the trash." (on Apps
  and on Transfers); Replace starts the same source again with
  `overwrite` (upload) or `replace` (SD). `details.self` and
  `422 unsupported_format` get their own texts: the pak's English reason
  is mapped by `PakRefusal` (ReminisLinkKit) to a translated message, an
  unknown reason is shown as it is after "The console refused this pak:".
- **Share extension**: unchanged; a shared zip can be sent from Add with
  the type "App (pak)", which uses the same path.
- **SD browser** (tap Storage on the Console tab, `SdFolderView`):
  `GET /sd/browse` folder by folder (opaque `sdEntryId`s only), size and
  date, `truncated` notice; a file downloads with progress and cancel
  (`GET /sd/browse/{id}/content`, streamed to disk) to the share sheet; a
  `404` reloads the folder.

The fake pak decides an installation from the name of its source
(`FakePakPaks.swift`): `broken` → not a zip, `nolaunch` → no `launch.sh`,
`twopaks` → two paks (`422`), `ReminisLink` → `409 details.self`, an
installed name (`Clock.pak.zip`) → `409 details.existing` unless
replacing. Its card holds `Downloads/` (`Clock.pak.zip`, `Weather.pak.zip`,
`broken.zip`, `NoLaunch.zip`, `README.txt`, the `Timer.pak` folder), the
paks under `Tools/tg5040` and `Emus/tg5040`, the library under `Roms/`,
and `.userdata/shared/` without ReminisLink's folder. `paksSupported =
false` is a pak older than M12.

Against the real pak: start a fresh `make headless HEADLESS_ARGS=--pairing-open`
and run

```sh
TEST_RUNNER_RL_UITEST_PAIR_URL_PAKS='reminislink://pair?...' \
  xcodebuild test -scheme ReminisLink -destination 'platform=iOS Simulator,name=iPhone 16' \
  -only-testing:ReminisLinkUITests/PaksUITests/testInstallAndDeleteAgainstHeadlessPak
```

The app generates a tiny pak at launch (`RL_UITEST_SHARE_PAK=<name>`,
Debug only: a `<name>.pak` folder with `launch.sh` and `pak.json`, zipped
by `PakArchiver` as a folder chosen in Files is) and receives it as the
share extension would; the test sends it as "App (pak)", waits for
"Installed" in Transfers, finds it (version 0.0.1) on Apps, deletes it,
then browses the root of the card (and checks `.userdata/shared/` has no
ReminisLink folder). `scripts/smoke-paks.sh` checks the pak side with curl.
