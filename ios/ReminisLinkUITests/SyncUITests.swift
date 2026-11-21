import XCTest

/// Save sync to the iPhone (milestone M9): the sync on connection, a
/// manual sync that only copies what changed, the versions of a save,
/// restoring one to the console (with its confirmation) and sharing one.
final class SyncUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    /// The demo keeps the saves in a folder of the app, as without iCloud:
    /// the dashboard says so.
    @MainActor
    func testSyncAndRestoreInDemoMode() throws {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        // The second listing finds Thistledown Isles played on the console.
        app.launchEnvironment["RL_UITEST_SAVE_CHANGES_ON_LISTING"] = "2"
        app.launch()
        app.open(URL(string: PairingUITests.demoLink)!)
        app.buttons["pairing.done"].tapAfterWaiting(timeout: 15)

        // Synced on connection: every save is new.
        expectReport(app, containing: "New versions: 4", timeout: 20)
        XCTAssertTrue(GameDataUITest.scrollTo(app.descendants(matching: .any)["sync.location.local"].firstMatch, in: app),
                      "no notice that iCloud is unavailable")
        attachScreenshot(of: app, named: "sync-first")
        app.swipeDown()

        // A manual sync copies only the save that changed.
        app.buttons["console.sync"].tapWhenEnabled(timeout: 15)
        expectReport(app, containing: "New versions: 1 · saves checked: 4", timeout: 20)

        // Its two versions; restore the older one.
        openVersions(app, system: "Handheld Advance (HA)", game: GameDataUITest.demoGame, timeout: 15)
        XCTAssertTrue(app.buttons["synced.version.1"].waitForExistence(timeout: 10), "two versions expected")
        attachScreenshot(of: app, named: "sync-versions")
        app.buttons["synced.version.1"].tap()
        app.dialogButton("synced.restore").tapAfterWaiting()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 10))
        XCTAssertTrue(alert.staticTexts.matching(NSPredicate(format: "label CONTAINS 'clock file on the console is kept'")).firstMatch.exists)
        alert.dialogButton("synced.restore.confirm").tap()
        GameDataUITest.waitForNotice(app)

        // Share the newest version.
        app.buttons["synced.version.0"].tapAfterWaiting()
        app.dialogButton("synced.share").tapAfterWaiting()
        GameDataUITest.expectShareSheetThenClose(app)

        // Back on the dashboard, the restored save is a change: one new version.
        app.goBack()
        app.goBack()
        app.buttons["console.sync"].tapWhenEnabled(timeout: 15)
        expectReport(app, containing: "New versions: 1", timeout: 20)
        openVersions(app, system: "Handheld Advance (HA)", game: GameDataUITest.demoGame, timeout: 15)
        XCTAssertTrue(app.buttons["synced.version.2"].waitForExistence(timeout: 10), "three versions expected")
    }

    /// Without iCloud (the demo), the settings say the saves stay on this
    /// iPhone, not that they go to iCloud Drive.
    @MainActor
    func testSettingsNameTheActualDestinationInDemoMode() throws {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        app.launch()
        app.buttons["consoles.settings"].tapAfterWaiting(timeout: 15)
        // "this iPhone" on an iPhone, "this device" elsewhere (M15).
        let device = ConsoleTabBar.isBottomBar ? "iPhone" : "device"
        let local = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "copied to this \(device) only")).firstMatch
        XCTAssertTrue(local.waitForExistence(timeout: 10), "the settings do not say the saves stay on this \(device)")
        XCTAssertFalse(app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'copied to your own iCloud Drive'")).firstMatch.exists)
        XCTAssertTrue(app.descendants(matching: .any)["sync.location.local"].firstMatch.exists)
    }

    /// Against a real pak (`make headless`, built from the M9 branch):
    /// sync on connection, then an incremental sync that finds nothing new,
    /// then the restoration of the newest version of Starlight Courier.
    ///
    ///     TEST_RUNNER_RL_UITEST_PAIR_URL_SYNC='reminislink://pair?...' xcodebuild test ...
    @MainActor
    func testSyncAndRestoreAgainstHeadlessPak() throws {
        let link = try GameDataUITest.headlessLink("SYNC")
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_RESET_STORE"] = "1"
        app.launch()
        app.open(link)
        app.buttons["pairing.done"].tapAfterWaiting(timeout: 30)

        // Synced on connection (new versions, or none if an earlier run
        // already copied these saves to this simulator).
        expectReport(app, containing: "saves checked", timeout: 60)
        attachScreenshot(of: app, named: "sync-headless-first")

        // Nothing changed since: nothing copied.
        app.buttons["console.sync"].tapWhenEnabled(timeout: 30)
        expectReport(app, containing: "Up to date", timeout: 60)

        openVersions(app, system: "Game Boy Advance (GBA)", game: GameDataUITest.headlessGame, timeout: 30)
        app.buttons["synced.version.0"].tapAfterWaiting(timeout: 10)
        app.dialogButton("synced.restore").tapAfterWaiting()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 10))
        alert.dialogButton("synced.restore.confirm").tap()
        GameDataUITest.waitForNotice(app, timeout: 30)
        attachScreenshot(of: app, named: "sync-headless-restored")
    }

    /// The report of the last sync shows `text`.
    @MainActor
    private func expectReport(_ app: XCUIApplication, containing text: String, timeout: TimeInterval,
                              file: StaticString = #filePath, line: UInt = #line) {
        let report = app.descendants(matching: .any).matching(identifier: "sync.report")
            .matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
        XCTAssertTrue(report.waitForExistence(timeout: timeout), "no sync report with \"\(text)\"", file: file, line: line)
        XCTAssertFalse(app.descendants(matching: .any)["sync.failures"].exists, "some saves failed", file: file, line: line)
    }

    /// From the dashboard to the versions of a synced save (a GBA game:
    /// the folder of its system is `<label> (<tag>)`).
    @MainActor
    private func openVersions(_ app: XCUIApplication, system: String, game: String, timeout: TimeInterval) {
        app.buttons["console.syncedSaves"].tapAfterWaiting(timeout: timeout)
        let row = app.buttons["synced.game.\(system)/\(game)"]
        XCTAssertTrue(GameDataUITest.scrollTo(row, in: app), "\(game) is not listed")
        row.tap()
        XCTAssertTrue(app.buttons["synced.version.0"].waitForExistence(timeout: timeout), "no version listed")
    }
}
