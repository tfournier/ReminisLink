import XCTest

/// Main path of milestone M3: a `reminislink://pair` link → the console is
/// added → navigation down to a game sheet.
final class PairingUITests: XCTestCase {
    /// Pairing link of the fake pak of the demo mode (ReminisLinkDemo's
    /// `FakePak`: device id `demoDeviceId`, secret bytes 0x00...0x1f).
    static let demoLink = "reminislink://pair?id=5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0&n=Demo%20Brick&s=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8&v=1"
    static let demoDeviceId = "5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0"

    override func setUp() {
        continueAfterFailure = false
    }

    /// Against the fake pak (`RL_DEMO=1`): no network, runs everywhere.
    @MainActor
    func testPairingLinkThenGameSheetInDemoMode() throws {
        let app = XCUIApplication()
        app.launchEnvironment["RL_DEMO"] = "1"
        app.launch()
        XCTAssertTrue(app.descendants(matching: .any)["welcome"].waitForExistence(timeout: 10))

        app.open(URL(string: Self.demoLink)!)
        try pairAndOpenConsole(app, deviceId: Self.demoDeviceId)

        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS '192.168.1.42'")).firstMatch
            .waitForExistence(timeout: 10))
        attachScreenshot(app, "dashboard")
        app.buttons["console.library"].tapWhenEnabled()
        app.buttons["system.GBA"].tapAfterWaiting()
        XCTAssertTrue(app.buttons["game.Sky Garden Quest"].waitForExistence(timeout: 10))
        attachScreenshot(app, "folder")
        app.buttons["game.Sky Garden Quest"].tapAfterWaiting(timeout: 10)
        XCTAssertTrue(app.staticTexts["game.title"].waitForExistence(timeout: 10))
        attachScreenshot(app, "game")
        XCTAssertEqual(app.staticTexts["game.title"].label, "Sky Garden Quest")
        XCTAssertTrue(app.staticTexts["Roms/Game Boy Advance (GBA)/Sky Garden Quest.gba"].waitForExistence(timeout: 10))
    }

    /// Pairing again a console that is already paired asks first, once the
    /// add sheet is closed.
    @MainActor
    func testRepairAsksForConfirmationInDemoMode() throws {
        let app = XCUIApplication()
        app.launchEnvironment["RL_DEMO"] = "1"
        app.launch()
        app.open(URL(string: Self.demoLink)!)
        try pairAndOpenConsole(app, deviceId: Self.demoDeviceId)
        app.navigationBars.buttons.element(boundBy: 0).tap()

        app.buttons["consoles.add"].tapAfterWaiting()
        app.buttons["demo.pair"].tapAfterWaiting()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 10), "no confirmation")
        alert.buttons["repair.confirm"].tap()
        XCTAssertTrue(app.buttons["pairing.done"].waitForExistence(timeout: 15))
    }

    /// Against a real pak (`make headless` on the Mac). Disabled unless the
    /// pairing link printed by the pak is given:
    ///
    ///     TEST_RUNNER_RL_UITEST_PAIR_URL='reminislink://pair?...' xcodebuild test ...
    @MainActor
    func testPairingLinkThenGameSheetAgainstHeadlessPak() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let link = environment["RL_UITEST_PAIR_URL"], let url = URL(string: link),
              let deviceId = URLComponents(url: url, resolvingAgainstBaseURL: false)?
                .queryItems?.first(where: { $0.name == "id" })?.value
        else {
            throw XCTSkip("Set TEST_RUNNER_RL_UITEST_PAIR_URL to the pairing link of a running pak (make headless).")
        }
        let app = XCUIApplication()
        app.launchEnvironment["RL_RESET_STORE"] = "1"
        app.launch()

        app.open(url)
        try pairAndOpenConsole(app, deviceId: deviceId, timeout: 30)

        app.buttons["console.library"].tapWhenEnabled(timeout: 30)
        let firstSystem = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'system.'")).firstMatch
        firstSystem.tapAfterWaiting(timeout: 30)
        let firstGame = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'game.'")).firstMatch
        firstGame.tapAfterWaiting(timeout: 30)
        XCTAssertTrue(app.staticTexts["game.title"].waitForExistence(timeout: 30))
    }

    /// Kept in the result bundle, for review.
    @MainActor
    private func attachScreenshot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    @MainActor
    private func pairAndOpenConsole(_ app: XCUIApplication, deviceId: String, timeout: TimeInterval = 15) throws {
        let done = app.buttons["pairing.done"]
        XCTAssertTrue(done.waitForExistence(timeout: timeout), "pairing did not finish")
        done.tap()
        // Pairing opens the console; going back shows it in the list.
        XCTAssertTrue(app.buttons["console.library"].waitForExistence(timeout: timeout))
        app.navigationBars.buttons.element(boundBy: 0).tap()
        app.buttons["console.\(deviceId)"].tapAfterWaiting(timeout: timeout)
    }
}

extension XCUIElement {
    @MainActor
    func tapAfterWaiting(timeout: TimeInterval = 10, file: StaticString = #filePath, line: UInt = #line) {
        XCTAssertTrue(waitForExistence(timeout: timeout), "\(self) not found", file: file, line: line)
        tap()
    }

    @MainActor
    func tapWhenEnabled(timeout: TimeInterval = 10, file: StaticString = #filePath, line: UInt = #line) {
        let enabled = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == true AND enabled == true"), object: self)
        XCTAssertEqual(XCTWaiter().wait(for: [enabled], timeout: timeout), .completed, "\(self) not enabled", file: file, line: line)
        tap()
    }
}
