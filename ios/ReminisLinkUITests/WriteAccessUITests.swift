import XCTest

/// Single writer (milestone M10): another paired iPhone holds the write
/// lease of the demo console. The app opens read-only (banner, write
/// actions disabled, reads still work), then takes over after confirming.
final class WriteAccessUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor
    func testReadOnlyBannerThenTakeOverInDemoMode() throws {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        app.launchEnvironment["RL_UITEST_OTHER_WRITER"] = "Other iPhone"
        app.launch()
        app.open(URL(string: PairingUITests.demoLink)!)
        app.buttons["pairing.done"].tapAfterWaiting(timeout: 15)

        // Read-only: the banner names the device that has write access.
        let banner = app.descendants(matching: .any)["readOnly"].firstMatch
        XCTAssertTrue(banner.waitForExistence(timeout: 15), "no read-only banner")
        let title = app.staticTexts["readOnly.title"]
        XCTAssertTrue(title.waitForExistence(timeout: 5))
        XCTAssertTrue(title.label.contains("Other iPhone has write access"), title.label)
        XCTAssertFalse(app.buttons["console.add"].isEnabled, "adding is a write")
        // Reads still work.
        app.buttons["console.library"].tapWhenEnabled(timeout: 15)
        XCTAssertTrue(app.buttons["system.GBA"].waitForExistence(timeout: 15))
        XCTAssertTrue(banner.exists, "the banner stays on the screens of the console")
        attachScreenshot(of: app, named: "read-only")
        app.navigationBars.buttons.element(boundBy: 0).tap()

        // Take over, after the confirmation.
        app.buttons["readOnly.takeOver"].tapAfterWaiting()
        let confirm = app.buttons["readOnly.confirm"].firstMatch
        if confirm.waitForExistence(timeout: 5) {
            confirm.tap()
        } else {
            app.sheets.buttons["Take Over"].firstMatch.tap()
        }
        XCTAssertTrue(banner.waitForNonExistence(timeout: 15), "still read-only after taking over")
        app.buttons["console.add"].tapWhenEnabled(timeout: 10)
        attachScreenshot(of: app, named: "took-over")
    }
}
