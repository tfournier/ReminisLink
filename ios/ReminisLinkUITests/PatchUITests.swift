import XCTest

/// Patch screen (milestone M6): a patch of the SD card for another ROM is
/// refused with a clear message, a matching one replaces the game; a
/// shared patch opens the Patch screen, the game is chosen, a new file is
/// made and opened.
///
/// XCUITest cannot drive the document picker: a shared patch is made by
/// the app at launch (`RL_UITEST_SHARE_PATCH=<name>:<source size>:<source
/// CRC32>:<target text>`, Debug only): a BPS that writes the target text
/// and declares that source. It goes into the share inbox exactly as the
/// share extension leaves files.
final class PatchUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    /// Against the fake pak, from the game sheet: SD card patches.
    @MainActor
    func testPatchFromTheSDCardInDemoMode() throws {
        let app = GameDataUITest.openDemoGame()
        XCTAssertTrue(GameDataUITest.scrollTo(app.buttons["game.patch"], in: app), "no Patch button")
        app.buttons["game.patch"].tap()
        app.segmentedControls["patch.source"].buttons.element(boundBy: 2).tapAfterWaiting()

        // A patch for another ROM.
        GameDataUITest.button(app, "patch.sdFile", containing: "Pixel Runner (Hack).ups").tapAfterWaiting(timeout: 15)
        app.buttons["patch.apply"].tapWhenEnabled()
        let error = app.descendants(matching: .any)["patch.error"].firstMatch
        XCTAssertTrue(error.waitForExistence(timeout: 20), "no mismatch message")
        XCTAssertTrue(error.label.contains("does not match this ROM"), error.label)
        attachScreenshot(of: app, named: "patch-mismatch")

        // The patch of this game, replacing it.
        GameDataUITest.button(app, "patch.sdFile", containing: "Sky Garden Quest (Translation).bps").tap()
        XCTAssertFalse(error.exists, "error kept after a new choice")
        app.buttons["Replace the Game"].firstMatch.tapAfterWaiting()
        XCTAssertTrue(app.descendants(matching: .any)["patch.replaceWarning"].firstMatch.waitForExistence(timeout: 5))
        app.buttons["patch.apply"].tapWhenEnabled()
        app.buttons["patch.replaceConfirm"].tapAfterWaiting()
        let done = app.buttons["patch.done"]
        XCTAssertTrue(done.waitForExistence(timeout: 20), "patch not applied")
        XCTAssertTrue(app.descendants(matching: .any)["notice"].firstMatch.exists)
        attachScreenshot(of: app, named: "patch-replaced")
        done.tap()
        XCTAssertTrue(app.staticTexts["game.title"].waitForExistence(timeout: 10))
    }

    /// Against the fake pak: a shared patch opens the Patch screen.
    @MainActor
    func testSharedPatchInDemoMode() throws {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        // Sky Garden Quest is 16 MiB in the demo; the fake pak checks the size.
        app.launchEnvironment["RL_UITEST_SHARE_PATCH"] = "UITest Hack.bps:16777216:00000000:UITest output"
        app.launch()
        app.open(URL(string: PairingUITests.demoLink)!)
        let pairingDone = app.buttons["pairing.done"]
        XCTAssertTrue(pairingDone.waitForExistence(timeout: 15), "pairing did not finish")
        pairingDone.tap()

        chooseGame(app, game: "Sky Garden Quest", timeout: 15)
        XCTAssertTrue(app.descendants(matching: .any)["patch.file"].firstMatch.waitForExistence(timeout: 10), "shared file not shown")
        let name = app.textFields["patch.fileName"]
        XCTAssertEqual(name.value as? String, "Sky Garden Quest (patched).gba")
        attachScreenshot(of: app, named: "patch-shared")
        app.buttons["patch.apply"].tapWhenEnabled()
        app.buttons["patch.openGame"].tapAfterWaiting(timeout: 30)
        let title = app.staticTexts["game.title"]
        XCTAssertTrue(title.waitForExistence(timeout: 15))
        XCTAssertEqual(title.label, "Sky Garden Quest (patched)")
    }

    /// Against a real pak (`make headless`), on Starlight Courier
    /// (`fake-rom-1`: 10 bytes, CRC32 4afa16bd). Three launches, one
    /// shared patch each: a BPS for another source is refused, a matching
    /// BPS makes a new file (opened), and a BPS for that new file
    /// replaces it. The pairing link is used by the first launch only.
    ///
    ///     TEST_RUNNER_RL_UITEST_PAIR_URL_PATCH='reminislink://pair?...' xcodebuild test ...
    @MainActor
    func testPatchAgainstHeadlessPak() throws {
        let link = try GameDataUITest.headlessLink("PATCH")
        let stamp = Int(Date().timeIntervalSince1970) % 100_000
        let newName = "UITest Patched \(stamp)"
        let first = Data("ReminisLink UI test \(stamp)".utf8)
        let second = Data("ReminisLink UI test \(stamp), replaced".utf8)

        // 1. Another source: refused, nothing written.
        var app = launch(patch: "UITest Wrong.bps:10:00000000:x", reset: true)
        app.open(link)
        let pairingDone = app.buttons["pairing.done"]
        XCTAssertTrue(pairingDone.waitForExistence(timeout: 30), "pairing did not finish")
        pairingDone.tap()
        chooseGame(app, game: GameDataUITest.headlessGame, timeout: 30)
        app.buttons["patch.apply"].tapWhenEnabled(timeout: 20)
        let error = app.descendants(matching: .any)["patch.error"].firstMatch
        XCTAssertTrue(error.waitForExistence(timeout: 30), "no mismatch message")
        XCTAssertTrue(error.label.contains("does not match this ROM"), error.label)
        attachScreenshot(of: app, named: "headless-mismatch")
        app.terminate()

        // 2. The right source: a new file, opened.
        app = launch(patch: "UITest Hack.bps:10:4afa16bd:\(String(decoding: first, as: UTF8.self))", reset: false)
        chooseGame(app, game: GameDataUITest.headlessGame, timeout: 30)
        let name = app.textFields["patch.fileName"]
        XCTAssertTrue(name.waitForExistence(timeout: 10))
        replaceText(of: name, with: "\(newName).gba")
        app.buttons["patch.apply"].tapWhenEnabled(timeout: 20)
        app.buttons["patch.openGame"].tapAfterWaiting(timeout: 60)
        let title = app.staticTexts["game.title"]
        XCTAssertTrue(title.waitForExistence(timeout: 30))
        XCTAssertEqual(title.label, newName)
        attachScreenshot(of: app, named: "headless-new-file")
        app.terminate()

        // 3. A patch for the new file: it replaces it.
        let crc = String(format: "%08x", Self.crc32(first))
        app = launch(patch: "UITest Hack 2.bps:\(first.count):\(crc):\(String(decoding: second, as: UTF8.self))", reset: false)
        chooseGame(app, game: newName, timeout: 30)
        app.buttons["Replace the Game"].firstMatch.tapAfterWaiting()
        app.buttons["patch.apply"].tapWhenEnabled(timeout: 20)
        app.buttons["patch.replaceConfirm"].tapAfterWaiting()
        XCTAssertTrue(app.buttons["patch.done"].waitForExistence(timeout: 60), "replacement not applied")
        attachScreenshot(of: app, named: "headless-replaced")
    }

    // MARK: Steps

    @MainActor
    private func launch(patch: String, reset: Bool) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        if reset { app.launchEnvironment["RL_RESET_STORE"] = "1" }
        app.launchEnvironment["RL_UITEST_SHARE_PATCH"] = patch
        app.launch()
        return app
    }

    /// The Patch screen of a shared patch: the game picker opens (or is
    /// opened), then the game is chosen.
    @MainActor
    private func chooseGame(_ app: XCUIApplication, game: String, timeout: TimeInterval) {
        let system = app.buttons["picker.system.GBA"]
        if !system.waitForExistence(timeout: 5) {
            app.buttons["patch.game"].tapAfterWaiting(timeout: timeout)
        }
        system.tapAfterWaiting(timeout: timeout)
        app.buttons["picker.game.\(game)"].tapAfterWaiting(timeout: timeout)
        XCTAssertTrue(app.buttons["patch.apply"].waitForExistence(timeout: timeout), "Patch screen not shown")
    }

    @MainActor
    private func replaceText(of field: XCUIElement, with text: String) {
        // The cursor at the end of the text.
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.97, dy: 0.5)).tap()
        let current = field.value as? String ?? ""
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: current.count + 2))
        field.typeText(text)
    }

    /// CRC-32 (IEEE), to describe the source of the next patch.
    static func crc32(_ data: Data) -> UInt32 {
        var crc: UInt32 = 0xffff_ffff
        for byte in data {
            crc ^= UInt32(byte)
            for _ in 0..<8 { crc = crc & 1 != 0 ? (crc >> 1) ^ 0xedb8_8320 : crc >> 1 }
        }
        return ~crc
    }
}
