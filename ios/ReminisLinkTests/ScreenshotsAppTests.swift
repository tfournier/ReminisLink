import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// Milestone M22 in the app: the screenshots of a console (list, previews
/// kept in memory, downloads for the share sheet, moves to the trash and
/// their restore), the `screenshots` event, and an older pak.
@MainActor
@Suite("Screenshots in the app (M22)", .serialized)
struct ScreenshotsAppTests {
    let secure = InMemorySecureStore()
    let root = FileManager.default.temporaryDirectory.appending(path: "rl-shots-\(UUID().uuidString)")

    /// Sessions hold their app unowned: the apps outlive the tests.
    static var kept: [AppModel] = []

    func makePak() -> FakePak {
        let pak = FakePak(host: "m22-\(UUID().uuidString.lowercased()).test")
        pak.register()
        return pak
    }

    func pairedApp(_ pak: FakePak) async throws -> AppModel {
        let suite = "ReminisLinkTests.\(UUID().uuidString)"
        let settings = UserDefaults(suiteName: suite)!
        settings.set(false, forKey: AppModel.syncOnConnectKey)
        let app = AppModel(
            store: DeviceStore(secureStore: secure, defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: root.appending(path: "art")),
            shareInbox: ShareInbox(root: root.appending(path: "inbox")),
            transferDirectory: root.appending(path: "transfers"),
            exportDirectory: root.appending(path: "exports"),
            settings: settings,
            lookupTimeout: .milliseconds(300),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
        Self.kept.append(app)
        try await app.pairDirectly(with: pak)
        return app
    }

    func waitUntil(timeout: Duration = .seconds(5), _ condition: () -> Bool) async {
        let deadline = ContinuousClock.now + timeout
        while !condition() && ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(10))
        }
    }

    func count(_ pak: FakePak, _ method: String, _ path: String) -> Int {
        pak.log.filter { $0.method == method && $0.path.hasPrefix(APIClient.apiPrefix + path) }.count
    }

    @Test func listsAndKeepsThePreviewsInMemory() async throws {
        let pak = makePak()
        pak.drawsArtwork = true
        let app = try await pairedApp(pak)
        let manager = app.session(for: pak.deviceId).screenshots
        await manager.load()
        #expect(manager.screenshots.map(\.name) == pak.screenshotNames)
        let first = try #require(manager.screenshots.first)
        #expect(manager.cachedThumbnail(for: first) == nil)
        let data = try #require(await manager.thumbnail(for: first))
        #expect(manager.cachedThumbnail(for: first) == data)
        let previews = count(pak, "GET", "/screenshots/\(first.screenshotId)/thumbnail")
        _ = await manager.thumbnail(for: first)
        #expect(count(pak, "GET", "/screenshots/\(first.screenshotId)/thumbnail") == previews)
        // A BMP gets no preview, and none is asked for.
        let bmp = try #require(manager.screenshots.first { !$0.hasThumbnail })
        #expect(await manager.thumbnail(for: bmp) == nil)
        #expect(count(pak, "GET", "/screenshots/\(bmp.screenshotId)/thumbnail") == 0)
    }

    @Test func exportsSeveralScreenshotsInOneFolder() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let manager = app.session(for: pak.deviceId).screenshots
        await manager.load()
        let picked = Array(manager.screenshots.prefix(3))
        let exported = try await manager.export(picked)
        let urls = [exported.file.url] + exported.others
        #expect(urls.map(\.lastPathComponent) == picked.map(\.name))
        #expect(Set(urls.map { $0.deletingLastPathComponent() }).count == 1)
        #expect(urls.allSatisfy { FileManager.default.fileExists(atPath: $0.path) })
        ExportedFiles.remove(exported.file)
        await waitUntil { !FileManager.default.fileExists(atPath: exported.file.directory.path) }
        #expect(!FileManager.default.fileExists(atPath: exported.file.directory.path))
        let detail = try await manager.displayImage(for: picked[0], maxPixels: 100)
        #expect(max(detail.width, detail.height) <= 100)
    }

    /// Codex review of M22: the detail screen decodes at most the size it
    /// shows, off the main actor, and refuses a file too large to show.
    @Test func theDetailImageIsDownsampled() throws {
        let directory = FileManager.default.temporaryDirectory.appending(path: "rl-big-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let url = directory.appending(path: "big.png")
        try DemoArtwork.screen(for: "Big", slot: 1, width: 3000, height: 2000).write(to: url)
        let image = try ScreenshotManager.downsampled(url, maxPixels: 600)
        #expect(image.width == 600 && image.height == 400)
        try Data("not an image".utf8).write(to: url)
        #expect(throws: AppError.self) { try ScreenshotManager.downsampled(url, maxPixels: 600) }
    }

    @Test func aScreenshotTooLargeIsNotShown() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let manager = app.session(for: pak.deviceId).screenshots
        let huge = Screenshot(screenshotId: FakePak.screenshotId("huge.bmp"), name: "huge.bmp", takenAt: Date(),
                              sizeBytes: ScreenshotManager.maxPreviewBytes + 1, format: .bmp, hasThumbnail: false)
        await #expect { _ = try await manager.displayImage(for: huge, maxPixels: 100) } throws: { error in
            (error as? AppError) == .screenshotTooLarge
        }
        #expect(count(pak, "GET", "/screenshots/\(huge.screenshotId)") == 0)
    }

    /// Codex review of M22: a selection of more than 500 whose last batch
    /// fails says how many moved, and both views are read again.
    @Test func aPartialMoveSaysSoAndReloadsBoth() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let manager = session.screenshots
        for i in 0..<500 {
            pak.addScreenshot(name: "Extra \(i).png", takenAt: Date(timeIntervalSince1970: 1_700_000_000 + Double(i)))
        }
        await manager.load()
        let picked = Array(manager.screenshots.prefix(501))
        #expect(picked.count == 501)
        pak.trashScreenshotsFromAnotherClient(names: [picked[500].name])
        let trashLoads = count(pak, "GET", "/trash")
        await #expect { try await manager.trash(picked) } throws: { error in
            (error as? AppError) == .screenshotsPartlyMoved(500)
        }
        #expect(count(pak, "GET", "/trash") > trashLoads)
        #expect(manager.screenshots.count == pak.screenshotNames.count)
        #expect(session.trash?.entries.contains { $0.screenshotCount == 500 } == true)
    }

    @Test func freeNamesIgnoreCase() {
        var taken: Set<String> = ["shot.png"]
        #expect(ScreenshotManager.freeName("SHOT.png", taken: &taken) == "SHOT (2).png")
        #expect(ScreenshotManager.freeName("shot.png", taken: &taken) == "shot (3).png")
        #expect(ScreenshotManager.freeName("other.png", taken: &taken) == "other.png")
    }

    @Test func movesToTheTrashThenRestores() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let manager = session.screenshots
        await manager.load()
        let total = manager.screenshots.count
        let picked = Array(manager.screenshots.prefix(2))
        try await manager.trash(picked)
        #expect(manager.screenshots.count == total - 2)
        #expect(!manager.screenshots.contains { picked.contains($0) })
        let entry = try #require(session.trash?.entries.first)
        #expect(entry.reason == TrashEntry.screenshotReason)
        #expect(entry.screenshotCount == 2)
        #expect(entry.displayLabel.contains("2"))
        _ = try await session.restoreTrashEntry(entry.id, onConflict: .fail)
        await waitUntil { manager.screenshots.count == total }
        #expect(manager.screenshots.count == total)
    }

    @Test func aScreenshotGoneMeanwhileReloadsTheList() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let manager = app.session(for: pak.deviceId).screenshots
        await manager.load()
        let first = try #require(manager.screenshots.first)
        pak.trashScreenshotsFromAnotherClient(names: [first.name])
        await #expect { try await manager.trash([first]) } throws: { error in
            (error as? AppError) == .screenshotGone
        }
        await waitUntil { !manager.screenshots.contains(first) }
        #expect(!manager.screenshots.contains(first))
        await #expect { _ = try await manager.displayImage(for: first, maxPixels: 100) } throws: { error in
            (error as? AppError) == .screenshotGone
        }
    }

    @Test func theScreenshotsEventRefreshesTheList() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        app.becomeActive()
        app.openConsole(pak.deviceId)
        let session = app.session(for: pak.deviceId)
        await waitUntil { session.eventsLive && session.catchUpsDone >= 1 }
        #expect(session.eventsLive)
        let manager = session.screenshots
        await manager.load()
        let revision = session.libraryRevision
        let gone = try #require(manager.screenshots.first)
        pak.trashScreenshotsFromAnotherClient(names: [gone.name])
        await waitUntil { !manager.screenshots.contains(gone) }
        #expect(!manager.screenshots.contains(gone))
        // The trash reloads, the library does not.
        await waitUntil { session.trash?.entries.contains { $0.screenshotCount == 1 } == true }
        #expect(session.trash?.entries.first?.label == gone.name)
        #expect(session.libraryRevision == revision)
    }

    @Test func anOlderPakSaysToUpdate() async throws {
        let pak = makePak()
        pak.screenshotsSupported = false
        let app = try await pairedApp(pak)
        let manager = app.session(for: pak.deviceId).screenshots
        await manager.load()
        #expect(manager.loadError == .screenshotsNeedNewerPak)
        #expect(manager.list == nil)
    }
}
