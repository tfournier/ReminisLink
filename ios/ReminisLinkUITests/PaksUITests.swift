import XCTest

/// Milestone M12: the apps (paks) of a console and the read-only SD
/// browser. In the demo mode: ReminisLink is marked and cannot be deleted,
/// a pak installed from the SD card (the browser as a picker) meets the
/// installed one and replaces it after confirmation, another pak goes to
/// the trash, and a file of the card is downloaded to the share sheet.
/// Against the pak (`make headless`): a tiny pak zip is installed, listed,
/// deleted, and the root of the card is browsed.
final class PaksUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor
    func testPaksAndSdBrowserInDemoMode() throws {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        app.launch()
        app.open(URL(string: PairingUITests.demoLink)!)
        app.buttons["pairing.done"].tapAfterWaiting(timeout: 15)

        // Console tab > Apps: tools, then emulators; ReminisLink marked.
        // Below the dashboard's first screen on a tall phone (rows are
        // created lazily): scrolled to once the console answered.
        let paks = app.buttons["console.paks"]
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS 'DemoOS-20261013'")).firstMatch
            .waitForExistence(timeout: 15), "not connected")
        XCTAssertTrue(GameDataUITest.scrollTo(paks, in: app), "no Apps row")
        paks.tap()
        XCTAssertTrue(app.navigationBars["Apps"].waitForExistence(timeout: 10))
        let me = app.descendants(matching: .any)["pak.ReminisLink"].firstMatch
        XCTAssertTrue(me.waitForExistence(timeout: 10), "ReminisLink not listed")
        XCTAssertTrue(me.label.contains("This app"), me.label)
        XCTAssertTrue(app.descendants(matching: .any)["pak.Advance"].exists)
        XCTAssertTrue(app.staticTexts["Emulators"].exists || app.staticTexts["EMULATORS"].exists)
        attachScreenshot(of: app, named: "paks")
        me.swipeLeft()
        XCTAssertFalse(app.buttons["pak.delete"].waitForExistence(timeout: 2), "ReminisLink can be deleted")
        // Nothing was revealed: no swipe back over the row (on iOS 26, a
        // swipe right anywhere on a pushed screen goes back).
        XCTAssertTrue(app.navigationBars["Apps"].exists, "left the Apps screen")

        // Install from the SD card: Downloads/Clock.pak.zip meets Clock.
        app.buttons["paks.install"].tapAfterWaiting()
        app.buttons["From the SD Card…"].tapAfterWaiting()
        app.buttons["sd.Downloads"].tapAfterWaiting(timeout: 10)
        XCTAssertTrue(app.buttons["sd.Timer.pak"].waitForExistence(timeout: 10), "the .pak folder is not offered")
        attachScreenshot(of: app, named: "sd-picker")
        app.buttons["sd.Clock.pak.zip"].tapAfterWaiting()
        app.buttons["pakInstall.confirm"].tapWhenEnabled(timeout: 10)
        // The action of the dialog (iOS 26 nests it in a second button
        // with the same identifier: the first match).
        let replace = app.buttons.matching(NSPredicate(format: "identifier == 'pakReplace.confirm' OR label == 'Replace'")).firstMatch
        XCTAssertTrue(replace.waitForExistence(timeout: 15), "the replacement is not asked")
        XCTAssertTrue(app.staticTexts["The current version goes to the trash of the console."].exists)
        attachScreenshot(of: app, named: "replace")
        replace.tap()
        let installed = app.staticTexts.matching(identifier: "pakInstall.status").matching(NSPredicate(format: "label == 'Installed'")).firstMatch
        XCTAssertTrue(installed.waitForExistence(timeout: 20), "not installed")

        // Delete another pak: to the trash, after a confirmation.
        let files = app.descendants(matching: .any)["pak.Files"].firstMatch
        XCTAssertTrue(GameDataUITest.scrollTo(files, in: app))
        files.swipeLeft()
        app.buttons["pak.delete"].tapAfterWaiting()
        app.dialogButton("pak.delete.confirm").tapAfterWaiting()
        GameDataUITest.waitForNotice(app)
        XCTAssertTrue(files.waitForNonExistence(timeout: 10), "still listed")
        attachScreenshot(of: app, named: "deleted")

        // The SD browser, from the Storage section of the Console tab.
        app.navigationBars["Apps"].buttons.firstMatch.tap()
        let storage = app.buttons["console.storage"].firstMatch
        XCTAssertTrue(GameDataUITest.scrollTo(storage, in: app))
        storage.tap()
        XCTAssertTrue(app.buttons["sd.Roms"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["sd..userdata"].exists)
        attachScreenshot(of: app, named: "sd-root")
        app.buttons["sd.Downloads"].tap()
        let download = app.buttons.matching(NSPredicate(format: "label == 'Download README.txt'")).firstMatch
        download.tapAfterWaiting(timeout: 10)
        GameDataUITest.expectShareSheetThenClose(app)
        XCTAssertFalse(app.descendants(matching: .any)["error"].exists, "an error is shown")
    }

    /// Against a real pak (a fresh `make headless HEADLESS_ARGS=--pairing-open`),
    /// with `TEST_RUNNER_RL_UITEST_PAIR_URL_PAKS=<its link>`. The app
    /// generates `<name>.pak.zip` (`RL_UITEST_SHARE_PAK`, Debug only: a
    /// `.pak` folder zipped as one chosen in Files is) and receives it as
    /// the share extension would; it is sent as an app (pak).
    @MainActor
    func testInstallAndDeleteAgainstHeadlessPak() throws {
        let link = try GameDataUITest.headlessLink("PAKS")
        let name = "UITestPak\(Int(Date().timeIntervalSince1970) % 1_000_000)"
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_RESET_STORE"] = "1"
        app.launchEnvironment["RL_UITEST_SHARE_PAK"] = name
        app.launch()
        app.open(link)
        app.buttons["pairing.done"].tapAfterWaiting(timeout: 30)

        // The Add screen opens with the archive: sent as an app.
        let send = app.buttons["add.send"]
        XCTAssertTrue(send.waitForExistence(timeout: 30), "the Add screen did not open")
        XCTAssertTrue(app.staticTexts["\(name).pak.zip"].waitForExistence(timeout: 10))
        app.buttons["add.kind"].tapAfterWaiting()
        app.buttons["App (pak)"].tapAfterWaiting()
        attachScreenshot(of: app, named: "add-pak")
        send.tapWhenEnabled(timeout: 20)

        // Transfers: the upload, then its installation job.
        let installed = app.staticTexts.matching(identifier: "job.status").matching(NSPredicate(format: "label == 'Installed'")).firstMatch
        XCTAssertTrue(installed.waitForExistence(timeout: 60), "not installed")
        attachScreenshot(of: app, named: "installed")

        // Console tab > Apps: listed; deleted to the trash.
        app.tabBars.buttons["Console"].tap()
        let paks = app.buttons["console.paks"]
        XCTAssertTrue(GameDataUITest.scrollTo(paks, in: app, attempts: 8), "no Apps row")
        paks.tap()
        let row = app.descendants(matching: .any)["pak.\(name)"].firstMatch
        XCTAssertTrue(GameDataUITest.scrollTo(row, in: app), "the pak is not listed")
        XCTAssertTrue(row.label.contains("0.0.1"), row.label)
        attachScreenshot(of: app, named: "listed")
        row.swipeLeft()
        app.buttons["pak.delete"].tapAfterWaiting()
        app.dialogButton("pak.delete.confirm").tapAfterWaiting()
        GameDataUITest.waitForNotice(app, timeout: 20)
        XCTAssertTrue(row.waitForNonExistence(timeout: 10), "still listed")

        // The root of the SD card, without ReminisLink's data.
        app.navigationBars["Apps"].buttons.firstMatch.tap()
        let storage = app.buttons["console.storage"].firstMatch
        XCTAssertTrue(GameDataUITest.scrollTo(storage, in: app))
        storage.tap()
        XCTAssertTrue(app.buttons["sd.Roms"].waitForExistence(timeout: 20), "no Roms folder at the root")
        attachScreenshot(of: app, named: "sd-root")
        let userdata = app.buttons["sd..userdata"]
        if userdata.exists {
            userdata.tap()
            let shared = app.buttons["sd.shared"]
            if shared.waitForExistence(timeout: 10) {
                shared.tap()
                XCTAssertFalse(app.buttons["sd.ReminisLink"].waitForExistence(timeout: 3), "ReminisLink's data is listed")
            }
        }
        XCTAssertFalse(app.descendants(matching: .any)["error"].exists, "an error is shown")
    }
}
