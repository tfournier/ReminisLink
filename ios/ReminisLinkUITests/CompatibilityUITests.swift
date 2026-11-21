import XCTest

/// Milestone M17: when the app and ReminisLink on the console cannot work
/// together, the app says which one to update, with a link, instead of a
/// generic error. Against the fake pak (`RL_DEMO=1`), made older than the
/// app's minimum or asking for a newer app.
final class CompatibilityUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor
    func testAnOldPakAsksToUpdateThePak() throws {
        let app = launch(["RL_UITEST_PAK_VERSION": "0.9.0"])
        app.open(URL(string: PairingUITests.demoLink)!)

        let update = app.descendants(matching: .any)["error.updatePak"]
        XCTAssertTrue(update.waitForExistence(timeout: 15), "no update link for the pak")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'Update the ReminisLink pak on the console'"))
            .firstMatch.exists)
        XCTAssertFalse(app.buttons["pairing.done"].exists)
        attachScreenshot(app, "update-pak")
    }

    @MainActor
    func testAPakThatNeedsANewerAppAsksToUpdateTheApp() throws {
        let app = launch(["RL_UITEST_MIN_CLIENT_VERSION": "99.0.0"])
        app.open(URL(string: PairingUITests.demoLink)!)

        let update = app.descendants(matching: .any)["error.updateApp"]
        XCTAssertTrue(update.waitForExistence(timeout: 15), "no App Store link")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'Update the ReminisLink app'"))
            .firstMatch.exists)
        XCTAssertFalse(app.buttons["pairing.done"].exists)
        attachScreenshot(app, "update-app")
    }

    @MainActor
    private func launch(_ environment: [String: String]) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        for (key, value) in environment { app.launchEnvironment[key] = value }
        app.launch()
        XCTAssertTrue(app.descendants(matching: .any)["welcome"].waitForExistence(timeout: 10))
        return app
    }

    @MainActor
    private func attachScreenshot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
