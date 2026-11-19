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
        let tabs = app.tabBars.firstMatch
        XCTAssertTrue(tabs.waitForExistence(timeout: 15), "no tab bar")
        for name in ["Console", "Library", "Transfers", "Trash"] {
            XCTAssertTrue(tabs.buttons[name].exists, "no \(name) tab")
        }
        // The Console tab: the dashboard, without adding (M11).
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS '192.168.1.42'")).firstMatch
            .waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["console.add"].exists, "adding from the Console tab")
        XCTAssertFalse(app.buttons.matching(NSPredicate(format: "label CONTAINS 'Add Files'")).firstMatch.isHittable)
        attachScreenshot(of: app, named: "tab-console")

        tabs.buttons["Library"].tap()
        XCTAssertTrue(app.buttons["library.add"].waitForExistence(timeout: 10), "adding stays in the Library")
        tabs.buttons["Transfers"].tap()
        XCTAssertTrue(app.navigationBars["Transfers"].waitForExistence(timeout: 10))
        tabs.buttons["Trash"].tap()
        XCTAssertTrue(app.navigationBars["Trash"].waitForExistence(timeout: 10))
        attachScreenshot(of: app, named: "tab-trash")

        // Back to the list of consoles, then into the console again.
        app.buttons["tab.consoles"].tapAfterWaiting()
        XCTAssertTrue(app.navigationBars["Consoles"].waitForExistence(timeout: 10))
        XCTAssertFalse(tabs.exists, "the list has no tab bar")
        app.buttons["console.\(PairingUITests.demoDeviceId)"].tapAfterWaiting()
        XCTAssertTrue(app.tabBars.firstMatch.waitForExistence(timeout: 10))
        XCTAssertTrue(app.tabBars.buttons["Console"].isSelected)
    }

    @MainActor
    func testFlatLibrarySearchAndFiltersInDemoMode() throws {
        let app = launchPaired()
        GameDataUITest.openLibraryTab(app, timeout: 15)
        // Every game of every system in one list.
        for name in ["Sky Garden Quest", "Moonlit Maze 🌙", "Starlight Saga", "Tiny Tactics (Demo)"] {
            XCTAssertTrue(app.buttons["game.\(name)"].waitForExistence(timeout: 10), "\(name) not listed")
        }
        XCTAssertTrue(app.buttons["game.Moonlit Maze 🌙"].label.contains("Game Boy"))
        attachScreenshot(of: app, named: "library-flat")

        // Search: name or file name.
        GameDataUITest.search(app, "moon")
        XCTAssertTrue(app.buttons["game.Moonlit Maze 🌙"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["game.Sky Garden Quest"].waitForNonExistence(timeout: 5))
        attachScreenshot(of: app, named: "library-search")
        app.buttons["Cancel"].firstMatch.tap()
        XCTAssertTrue(app.buttons["game.Sky Garden Quest"].waitForExistence(timeout: 5))

        // Filter: games with cheats.
        app.buttons["library.filter"].tapAfterWaiting()
        app.buttons["Has Cheats"].tapAfterWaiting()
        XCTAssertTrue(app.buttons["game.Pixel Runner"].waitForNonExistence(timeout: 5), "filter not applied")
        XCTAssertTrue(app.buttons["game.Sky Garden Quest"].exists)
        XCTAssertTrue(app.buttons["game.Cartographe Égaré"].exists)
        attachScreenshot(of: app, named: "library-filter")

        // Filter: one console (the label, GBA and MGBA alike).
        app.buttons["library.filter"].tap()
        app.buttons["Filter by Console"].tapAfterWaiting()
        app.buttons["Game Boy"].tapAfterWaiting()
        XCTAssertTrue(app.buttons["game.Sky Garden Quest"].waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["No games match the filters"].waitForExistence(timeout: 5))
        app.buttons["library.clearFilters"].firstMatch.tap()
        XCTAssertTrue(app.buttons["game.Pixel Runner"].waitForExistence(timeout: 5), "filters not cleared")
    }

    @MainActor
    func testDownloadAFileOfAGameInDemoMode() throws {
        let app = GameDataUITest.openDemoGame()
        // The cheats file, then the ROM (16 MiB, with its progress).
        for name in ["Sky Garden Quest.gba.cht", "Sky Garden Quest.gba"] {
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
        XCTAssertTrue(app.tabBars.firstMatch.waitForExistence(timeout: 15))
        app.tabBars.buttons["Library"].tap()
        let notice = app.descendants(matching: .any)["consoles.closedNotice"].firstMatch
        XCTAssertTrue(notice.waitForExistence(timeout: 15), "not back to the list")
        XCTAssertTrue(notice.label.contains("The console closed ReminisLink"), notice.label)
        XCTAssertFalse(app.tabBars.firstMatch.exists)
        XCTAssertFalse(app.descendants(matching: .any)["error"].exists, "an error is shown")
        attachScreenshot(of: app, named: "console-closed")

        // ReminisLink is open again on the console: the user opens it here.
        _ = XCTWaiter.wait(for: [XCTestExpectation(description: "pak reopens")], timeout: 6)
        app.buttons["console.\(PairingUITests.demoDeviceId)"].tapAfterWaiting()
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS '192.168.1.42'")).firstMatch
            .waitForExistence(timeout: 15), "not connected again")
        XCTAssertFalse(notice.exists)
        app.tabBars.buttons["Library"].tap()
        XCTAssertTrue(app.buttons["game.Sky Garden Quest"].waitForExistence(timeout: 10))
    }
}
