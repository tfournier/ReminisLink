# ReminisLink for iOS

| Path | Content |
|---|---|
| `ReminisLink.xcodeproj` | App, share extension and UI tests (hand-written project, see below) |
| `Config/Config.xcconfig` | Identifier prefix and versions shared by every target |
| `Config/*-Info.plist`, `Config/*.entitlements` | Info.plist keys and capabilities of docs/specs/06-app-ios.md §3 |
| `ReminisLink/` | SwiftUI app: `App/` (state, pairing, connections, errors), `Views/`, `Resources/` (String Catalogs EN + FR, assets) |
| `ReminisLinkShare/` | Share extension: copies shared files and links into the app group inbox, then opens the app on Add (it never uploads) |
| `ReminisLinkUITests/` | XCUITests of the main paths (pairing, add a file, delete, save, states, cheats) |
| `ReminisLinkKit/` | Swift package: `ReminisLinkKit` (signing, pairing, API client, discovery, stores, `Transfers/`: resumable uploads, streamed SHA-256, queue, share inbox; `CheatEditor`) and `ReminisLinkDemo` (fake pak, M4 and M5 routes included) |

## Build and test

```sh
cd ios/ReminisLinkKit && swift test
cd ios && xcodebuild test -scheme ReminisLink -destination 'platform=iOS Simulator,name=iPhone 16'
```

No Apple Developer account is needed for the simulator. To run on a device,
set `RL_BUNDLE_PREFIX` (your reverse-DNS prefix) and `DEVELOPMENT_TEAM` in
`Config/Config.xcconfig`: the bundle ids, the app group and the shared
Keychain group all derive from the prefix.

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

They work on "Starlight Courier" (GBA) of `testdata/sdcard`. Run one at a
time with `-only-testing:ReminisLinkUITests/<Class>/<test>`.

Acceptance of M5 (a locked state overwritten by hand is restored at the
next start and shown "Restored"):

1. `make headless`; note the SD card folder it prints (`$sd`).
2. Run `testLocksAgainstHeadlessPak` with a link: it locks slot 1
   (shown "Slot 2") of Starlight Courier and leaves it locked.
3. Stop the pak, change the state by hand:
   `echo changed > "$sd/.userdata/shared/.minui/GBA/Starlight Courier.gba.st1"`
   (the states folder of the fake card today; `<TAG>-<core>/` once the pak
   follows docs/specs/05 §3.1), then start
   the pak again **on the same card**: `build/reminislink-host --headless --sd "$sd"`.
4. Run `testRestoredBadgeAgainstHeadlessPak` with the new link: slot 2
   shows "Restored".
