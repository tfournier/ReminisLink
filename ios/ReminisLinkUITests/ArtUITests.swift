import XCTest

/// The image of a game (milestone M8): changed with a drawn test picture
/// (Photos and Files cannot be driven by XCUITest), shown, then removed
/// after a confirmation. In the demo mode and against a real pak.
final class ArtUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor
    func testChangeThenRemoveTheImageInDemoMode() throws {
        let app = GameDataUITest.openDemoGame(["RL_UITEST_ART_PICTURE": "1"])
        changeThenRemove(app, timeout: 15)
    }

    /// Against a real pak (`make headless`), on Starlight Courier (GBA),
    /// which has an image on the test SD card.
    ///
    ///     TEST_RUNNER_RL_UITEST_PAIR_URL_ART='reminislink://pair?...' xcodebuild test ...
    @MainActor
    func testChangeThenRemoveTheImageAgainstHeadlessPak() throws {
        let link = try GameDataUITest.headlessLink("ART")
        let app = GameDataUITest.openHeadlessGame(link: link, environment: ["RL_UITEST_ART_PICTURE": "1"])
        changeThenRemove(app, timeout: 30)
    }

    @MainActor
    private func changeThenRemove(_ app: XCUIApplication, timeout: TimeInterval) {
        let art = app.descendants(matching: .any)["game.art"].firstMatch
        XCTAssertTrue(art.waitForExistence(timeout: timeout), "no game image view")
        let change = app.buttons["game.changeArt"]
        XCTAssertTrue(GameDataUITest.scrollTo(change, in: app), "no Change Image button")
        change.tap()
        app.buttons["art.test"].tapAfterWaiting(timeout: 10)
        GameDataUITest.waitForNotice(app, timeout: timeout)
        XCTAssertTrue(app.descendants(matching: .any)["notice"].firstMatch.label.contains("Image changed"))
        app.swipeDown()
        XCTAssertTrue(waitForValue(art, "image", timeout: timeout), "the new image is not shown")
        attachScreenshot(of: app, named: "art-changed")

        let remove = app.buttons["game.removeArt"]
        XCTAssertTrue(GameDataUITest.scrollTo(remove, in: app), "no Remove Image button")
        remove.tap()
        app.buttons["art.removeConfirm"].tapAfterWaiting(timeout: 10)
        let removed = app.descendants(matching: .any).matching(identifier: "notice")
            .matching(NSPredicate(format: "label CONTAINS 'Image removed'")).firstMatch
        XCTAssertTrue(removed.waitForExistence(timeout: timeout), "not removed")
        XCTAssertTrue(remove.waitForNonExistence(timeout: 10), "the game still has an image")
        app.swipeDown()
        XCTAssertTrue(waitForValue(art, "none", timeout: timeout), "an image is still shown")
        attachScreenshot(of: app, named: "art-removed")
    }

    @MainActor
    private func waitForValue(_ element: XCUIElement, _ value: String, timeout: TimeInterval) -> Bool {
        let predicate = NSPredicate(format: "value == %@", value)
        return XCTWaiter().wait(for: [expectation(for: predicate, evaluatedWith: element)], timeout: timeout) == .completed
    }
}
