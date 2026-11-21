import XCTest

/// Milestone M15: a window that changes width (Split View, Stage Manager,
/// the screens of an iPhone Duo) keeps the screen shown and its work in
/// progress. XCUITest cannot resize a window: a Debug link
/// (`reminislink://uitest-width?class=...`, honoured only with
/// `RL_UITEST_WIDTH_LINKS=1`) changes the width class of the console
/// screens instead, as a narrower or wider window does.
final class UniversalUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    /// The row of the game in the list of the Library tab.
    @MainActor
    private func listRow(_ app: XCUIApplication) -> XCUIElement {
        app.buttons.matching(identifier: "game.\(GameDataUITest.demoGame)").firstMatch
    }

    @MainActor
    private func setWidth(_ app: XCUIApplication, _ sizeClass: String) {
        // Through the system: XCUIApplication.open would launch the app again.
        XCUIDevice.shared.system.open(URL(string: "reminislink://uitest-width?class=\(sizeClass)")!)
        _ = app.staticTexts["game.title"].waitForExistence(timeout: 2)
    }

    /// The visible effect, with the game shown: in a wide window the list
    /// and the game side by side, in a narrow one the game alone.
    @MainActor
    private func expectLayout(_ app: XCUIApplication, wide: Bool, file: StaticString = #filePath, line: UInt = #line) {
        let title = app.staticTexts["game.title"]
        XCTAssertTrue(title.waitForExistence(timeout: 10), "the game is not shown", file: file, line: line)
        let row = listRow(app)
        let deadline = Date().addingTimeInterval(10)
        while (row.exists && row.isHittable) != wide && Date() < deadline {
            _ = row.waitForExistence(timeout: 0.5)
        }
        XCTAssertEqual(row.exists && row.isHittable, wide, wide ? "the list is not beside the game" : "the list is still beside the game",
                       file: file, line: line)
    }

    /// Wide, narrow, wide again: a download keeps going, a cheat being
    /// edited keeps its sheet and its draft, the Patch sheet stays open;
    /// in a narrow window Back from the game reaches the list.
    @MainActor
    func testAChangeOfWidthKeepsTheWorkInProgressInDemoMode() throws {
        let app = GameDataUITest.openDemoGame(["RL_UITEST_DOWNLOAD_DELAY": "10", "RL_UITEST_WIDTH_LINKS": "1"])
        // A window that is wide to begin with (an iPad), whatever the device.
        guard app.showsLibraryColumns else {
            throw XCTSkip("The window is narrow: a change to a wide window is not what this window does.")
        }
        setWidth(app, "regular")
        expectLayout(app, wide: true)

        // A download in progress (its answer comes 10 s late, under the 15 s request timeout). Each check
        // accepts a download that already ended: its share sheet is open.
        let button = app.buttons.matching(identifier: "file.download")
            .matching(NSPredicate(format: "label == %@", "Download Thistledown Isles.hab.sav")).firstMatch
        XCTAssertTrue(GameDataUITest.scrollTo(button, in: app), "no download button")
        button.tap()
        let progress = app.descendants(matching: .any)["file.progress"].firstMatch
        let shareSheet = app.otherElements["ActivityListView"]
        XCTAssertTrue(progress.waitForExistence(timeout: 5), "no download in progress")
        setWidth(app, "compact")
        if !shareSheet.exists {
            expectLayout(app, wide: false)
            XCTAssertTrue(progress.exists || shareSheet.exists, "the download stopped when the width changed")
            setWidth(app, "regular")
            XCTAssertTrue(progress.exists || shareSheet.exists, "the download stopped when the width changed back")
        }
        // It ends normally: the share sheet opens.
        GameDataUITest.expectShareSheetThenClose(app)
        expectLayout(app, wide: true)

        // A cheat being edited, not saved.
        _ = GameDataUITest.scrollTo(app.buttons["game.cheats"], in: app)
        app.buttons["game.cheats"].tapAfterWaiting(timeout: 15)
        app.buttons.matching(identifier: "cheat.edit").firstMatch.tapAfterWaiting(timeout: 15)
        let description = app.textFields["cheat.description"]
        description.tapAfterWaiting(timeout: 10)
        description.typeText(" (draft)")
        setWidth(app, "compact")
        XCTAssertTrue(description.waitForExistence(timeout: 5), "the edit sheet closed")
        XCTAssertTrue((description.value as? String ?? "").hasSuffix(" (draft)"), "the draft is lost")
        setWidth(app, "regular")
        XCTAssertTrue(description.waitForExistence(timeout: 5), "the edit sheet closed")
        XCTAssertTrue((description.value as? String ?? "").hasSuffix(" (draft)"), "the draft is lost")
        app.buttons["Cancel"].firstMatch.tap()
        XCTAssertTrue(description.waitForNonExistence(timeout: 5))

        // The Patch sheet of the game.
        app.goBack()
        XCTAssertTrue(GameDataUITest.scrollTo(app.buttons["game.patch"], in: app), "no Patch button")
        app.buttons["game.patch"].tap()
        let source = app.segmentedControls["patch.source"]
        XCTAssertTrue(source.waitForExistence(timeout: 10))
        setWidth(app, "compact")
        XCTAssertTrue(source.waitForExistence(timeout: 5), "the Patch sheet closed")
        setWidth(app, "regular")
        XCTAssertTrue(source.waitForExistence(timeout: 5), "the Patch sheet closed")
        app.buttons["patch.close"].tapAfterWaiting(timeout: 5)
        XCTAssertTrue(source.waitForNonExistence(timeout: 5))

        // Narrow: the game alone, Back reaches the list.
        setWidth(app, "compact")
        expectLayout(app, wide: false)
        app.goBack()
        XCTAssertTrue(listRow(app).waitForExistence(timeout: 10), "Back did not reach the list")
        XCTAssertTrue(app.staticTexts["game.title"].waitForNonExistence(timeout: 5), "the game is still shown")
        setWidth(app, "system")
    }
}
