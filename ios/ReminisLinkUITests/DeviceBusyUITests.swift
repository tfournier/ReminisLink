import XCTest

/// Writes one at a time (milestone M13, replaces the M10 lease): the demo
/// console finds the next write busy (`429 Device busy`). A user action
/// says so (nothing was changed, try again); trying again works.
final class DeviceBusyUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor
    func testABusyConsoleSaysSoThenTryingAgainWorksInDemoMode() throws {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        app.launchEnvironment["RL_UITEST_BUSY_WRITES"] = "1"
        app.launch()
        app.open(URL(string: PairingUITests.demoLink)!)
        app.buttons["pairing.done"].tapAfterWaiting(timeout: 15)

        // No read-only state any more: adding is possible at once.
        GameDataUITest.openLibraryTab(app, timeout: 15)
        XCTAssertTrue(app.buttons["library.add"].waitForExistence(timeout: 15))
        XCTAssertTrue(app.buttons["library.add"].isEnabled)
        XCTAssertFalse(app.descendants(matching: .any)["readOnly"].exists)

        // Delete a game: the console is busy with another write.
        let game = app.buttons["game.Brambleway Dash"]
        XCTAssertTrue(game.waitForExistence(timeout: 15))
        game.swipeLeft()
        app.buttons["game.delete"].tapAfterWaiting()
        app.buttons["delete.confirm"].tapWhenEnabled(timeout: 20)
        let busy = app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'The console is busy with another change'")).firstMatch
        XCTAssertTrue(busy.waitForExistence(timeout: 15), "no busy message")
        XCTAssertTrue(busy.label.contains("Nothing was changed"), busy.label)
        attachScreenshot(of: app, named: "busy")

        // Trying again deletes it.
        app.buttons["delete.confirm"].tapWhenEnabled(timeout: 20)
        XCTAssertTrue(app.descendants(matching: .any)["delete.result"].firstMatch.waitForExistence(timeout: 20))
        attachScreenshot(of: app, named: "deleted")
    }
}
