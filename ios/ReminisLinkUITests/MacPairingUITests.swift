import XCTest

/// M15: Add a Console without VisionKit's scanner (an iPad app on a Mac,
/// and the simulator): the camera fallback or, without a camera, the import
/// of a picture of the QR code, and never "the Camera app".
final class MacPairingUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor
    private func openAddConsole(_ environment: [String: String] = [:]) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments += GameDataUITest.englishArguments
        app.launchEnvironment["RL_DEMO"] = "1"
        for (key, value) in environment { app.launchEnvironment[key] = value }
        app.launch()
        app.buttons["welcome.add"].tapAfterWaiting()
        return app
    }

    /// No camera (the simulator has none, and VisionKit's scanner is not
    /// supported there): the picture import and pairing from another device.
    @MainActor
    func testWithoutACameraAPictureCanBeImported() throws {
        let app = openAddConsole()
        let noCamera = app.staticTexts["pairing.noCamera"]
        XCTAssertTrue(noCamera.waitForExistence(timeout: 10))
        XCTAssertTrue(noCamera.label.contains("iCloud Keychain"), noCamera.label)
        XCTAssertFalse(noCamera.label.contains("Camera app"), noCamera.label)
        XCTAssertFalse(app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'Camera app'")).firstMatch.exists)
        XCTAssertTrue(app.buttons["pairing.importPhoto"].exists)
        XCTAssertTrue(app.buttons["pairing.importFile"].exists)
        attachScreenshot(app, "add-console-no-camera")

        // The Files picker opens (and is cancelled).
        app.buttons["pairing.importFile"].tap()
        let cancel = app.buttons["Cancel"].firstMatch
        XCTAssertTrue(cancel.waitForExistence(timeout: 10))
        cancel.tap()
        XCTAssertTrue(app.buttons["pairing.importFile"].waitForExistence(timeout: 10))
    }

    /// The same, forced (as on a device whose camera is missing).
    @MainActor
    func testForcedNoScannerShowsTheFallback() throws {
        let app = openAddConsole(["RL_UITEST_SCANNER": "none"])
        XCTAssertTrue(app.staticTexts["pairing.noCamera"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["pairing.importPhoto"].exists)
        // The default name of this device is its model, "Mac" on a Mac.
        let name = app.textFields["pairing.clientName"]
        XCTAssertTrue(name.waitForExistence(timeout: 5))
        let placeholder = name.placeholderValue ?? ""
        XCTAssertTrue(["iPhone", "iPad"].contains(placeholder), placeholder)
    }

    @MainActor
    private func attachScreenshot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
