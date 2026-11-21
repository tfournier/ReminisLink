import XCTest

/// Milestone M18 against the fake pak (`RL_DEMO=1`): a deleted game comes
/// back from the Trash tab, after the preview of what comes back; the
/// entry of the demo trash comes back too. Conflicts (a place taken, the
/// free name) are covered by ReminisLinkKit's tests and the pak's smoke.
final class TrashRestoreUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor
    func testRestoreADeletedGameFromTheTrashTab() throws {
        // A game with few files: the delete button stays on screen.
        let game = "Brambleway Dash"
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        app.launch()
        app.open(URL(string: PairingUITests.demoLink)!)
        app.buttons["pairing.done"].tapAfterWaiting(timeout: 20)

        // Delete the game from the library (swipe, then the delete sheet).
        let row = libraryRow(app, game)
        row.swipeLeft()
        app.buttons["game.delete"].tapAfterWaiting(timeout: 10)
        app.buttons["delete.confirm"].tapWhenEnabled(timeout: 20)
        app.buttons["delete.done"].tapAfterWaiting(timeout: 20)
        XCTAssertTrue(row.waitForNonExistence(timeout: 20), "game still listed")

        // Trash tab: tap the entry, read the preview, restore.
        app.consoleTabs["Trash"].tapWhenEnabled(timeout: 10)
        restore(app, label: game, screenshot: "trash-restore-preview")

        // Back in the library.
        XCTAssertTrue(libraryRow(app, game).exists)

        // The entry of the demo trash puts a game back too.
        app.consoleTabs["Trash"].tapWhenEnabled(timeout: 10)
        restore(app, label: "Old Prototype", screenshot: nil)
        XCTAssertTrue(libraryRow(app, "Old Prototype").exists)
    }

    /// Opens the restore sheet of a trash entry, restores it, closes the
    /// sheet and checks the entry left the trash.
    @MainActor
    private func restore(_ app: XCUIApplication, label: String, screenshot: String?) {
        let entry = GameDataUITest.button(app, "trash.entry", containing: label)
        if !entry.waitForExistence(timeout: 10) {
            // Pull to refresh: the trash is read again.
            app.swipeDown()
        }
        entry.tapAfterWaiting(timeout: 20)
        if !app.buttons["trashRestore.confirm"].waitForExistence(timeout: 5) && entry.isHittable {
            entry.tap() // the first tap landed while the list was reloading
        }
        let confirm = app.buttons["trashRestore.confirm"]
        XCTAssertTrue(confirm.waitForExistence(timeout: 20), "no preview")
        XCTAssertFalse(app.buttons["trashRestore.rename"].exists, "an unexpected conflict")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", label)).firstMatch.exists)
        if let screenshot { attachScreenshot(of: app, named: screenshot) }
        confirm.tapWhenEnabled(timeout: 10)
        let done = app.buttons["trashRestore.done"]
        XCTAssertTrue(done.waitForExistence(timeout: 20), "not restored")
        XCTAssertFalse(app.descendants(matching: .any)["error"].exists, "an error is shown")
        if screenshot != nil { attachScreenshot(of: app, named: "trash-restore-done") }
        done.tap()
        XCTAssertTrue(entry.waitForNonExistence(timeout: 20), "entry still in the trash")
    }

    /// The row of a game in the Library tab (searched when not visible).
    @MainActor
    private func libraryRow(_ app: XCUIApplication, _ name: String) -> XCUIElement {
        GameDataUITest.openLibraryTab(app, timeout: 20)
        let row = app.buttons.matching(identifier: "game.\(name)").firstMatch
        if !row.waitForExistence(timeout: 5) || !row.isHittable {
            if app.searchFields.firstMatch.exists, let value = app.searchFields.firstMatch.value as? String, !value.isEmpty {
                GameDataUITest.endSearch(app)
            }
            if !row.waitForExistence(timeout: 3) || !row.isHittable {
                GameDataUITest.search(app, name)
            }
        }
        XCTAssertTrue(row.waitForExistence(timeout: 20), "\(name) not in the library")
        return row
    }
}
