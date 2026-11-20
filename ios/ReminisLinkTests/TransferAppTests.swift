import CryptoKit
import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// Milestone M4 in the app: share requests, the transfer queue of a
/// session, deletion and the error texts.
@MainActor
@Suite("Transfers in the app", .serialized)
struct TransferAppTests {
    let secure = InMemorySecureStore()
    let root = FileManager.default.temporaryDirectory.appending(path: "rl-app-\(UUID().uuidString)")

    func makePak() -> FakePak {
        let pak = FakePak(host: "m4-\(UUID().uuidString.lowercased()).test")
        pak.register()
        return pak
    }

    func pairedApp(_ pak: FakePak, devices: Int = 1) async throws -> AppModel {
        let suite = "ReminisLinkTests.\(UUID().uuidString)"
        let app = AppModel(
            store: DeviceStore(secureStore: secure, defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: root.appending(path: "art")),
            shareInbox: ShareInbox(root: root.appending(path: "inbox")),
            transferDirectory: root.appending(path: "transfers"),
            lookupTimeout: .milliseconds(300),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
        try await app.pairDirectly(with: pak)
        if devices > 1 {
            try app.store.save(deviceId: "00000000-0000-4000-8000-000000000002", name: "Other", model: .brick,
                               pin: pak.pin)
        }
        return app
    }

    func sharedFile(_ app: AppModel, name: String = "Shared.gba", size: Int = 50_000) throws -> ShareRequest {
        let directory = root.appending(path: "src-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let file = directory.appending(path: name)
        try Data((0..<size).map { UInt8(truncatingIfNeeded: $0) }).write(to: file)
        return try app.shareInbox.write(files: [file])
    }

    // MARK: Share requests

    @Test func shareLinkOpensAddForTheOnlyConsole() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let request = try sharedFile(app)
        app.handle(url: ShareInbox.url(for: request))
        guard case .add(let context) = app.sheet else { Issue.record("no add sheet"); return }
        #expect(context.deviceId == pak.deviceId)
        #expect(context.request == request)
        // The same request is not opened twice.
        app.checkShareInbox()
        guard case .add(let again) = app.sheet else { Issue.record("sheet replaced"); return }
        #expect(again.id == context.id)
    }

    @Test func severalConsolesLetTheUserChoose() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak, devices: 2)
        _ = try sharedFile(app)
        app.becomeActive()
        guard case .add(let context) = app.sheet else { Issue.record("no add sheet"); return }
        #expect(context.deviceId == nil)
    }

    @Test func shareWaitsForTheCurrentSheet() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        app.sheet = .addConsole()
        let request = try sharedFile(app)
        app.handle(url: ShareInbox.url(for: request))
        guard case .addConsole = app.sheet else { Issue.record("sheet replaced"); return }
        app.sheet = nil
        app.sheetDismissed()
        guard case .add(let context) = app.sheet else { Issue.record("no add sheet"); return }
        #expect(context.request?.id == request.id)
    }

    @Test func noConsoleNoAddSheet() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        try app.forget(deviceId: pak.deviceId)
        let request = try sharedFile(app)
        app.handle(url: ShareInbox.url(for: request))
        #expect(app.sheet == nil)
        // Kept until a console is paired.
        #expect(app.shareInbox.request(id: request.id) != nil)
    }

    /// Regression: swiping the Add sheet down consumes the request, which
    /// must not open again.
    @Test func dismissedAddSheetConsumesTheRequest() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let request = try sharedFile(app)
        app.becomeActive()
        guard case .add = app.sheet else { Issue.record("no add sheet"); return }
        app.sheet = nil
        app.sheetDismissed()
        try await Task.sleep(for: .milliseconds(100))
        #expect(app.shareInbox.request(id: request.id) == nil)
        #expect(app.sheet == nil)
        app.becomeActive()
        #expect(app.sheet == nil)
    }

    @Test func finishedShareIsRemoved() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let request = try sharedFile(app)
        app.finishShare(request)
        #expect(app.shareInbox.pending().isEmpty)
    }

    // MARK: Session transfers

    @Test func uploadThroughTheSessionRefreshesTheLibrary() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let revision = session.libraryRevision
        let request = try sharedFile(app)
        let copy = try await AddView.makeLocalCopy(
            of: AddFile(url: app.shareInbox.fileURL("Shared.gba", in: request), name: "Shared.gba", sizeBytes: 50_000,
                        isSecurityScoped: false, request: request, kind: .rom),
            in: app.transferDirectory, inbox: app.shareInbox
        )
        #expect(copy.path.hasPrefix(app.transferDirectory.path))
        // Moved out of the inbox, not copied.
        #expect(!FileManager.default.fileExists(atPath: app.shareInbox.fileURL("Shared.gba", in: request).path))
        let item = try session.transfers.enqueue(
            fileURL: copy, fileName: "Shared.gba", target: .rom(systemId: DemoLibrary.id("Handheld Advance (GBA)"), fileName: "Shared.gba"),
            destination: "Handheld Advance", ownsFile: true
        )
        await session.transfers.waitUntilIdle()
        #expect(item.phase == .done)
        #expect(session.libraryRevision == revision + 1)
        let games = try await session.perform { try await $0.games(systemId: DemoLibrary.id("Handheld Advance (GBA)")) }
        #expect(games.games.contains { $0.fileName == "Shared.gba" })
    }

    @Test func backgroundPausesAndActiveResumes() async throws {
        let pak = makePak()
        pak.chunkSize = 16 * 1024
        pak.responseDelay = 0.02
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let request = try sharedFile(app, size: 400_000)
        let item = try session.transfers.enqueue(
            fileURL: app.shareInbox.fileURL("Shared.gba", in: request), fileName: "Shared.gba",
            target: .rom(systemId: DemoLibrary.id("Handheld Advance (GBA)"), fileName: "Shared.gba"),
            destination: "", ownsFile: false
        )
        while item.sentBytes == 0 { try await Task.sleep(for: .milliseconds(10)) }
        app.enterBackground()
        await session.transfers.waitUntilIdle()
        #expect(item.phase == .paused)
        #expect(session.transfers.isSuspended)
        app.becomeActive()
        await session.transfers.waitUntilIdle()
        #expect(item.phase == .done)
    }

    @Test func deleteGameGoesToTheTrash() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let gameId = DemoLibrary.id("Handheld Advance (GBA)/Pixel Runner.gba")
        let result = try await session.deleteGame(id: gameId, keepSave: false)
        #expect(result.moved == ["Roms/Handheld Advance (GBA)/Pixel Runner.gba"])
        try await Task.sleep(for: .milliseconds(200))
        #expect(session.trash?.entries.contains { $0.id == result.trashEntryId } == true)
        try await session.emptyTrash()
        #expect(session.trash?.entries.isEmpty == true)
    }

    /// After an unknown outcome, a save or a state sent again replaces the
    /// one on the console: confirmed first, never announced as "both kept".
    @Test func sendingAgainASaveOrAStateReplacesIt() {
        typealias Resend = UploadRow.Resend
        #expect(Resend(.rom(systemId: "s", fileName: "a.gba")) == .keepBoth)
        #expect(Resend(.staging(fileName: "a.ips")) == .keepBoth)
        #expect(Resend(.save(gameId: "g")) == .replaceSave)
        #expect(Resend(.state(gameId: "g", slot: 1)) == .replaceState)
        #expect(Resend.keepBoth.onConflict == .rename)
        #expect(!Resend.keepBoth.needsConfirmation)
        #expect(Resend.replaceSave.needsConfirmation)
        #expect(Resend.replaceState.needsConfirmation)
        #expect(Resend.replaceSave.onConflict == .overwrite)
    }

    // MARK: Errors

    @Test func transferErrorTexts() {
        let conflict = AppError(APIError.server(ServerError(status: 409, code: .conflict, message: "x",
                                                            details: ["existing": .string("Roms/GBA/a.gba")])))
        #expect(conflict.message.contains("Roms/GBA/a.gba"))
        let storage = AppError(APIError.server(ServerError(status: 507, code: .insufficientStorage, message: "x",
                                                           details: ["required": .integer(3_000_000), "available": .integer(1_000_000)])))
        #expect(storage.message.contains("2"))
        #expect(AppError(UploadFailure.outcomeUnknown) == .upload(.outcomeUnknown))
        #expect(AppError(CocoaError(.fileReadNoPermission)) == .file)
        #expect(AppError(APIError.network(.networkConnectionLost)).action == .retry)
        for error in [AppError.upload(.outcomeUnknown), .upload(.fileChanged), .upload(.tooManyRestarts), .file] {
            #expect(!error.message.isEmpty)
        }
    }
}
