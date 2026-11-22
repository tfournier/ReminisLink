import XCTest

/// Milestone M22: the screenshots of a console, against the demo console
/// (`RL_DEMO`, images drawn in code), as the other feature tests. The grid
/// shows the previews; one opens whole; Share downloads it to the share
/// sheet; two are selected and moved to the trash of the console after a
/// confirmation, where one entry holds them.
final class ScreenshotsUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    /// Taps the middle of a cell of the grid. On iPad, with the bars of the
    /// selection shown, XCTest reports the cells under the scroll edge
    /// effect of the bars as not hittable although a tap reaches them.
    @MainActor
    private func tapCell(_ cell: XCUIElement, file: StaticString = #filePath, line: UInt = #line) {
        XCTAssertTrue(cell.waitForExistence(timeout: 5), "no cell", file: file, line: line)
        cell.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
    }

    @MainActor
    func testScreenshotsInDemoMode() throws {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        app.launch()
        app.open(URL(string: PairingUITests.demoLink)!)
        app.buttons["pairing.done"].tapAfterWaiting(timeout: 15)

        // Console tab > Screenshots, once the console answered.
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS 'DemoOS-20261013'")).firstMatch
            .waitForExistence(timeout: 15), "not connected")
        let row = app.buttons["console.screenshots"]
        XCTAssertTrue(GameDataUITest.scrollTo(row, in: app), "no Screenshots row")
        row.tap()
        XCTAssertTrue(app.navigationBars["Screenshots"].waitForExistence(timeout: 10))
        let cells = app.buttons.matching(identifier: "screenshots.cell")
        XCTAssertTrue(cells.firstMatch.waitForExistence(timeout: 10), "no screenshot")
        XCTAssertTrue(cells.firstMatch.label.contains("Thistledown Isles"), cells.firstMatch.label)
        let before = cells.count
        XCTAssertGreaterThanOrEqual(before, 4)
        attachScreenshot(of: app, named: "screenshots-grid")

        // One screenshot, whole; Share downloads it (demo: no share sheet).
        cells.firstMatch.tap()
        XCTAssertTrue(app.images["screenshot.image"].waitForExistence(timeout: 15), "the image did not load")
        attachScreenshot(of: app, named: "screenshot-detail")
        app.buttons["screenshot.share"].tapAfterWaiting()
        GameDataUITest.expectShareSheetThenClose(app)
        app.goBack()

        // Select two, move them to the trash.
        app.buttons["screenshots.select"].tapAfterWaiting()
        tapCell(cells.element(boundBy: 0))
        tapCell(cells.element(boundBy: 1))
        XCTAssertTrue(app.staticTexts["2 Selected"].waitForExistence(timeout: 5), "not 2 selected")
        attachScreenshot(of: app, named: "screenshots-selected")
        app.buttons["screenshots.trash"].tapAfterWaiting()
        app.dialogButton("screenshots.trash.confirm").tapAfterWaiting()
        let fewer = NSPredicate(format: "count == %d", before - 2)
        expectation(for: fewer, evaluatedWith: cells)
        waitForExpectations(timeout: 15)
        XCTAssertFalse(app.descendants(matching: .any)["error"].exists, "an error is shown")

        // The trash of the console holds them, in one entry.
        app.consoleTabs["Trash"].tap()
        XCTAssertTrue(app.staticTexts["2 screenshots"].waitForExistence(timeout: 10), "no trash entry for the screenshots")
        attachScreenshot(of: app, named: "screenshots-trash")
    }

    /// The demo mode of the published app (M16, "Try Without a Console"):
    /// the screenshots are there, and nothing leaves the app (Share and
    /// Save to Photos confirm instead).
    @MainActor
    func testScreenshotsInThePublishedDemo() throws {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_UITEST_MEMORY_STORE"] = "1"
        app.launch()
        app.buttons["welcome.demo"].tapAfterWaiting(timeout: 10)
        XCTAssertTrue(app.buttons["demo.quit"].waitForExistence(timeout: 10), "not in the demo")
        app.consoleTabs["Console"].tap()
        let row = app.buttons["console.screenshots"]
        XCTAssertTrue(GameDataUITest.scrollTo(row, in: app), "no Screenshots row")
        row.tap()
        let cells = app.buttons.matching(identifier: "screenshots.cell")
        XCTAssertTrue(cells.firstMatch.waitForExistence(timeout: 10), "no screenshot")

        app.buttons["screenshots.select"].tapAfterWaiting()
        tapCell(cells.element(boundBy: 0))
        app.buttons["screenshots.photos"].tapWhenEnabled()
        let saved = app.alerts.firstMatch
        XCTAssertTrue(saved.waitForExistence(timeout: 10), "no confirmation in the demo")
        saved.dialogButton("demo.photos.ok").tap()
        app.buttons["screenshots.share"].tapAfterWaiting()
        let downloaded = app.alerts.firstMatch
        XCTAssertTrue(downloaded.waitForExistence(timeout: 15), "no confirmation of the demo download")
        XCTAssertFalse(app.otherElements["ActivityListView"].exists, "the share sheet opened in the demo")
        downloaded.dialogButton("demo.export.ok").tap()
        app.buttons["demo.quit"].tap()
        XCTAssertTrue(app.buttons["welcome.demo"].waitForExistence(timeout: 10), "not back on the first screen")
    }

    /// Codex review of M22: the selection bar in French, whose texts are
    /// the longest, keeps its three actions and the count reachable (the
    /// screenshot is kept for review).
    @MainActor
    func testTheSelectionBarInFrench() throws {
        let app = XCUIApplication()
        app.launchArguments += ["-AppleLanguages", "(fr)", "-AppleLocale", "fr_FR"]
        app.launchEnvironment["RL_UITEST_MEMORY_STORE"] = "1"
        app.launch()
        app.buttons["welcome.demo"].tapAfterWaiting(timeout: 10)
        XCTAssertTrue(app.buttons["demo.quit"].waitForExistence(timeout: 10), "not in the demo")
        app.consoleTabs["Console"].tap()
        let row = app.buttons["console.screenshots"]
        XCTAssertTrue(GameDataUITest.scrollTo(row, in: app), "no Screenshots row")
        row.tap()
        let cells = app.buttons.matching(identifier: "screenshots.cell")
        XCTAssertTrue(cells.firstMatch.waitForExistence(timeout: 10), "no screenshot")
        app.buttons["screenshots.select"].tapAfterWaiting()
        tapCell(cells.element(boundBy: 0))
        tapCell(cells.element(boundBy: 1))
        XCTAssertTrue(app.staticTexts["2 sélectionnée(s)"].waitForExistence(timeout: 5), "no French count")
        attachScreenshot(of: app, named: "screenshots-selection-fr")
        for id in ["screenshots.share", "screenshots.photos", "screenshots.trash"] {
            XCTAssertTrue(app.buttons[id].isHittable, "\(id) not reachable")
        }
        // The count is never cut ("2 séle…").
        let count = app.staticTexts["screenshots.count"]
        XCTAssertEqual(count.label, "2 sélectionnée(s)")
        // Select All (bottom bar) and Done (top) stay visible, iPad too.
        XCTAssertTrue(app.buttons["screenshots.select"].isHittable, "Done not reachable")
        let selectAll = app.buttons["screenshots.selectAll"].firstMatch
        XCTAssertTrue(selectAll.isHittable, "Select All not reachable")
        selectAll.tap()
        let all = NSPredicate(format: "label == %@", "\(cells.count) sélectionnée(s)")
        XCTAssertTrue(app.staticTexts.matching(all).firstMatch.waitForExistence(timeout: 5), "Select All did not select all")
        attachScreenshot(of: app, named: "screenshots-all-selected-fr")
    }
}
