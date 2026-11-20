import XCTest

/// Main path of milestone M3: a `reminislink://pair` link → the console is
/// added → navigation down to a game sheet.
final class PairingUITests: XCTestCase {
    /// Pairing link v2 of the fake pak of the demo mode (ReminisLinkDemo's
    /// `FakePak`: device id `demoDeviceId`, secret bytes 0x00...0x1f, pin
    /// bytes 0xff...0xe0; no address: the demo discovery finds it).
    static let demoLink = "reminislink://pair?v=2&id=5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0&n=Demo%20Brick&s=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8&k=__79_Pv6-fj39vX08_Lx8O_u7ezr6uno5-bl5OPi4eA"
    /// The same console's link before M13 (`v=1`, no pin).
    static let rl1DemoLink = "reminislink://pair?id=5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0&n=Demo%20Brick&s=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8&v=1"
    static let demoDeviceId = "5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0"

    override func setUp() {
        continueAfterFailure = false
    }

    /// Against the fake pak (`RL_DEMO=1`): no network, runs everywhere.
    @MainActor
    func testPairingLinkThenGameSheetInDemoMode() throws {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        app.launch()
        XCTAssertTrue(app.descendants(matching: .any)["welcome"].waitForExistence(timeout: 10))

        app.open(URL(string: Self.demoLink)!)
        try pairAndOpenConsole(app, deviceId: Self.demoDeviceId)

        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS '192.168.1.42'")).firstMatch
            .waitForExistence(timeout: 10))
        attachScreenshot(app, "dashboard")
        GameDataUITest.openLibraryTab(app, timeout: 10)
        XCTAssertTrue(app.buttons["game.Thistledown Isles"].waitForExistence(timeout: 10))
        attachScreenshot(app, "library")
        app.buttons["game.Thistledown Isles"].tapAfterWaiting(timeout: 10)
        XCTAssertTrue(app.staticTexts["game.title"].waitForExistence(timeout: 10))
        attachScreenshot(app, "game")
        XCTAssertEqual(app.staticTexts["game.title"].label, "Thistledown Isles")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'Thistledown Isles.gba'")).firstMatch
            .waitForExistence(timeout: 10))
    }

    /// The dashboard shows the NextUI release string as the pak sends it
    /// (decision D4), and the library marks a system without an emulator
    /// but keeps it open (decision D6).
    @MainActor
    func testReleaseStringAndSystemWithoutEmulatorInDemoMode() throws {
        let app = XCUIApplication()
        app.launchEnvironment["RL_DEMO"] = "1"
        app.launchArguments += GameDataUITest.englishArguments
        app.launch()
        XCTAssertTrue(app.descendants(matching: .any)["welcome"].waitForExistence(timeout: 10))
        app.open(URL(string: Self.demoLink)!)
        try pairAndOpenConsole(app, deviceId: Self.demoDeviceId)

        XCTAssertTrue(app.descendants(matching: .any)
            .matching(NSPredicate(format: "label CONTAINS 'DemoOS-20261013'")).firstMatch
            .waitForExistence(timeout: 10))
        // The flat library's menu opens the systems (M11).
        GameDataUITest.openLibraryTab(app, timeout: 10)
        app.buttons["library.filter"].tapAfterWaiting()
        app.buttons["library.bySystem"].tapAfterWaiting()
        let playStation = app.buttons["system.PS"]
        XCTAssertTrue(playStation.waitForExistence(timeout: 10))
        XCTAssertTrue(playStation.label.contains("Emulator not installed on the console"), playStation.label)
        XCTAssertFalse(app.buttons["system.GBA"].label.contains("Emulator"), app.buttons["system.GBA"].label)
        attachScreenshot(app, "systems")
        playStation.tapAfterWaiting()
        XCTAssertTrue(app.buttons["game.Orrery of Nine Tides"].waitForExistence(timeout: 10))
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
        app.buttons["tab.consoles"].tapAfterWaiting()

        app.buttons["consoles.add"].tapAfterWaiting()
        app.buttons["demo.pair"].tapAfterWaiting()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 10), "no confirmation")
        alert.buttons["repair.confirm"].tap()
        XCTAssertTrue(app.buttons["pairing.done"].waitForExistence(timeout: 15))
    }

    /// M13: the QR code of a pak older than M13 asks to update the pak.
    @MainActor
    func testAnRL1LinkAsksToUpdateThePakInDemoMode() throws {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        app.launch()
        XCTAssertTrue(app.descendants(matching: .any)["welcome"].waitForExistence(timeout: 10))
        app.open(URL(string: Self.rl1DemoLink)!)
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 10))
        XCTAssertTrue(alert.staticTexts.matching(NSPredicate(format: "label CONTAINS 'Update ReminisLink on the console'")).firstMatch.exists)
        alert.buttons.firstMatch.tap()
        XCTAssertTrue(app.descendants(matching: .any)["welcome"].exists)
    }

    /// M13: a console paired before M13 is listed with "Pair Again"; its
    /// QR code pairs it without asking (nothing to replace), and the
    /// console opens.
    @MainActor
    func testAnRL1ConsoleIsPairedAgainInDemoMode() throws {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        app.launchEnvironment["RL_UITEST_RL1_CONSOLE"] = "1"
        app.launch()
        let row = app.buttons["console.\(Self.demoDeviceId)"]
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        XCTAssertTrue(app.descendants(matching: .any)["console.needsRepair"].firstMatch.exists)
        attachScreenshot(app, "needs-repair")
        // Tapping it opens the scanner ("Pair Again").
        row.tap()
        XCTAssertTrue(app.navigationBars["Pair Again"].waitForExistence(timeout: 10))
        // The demo console's QR code, as if scanned.
        app.buttons["demo.pair"].tapAfterWaiting()
        XCTAssertFalse(app.alerts.firstMatch.waitForExistence(timeout: 2), "no confirmation for a console to pair again")
        try pairAndOpenConsole(app, deviceId: Self.demoDeviceId)
        XCTAssertFalse(app.descendants(matching: .any)["console.needsRepair"].firstMatch.exists)
    }

    /// A console to pair again is forgotten only after a confirmation
    /// (no full swipe).
    @MainActor
    func testForgettingAnRL1ConsoleAsksFirstInDemoMode() throws {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        app.launchEnvironment["RL_UITEST_RL1_CONSOLE"] = "1"
        app.launch()
        let row = app.buttons["console.\(Self.demoDeviceId)"]
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        row.swipeLeft(velocity: .fast)
        // A full swipe deletes nothing: the row stays.
        XCTAssertTrue(row.exists)
        app.buttons["console.forget"].tapAfterWaiting()
        let confirm = app.buttons["console.forget.confirm"].firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), "no confirmation")
        XCTAssertTrue(row.exists, "forgotten before the confirmation")
        confirm.tap()
        XCTAssertTrue(app.descendants(matching: .any)["welcome"].waitForExistence(timeout: 10))
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
        // The Library tab is found by its English name.
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_RESET_STORE"] = "1"
        app.launch()

        app.open(url)
        try pairAndOpenConsole(app, deviceId: deviceId, timeout: 30)

        GameDataUITest.openLibraryTab(app, timeout: 30)
        // The flat library (M11), or the systems of an older pak.
        let firstSystem = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'system.'")).firstMatch
        if firstSystem.waitForExistence(timeout: 5) { firstSystem.tap() }
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
        // Pairing opens the console (M11: its tabs); going back shows it
        // in the list.
        XCTAssertTrue(app.tabBars.buttons.element(boundBy: 1).waitForExistence(timeout: timeout))
        app.buttons["tab.consoles"].tapAfterWaiting(timeout: timeout)
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
