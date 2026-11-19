import XCTest

/// Save section of the game sheet (milestone M5): make a backup copy with
/// a label, restore it (the current save is copied first), download the
/// save through the share sheet, delete the copy. And a save sent from the
/// Add screen to a chosen game.
final class SaveUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor
    func testBackupCopiesInDemoMode() throws {
        let app = GameDataUITest.openDemoGame()
        try backupCopies(app, timeout: 15)
    }

    /// Against a real pak (`make headless`), on Starlight Courier.
    ///
    ///     TEST_RUNNER_RL_UITEST_PAIR_URL_SAVE='reminislink://pair?...' xcodebuild test ...
    @MainActor
    func testBackupCopiesAgainstHeadlessPak() throws {
        let app = GameDataUITest.openHeadlessGame(link: try GameDataUITest.headlessLink("SAVE"))
        try backupCopies(app, timeout: 30)
    }

    /// A `.sav` shared to the app: the Add screen offers it as a save, the
    /// game is chosen, it is sent, and the previous save is kept as a copy.
    @MainActor
    func testSaveFromTheAddScreenInDemoMode() throws {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        app.launchEnvironment["RL_UITEST_SHARE_FILE"] = "Sky Garden Quest.gba.sav:2048"
        app.launch()
        app.open(URL(string: PairingUITests.demoLink)!)
        app.buttons["pairing.done"].tapAfterWaiting(timeout: 15)

        let send = app.buttons["add.send"]
        XCTAssertTrue(send.waitForExistence(timeout: 20), "the Add screen did not open")
        XCTAssertTrue(app.staticTexts["The current save is first copied (backup copy)"].waitForExistence(timeout: 10))
        XCTAssertFalse(send.isEnabled, "a save needs its game")
        app.buttons["add.game"].tapAfterWaiting()
        app.buttons["picker.system.GBA"].tapAfterWaiting(timeout: 15)
        app.buttons["picker.game.\(GameDataUITest.demoGame)"].tapAfterWaiting(timeout: 15)
        attachScreenshot(of: app, named: "add-save")
        send.tapWhenEnabled(timeout: 10)

        XCTAssertTrue(app.staticTexts["upload.done"].waitForExistence(timeout: 30), "upload not finished")
        XCTAssertTrue(app.staticTexts["upload.done"].label.contains("backup copy"))
        attachScreenshot(of: app, named: "add-save-sent")
    }

    @MainActor
    private func backupCopies(_ app: XCUIApplication, timeout: TimeInterval) throws {
        app.buttons["game.save"].tapAfterWaiting(timeout: timeout)
        let label = "UITest copy \(Int(Date().timeIntervalSince1970) % 100_000)"

        // Create a copy with a label.
        app.buttons["save.createBackup"].tapAfterWaiting(timeout: timeout)
        let field = app.textFields["backup.label"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        field.tap()
        field.typeText(label)
        app.buttons["backup.create"].tap()
        GameDataUITest.waitForNotice(app, timeout: timeout)
        let row = GameDataUITest.button(app, "backup.row", containing: label)
        XCTAssertTrue(row.waitForExistence(timeout: timeout), "copy not listed")
        attachScreenshot(of: app, named: "save-copies")

        // Restore it: the confirmation says the current save is copied first.
        row.tap()
        app.buttons["backup.restore"].tapAfterWaiting()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 10))
        XCTAssertTrue(alert.staticTexts.matching(NSPredicate(format: "label CONTAINS 'first copied'")).firstMatch.exists)
        alert.buttons["backup.restoreConfirm"].tap()
        GameDataUITest.waitForNotice(app, timeout: timeout)
        XCTAssertTrue(GameDataUITest.button(app, "backup.row", containing: "Before restoration").waitForExistence(timeout: timeout))

        // Download the save: share sheet.
        app.buttons["save.download"].tapAfterWaiting(timeout: timeout)
        GameDataUITest.expectShareSheetThenClose(app)

        // Delete the copy.
        row.tapAfterWaiting(timeout: timeout)
        app.buttons["backup.delete"].tapAfterWaiting()
        XCTAssertTrue(alert.waitForExistence(timeout: 10))
        alert.buttons["backup.deleteConfirm"].tap()
        GameDataUITest.waitForNotice(app, timeout: timeout)
        XCTAssertTrue(row.waitForNonExistence(timeout: timeout), "copy still listed")
    }
}
