import XCTest

/// Milestone M14 against a real pak (`build/reminislink-host --headless
/// --pairing-open` on a copy of testdata/sdcard): what the event stream
/// `GET /events` changes on screen, with no action of the user. Skipped
/// unless their link is given; driven by a runner that stops the pak or
/// writes from a second client (ios/README.md).
///
/// Optional, for the runner (paths on the Mac, shared with the simulator):
/// `RL_UITEST_EVENTS_READY_FILE` is created by the test once the screen
/// waits for the event; `RL_UITEST_EVENTS_TRIGGER_FILE` holds the time
/// (epoch seconds) the runner sent SIGTERM or made the change. With both,
/// the test asserts the delay from the trigger to the screen.
final class EventsUITests: XCTestCase {
    /// Longest accepted delay from the trigger to the screen: M14 asks for
    /// less than 2 s; the margin covers the polling of the UI test.
    private let maxDelay: TimeInterval = 3

    override func setUp() {
        continueAfterFailure = false
    }

    /// Stays idle on the Console tab; the runner sends SIGTERM to the pak.
    /// The list of consoles with the "closed" notice must show at once.
    ///
    ///     TEST_RUNNER_RL_UITEST_PAIR_URL_EVENTS_QUIT='reminislink://pair?...' xcodebuild test ...
    @MainActor
    func testPakQuitReturnsToListAgainstHeadlessPak() throws {
        let link = try GameDataUITest.headlessLink("EVENTS_QUIT")
        let app = launchAndPair(link)
        XCTAssertTrue(app.tabBars.firstMatch.waitForExistence(timeout: 30))
        XCTAssertTrue(app.tabBars.buttons["Console"].isSelected)
        attachScreenshot(of: app, named: "events-quit-dashboard")
        signalReady()

        let notice = app.descendants(matching: .any)["consoles.closedNotice"].firstMatch
        let seenAt = poll(timeout: 120) { notice.exists }
        XCTAssertNotNil(seenAt, "not back to the list")
        // The screenshot once the navigation animation is over.
        XCTAssertTrue(app.navigationBars["Consoles"].waitForExistence(timeout: 5))
        _ = XCTWaiter.wait(for: [XCTestExpectation(description: "animation")], timeout: 1)
        attachScreenshot(of: app, named: "events-quit-list")
        XCTAssertTrue(notice.label.contains("The console closed ReminisLink"), notice.label)
        XCTAssertFalse(app.tabBars.firstMatch.exists)
        XCTAssertFalse(app.descendants(matching: .any)["error"].exists, "an error is shown")
        if let seenAt { checkDelay(until: seenAt, what: "SIGTERM to the list") }
    }

    /// Shows the Library tab (10 games); the runner deletes "Lantern Keeper
    /// [b1]" (GBA) with a second client. The list must show 9 games without
    /// pull-to-refresh.
    ///
    ///     TEST_RUNNER_RL_UITEST_PAIR_URL_EVENTS_LIBRARY='reminislink://pair?...' xcodebuild test ...
    @MainActor
    func testLibraryRefreshFromAnotherClientAgainstHeadlessPak() throws {
        let link = try GameDataUITest.headlessLink("EVENTS_LIBRARY")
        let app = launchAndPair(link)
        GameDataUITest.openLibraryTab(app, timeout: 30)
        let ten = app.staticTexts.matching(NSPredicate(format: "label ==[c] '10 games'")).firstMatch
        XCTAssertTrue(ten.waitForExistence(timeout: 30), "not the 10 games of testdata/sdcard")
        attachScreenshot(of: app, named: "events-library-before")
        signalReady()

        let nine = app.staticTexts.matching(NSPredicate(format: "label ==[c] '9 games'")).firstMatch
        let seenAt = poll(timeout: 120) { nine.exists }
        XCTAssertNotNil(seenAt, "the library was not refreshed")
        attachScreenshot(of: app, named: "events-library-after")
        XCTAssertFalse(app.buttons["game.Lantern Keeper [b1]"].exists, "the deleted game is still listed")
        XCTAssertFalse(app.descendants(matching: .any)["error"].exists, "an error is shown")
        if let seenAt { checkDelay(until: seenAt, what: "deletion by another client to the refreshed library") }
    }

    // MARK: - Helpers

    @MainActor
    private func launchAndPair(_ link: URL) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_RESET_STORE"] = "1"
        app.launch()
        app.open(link)
        app.buttons["pairing.done"].tapAfterWaiting(timeout: 30)
        return app
    }

    private var environment: [String: String] { ProcessInfo.processInfo.environment }

    private func signalReady() {
        guard let path = environment["RL_UITEST_EVENTS_READY_FILE"] else { return }
        if !FileManager.default.createFile(atPath: path, contents: Data("ready\n".utf8)) {
            print("RL_EVENTS: cannot create the ready file (the runner falls back to a delay)")
        }
    }

    /// Checks `condition` until it holds; returns when it was first seen.
    @MainActor
    private func poll(timeout: TimeInterval, _ condition: () -> Bool) -> Date? {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if condition() { return Date() }
            RunLoop.current.run(until: Date().addingTimeInterval(0.05))
        }
        return nil
    }

    /// The delay from the runner's trigger to `seenAt`, when the runner
    /// wrote it.
    @MainActor
    private func checkDelay(until seenAt: Date, what: String) {
        guard let path = environment["RL_UITEST_EVENTS_TRIGGER_FILE"] else {
            print("RL_EVENTS: no trigger file, delay not measured")
            return
        }
        var text: String?
        for _ in 0..<40 {
            text = try? String(contentsOfFile: path, encoding: .utf8)
            if text != nil { break }
            Thread.sleep(forTimeInterval: 0.05)
        }
        guard let text, let trigger = TimeInterval(text.trimmingCharacters(in: .whitespacesAndNewlines)) else {
            print("RL_EVENTS: trigger file unreadable, delay not measured")
            return
        }
        let delay = seenAt.timeIntervalSince1970 - trigger
        let line = String(format: "RL_EVENTS_DELAY %@: %.2f s", what, delay)
        print(line)
        let attachment = XCTAttachment(string: line)
        attachment.name = "delay"
        attachment.lifetime = .keepAlways
        add(attachment)
        XCTAssertGreaterThanOrEqual(delay, 0, "seen before the trigger: \(line)")
        XCTAssertLessThan(delay, maxDelay, line)
    }
}
