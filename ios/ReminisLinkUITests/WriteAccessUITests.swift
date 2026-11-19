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
        // Reads still work (M11: the Library tab, where adding is).
        GameDataUITest.openLibraryTab(app, timeout: 15)
        XCTAssertTrue(app.buttons["game.Sky Garden Quest"].waitForExistence(timeout: 15))
        XCTAssertFalse(app.buttons["library.add"].isEnabled, "adding is a write")
        XCTAssertTrue(banner.exists, "the banner stays on the screens of the console")
        attachScreenshot(of: app, named: "read-only")

        // Take over (the banner is on every tab), after the confirmation.
        app.buttons["readOnly.takeOver"].tapAfterWaiting()
        let confirm = app.buttons["readOnly.confirm"].firstMatch
        if confirm.waitForExistence(timeout: 5) {
            confirm.tap()
        } else {
            app.sheets.buttons["Take Over"].firstMatch.tap()
        }
        XCTAssertTrue(banner.waitForNonExistence(timeout: 15), "still read-only after taking over")
        app.buttons["library.add"].tapWhenEnabled(timeout: 10)
        attachScreenshot(of: app, named: "took-over")
    }

    /// The same against the real pak: `scripts/smoke-writer.sh` in hold mode
    /// (`HOLD=1`) holds the lease as "smoke-writer B" (or
    /// `TEST_RUNNER_RL_UITEST_WRITER_NAME`); after the take-over it checks
    /// that its writes get `423` naming this iPhone.
    @MainActor
    func testReadOnlyThenTakeOverAgainstHeadlessPak() throws {
        let link = try GameDataUITest.headlessLink("WRITER")
        let holder = ProcessInfo.processInfo.environment["RL_UITEST_WRITER_NAME"] ?? "smoke-writer B"
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_RESET_STORE"] = "1"
        app.launch()
        app.open(link)
        app.buttons["pairing.done"].tapAfterWaiting(timeout: 30)

        // Read-only: the banner names the client of the script.
        let banner = app.descendants(matching: .any)["readOnly"].firstMatch
        XCTAssertTrue(banner.waitForExistence(timeout: 30), "no read-only banner")
        let title = app.staticTexts["readOnly.title"]
        XCTAssertTrue(title.waitForExistence(timeout: 5))
        XCTAssertTrue(title.label.contains("\(holder) has write access"), title.label)
        GameDataUITest.openLibraryTab(app, timeout: 30)
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'game.' OR identifier BEGINSWITH 'system.'"))
            .firstMatch.waitForExistence(timeout: 30), "reads still work")
        XCTAssertFalse(app.buttons["library.add"].isEnabled, "adding is a write")
        attachScreenshot(of: app, named: "read-only-headless")

        // Take over (the banner is on every tab), after the confirmation.
        app.buttons["readOnly.takeOver"].tapAfterWaiting()
        let confirm = app.buttons["readOnly.confirm"].firstMatch
        if confirm.waitForExistence(timeout: 5) {
            confirm.tap()
        } else {
            app.sheets.buttons["Take Over"].firstMatch.tap()
        }
        XCTAssertTrue(banner.waitForNonExistence(timeout: 15), "still read-only after taking over")
        let enabled = XCTNSPredicateExpectation(predicate: NSPredicate(format: "isEnabled == true"), object: app.buttons["library.add"])
        XCTAssertEqual(XCTWaiter.wait(for: [enabled], timeout: 10), .completed, "adding stays disabled")
        attachScreenshot(of: app, named: "took-over-headless")
        // Stay in the foreground while the script sees the take-over (it
        // renews every 5 s).
        _ = XCTWaiter.wait(for: [XCTestExpectation(description: "script check")], timeout: 12)
    }
}
