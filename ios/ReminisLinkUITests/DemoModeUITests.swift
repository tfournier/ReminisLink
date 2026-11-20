import XCTest

/// The demo mode of the published app (milestone M16): "Try Without a
/// Console" from the first screen and from the list of consoles, a feature
/// used on the demo console, then Quit back to the list, leaving nothing.
final class DemoModeUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor
    func testTryWithoutAConsoleThenQuit() throws {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        // A fresh install: no console paired (a store in memory: the
        // Keychain of the simulator is never touched).
        app.launchEnvironment["RL_UITEST_MEMORY_STORE"] = "1"
        app.launch()

        let tryDemo = app.buttons["welcome.demo"]
        XCTAssertTrue(tryDemo.waitForExistence(timeout: 10), "no Try Without a Console on the first screen")
        XCTAssertEqual(tryDemo.label, "Try Without a Console")
        XCTAssertTrue(app.buttons["welcome.add"].exists)
        tryDemo.tap()

        // The demo console opens on its tabs, under the demo banner.
        let banner = app.otherElements["demo.banner"].firstMatch
        XCTAssertTrue(app.buttons["demo.quit"].waitForExistence(timeout: 10), "no Quit")
        XCTAssertTrue(banner.exists || app.staticTexts["Demo Mode"].exists, "no demo banner")
        XCTAssertTrue(app.tabBars.buttons["Library"].waitForExistence(timeout: 10))

        // A feature: the states of a demo game (made-up name).
        GameDataUITest.openLibraryGame(app, system: "HA", game: GameDataUITest.demoGame, timeout: 15)
        XCTAssertTrue(app.staticTexts["game.title"].waitForExistence(timeout: 15))
        app.buttons["game.states"].tapAfterWaiting(timeout: 15)
        XCTAssertTrue(app.buttons["state.0"].waitForExistence(timeout: 15), "no state slot")
        XCTAssertTrue(app.buttons["demo.quit"].exists, "the banner is gone on a pushed screen")
        attachScreenshot(of: app, named: "demo-states")

        // A download never reaches the share sheet in the demo.
        app.navigationBars.buttons.element(boundBy: 0).tap()
        app.buttons["game.save"].tapAfterWaiting(timeout: 15)
        app.buttons["save.download"].tapAfterWaiting(timeout: 15)
        let downloaded = app.alerts.firstMatch
        XCTAssertTrue(downloaded.waitForExistence(timeout: 15), "no confirmation of the demo download")
        XCTAssertFalse(app.otherElements["ActivityListView"].exists, "the share sheet opened in the demo")
        downloaded.dialogButton("demo.export.ok").tap()

        // The console list of the demo: the demo console, no Add a Console.
        app.tabBars.buttons["Console"].tap()
        app.buttons["tab.consoles"].tapAfterWaiting()
        XCTAssertTrue(app.staticTexts["Demo Brick"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["consoles.add"].exists, "Add a Console in the demo")

        // Quit: back to the empty list of real consoles.
        app.buttons["demo.quit"].tap()
        XCTAssertTrue(app.buttons["welcome.demo"].waitForExistence(timeout: 10), "not back on the first screen")
        XCTAssertFalse(app.buttons["demo.quit"].exists)
        XCTAssertFalse(app.staticTexts["Demo Brick"].exists)

        // A new launch does not resume the demo.
        app.terminate()
        app.launch()
        XCTAssertTrue(app.buttons["welcome.demo"].waitForExistence(timeout: 10), "the demo left a console behind")
        XCTAssertFalse(app.buttons["demo.quit"].exists)
    }

    /// From the list of consoles (a console is paired): the demo is a row
    /// of the list; the real console is still there after Quit.
    @MainActor
    func testTryFromTheListOfConsoles() throws {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        // The Debug demo switch with a console in the list (paired before
        // M13): stands for a real console here.
        app.launchEnvironment["RL_DEMO"] = "1"
        app.launchEnvironment["RL_UITEST_RL1_CONSOLE"] = "1"
        app.launch()

        let row = app.buttons["console.\(PairingUITests.demoDeviceId)"]
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        let tryDemo = app.buttons["consoles.demo"]
        XCTAssertTrue(GameDataUITest.scrollTo(tryDemo, in: app), "no Try Without a Console in the list")
        tryDemo.tap()
        XCTAssertTrue(app.buttons["demo.quit"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.tabBars.buttons["Library"].waitForExistence(timeout: 10))
        app.buttons["demo.quit"].tap()
        XCTAssertTrue(row.waitForExistence(timeout: 10), "the real console is gone")
        XCTAssertFalse(app.buttons["demo.quit"].exists)
    }
}
