import XCTest

/// States section of the game sheet (milestone M5): the "Restored" badge,
/// the help on locks, lock, unlock, delete (refused while locked), the
/// resume history.
final class StatesUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor
    func testLocksInDemoMode() throws {
        let app = GameDataUITest.openDemoGame()
        app.buttons["game.states"].tapAfterWaiting(timeout: 15)

        // Slot 0 of the demo game was overwritten in game and put back at
        // the last start of the pak.
        let slot0 = app.buttons["state.0"]
        XCTAssertTrue(slot0.waitForExistence(timeout: 15))
        XCTAssertTrue(slot0.label.contains("Restored"), slot0.label)
        // Slots 0 to 7 are shown 1 to 8, as on NextUI; slot 9 is the resume state.
        XCTAssertTrue(slot0.label.contains("Slot 1"), slot0.label)
        let resume = app.buttons["state.9"]
        XCTAssertTrue(GameDataUITest.scrollTo(resume, in: app))
        XCTAssertTrue(resume.label.contains("Automatic Resume"), resume.label)
        app.swipeDown()
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(
            format: "label CONTAINS 'restored the next time ReminisLink is opened on the console'"
        )).firstMatch.exists, "no help on locks")
        attachScreenshot(of: app, named: "states")

        // Locked: no Delete; unlock, then delete (to the trash).
        slot0.tap()
        XCTAssertTrue(app.dialogButton("state.unlock").waitForExistence(timeout: 10))
        XCTAssertFalse(app.dialogButton("state.delete").exists, "a locked state offers Delete")
        app.dialogButton("state.unlock").tap()
        app.alerts.firstMatch.dialogButton("confirm.ok").tapAfterWaiting()
        GameDataUITest.waitForNotice(app)
        slot0.tapAfterWaiting()
        app.dialogButton("state.delete").tapAfterWaiting()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 10))
        XCTAssertTrue(alert.staticTexts.matching(NSPredicate(format: "label CONTAINS 'trash'")).firstMatch.exists)
        alert.dialogButton("confirm.ok").tap()
        GameDataUITest.waitForNotice(app)
        XCTAssertTrue(slot0.waitForNonExistence(timeout: 10))

        // Lock slot 1.
        let slot1 = app.buttons["state.1"]
        slot1.tapAfterWaiting()
        app.dialogButton("state.lock").tapAfterWaiting()
        GameDataUITest.waitForNotice(app)
        XCTAssertEqual(slot1.value as? String, "Locked")

        // Resume history: restore the newest version.
        app.swipeUp()
        app.buttons.matching(identifier: "resume.entry").firstMatch.tapAfterWaiting()
        app.alerts.firstMatch.dialogButton("confirm.ok").tapAfterWaiting()
        GameDataUITest.waitForNotice(app)
        attachScreenshot(of: app, named: "states-after")
    }

    /// Against a real pak (`make headless`), on Starlight Courier: locks
    /// slot 1 and leaves it locked, for ``testRestoredBadgeAgainstHeadlessPak``.
    ///
    ///     TEST_RUNNER_RL_UITEST_PAIR_URL_STATES='reminislink://pair?...' xcodebuild test ...
    @MainActor
    func testLocksAgainstHeadlessPak() throws {
        let app = GameDataUITest.openHeadlessGame(link: try GameDataUITest.headlessLink("STATES"))
        app.buttons["game.states"].tapAfterWaiting(timeout: 30)
        let slot1 = app.buttons["state.1"]
        XCTAssertTrue(slot1.waitForExistence(timeout: 30))
        slot1.tap()
        if app.dialogButton("state.lock").waitForExistence(timeout: 10) {
            app.dialogButton("state.lock").tap()
            GameDataUITest.waitForNotice(app, timeout: 30)
        } else {
            // Already locked by a previous run.
            GameDataUITest.dismissDialog(app)
        }
        XCTAssertEqual(slot1.value as? String, "Locked")
        // Locked: deleting is not offered; the state stays.
        slot1.tap()
        XCTAssertFalse(app.dialogButton("state.delete").waitForExistence(timeout: 3))
        GameDataUITest.dismissDialog(app)
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(
            format: "label CONTAINS 'restored the next time ReminisLink is opened on the console'"
        )).firstMatch.exists)
        attachScreenshot(of: app, named: "headless-states")
    }

    /// Acceptance test of milestone M5 against a real pak: after
    /// ``testLocksAgainstHeadlessPak``, `Starlight Courier.gba.st1` was
    /// changed by hand on the fake SD card and the pak was started again on
    /// it (ios/README.md). The app shows slot 1 "Restored".
    ///
    ///     TEST_RUNNER_RL_UITEST_PAIR_URL_RESTORED='reminislink://pair?...' xcodebuild test ...
    @MainActor
    func testRestoredBadgeAgainstHeadlessPak() throws {
        let app = GameDataUITest.openHeadlessGame(link: try GameDataUITest.headlessLink("RESTORED"))
        app.buttons["game.states"].tapAfterWaiting(timeout: 30)
        let slot1 = app.buttons["state.1"]
        XCTAssertTrue(slot1.waitForExistence(timeout: 30))
        attachScreenshot(of: app, named: "headless-restored")
        XCTAssertTrue(slot1.label.contains("Restored"), slot1.label)
    }
}
