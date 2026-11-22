import XCTest

/// Patch screen (milestone M6): a patch of the SD card for another ROM is
/// refused with a clear message, a matching one replaces the game (the
/// screen closes by itself on the game, which says so); an IPS keeps the
/// screen for its warning; a shared patch opens the Patch screen, the
/// game is chosen, a new file is made and opened by itself.
///
/// XCUITest cannot drive the document picker: a shared patch is made by
/// the app at launch (`RL_UITEST_SHARE_PATCH=<name>:<source size>:<source
/// CRC32>:<target text>`, Debug only): a BPS that writes the target text
/// and declares that source. It goes into the share inbox exactly as the
/// share extension leaves files.
final class PatchUITests: XCTestCase {
    /// The "Patch applied" notice stays 30 s instead of 5: a query takes
    /// up to 4 s on a slow CI runner, and the notice was gone before the
    /// test could read it.
    static let longNotice = ["RL_UITEST_NOTICE_SECONDS": "30"]

    override func setUp() {
        continueAfterFailure = false
    }

    /// Against the fake pak, from the game sheet: SD card patches.
    @MainActor
    func testPatchFromTheSDCardInDemoMode() throws {
        let app = GameDataUITest.openDemoGame(Self.longNotice)
        XCTAssertTrue(GameDataUITest.scrollTo(app.buttons["game.patch"], in: app), "no Patch button")
        app.buttons["game.patch"].tap()
        app.segmentedControls["patch.source"].buttons.element(boundBy: 1).tapAfterWaiting()

        // A patch for another ROM.
        GameDataUITest.button(app, "patch.sdFile", containing: "Brambleway Dash (Hack).ups").tapAfterWaiting(timeout: 15)
        app.buttons["patch.apply"].tapWhenEnabled()
        let error = app.descendants(matching: .any)["patch.error"].firstMatch
        XCTAssertTrue(error.waitForExistence(timeout: 20), "no mismatch message")
        XCTAssertTrue(error.label.contains("does not match this ROM"), error.label)
        attachScreenshot(of: app, named: "patch-mismatch")

        // The patch of this game, replacing it.
        GameDataUITest.button(app, "patch.sdFile", containing: "Thistledown Isles (Translation).bps").tap()
        XCTAssertFalse(error.exists, "error kept after a new choice")
        // Below the fold in the shorter sheet of an iPad (M15).
        let replace = app.buttons["Replace the Game"].firstMatch
        XCTAssertTrue(GameDataUITest.scrollTo(replace, in: app), "no Replace the Game")
        replace.tapAfterWaiting()
        XCTAssertTrue(app.descendants(matching: .any)["patch.replaceWarning"].firstMatch.waitForExistence(timeout: 5))
        app.buttons["patch.apply"].tapWhenEnabled()
        app.dialogButton("patch.replaceConfirm").tapAfterWaiting()
        // Applied: the screen closes by itself, the game sheet says so.
        assertClosedOnGame(app, named: "Thistledown Isles", timeout: 20)
        attachScreenshot(of: app, named: "patch-replaced")

        // An IPS: applied unverified, the screen stays for its warning.
        XCTAssertTrue(GameDataUITest.scrollTo(app.buttons["game.patch"], in: app), "no Patch button")
        app.buttons["game.patch"].tapAfterWaiting()
        app.segmentedControls["patch.source"].buttons.element(boundBy: 1).tapAfterWaiting()
        GameDataUITest.button(app, "patch.sdFile", containing: "Bonus Levels.ips").tapAfterWaiting(timeout: 15)
        app.buttons["patch.apply"].tapWhenEnabled()
        XCTAssertTrue(app.descendants(matching: .any)["patch.unverified"].firstMatch.waitForExistence(timeout: 20),
                      "no warning for the IPS")
        XCTAssertTrue(app.buttons["patch.openGame"].exists, "the screen did not stay")
        attachScreenshot(of: app, named: "patch-ips-unverified")
    }

    /// Against the fake pak: a shared patch opens the Patch screen.
    @MainActor
    func testSharedPatchInDemoMode() throws {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        // Thistledown Isles is 16 MiB in the demo; the fake pak checks the size.
        app.launchEnvironment["RL_UITEST_SHARE_PATCH"] = "UITest Hack.bps:16777216:00000000:UITest output"
        for (key, value) in Self.longNotice { app.launchEnvironment[key] = value }
        app.launch()
        app.open(URL(string: PairingUITests.demoLink)!)
        let pairingDone = app.buttons["pairing.done"]
        XCTAssertTrue(pairingDone.waitForExistence(timeout: 15), "pairing did not finish")
        pairingDone.tap()

        chooseGame(app, system: "HA", game: "Thistledown Isles", timeout: 15)
        XCTAssertTrue(app.descendants(matching: .any)["patch.file"].firstMatch.waitForExistence(timeout: 10), "shared file not shown")
        let name = app.textFields["patch.fileName"]
        XCTAssertEqual(name.value as? String, "Thistledown Isles (patched).hab")
        // Without extension: the ROM's is added (shown, then sent).
        replaceText(of: name, with: "Thistledown Isles DX")
        let finalName = app.staticTexts["patch.finalName"]
        XCTAssertTrue(finalName.waitForExistence(timeout: 5))
        XCTAssertTrue(finalName.label.contains("Thistledown Isles DX.hab"), finalName.label)
        attachScreenshot(of: app, named: "patch-shared")
        app.buttons["patch.apply"].tapWhenEnabled()
        // The new game opens by itself.
        assertClosedOnGame(app, named: "Thistledown Isles DX", timeout: 30)
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
        chooseGame(app, system: "GBA", game: GameDataUITest.headlessGame, timeout: 30)
        app.buttons["patch.apply"].tapWhenEnabled(timeout: 20)
        let error = app.descendants(matching: .any)["patch.error"].firstMatch
        XCTAssertTrue(error.waitForExistence(timeout: 30), "no mismatch message")
        XCTAssertTrue(error.label.contains("does not match this ROM"), error.label)
        attachScreenshot(of: app, named: "headless-mismatch")
        app.terminate()

        // 2. The right source: a new file, opened.
        app = launch(patch: "UITest Hack.bps:10:4afa16bd:\(String(decoding: first, as: UTF8.self))", reset: false)
        chooseGame(app, system: "GBA", game: GameDataUITest.headlessGame, timeout: 30)
        let name = app.textFields["patch.fileName"]
        XCTAssertTrue(name.waitForExistence(timeout: 10))
        replaceText(of: name, with: "\(newName).gba")
        app.buttons["patch.apply"].tapWhenEnabled(timeout: 20)
        assertClosedOnGame(app, named: newName, timeout: 60)
        attachScreenshot(of: app, named: "headless-new-file")
        app.terminate()

        // 3. A patch for the new file: it replaces it.
        let crc = String(format: "%08x", Self.crc32(first))
        app = launch(patch: "UITest Hack 2.bps:\(first.count):\(crc):\(String(decoding: second, as: UTF8.self))", reset: false)
        chooseGame(app, system: "GBA", game: newName, timeout: 30)
        app.buttons["Replace the Game"].firstMatch.tapAfterWaiting()
        app.buttons["patch.apply"].tapWhenEnabled(timeout: 20)
        app.dialogButton("patch.replaceConfirm").tapAfterWaiting()
        // Shared, in place: the screen closes by itself on the game.
        assertClosedOnGame(app, named: newName, timeout: 60)
        attachScreenshot(of: app, named: "headless-replaced")
    }

    // MARK: Steps

    /// The patch is applied: the Patch screen closed by itself and the
    /// sheet of the game shows, with its notice.
    @MainActor
    private func assertClosedOnGame(_ app: XCUIApplication, named name: String, timeout: TimeInterval,
                                    file: StaticString = #filePath, line: UInt = #line) {
        let title = app.staticTexts.matching(NSPredicate(format: "identifier == 'game.title' AND label == %@", name)).firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: timeout), "not on the game \(name)", file: file, line: line)
        // The notice (kept 30 s here, ``longNotice``), with its text, in one
        // query.
        let notice = app.descendants(matching: .any)
            .matching(NSPredicate(format: "identifier == 'notice' AND label CONTAINS 'Patch applied'")).firstMatch
        XCTAssertTrue(notice.waitForExistence(timeout: 10), "no Patch applied notice", file: file, line: line)
        XCTAssertTrue(app.buttons["patch.close"].waitForNonExistence(timeout: 10), "Patch screen still shown",
                      file: file, line: line)
    }

    @MainActor
    private func launch(patch: String, reset: Bool) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        if reset { app.launchEnvironment["RL_RESET_STORE"] = "1" }
        app.launchEnvironment["RL_UITEST_SHARE_PATCH"] = patch
        for (key, value) in Self.longNotice { app.launchEnvironment[key] = value }
        app.launch()
        return app
    }

    /// The Patch screen of a shared patch: the game picker opens (or is
    /// opened), then the game is chosen.
    @MainActor
    private func chooseGame(_ app: XCUIApplication, system tag: String, game: String, timeout: TimeInterval) {
        let system = app.buttons["picker.system.\(tag)"]
        if !system.waitForExistence(timeout: 5) {
            app.buttons["patch.game"].tapAfterWaiting(timeout: timeout)
        }
        system.tapAfterWaiting(timeout: timeout)
        app.buttons["picker.game.\(game)"].tapAfterWaiting(timeout: timeout)
        XCTAssertTrue(app.buttons["patch.apply"].waitForExistence(timeout: timeout), "Patch screen not shown")
    }

    /// Replaces the name of the new file: clears it with its Clear
    /// button, waits until the field really has the keyboard focus (a tap
    /// during a sheet animation is lost), then types.
    @MainActor
    private func replaceText(of field: XCUIElement, with text: String, file: StaticString = #filePath, line: UInt = #line) {
        let app = XCUIApplication()
        XCTAssertTrue(field.waitForExistence(timeout: 10), "no field", file: file, line: line)
        let clear = app.buttons["patch.clearName"]
        if clear.waitForExistence(timeout: 3) { clear.tap() }
        XCTAssertTrue((field.value as? String ?? "").isEmpty || field.value as? String == field.placeholderValue,
                      "field not cleared", file: file, line: line)
        let focused = NSPredicate(format: "hasKeyboardFocus == true")
        var hasFocus = false
        for _ in 0..<5 where !hasFocus {
            if !field.isHittable { app.swipeUp() }
            field.tap()
            hasFocus = XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: focused, object: field)], timeout: 2) == .completed
        }
        XCTAssertTrue(hasFocus, "the field never got the keyboard focus", file: file, line: line)
        field.typeText(text)
        XCTAssertEqual(field.value as? String, text, file: file, line: line)
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
