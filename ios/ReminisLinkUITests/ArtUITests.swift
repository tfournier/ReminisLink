import XCTest

/// The image of a game (milestone M8), in the demo mode: changed with a
/// drawn test picture (Photos and Files cannot be driven by XCUITest),
/// then removed after a confirmation.
final class ArtUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor
    func testChangeThenRemoveTheImageInDemoMode() throws {
        let app = GameDataUITest.openDemoGame(["RL_UITEST_ART_PICTURE": "1"])
        let change = app.buttons["game.changeArt"]
        XCTAssertTrue(GameDataUITest.scrollTo(change, in: app), "no Change Image button")
        change.tap()
        app.buttons["art.test"].tapAfterWaiting(timeout: 10)
        GameDataUITest.waitForNotice(app)
        XCTAssertTrue(app.descendants(matching: .any)["notice"].firstMatch.label.contains("Image changed"))
        attachScreenshot(of: app, named: "art-changed")

        let remove = app.buttons["game.removeArt"]
        XCTAssertTrue(GameDataUITest.scrollTo(remove, in: app), "no Remove Image button")
        remove.tap()
        app.buttons["art.removeConfirm"].tapAfterWaiting(timeout: 10)
        let removed = app.descendants(matching: .any).matching(identifier: "notice")
            .matching(NSPredicate(format: "label CONTAINS 'Image removed'")).firstMatch
        XCTAssertTrue(removed.waitForExistence(timeout: 15), "not removed")
        XCTAssertTrue(remove.waitForNonExistence(timeout: 10), "the game still has an image")
    }
}
