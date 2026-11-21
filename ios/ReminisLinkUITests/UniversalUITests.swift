import XCTest

/// Milestone M15: a window that changes width (Split View, Stage Manager,
/// the screens of an iPhone Duo) keeps the screen shown and its work in
/// progress. XCUITest cannot resize a window: a Debug link
/// (`reminislink://uitest-width?class=...`) changes the width class of the
/// console screens instead, as a narrower or wider window does.
final class UniversalUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor
    private func setWidth(_ app: XCUIApplication, _ sizeClass: String) {
        // Through the system: XCUIApplication.open would launch the app again.
        XCUIDevice.shared.system.open(URL(string: "reminislink://uitest-width?class=\(sizeClass)")!)
        let presentation = sizeClass == "compact" ? "library.stack" : "library.columns"
        if sizeClass != "system" {
            XCTAssertTrue(app.descendants(matching: .any)[presentation].firstMatch.waitForExistence(timeout: 10),
                          "not shown as \(presentation)")
        }
    }

    /// Wide, narrow, wide again: a download keeps going, a cheat being
    /// edited keeps its sheet and its draft, the Patch sheet stays open.
    @MainActor
    func testAChangeOfWidthKeepsTheWorkInProgressInDemoMode() throws {
        let app = GameDataUITest.openDemoGame(["RL_UITEST_DOWNLOAD_DELAY": "6"])
        // A window that is wide to begin with (an iPad), whatever the device.
        guard app.showsLibraryColumns else {
            throw XCTSkip("The window is narrow: a change to a wide window is not what this window does.")
        }
        setWidth(app, "regular")

        // A download in progress (its answer comes 6 s late).
        let button = app.buttons.matching(identifier: "file.download")
            .matching(NSPredicate(format: "label == %@", "Download Thistledown Isles.hab.sav")).firstMatch
        XCTAssertTrue(GameDataUITest.scrollTo(button, in: app), "no download button")
        button.tap()
        let progress = app.descendants(matching: .any)["file.progress"].firstMatch
        XCTAssertTrue(progress.waitForExistence(timeout: 5), "no download in progress")
        setWidth(app, "compact")
        XCTAssertTrue(app.staticTexts["game.title"].waitForExistence(timeout: 5), "left the game")
        XCTAssertTrue(progress.exists, "the download stopped when the width changed")
        setWidth(app, "regular")
        XCTAssertTrue(progress.exists, "the download stopped when the width changed back")
        // It ends normally: the share sheet opens.
        GameDataUITest.expectShareSheetThenClose(app)

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
        setWidth(app, "system")
    }
}
