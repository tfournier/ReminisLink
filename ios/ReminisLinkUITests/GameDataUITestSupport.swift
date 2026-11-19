import XCTest

/// Shared steps of the save, states and cheats UI tests (milestone M5).
enum GameDataUITest {
    /// The game of the fake SD card of `make headless` (testdata/sdcard)
    /// with a save, states (slots 0, 1 and 9) and cheats.
    static let headlessGame = "Starlight Courier"
    /// The demo game with a save, three states (slot 0 locked) and cheats.
    static let demoGame = "Sky Garden Quest"
    /// The checks read English texts, whatever the simulator language.
    static let englishArguments = ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"]

    /// The pairing link of a running pak for one test. A link can be used
    /// only once, so each test against the pak reads its own variable:
    /// `TEST_RUNNER_RL_UITEST_PAIR_URL_<name>`.
    static func headlessLink(_ name: String) throws -> URL {
        let variable = "RL_UITEST_PAIR_URL_\(name)"
        guard let link = ProcessInfo.processInfo.environment[variable], let url = URL(string: link) else {
            throw XCTSkip("Set TEST_RUNNER_\(variable) to a new pairing link of a running pak (make headless).")
        }
        return url
    }

    /// Launches the demo, pairs with the fake pak and opens a game.
    @MainActor
    static func openDemoGame(_ environment: [String: String] = [:]) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments += englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        for (key, value) in environment { app.launchEnvironment[key] = value }
        app.launch()
        app.open(URL(string: PairingUITests.demoLink)!)
        openGame(app, system: "GBA", game: demoGame, timeout: 15)
        return app
    }

    /// Launches the app against a real pak (Bonjour) and opens a game.
    @MainActor
    static func openHeadlessGame(link: URL, environment: [String: String] = [:]) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments += englishArguments
        app.launchEnvironment["RL_RESET_STORE"] = "1"
        for (key, value) in environment { app.launchEnvironment[key] = value }
        app.launch()
        app.open(link)
        openGame(app, system: "GBA", game: headlessGame, timeout: 30)
        return app
    }

    /// From the pairing screen down to the game sheet.
    @MainActor
    static func openGame(_ app: XCUIApplication, system: String, game: String, timeout: TimeInterval) {
        let done = app.buttons["pairing.done"]
        XCTAssertTrue(done.waitForExistence(timeout: timeout), "pairing did not finish")
        done.tap()
        app.buttons["console.library"].tapWhenEnabled(timeout: timeout)
        app.buttons["system.\(system)"].tapAfterWaiting(timeout: timeout)
        app.buttons["game.\(game)"].tapAfterWaiting(timeout: timeout)
        XCTAssertTrue(app.staticTexts["game.title"].waitForExistence(timeout: timeout))
    }

    /// The confirmation of what an action did.
    @MainActor
    static func waitForNotice(_ app: XCUIApplication, timeout: TimeInterval = 15, file: StaticString = #filePath, line: UInt = #line) {
        let notice = app.descendants(matching: .any)["notice"].firstMatch
        XCTAssertTrue(notice.waitForExistence(timeout: timeout), "no confirmation", file: file, line: line)
        XCTAssertFalse(app.descendants(matching: .any)["error"].exists, "an error is shown", file: file, line: line)
    }

    /// The iOS share sheet opens for a downloaded file; it is then closed.
    @MainActor
    static func expectShareSheetThenClose(_ app: XCUIApplication, file: StaticString = #filePath, line: UInt = #line) {
        let sheet = app.otherElements["ActivityListView"]
        XCTAssertTrue(sheet.waitForExistence(timeout: 20), "no share sheet", file: file, line: line)
        let close = app.buttons["header.closeButton"]
        if close.exists {
            close.tap()
        } else {
            app.buttons.matching(NSPredicate(format: "label == 'Close'")).firstMatch.tap()
        }
        XCTAssertTrue(sheet.waitForNonExistence(timeout: 10), "share sheet still shown", file: file, line: line)
    }

    /// Scrolls the list until `element` exists (rows are created lazily).
    @MainActor
    static func scrollTo(_ element: XCUIElement, in app: XCUIApplication, attempts: Int = 8) -> Bool {
        if element.waitForExistence(timeout: 3) { return true }
        for _ in 0..<attempts {
            app.swipeUp()
            if element.waitForExistence(timeout: 1) { return true }
        }
        return false
    }

    /// A button whose identifier is `identifier` and whose label contains `text`.
    @MainActor
    static func button(_ app: XCUIApplication, _ identifier: String, containing text: String) -> XCUIElement {
        app.buttons.matching(identifier: identifier).matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }
}

extension XCTestCase {
    /// Kept in the result bundle, for review.
    @MainActor
    func attachScreenshot(of app: XCUIApplication, named name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
