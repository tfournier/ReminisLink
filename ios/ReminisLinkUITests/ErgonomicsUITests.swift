import XCTest

/// Milestone M11 in the demo mode: the tabs of an open console, the flat
/// library (search and filters), the download of a file of a game to the
/// share sheet, and the return to the list when the console closes
/// ReminisLink. (Tapping the image of a game: ArtUITests.)
final class ErgonomicsUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor
    private func launchPaired(_ environment: [String: String] = [:]) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        for (key, value) in environment { app.launchEnvironment[key] = value }
        app.launch()
        app.open(URL(string: PairingUITests.demoLink)!)
        app.buttons["pairing.done"].tapAfterWaiting(timeout: 15)
        return app
    }

    @MainActor
    func testTabBarOfAnOpenConsoleInDemoMode() throws {
        let app = launchPaired()
        let tabs = app.consoleTabs
        XCTAssertTrue(tabs.waitForExistence(timeout: 15), "no tab bar")
        for name in ["Console", "Library", "Transfers", "Trash"] {
            XCTAssertTrue(tabs[name].exists, "no \(name) tab")
        }
        // The Console tab: the dashboard, without adding (M11).
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS '192.168.1.42'")).firstMatch
            .waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["console.add"].exists, "adding from the Console tab")
        XCTAssertFalse(app.buttons.matching(NSPredicate(format: "label CONTAINS 'Add Files'")).firstMatch.isHittable)
        attachScreenshot(of: app, named: "tab-console")

        tabs["Library"].tap()
        XCTAssertTrue(app.buttons["library.add"].waitForExistence(timeout: 10), "adding stays in the Library")
        tabs["Transfers"].tap()
        XCTAssertTrue(app.navigationBars["Transfers"].waitForExistence(timeout: 10))
        tabs["Trash"].tap()
        XCTAssertTrue(app.navigationBars["Trash"].waitForExistence(timeout: 10))
        attachScreenshot(of: app, named: "tab-trash")

        // Back to the list of consoles, then into the console again.
        app.buttons["tab.consoles"].tapAfterWaiting()
        XCTAssertTrue(app.navigationBars["Consoles"].waitForExistence(timeout: 10))
        XCTAssertFalse(tabs.exists, "the list has no tab bar")
        app.buttons["console.\(PairingUITests.demoDeviceId)"].tapAfterWaiting()
        XCTAssertTrue(app.consoleTabs.waitForExistence(timeout: 10))
        XCTAssertTrue(app.consoleTabs["Console"].isSelected)
    }

    @MainActor
    func testFlatLibrarySearchAndFiltersInDemoMode() throws {
        let app = launchPaired()
        GameDataUITest.openLibraryTab(app, timeout: 15)
        // Every game of every system in one list.
        for name in ["Thistledown Isles", "Mosslight Maze 🌙", "Orrery of Nine Tides", "Quillwork Tactics"] {
            XCTAssertTrue(app.buttons["game.\(name)"].waitForExistence(timeout: 10), "\(name) not listed")
        }
        XCTAssertTrue(app.buttons["game.Mosslight Maze 🌙"].label.contains("Pocket Classic"))
        attachScreenshot(of: app, named: "library-flat")

        // Search: name or file name.
        GameDataUITest.search(app, "moss")
        XCTAssertTrue(app.buttons["game.Mosslight Maze 🌙"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["game.Thistledown Isles"].waitForNonExistence(timeout: 5))
        attachScreenshot(of: app, named: "library-search")
        GameDataUITest.endSearch(app)
        XCTAssertTrue(app.buttons["game.Thistledown Isles"].waitForExistence(timeout: 5))

        // Filter: games with cheats.
        app.buttons["library.filter"].tapAfterWaiting()
        app.buttons["Has Cheats"].tapAfterWaiting()
        XCTAssertTrue(app.buttons["game.Brambleway Dash"].waitForNonExistence(timeout: 5), "filter not applied")
        XCTAssertTrue(app.buttons["game.Thistledown Isles"].exists)
        XCTAssertTrue(app.buttons["game.Cartographe Égaré"].exists)
        attachScreenshot(of: app, named: "library-filter")

        // Filter: one console (the label, GBA and MGBA alike).
        app.buttons["library.filter"].tap()
        app.buttons["Filter by Console"].tapAfterWaiting()
        app.buttons["Pocket Classic"].tapAfterWaiting()
        XCTAssertTrue(app.buttons["game.Thistledown Isles"].waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["No games match the filters"].waitForExistence(timeout: 5))
        app.buttons["library.clearFilters"].firstMatch.tap()
        XCTAssertTrue(app.buttons["game.Brambleway Dash"].waitForExistence(timeout: 5), "filters not cleared")
    }

    @MainActor
    func testDownloadAFileOfAGameInDemoMode() throws {
        let app = GameDataUITest.openDemoGame()
        // The cheats file, then the ROM (16 MiB, with its progress).
        for name in ["Thistledown Isles.hab.cht", "Thistledown Isles.hab"] {
            let button = app.buttons.matching(identifier: "file.download")
                .matching(NSPredicate(format: "label == %@", "Download \(name)")).firstMatch
            XCTAssertTrue(GameDataUITest.scrollTo(button, in: app), "no download button for \(name)")
            button.tap()
            GameDataUITest.expectShareSheetThenClose(app)
            XCTAssertFalse(app.descendants(matching: .any)["error"].exists, "an error is shown")
        }
        attachScreenshot(of: app, named: "files")
    }

    @MainActor
    func testTheConsoleClosingGoesBackToTheListInDemoMode() throws {
        // The fake pak quits on the flat library's request, then opens
        // again 5 seconds later.
        let app = launchPaired(["RL_UITEST_PAK_CLOSES_ON": "GET /games"])
        XCTAssertTrue(app.consoleTabs.waitForExistence(timeout: 15))
        // The tabs have slid in (a tap during the transition is lost).
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS '192.168.1.42'")).firstMatch
            .waitForExistence(timeout: 15), "the console is not shown")
        app.consoleTabs["Library"].tap()
        let notice = app.descendants(matching: .any)["consoles.closedNotice"].firstMatch
        XCTAssertTrue(notice.waitForExistence(timeout: 15), "not back to the list")
        XCTAssertTrue(notice.label.contains("The console closed ReminisLink"), notice.label)
        XCTAssertFalse(app.consoleTabs.exists)
        XCTAssertFalse(app.descendants(matching: .any)["error"].exists, "an error is shown")
        attachScreenshot(of: app, named: "console-closed")

        // ReminisLink is open again on the console: the user opens it here.
        _ = XCTWaiter.wait(for: [XCTestExpectation(description: "pak reopens")], timeout: 6)
        app.buttons["console.\(PairingUITests.demoDeviceId)"].tapAfterWaiting()
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS '192.168.1.42'")).firstMatch
            .waitForExistence(timeout: 15), "not connected again")
        XCTAssertFalse(notice.exists)
        app.consoleTabs["Library"].tap()
        XCTAssertTrue(app.buttons["game.Thistledown Isles"].waitForExistence(timeout: 10))
    }
    /// Against a real pak (`make headless` on a copy of testdata/sdcard):
    /// the Library tab shows the flat list of `GET /games` (every system,
    /// subfolders included), the search finds a game of a subfolder
    /// (Tiny Trial, GBA `Hacks/Deeper`), and its ROM is downloaded up to the
    /// share sheet; then the cheats file of Pixel Garden 🌱 (GBA).
    ///
    ///     TEST_RUNNER_RL_UITEST_PAIR_URL_LIBRARY='reminislink://pair?...' xcodebuild test ...
    @MainActor
    func testLibraryAndDownloadAgainstHeadlessPak() throws {
        let link = try GameDataUITest.headlessLink("LIBRARY")
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_RESET_STORE"] = "1"
        app.launch()
        app.open(link)
        app.buttons["pairing.done"].tapAfterWaiting(timeout: 30)
        GameDataUITest.openLibraryTab(app, timeout: 30)

        // The flat list: the 10 games of testdata/sdcard, of several
        // systems, without browsing per system.
        // (The section header is shown in capitals.)
        let count = app.staticTexts.matching(NSPredicate(format: "label ==[c] '10 games'")).firstMatch
        XCTAssertTrue(count.waitForExistence(timeout: 30), "not the flat list of the 10 games")
        XCTAssertFalse(app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'system.'")).firstMatch.exists,
                       "browsing per system instead of the flat list")
        for name in ["Puddle Jumper", "Café Rêverie (France)", "Starlight Courier Remix"] {
            XCTAssertTrue(GameDataUITest.scrollTo(app.buttons["game.\(name)"], in: app), "\(name) not listed")
        }
        XCTAssertTrue(app.buttons["game.Puddle Jumper"].label.contains("Game Boy"), app.buttons["game.Puddle Jumper"].label)
        attachScreenshot(of: app, named: "library-headless")

        // Search: a game of a subfolder.
        GameDataUITest.search(app, "tiny")
        let tiny = app.buttons["game.Tiny Trial"]
        XCTAssertTrue(tiny.waitForExistence(timeout: 10), "Tiny Trial not found")
        XCTAssertTrue(app.buttons["game.Puddle Jumper"].waitForNonExistence(timeout: 5), "search not applied")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH[c] '1 game'")).firstMatch.exists, "not one result")
        attachScreenshot(of: app, named: "library-headless-search")
        tiny.tap()
        XCTAssertTrue(app.staticTexts["game.title"].waitForExistence(timeout: 30))
        download(app, "Tiny Trial.gba")
        attachScreenshot(of: app, named: "library-headless-rom")

        // The cheats file of another game (the list is beside the game in
        // two columns).
        app.showLibraryList()
        let field = app.searchFields.firstMatch
        field.tapAfterWaiting(timeout: 10)
        let clear = field.buttons["Clear text"].firstMatch
        if clear.waitForExistence(timeout: 2) { clear.tap() }
        field.typeText("pixel")
        app.buttons["game.Pixel Garden 🌱"].tapAfterWaiting(timeout: 10)
        XCTAssertTrue(app.staticTexts["game.title"].waitForExistence(timeout: 30))
        download(app, "Pixel Garden 🌱.gba.cht")
    }

    /// Against a real pak whose process is ended (SIGTERM) by the person
    /// running the test while the dashboard is shown: the app goes back to
    /// the list of consoles with the notice, without an error screen.
    /// Quit the pak (`kill -TERM <pid>`) within 90 s of the pairing.
    ///
    ///     TEST_RUNNER_RL_UITEST_PAIR_URL_CLOSING='reminislink://pair?...' xcodebuild test ...
    @MainActor
    func testTheConsoleClosingAgainstHeadlessPak() throws {
        let link = try GameDataUITest.headlessLink("CLOSING")
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_RESET_STORE"] = "1"
        app.launch()
        app.open(link)
        app.buttons["pairing.done"].tapAfterWaiting(timeout: 30)
        XCTAssertTrue(app.consoleTabs.waitForExistence(timeout: 30))
        XCTAssertTrue(app.consoleTabs["Console"].isSelected)
        attachScreenshot(of: app, named: "closing-dashboard")

        let notice = app.descendants(matching: .any)["consoles.closedNotice"].firstMatch
        XCTAssertTrue(notice.waitForExistence(timeout: 90), "not back to the list")
        XCTAssertTrue(notice.label.contains("The console closed ReminisLink"), notice.label)
        XCTAssertFalse(app.consoleTabs.exists)
        XCTAssertFalse(app.descendants(matching: .any)["error"].exists, "an error is shown")
        attachScreenshot(of: app, named: "closing-headless")
    }

    /// Taps the download button of `name`, then closes the share sheet.
    @MainActor
    private func download(_ app: XCUIApplication, _ name: String) {
        let button = app.buttons.matching(identifier: "file.download")
            .matching(NSPredicate(format: "label == %@", "Download \(name)")).firstMatch
        XCTAssertTrue(GameDataUITest.scrollTo(button, in: app), "no download button for \(name)")
        button.tap()
        GameDataUITest.expectShareSheetThenClose(app)
        XCTAssertFalse(app.descendants(matching: .any)["error"].exists, "an error is shown")
    }
}
