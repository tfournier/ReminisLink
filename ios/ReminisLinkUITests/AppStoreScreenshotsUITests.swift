import XCTest

/// App Store screenshots (milestones M16, M15), taken from the demo mode
/// without its banner (`-RLScreenshots YES`, Debug only). Skipped unless
/// `TEST_RUNNER_RL_SCREENSHOTS=en` or `fr` is set: the screenshots are kept
/// as attachments of the result bundle, named `1-library` to `6-consoles`
/// (ios/README.md, "App Store screenshots").
final class AppStoreScreenshotsUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor
    func testAppStoreScreenshots() throws {
        guard let language = ProcessInfo.processInfo.environment["RL_SCREENSHOTS"], ["en", "fr"].contains(language) else {
            throw XCTSkip("Set TEST_RUNNER_RL_SCREENSHOTS to en or fr to take the App Store screenshots.")
        }
        let app = XCUIApplication()
        app.launchArguments += [
            "-AppleLanguages", "(\(language))", "-AppleLocale", language == "fr" ? "fr_FR" : "en_US",
            "-RLScreenshots", "YES",
        ]
        app.launchEnvironment["RL_UITEST_MEMORY_STORE"] = "1"
        app.launch()
        XCUIDevice.shared.orientation = .portrait

        app.buttons["welcome.demo"].tapAfterWaiting(timeout: 15)
        XCTAssertTrue(app.consoleTabs["Console"].waitForExistence(timeout: 15))
        // 1. The library: on an iPad, the list and a game side by side.
        app.consoleTabs["Library"].tapWhenEnabled(timeout: 15)
        let other = app.buttons.matching(identifier: "game.Mosslight Maze 🌙").firstMatch
        XCTAssertTrue(other.waitForExistence(timeout: 15))
        if app.showsLibraryColumns { other.tap() }
        settle()
        shoot(app, "1-library")

        // 2. A game.
        app.buttons.matching(identifier: "game.\(GameDataUITest.demoGame)").firstMatch.tapAfterWaiting(timeout: 15)
        XCTAssertTrue(app.staticTexts["game.title"].waitForExistence(timeout: 15))
        settle()
        shoot(app, "2-game")

        // 3. Its states.
        app.buttons["game.states"].tapAfterWaiting(timeout: 15)
        XCTAssertTrue(app.buttons["state.0"].waitForExistence(timeout: 15))
        settle()
        shoot(app, "3-states")

        // 4. Its cheats.
        app.goBack()
        app.buttons["game.cheats"].tapAfterWaiting(timeout: 15)
        XCTAssertTrue(app.switches.matching(identifier: "cheat.toggle").firstMatch.waitForExistence(timeout: 15))
        settle()
        shoot(app, "4-cheats")

        // 5. The apps of the console.
        app.consoleTabs["Apps"].tapWhenEnabled(timeout: 10)
        XCTAssertTrue(app.navigationBars["Apps"].waitForExistence(timeout: 10))
        settle(3)
        shoot(app, "5-apps")

        // 6. The list of consoles: its button is on the first screen of
        // the Console tab (M23: Apps is a tab, with no Back button).
        app.consoleTabs["Console"].tapWhenEnabled(timeout: 10)
        app.buttons["tab.consoles"].tapAfterWaiting()
        XCTAssertTrue(app.buttons["console.\(PairingUITests.demoDeviceId)"].waitForExistence(timeout: 10)
            || app.staticTexts[language == "fr" ? "Console de démo" : "Demo Handheld"].waitForExistence(timeout: 10))
        settle()
        shoot(app, "6-consoles")
    }

    /// Lets images load and animations end.
    private func settle(_ seconds: UInt32 = 2) {
        sleep(seconds)
    }

    /// The whole screen, status bar included, at the size of the device.
    @MainActor
    private func shoot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
