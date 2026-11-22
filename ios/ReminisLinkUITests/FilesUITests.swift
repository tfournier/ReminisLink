import XCTest

/// Milestone M23 in demo mode: the Files tab (a new folder, its deletion to
/// the trash with a confirmation that names it, the protected folders) and
/// the rename of a game with its cheat count.
final class FilesUITests: XCTestCase {
    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    @MainActor
    private func launchPaired() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        app.launch()
        app.open(URL(string: PairingUITests.demoLink)!)
        app.buttons["pairing.done"].tapAfterWaiting(timeout: 15)
        return app
    }

    @MainActor
    func testNewFolderThenTrashInDemoMode() throws {
        let app = launchPaired()
        app.consoleTabs["Files"].tapWhenEnabled(timeout: 15)
        XCTAssertTrue(app.buttons["sd.Roms"].waitForExistence(timeout: 10), "no Roms folder")
        // Downloads: a folder the app may change.
        app.buttons["sd.Downloads"].tap()

        app.buttons["sd.add"].tapAfterWaiting()
        app.buttons["sd.newFolder"].tapAfterWaiting()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 10), "no New Folder alert")
        let field = alert.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        field.typeText("UITest Folder")
        alert.dialogButton("sd.newFolder.create").tap()
        let folder = app.buttons["sd.UITest Folder"]
        XCTAssertTrue(folder.waitForExistence(timeout: 10), "folder not listed")
        attachScreenshot(of: app, named: "new-folder")

        folder.swipeLeft()
        app.buttons["Move to Trash"].firstMatch.tapAfterWaiting()
        // The confirmation names what goes to the trash.
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'UITest Folder'")).firstMatch.waitForExistence(timeout: 5))
        attachScreenshot(of: app, named: "trash-confirmation")
        app.dialogButton("sd.trash.confirm").tapAfterWaiting()
        XCTAssertTrue(folder.waitForNonExistence(timeout: 10), "folder still listed")

        // In the trash of the console.
        app.openTrash()
        XCTAssertTrue(GameDataUITest.button(app, "trash.entry", containing: "UITest Folder").waitForExistence(timeout: 10))
    }

    @MainActor
    func testRenameAGameInDemoMode() throws {
        let app = GameDataUITest.openDemoGame()
        // M23: the number of cheats, not "Yes".
        let cheats = app.buttons["game.cheats"]
        XCTAssertTrue(cheats.waitForExistence(timeout: 10))
        XCTAssertTrue(cheats.label.contains("12"), cheats.label)

        let rename = app.buttons["game.rename"]
        XCTAssertTrue(GameDataUITest.scrollTo(rename, in: app), "no Rename button")
        rename.tap()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 10), "no Rename alert")
        let field = alert.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        XCTAssertEqual(field.value as? String, GameDataUITest.demoGame)
        // The cursor is at the end of the name: add to it.
        field.typeText(" (Europe)")
        attachScreenshot(of: app, named: "rename")
        alert.dialogButton("game.rename.confirm").tap()
        XCTAssertTrue(app.navigationBars["Thistledown Isles (Europe)"].waitForExistence(timeout: 10), "not renamed")
        XCTAssertTrue(app.buttons["game.cheats"].label.contains("12"))
    }
}
