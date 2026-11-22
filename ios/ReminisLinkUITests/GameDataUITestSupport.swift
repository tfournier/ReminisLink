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
        app.consoleTabs["Library"].tapWhenEnabled(timeout: timeout)
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

/// The tabs of an open console (M11), in the presentation actually shown
/// (M15): the tab bar at the bottom of a narrow window, or, in a wide one,
/// the bar at the top (or the sidebar) whose items are named after their
/// symbol and are not grouped in a tab bar.
@MainActor
struct ConsoleTabBar {
    let app: XCUIApplication

    /// The symbol of each tab and its labels (English, French: some tests
    /// run in the language of the simulator).
    static let tabs: [String: (symbol: String, labels: [String])] = [
        "Console": ("gamecontroller", ["Console"]),
        "Library": ("square.grid.2x2", ["Library", "Bibliothèque"]),
        "Apps": ("square.stack.3d.down.right", ["Apps"]),
        "Files": ("folder", ["Files", "Fichiers"]),
    ]

    /// M23: Transfers and Trash open from the Console tab
    /// (``XCUIApplication/openConsoleRow(_:)``).
    static let order = ["Console", "Library", "Apps", "Files"]

    /// A tab of the bottom tab bar, by position, whatever the language.
    private func bottom(_ name: String) -> XCUIElement {
        guard let index = Self.order.firstIndex(of: name) else { return app.tabBars.buttons[name] }
        return app.tabBars.buttons.element(boundBy: index)
    }

    /// A tab of the top bar or of the sidebar of a wide window.
    private func wide(_ name: String) -> XCUIElement {
        let tab = Self.tabs[name] ?? (name, [name])
        let predicate = NSPredicate(format: "identifier == %@ AND label IN %@", tab.symbol, tab.labels)
        // A button of the top bar; a cell when the sidebar is open.
        let button = app.buttons.matching(predicate).firstMatch
        return button.exists ? button : app.cells.matching(predicate).firstMatch
    }

    /// A tab by its English name, in the presentation shown (waits up to
    /// 15 s for the tabs to appear).
    subscript(name: String) -> XCUIElement {
        let deadline = Date().addingTimeInterval(15)
        repeat {
            if app.tabBars.firstMatch.exists { return bottom(name) }
            let item = wide(name)
            if item.exists { return item }
            _ = app.tabBars.firstMatch.waitForExistence(timeout: 0.5)
        } while Date() < deadline
        return bottom(name)
    }

    /// Some presentation of the tabs is shown.
    var exists: Bool { app.tabBars.firstMatch.exists || wide("Console").exists }

    func waitForExistence(timeout: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        repeat {
            if exists { return true }
            _ = app.tabBars.firstMatch.waitForExistence(timeout: 0.5)
        } while Date() < deadline
        return exists
    }
}

extension XCUIApplication {
    var consoleTabs: ConsoleTabBar { ConsoleTabBar(app: self) }

    /// M23: opens a row of the dashboard (`console.trash`,
    /// `console.transfers`, `console.screenshots`): the Console tab, back
    /// to its first screen, then the row (scrolled to, either way: the
    /// dashboard may have been scrolled down to its Console section).
    func openConsoleRow(_ identifier: String, file: StaticString = #filePath, line: UInt = #line) {
        consoleTabs["Console"].tapWhenEnabled(timeout: 10, file: file, line: line)
        let row = buttons[identifier].firstMatch
        var backs = 0
        while !row.waitForExistence(timeout: 2) && backs < 4 && !navigationBars.buttons["tab.consoles"].exists {
            goBack()
            backs += 1
        }
        // These rows are shown even offline: on the first screen, a missing
        // one is above the part of the list shown.
        var downs = 0
        while !row.waitForExistence(timeout: 1) && downs < 4 {
            swipeDown()
            downs += 1
        }
        XCTAssertTrue(GameDataUITest.scrollTo(row, in: self), "no \(identifier) row", file: file, line: line)
        row.tap()
    }

    /// The IP address of the demo console on its dashboard (the Console
    /// tab): the console is connected and shown. M23: the Transfers, Trash
    /// and Screenshots rows put the Console section below the fold on an
    /// iPhone, where a row not shown yet does not exist: once the console
    /// answered (its ReminisLink version is listed), the list is scrolled
    /// up to the address.
    func waitForDemoAddress(timeout: TimeInterval) -> Bool {
        let address = descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS '192.168.1.42'")).firstMatch
        let answered = staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'ReminisLink version'")).firstMatch
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if address.exists { return true }
            if answered.exists {
                swipeUp()
            } else {
                _ = answered.waitForExistence(timeout: 0.5)
            }
        }
        return address.exists
    }

    /// M23: the Trash screen, from the Console tab.
    func openTrash(file: StaticString = #filePath, line: UInt = #line) {
        openConsoleRow("console.trash", file: file, line: line)
    }

    /// The Library tab shows the list and the selection side by side (M15).
    var showsLibraryColumns: Bool {
        descendants(matching: .any)["library.columns"].firstMatch.exists
    }

    /// Taps the Back button of the screen shown; never another button. The
    /// system names it `BackButton` in the detail column of a wide window;
    /// in one stack (iPhone) it has no identifier and is labelled with the
    /// previous screen, at the leading edge of the bar: every button the app
    /// puts in a navigation bar has an identifier.
    func goBack(file: StaticString = #filePath, line: UInt = #line) {
        let deadline = Date().addingTimeInterval(5)
        repeat {
            let named = navigationBars.buttons["BackButton"].firstMatch
            if named.exists {
                named.tap()
                return
            }
            for bar in navigationBars.allElementsBoundByIndex where bar.isHittable {
                let leading = bar.buttons.matching(NSPredicate(format: "identifier == ''")).allElementsBoundByIndex
                    .first { $0.frame.minX - bar.frame.minX < 24 && $0.isHittable }
                if let leading {
                    leading.tap()
                    return
                }
            }
            _ = navigationBars.buttons["BackButton"].firstMatch.waitForExistence(timeout: 0.5)
        } while Date() < deadline
        XCTFail("no Back button", file: file, line: line)
    }

    /// Back to the list of games: a Back button in one stack; nothing to
    /// do when the list is beside the game (M15).
    func showLibraryList(file: StaticString = #filePath, line: UInt = #line) {
        if !showsLibraryColumns { goBack(file: file, line: line) }
    }
}

@MainActor
enum UITestDevice {
    /// Texts that vary by device in the String Catalog ("this iPhone",
    /// "this device"): the device itself, not the presentation.
    static var isPhone: Bool { UIDevice.current.userInterfaceIdiom == .phone }
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
