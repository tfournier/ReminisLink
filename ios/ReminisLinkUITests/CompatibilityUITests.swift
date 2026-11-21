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

    /// The same two messages and links in French (the catalog's French
    /// entries, not the English fallback).
    @MainActor
    func testTheMessagesInFrench() throws {
        let french = ["-AppleLanguages", "(fr)", "-AppleLocale", "fr_FR"]
        var app = launch(["RL_UITEST_PAK_VERSION": "0.9.0"], arguments: french)
        app.open(URL(string: PairingUITests.demoLink)!)
        let updatePak = app.descendants(matching: .any)["error.updatePak"]
        XCTAssertTrue(updatePak.waitForExistence(timeout: 15), "no update link for the pak")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'Mets à jour le pak ReminisLink sur la console'"))
            .firstMatch.exists)
        XCTAssertEqual(updatePak.label, "Comment mettre à jour")
        attachScreenshot(app, "update-pak-fr")
        app.terminate()

        app = launch(["RL_UITEST_MIN_CLIENT_VERSION": "99.0.0"], arguments: french)
        app.open(URL(string: PairingUITests.demoLink)!)
        let updateApp = app.descendants(matching: .any)["error.updateApp"]
        XCTAssertTrue(updateApp.waitForExistence(timeout: 15), "no App Store link")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'Mets à jour l’app ReminisLink'"))
            .firstMatch.exists)
        XCTAssertEqual(updateApp.label, "Ouvrir l’App Store")
        attachScreenshot(app, "update-app-fr")
    }

    @MainActor
    private func launch(_ environment: [String: String], arguments: [String] = GameDataUITest.englishArguments) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments += arguments
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
