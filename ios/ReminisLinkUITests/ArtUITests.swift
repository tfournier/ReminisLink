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

    /// M11: the image of the game sheet's header is the button: tap it to
    /// change or remove the image.
    @MainActor
    private func changeThenRemove(_ app: XCUIApplication, timeout: TimeInterval) {
        let art = app.buttons["game.changeArt"]
        XCTAssertTrue(art.waitForExistence(timeout: timeout), "no game image button")
        art.tapWhenEnabled(timeout: timeout)
        app.dialogButton("art.test").tapAfterWaiting(timeout: 10)
        GameDataUITest.waitForNotice(app, timeout: timeout)
        XCTAssertTrue(app.descendants(matching: .any)["notice"].firstMatch.label.contains("Image changed"))
        XCTAssertTrue(waitForValue(art, "image", timeout: timeout), "the new image is not shown")
        attachScreenshot(of: app, named: "art-changed")

        art.tapWhenEnabled(timeout: timeout)
        app.dialogButton("game.removeArt").tapAfterWaiting(timeout: 10)
        app.dialogButton("art.removeConfirm").tapAfterWaiting(timeout: 10)
        let removed = app.descendants(matching: .any).matching(identifier: "notice")
            .matching(NSPredicate(format: "label CONTAINS 'Image removed'")).firstMatch
        XCTAssertTrue(removed.waitForExistence(timeout: timeout), "not removed")
        XCTAssertTrue(waitForValue(art, "none", timeout: timeout), "an image is still shown")
        // No image: the menu no longer offers to remove one.
        art.tapWhenEnabled(timeout: timeout)
        XCTAssertTrue(app.dialogButton("art.photos").waitForExistence(timeout: 10))
        XCTAssertFalse(app.dialogButton("game.removeArt").exists, "the game still has an image")
        attachScreenshot(of: app, named: "art-removed")
    }

    @MainActor
    private func waitForValue(_ element: XCUIElement, _ value: String, timeout: TimeInterval) -> Bool {
        let predicate = NSPredicate(format: "value == %@", value)
        return XCTWaiter().wait(for: [expectation(for: predicate, evaluatedWith: element)], timeout: timeout) == .completed
    }
}
