import XCTest

/// Cheats section of the game sheet (milestone M5): a switch, a new cheat,
/// saving with If-Match, the conflict when the list changed on the console
/// (reload, never overwrite), import from the SD card, a file without
/// cheats.
final class CheatsUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor
    func testCheatsInDemoMode() throws {
        let app = GameDataUITest.openDemoGame()
        try editSaveAndImport(app, timeout: 15)
    }

    /// The first save meets a change made on the console (`412`): the app
    /// asks to reload and never overwrites.
    @MainActor
    func testConflictAsksToReloadInDemoMode() throws {
        let app = GameDataUITest.openDemoGame(["RL_UITEST_CHEATS_CONFLICT": "1"])
        app.buttons["game.cheats"].tapAfterWaiting(timeout: 15)
        toggleFirstCheat(app)
        app.buttons["cheats.save"].tapAfterWaiting()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 10), "no conflict alert")
        XCTAssertTrue(alert.label.contains("changed on the console"), alert.label)
        attachScreenshot(of: app, named: "cheats-conflict")
        alert.buttons["cheats.reload"].tap()
        // The list of the console is shown, with the cheat added there.
        XCTAssertTrue(GameDataUITest.scrollTo(GameDataUITest.button(app, "cheat.edit", containing: "Added on the console"), in: app))
        XCTAssertFalse(app.buttons["cheats.save"].exists, "local changes kept after reload")
    }

    /// Against a real pak (`make headless`), on Starlight Courier.
    ///
    ///     TEST_RUNNER_RL_UITEST_PAIR_URL_CHEATS='reminislink://pair?...' xcodebuild test ...
    @MainActor
    func testCheatsAgainstHeadlessPak() throws {
        let app = GameDataUITest.openHeadlessGame(link: try GameDataUITest.headlessLink("CHEATS"))
        try editSaveAndImport(app, timeout: 30)
    }

    @MainActor
    private func toggleFirstCheat(_ app: XCUIApplication) {
        let toggle = app.switches.matching(identifier: "cheat.toggle").firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 15), "no cheat")
        let before = toggle.value as? String
        toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.8, dy: 0.5)).tap()
        XCTAssertNotEqual(toggle.value as? String, before, "switch did not change")
        XCTAssertTrue(app.buttons["cheats.save"].waitForExistence(timeout: 5), "no Save button")
    }

    @MainActor
    private func editSaveAndImport(_ app: XCUIApplication, timeout: TimeInterval) throws {
        app.buttons["game.cheats"].tapAfterWaiting(timeout: timeout)

        // A switch, then Save.
        toggleFirstCheat(app)
        app.buttons["cheats.save"].tap()
        GameDataUITest.waitForNotice(app, timeout: timeout)
        XCTAssertFalse(app.buttons["cheats.save"].exists)

        // A new cheat, then Save.
        let name = "UITest cheat \(Int(Date().timeIntervalSince1970) % 100_000)"
        app.buttons["cheats.add"].tapAfterWaiting()
        app.buttons["cheats.addCheat"].tapAfterWaiting()
        let description = app.textFields["cheat.description"]
        XCTAssertTrue(description.waitForExistence(timeout: 10))
        description.tap()
        description.typeText(name)
        let code = app.textFields["cheat.code"]
        code.tap()
        code.typeText("02000000+0001")
        app.buttons["cheat.done"].tap()
        XCTAssertTrue(app.buttons["cheats.save"].waitForExistence(timeout: 5))
        app.buttons["cheats.save"].tap()
        GameDataUITest.waitForNotice(app, timeout: timeout)
        XCTAssertTrue(GameDataUITest.scrollTo(GameDataUITest.button(app, "cheat.edit", containing: name), in: app), "new cheat not listed")
        app.swipeDown()
        attachScreenshot(of: app, named: "cheats")

        // Import a .cht of the SD card, added to the list.
        app.buttons["cheats.add"].tapAfterWaiting()
        app.buttons["cheats.import"].tapAfterWaiting()
        app.segmentedControls["import.source"].buttons.element(boundBy: 2).tapAfterWaiting()
        app.buttons.matching(identifier: "import.sdFile").firstMatch.tapAfterWaiting(timeout: timeout)
        attachScreenshot(of: app, named: "cheats-import")
        app.buttons["import.run"].tapWhenEnabled()
        GameDataUITest.waitForNotice(app, timeout: timeout)
    }
}
