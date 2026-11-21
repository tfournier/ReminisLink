import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// Milestone M18 in the app: the restore of a trash entry through the
/// session (preview, restore, trash and paks reloaded), conflicts, and
/// the errors shown by the restore sheet.
@MainActor
@Suite("Restore from the trash in the app (M18)", .serialized)
struct TrashRestoreAppTests {
    let secure = InMemorySecureStore()
    let root = FileManager.default.temporaryDirectory.appending(path: "rl-trash-\(UUID().uuidString)")

    /// Sessions hold their app unowned: the apps outlive the tests.
    static var kept: [AppModel] = []

    func makePak() -> FakePak {
        let pak = FakePak(host: "m18-\(UUID().uuidString.lowercased()).test")
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

    @Test func restoresTheDemoEntryAndReloadsTheTrash() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        await session.loadTrash()
        let entry = try #require(session.trash?.entries.first)

        let preview = try await session.trashRestorePreview(entry.id)
        #expect(!preview.conflict && preview.label == entry.label && !preview.items.isEmpty)
        let result = try await session.restoreTrashEntry(entry.id, onConflict: .fail)
        #expect(!result.renamed && result.restored == preview.items.map(\.name))
        await waitUntil { session.trash?.entries.contains { $0.id == entry.id } == false }
        #expect(session.trash?.entries.contains { $0.id == entry.id } == false)
        #expect(pak.library.systems.flatMap(\.games).contains { $0.name == "Old Prototype" })
    }

    @Test func aTakenPlaceIsReportedThenRestoredUnderAFreeName() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        let clock = try #require(try await session.client?.paks().first { $0.name == "Clock" })
        let moved = try await #require(session.client).deletePak(id: clock.pakId)
        pak.installPakFromAnotherClient(name: "Clock")

        await #expect { try await session.restoreTrashEntry(moved.trashEntryId, onConflict: .fail) } throws: { error in
            guard case .api(.server(let server)) = AppError(error) else { return false }
            return server.trashRestoreConflicts == ["Tools/tg5040/Clock.pak"]
        }
        let preview = try await session.trashRestorePreview(moved.trashEntryId)
        #expect(preview.conflict && preview.items.allSatisfy { $0.freeName.hasPrefix("Tools/tg5040/Clock (2).pak") })
        let result = try await session.restoreTrashEntry(moved.trashEntryId, onConflict: .rename)
        #expect(result.renamed)
        #expect(Set(pak.installedPakNames).isSuperset(of: ["Clock", "Clock (2)"]))
    }

    @Test func theErrorsOfARestore() {
        let gone = APIError.server(ServerError(status: 404, code: .notFound, message: "Trash entry not found"))
        #expect(AppError.trashRestore(gone) == .trashEntryGone)
        #expect(AppError.trashRestore(APIError.unexpectedStatus(405)) == .trashEntryGone)
        let refused = APIError.server(ServerError(status: 422, code: .unsupportedFormat, message: "Invalid pak: no launch.sh in the pak folder"))
        #expect(AppError.trashRestore(refused) == .pakRefused(.noLaunchScript))
        #expect(!AppError.trashEntryGone.message.isEmpty && AppError.trashEntryGone.action == nil)
    }
}
