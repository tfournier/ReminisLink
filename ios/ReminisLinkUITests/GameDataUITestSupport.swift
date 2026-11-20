import XCTest

/// Shared steps of the save, states and cheats UI tests (milestone M5).
enum GameDataUITest {
    /// The game of the fake SD card of `make headless` (testdata/sdcard)
    /// with a save, states (slots 0, 1 and 9) and cheats.
    static let headlessGame = "Starlight Courier"
    /// The demo game with a save, three states (slot 0 locked) and cheats.
    static let demoGame = "Thistledown Isles"
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
        openGame(app, system: "HA", game: demoGame, timeout: 15)
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
        openLibraryGame(app, system: system, game: game, timeout: timeout)
        XCTAssertTrue(app.staticTexts["game.title"].waitForExistence(timeout: timeout))
    }

    /// The Library tab of the open console (M11).
    @MainActor
    static func openLibraryTab(_ app: XCUIApplication, timeout: TimeInterval) {
        app.tabBars.buttons["Library"].tapWhenEnabled(timeout: timeout)
    }

    /// From the open console to a game: the flat library (searched by
    /// name), or the systems when the pak browses per system (an older
    /// pak, or more than 20 000 games).
    @MainActor
    static func openLibraryGame(_ app: XCUIApplication, system: String, game: String, timeout: TimeInterval) {
        openLibraryTab(app, timeout: timeout)
        // The first row of that name: two systems with the same label (GBA
        // and MGBA of testdata/sdcard) can each hold a game of that name;
        // the flat list keeps the pak's order (system folder) between them.
        let row = app.buttons.matching(identifier: "game.\(game)").firstMatch
        let systemRow = app.buttons["system.\(system)"]
        let deadline = Date().addingTimeInterval(timeout)
        while !row.exists && !systemRow.exists && Date() < deadline {
            _ = row.waitForExistence(timeout: 0.5)
        }
        if !row.exists && systemRow.exists {
            systemRow.tap()
        } else if !row.isHittable {
            search(app, game)
        }
        row.tapAfterWaiting(timeout: timeout)
    }

    /// Types in the search field of the flat library.
    @MainActor
    static func search(_ app: XCUIApplication, _ text: String) {
        let field = app.searchFields.firstMatch
        if !field.waitForExistence(timeout: 2) {
            app.swipeDown()
        }
        field.tapAfterWaiting(timeout: 5)
        field.typeText(text)
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
        // A tap while the sheet still slides in is lost: wait until its
        // close button can be tapped, and tap again if the sheet stays.
        let close = app.buttons.matching(NSPredicate(format: "identifier == 'header.closeButton' OR label == 'Close'")).firstMatch
        for _ in 0..<3 {
            if close.waitUntilHittable(timeout: 5) { close.tap() }
            if sheet.waitForNonExistence(timeout: 5) { return }
        }
        XCTFail("share sheet still shown", file: file, line: line)
    }

    /// Dismisses the search of the flat library: its Cancel button on
    /// iOS 18; iOS 26 has a Close (xmark) button instead: the text is
    /// cleared, then the search closed.
    @MainActor
    static func endSearch(_ app: XCUIApplication) {
        let cancel = app.buttons["Cancel"].firstMatch
        if cancel.exists {
            cancel.tap()
            return
        }
        let field = app.searchFields.firstMatch
        let clear = field.buttons["Clear text"].firstMatch
        if clear.exists { clear.tap() }
        let close = app.buttons["Close"].firstMatch
        if close.exists { close.tap() }
    }

    /// Closes a confirmation dialog without choosing an action: its Cancel
    /// button on iOS 18; on iOS 26 it is a popover without one, closed by
    /// a tap outside.
    @MainActor
    static func dismissDialog(_ app: XCUIApplication) {
        let cancel = app.buttons["Cancel"].firstMatch
        if cancel.exists {
            cancel.tap()
        } else {
            app.otherElements["PopoverDismissRegion"].firstMatch.tap()
        }
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
