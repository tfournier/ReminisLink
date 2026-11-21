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

    /// A deleted backup copy (a replaced save) replaces the current save,
    /// which is kept as a backup copy first.
    @MainActor
    func testReplaceTheCurrentSaveWithADeletedCopy() throws {
        let app = GameDataUITest.openDemoGame()
        app.buttons["game.save"].tapAfterWaiting(timeout: 15)
        let label = "Copy \(Int(Date().timeIntervalSince1970) % 100_000)"
        app.buttons["save.createBackup"].tapAfterWaiting(timeout: 15)
        let field = app.textFields["backup.label"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        field.tap()
        field.typeText(label)
        app.buttons["backup.create"].tap()
        let row = GameDataUITest.button(app, "backup.row", containing: label)
        XCTAssertTrue(row.waitForExistence(timeout: 15), "copy not listed")
        row.tap()
        app.dialogButton("backup.delete").tapAfterWaiting()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 10))
        alert.dialogButton("backup.deleteConfirm").tap()
        XCTAssertTrue(row.waitForNonExistence(timeout: 15), "copy still listed")

        app.consoleTabs["Trash"].tapWhenEnabled(timeout: 10)
        let entry = GameDataUITest.button(app, "trash.entry", containing: GameDataUITest.demoGame)
        entry.tapAfterWaiting(timeout: 20)
        let replace = app.buttons["trashRestore.replace"]
        XCTAssertTrue(replace.waitForExistence(timeout: 20), "replace not offered")
        attachScreenshot(of: app, named: "trash-replace-preview")
        replace.tap()
        app.dialogButton("trashRestore.replaceConfirm").tapAfterWaiting(timeout: 10)
        let done = app.buttons["trashRestore.done"]
        XCTAssertTrue(done.waitForExistence(timeout: 20), "not replaced")
        XCTAssertFalse(app.descendants(matching: .any)["error"].exists, "an error is shown")
        attachScreenshot(of: app, named: "trash-replace-done")
        done.tap()
        XCTAssertTrue(entry.waitForNonExistence(timeout: 20), "entry still in the trash")

        // Back on the save screen: the copy is listed again, and the save
        // it replaced is kept as a copy.
        app.consoleTabs["Library"].tapWhenEnabled(timeout: 10)
        XCTAssertTrue(row.waitForExistence(timeout: 20), "copy not back")
        XCTAssertTrue(GameDataUITest.button(app, "backup.row", containing: "Before restoration").waitForExistence(timeout: 20))
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
