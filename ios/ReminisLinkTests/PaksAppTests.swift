import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// Milestone M12 in the app: the paks of a console (list, deletion to the
/// trash, ReminisLink protected), their installation from the iPhone, a
/// link or the SD card (conflict, then replacement after confirmation;
/// refused archives), the `paks` event, and the SD browser.
@MainActor
@Suite("Paks and SD browser in the app (M12)", .serialized)
struct PaksAppTests {
    let secure = InMemorySecureStore()
    let root = FileManager.default.temporaryDirectory.appending(path: "rl-paks-\(UUID().uuidString)")

    /// Sessions hold their app unowned: the apps outlive the tests.
    static var kept: [AppModel] = []

    func makePak() -> FakePak {
        let pak = FakePak(host: "m12-\(UUID().uuidString.lowercased()).test")
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

    /// A copy in the transfer directory, as the Install sheet makes it.
    func pickedFile(_ app: AppModel, name: String, size: Int = 4000) throws -> PickedFile {
        let directory = app.transferDirectory.appending(path: UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let url = directory.appending(path: name)
        try Data((0..<size).map { UInt8(truncatingIfNeeded: $0 &* 7) }).write(to: url)
        return PickedFile(url: url, name: name, sizeBytes: Int64(size))
    }

    /// Polls the jobs (no event stream here) until `condition` holds.
    func follow(_ session: ConsoleSession, timeout: Duration = .seconds(10), until condition: () -> Bool) async {
        let deadline = ContinuousClock.now + timeout
        while !condition() && ContinuousClock.now < deadline {
            await session.transfers.refreshJobs()
            try? await Task.sleep(for: .milliseconds(20))
        }
    }

    func waitUntil(timeout: Duration = .seconds(5), _ condition: () -> Bool) async {
        let deadline = ContinuousClock.now + timeout
        while !condition() && ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(10))
        }
    }

    func count(_ pak: FakePak, _ method: String, _ path: String) -> Int {
        pak.log.filter { $0.method == method && $0.path == APIClient.apiPrefix + path }.count
    }

    // MARK: - List and deletion

    @Test func listsThePaksAndNeverDeletesReminisLink() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let manager = app.session(for: pak.deviceId).paks
        await manager.load()
        let paks = try #require(manager.paks)
        let groups = PakList.grouped(paks)
        #expect(groups.map(\.kind) == [.tool, .emu])
        let me = try #require(paks.first { $0.isSelf })
        #expect(me.name == "ReminisLink")
        await #expect(throws: AppError.pakSelf) { try await manager.delete(me) }
        // Nothing was sent.
        #expect(count(pak, "DELETE", "/paks/\(me.pakId)") == 0)
        // Refused by the pak too (another client, an older list): its own text.
        let refusal = AppError.pak(APIError.server(ServerError(status: 409, code: .conflict, message: "x", details: ["self": .bool(true)])))
        #expect(refusal == .pakSelf)
        #expect(refusal.message == String(localized: "ReminisLink cannot delete or replace itself while it runs on the console."))
    }

    @Test func deletesAPakToTheTrash() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let manager = session.paks
        await manager.load()
        let clock = try #require(manager.paks?.first { $0.name == "Clock" })
        try await manager.delete(clock)
        #expect(manager.paks?.contains { $0.name == "Clock" } == false)
        #expect(session.trash?.entries.first?.label == "Clock")
        #expect(session.trash?.entries.first?.reason == "delete-pak")
        #expect(!pak.installedPakNames.contains("Clock"))
    }

    @Test func aBusyConsoleSaysSoAndTryingAgainWorks() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let manager = app.session(for: pak.deviceId).paks
        await manager.load()
        let clock = try #require(manager.paks?.first { $0.name == "Clock" })
        pak.busyNextWrites(1)
        do {
            try await manager.delete(clock)
            Issue.record("not refused")
        } catch {
            #expect(AppError(error).isDeviceBusy)
        }
        #expect(manager.paks?.contains { $0.name == "Clock" } == true)
        try await manager.delete(clock)
        #expect(manager.paks?.contains { $0.name == "Clock" } == false)
    }

    @Test func anOlderPakAsksForAnUpdate() async throws {
        let pak = makePak()
        pak.paksSupported = false
        let app = try await pairedApp(pak)
        let manager = app.session(for: pak.deviceId).paks
        await manager.load()
        #expect(manager.loadError == .needsNewerPak)
        #expect(manager.paks == nil)
    }

    // MARK: - Installations

    @Test func installFromTheIPhoneMeetsTheConflictThenReplaces() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let manager = session.paks
        await manager.load()
        let file = try pickedFile(app, name: "Clock.pak.zip")
        let install = await manager.install(.file(file), name: file.name, kind: .auto)
        #expect(install.upload?.target == .pak(kind: .auto, fileName: "Clock.pak.zip"))
        await follow(session) { install.phase == .needsReplacement(name: "Clock") }
        #expect(install.phase == .needsReplacement(name: "Clock"))
        #expect(manager.pendingReplacement === install)
        // Kept until the user answers: a replacement sends it again.
        #expect(FileManager.default.fileExists(atPath: file.url.path))
        #expect(pak.installedPak(named: "Clock")?.version == "1.2")

        await manager.replace(install)
        #expect(manager.pendingReplacement == nil)
        #expect(install.replacing)
        #expect(install.upload?.onConflict == .overwrite)
        await follow(session) { install.phase == .installed }
        #expect(install.phase == .installed)
        #expect(pak.installedPak(named: "Clock")?.version == "1.0")
        await waitUntil { session.trash?.entries.first?.reason == "replace-pak" }
        #expect(session.trash?.entries.first?.reason == "replace-pak")
        await waitUntil { manager.paks?.first { $0.name == "Clock" }?.version == "1.0" }
        #expect(manager.paks?.first { $0.name == "Clock" }?.version == "1.0")
        // The copy is deleted once installed; the upload did not reload the library.
        await waitUntil { !FileManager.default.fileExists(atPath: file.directory.path) }
        #expect(!FileManager.default.fileExists(atPath: file.directory.path))
        #expect(session.libraryRevision == 0)
    }

    @Test func keepingTheInstalledPakEndsTheInstallation() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let manager = session.paks
        let file = try pickedFile(app, name: "Clock.pak.zip")
        let install = await manager.install(.file(file), name: file.name, kind: .auto)
        await follow(session) { install.phase == .needsReplacement(name: "Clock") }
        manager.keepInstalled(install)
        #expect(install.phase == .cancelled)
        #expect(manager.pendingReplacement == nil)
        await waitUntil { !FileManager.default.fileExists(atPath: file.directory.path) }
        #expect(!FileManager.default.fileExists(atPath: file.directory.path))
        manager.clearFinished()
        #expect(manager.installs.isEmpty)
    }

    @Test func aRefusedArchiveSaysWhy() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let manager = session.paks
        let broken = try pickedFile(app, name: "broken.zip")
        let install = await manager.install(.file(broken), name: broken.name, kind: .auto)
        await follow(session) { install.isFinished }
        #expect(install.phase == .failed(.pakRefused(.notAnArchive)))
        #expect(AppError.pakRefused(.notAnArchive).message == String(localized: "This file is not a zip archive the console can read."))
        #expect(manager.pendingReplacement == nil)

        let noLaunch = try pickedFile(app, name: "NoLaunch.zip")
        let second = await manager.install(.file(noLaunch), name: noLaunch.name, kind: .tool)
        await follow(session) { second.isFinished }
        #expect(second.phase == .failed(.pakRefused(.noLaunchScript)))
        // A reason the app does not know: the console's own words.
        #expect(AppError.pakRefused(.other("weird thing")).message == String(localized: "The console refused this pak: \("weird thing")"))
        // ReminisLink itself is never replaced, and never offered.
        let me = try pickedFile(app, name: "ReminisLink.zip")
        let third = await manager.install(.file(me), name: me.name, kind: .auto)
        await follow(session) { third.isFinished }
        #expect(third.phase == .failed(.pakSelf))
    }

    @Test func installFromTheSdCardInPickerMode() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let manager = session.paks
        // What the picker shows: Downloads, its .pak folder and its zips.
        let rootFolder = try await session.perform { try await $0.browseSd() }
        let downloads = try #require(rootFolder.entries.first { $0.name == "Downloads" })
        let folder = try await session.perform { try await $0.browseSd(folderId: downloads.sdEntryId) }
        let installable = folder.entries.filter(\.isInstallableAsPak).map(\.name)
        #expect(installable.contains("Timer.pak") && installable.contains("Clock.pak.zip"))
        #expect(!installable.contains("README.txt"))

        let timer = try #require(folder.entries.first { $0.name == "Timer.pak" })
        let first = await manager.install(.sdCard(timer), name: timer.name, kind: .auto)
        await follow(session) { first.isFinished }
        #expect(first.phase == .installed)
        #expect(pak.installedPak(named: "Timer")?.kind == "tool")

        let clock = try #require(folder.entries.first { $0.name == "Clock.pak.zip" })
        let second = await manager.install(.sdCard(clock), name: clock.name, kind: .auto)
        await follow(session) { second.phase == .needsReplacement(name: "Clock") }
        await manager.replace(second)
        await follow(session) { second.isFinished }
        #expect(second.phase == .installed)
        let replaceRequests = pak.log.filter { $0.method == "POST" && $0.path == APIClient.apiPrefix + "/paks/install" }
        #expect(replaceRequests.count == 3)
    }

    @Test func aCancelledUploadDeletesItsCopyAtOnce() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let manager = session.paks
        // The upload waits at its creation: cancelled from the Transfers
        // tab, without the Apps screen ever loading.
        let created = pak.holdNextArrival(method: "POST", path: "/api/v1/uploads")
        let file = try pickedFile(app, name: "Weather.pak.zip")
        let install = await manager.install(.file(file), name: file.name, kind: .auto)
        let item = try #require(install.upload)
        await waitUntil { created.reached }
        session.transfers.cancel(item)
        created.open()
        await waitUntil { install.phase == .cancelled && !FileManager.default.fileExists(atPath: file.directory.path) }
        #expect(install.phase == .cancelled)
        #expect(!FileManager.default.fileExists(atPath: file.directory.path))
        session.transfers.clearFinished()
        #expect(count(pak, "GET", "/paks") == 0)
    }

    @Test func aReplacementThatInstalledAnotherPakSaysSo() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let manager = session.paks
        let clock = SdEntry(sdEntryId: FakePak.sdEntryId("Downloads/Clock.pak.zip"), name: "Clock.pak.zip", kind: .file,
                            sizeBytes: 2048, modifiedAt: Date())
        let install = await manager.install(.sdCard(clock), name: clock.name, kind: .auto)
        await follow(session) { install.phase == .needsReplacement(name: "Clock") }
        // The file on the card changed meanwhile: it now holds Files.
        pak.changeNextPakSource(to: "Files.zip")
        await manager.replace(install)
        await follow(session) { install.isFinished }
        #expect(install.phase == .installed)
        await waitUntil { install.mismatch != nil }
        #expect(install.mismatch?.installed == "Files")
        #expect(install.mismatch?.replacedOne == true)
        #expect(session.trash?.entries.first?.label == "Files")
        #expect(pak.installedPak(named: "Clock")?.version == "1.2")

        // The confirmed one replaced: nothing to say.
        let again = await manager.install(.sdCard(clock), name: clock.name, kind: .auto)
        await follow(session) { again.phase == .needsReplacement(name: "Clock") }
        await manager.replace(again)
        await follow(session) { again.isFinished }
        try? await Task.sleep(for: .milliseconds(200))
        #expect(again.phase == .installed && again.mismatch == nil)
    }

    @Test func aCancelledUploadKeepsItsCopyUntilTheConsoleConfirms() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let manager = session.paks
        let created = pak.holdNextAnswer(method: "POST", path: "/api/v1/uploads")
        let file = try pickedFile(app, name: "Weather.pak.zip")
        let install = await manager.install(.file(file), name: file.name, kind: .auto)
        let item = try #require(install.upload)
        await waitUntil { created.reached }
        let uploadId = try #require(pak.uploadIds.first)
        let chunk = pak.holdNextArrival(method: "PUT", path: "/api/v1/uploads/\(uploadId)")
        created.open()
        await waitUntil { chunk.reached }
        let delete = pak.holdNextAnswer(method: "DELETE", path: "/api/v1/uploads/\(uploadId)")
        session.transfers.cancel(item)
        await waitUntil { delete.reached }
        // Not confirmed yet: the copy stays.
        #expect(item.phase == .cancelling)
        #expect(install.phase == .sending)
        #expect(FileManager.default.fileExists(atPath: file.url.path))
        delete.open()
        await waitUntil { install.phase == .cancelled && !FileManager.default.fileExists(atPath: file.directory.path) }
        #expect(item.phase == .cancelled)
        #expect(install.phase == .cancelled)
        #expect(!FileManager.default.fileExists(atPath: file.directory.path))
        chunk.open()
    }

    @Test func anOldReplacementInTheTrashIsNotTakenForThisOne() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let manager = session.paks
        pak.addTrashEntry(reason: "replace-pak", label: "Weather", trashedAt: Date(timeIntervalSinceNow: -86_400))
        let clock = SdEntry(sdEntryId: FakePak.sdEntryId("Downloads/Clock.pak.zip"), name: "Clock.pak.zip", kind: .file,
                            sizeBytes: 2048, modifiedAt: Date())
        let install = await manager.install(.sdCard(clock), name: clock.name, kind: .auto)
        await follow(session) { install.phase == .needsReplacement(name: "Clock") }
        // Now it holds Weather, which is not installed: nothing is replaced.
        pak.changeNextPakSource(to: "Weather.zip")
        await manager.replace(install)
        await follow(session) { install.isFinished }
        await waitUntil { install.mismatch != nil }
        #expect(install.mismatch?.installed == "Weather")
        #expect(install.mismatch?.replacedOne == false)
    }

    @Test func theReplacementIsCheckedOnAListReadAfterIt() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let manager = session.paks
        let clock = SdEntry(sdEntryId: FakePak.sdEntryId("Downloads/Clock.pak.zip"), name: "Clock.pak.zip", kind: .file,
                            sizeBytes: 2048, modifiedAt: Date())
        let install = await manager.install(.sdCard(clock), name: clock.name, kind: .auto)
        await follow(session) { install.phase == .needsReplacement(name: "Clock") }
        pak.changeNextPakSource(to: "Files.zip")
        // The list read when the job ends is held, then overtaken by a
        // newer one (a `paks` event): the check waits for a list it may use.
        let held = pak.holdNextAnswer(method: "GET", path: "/api/v1/paks")
        await manager.replace(install)
        await follow(session) { install.isFinished }
        await waitUntil { held.reached }
        #expect(install.mismatch == nil && install.pendingCheck != nil)
        manager.paksChanged()
        await waitUntil { install.mismatch != nil }
        #expect(install.mismatch?.installed == "Files")
        #expect(install.mismatch?.replacedOne == true)
        #expect(install.pendingCheck == nil)
        held.open()
    }

    @Test func installFromALink() async throws {
        let pak = makePak()
        pak.downloadSize = 4096
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let manager = session.paks
        let url = try #require(URL(string: "https://example.invalid/paks/Notes.pak.zip"))
        let install = await manager.install(.link(url), name: "Notes.pak.zip", kind: .emu)
        await follow(session) { install.isFinished }
        #expect(install.phase == .installed)
        #expect(pak.installedPak(named: "Notes")?.kind == "emu")
        let again = await manager.install(.link(url), name: "Notes.pak.zip", kind: .emu)
        await follow(session) { again.phase == .needsReplacement(name: "Notes") }
        await manager.replace(again)
        await follow(session) { again.isFinished }
        #expect(again.phase == .installed)
        // The job of the download says it installed.
        let job = try #require(session.transfers.jobs.last)
        #expect(job.job?.resultPakId != nil)
    }

    @Test func aPakFolderIsZippedOnTheIPhone() async throws {
        let source = root.appending(path: "src-\(UUID().uuidString)/Timer.pak", directoryHint: .isDirectory)
        try FileManager.default.createDirectory(at: source, withIntermediateDirectories: true)
        try Data("#!/bin/sh\n".utf8).write(to: source.appending(path: "launch.sh"))
        let out = root.appending(path: "out")
        let zipped = try await PakArchiver.prepare(source, into: out)
        #expect(zipped.name == "Timer.pak.zip")
        let head = try FileHandle(forReadingFrom: zipped.url).read(upToCount: 2)
        #expect(head == Data("PK".utf8))

        let other = root.appending(path: "src-\(UUID().uuidString)/Roms", directoryHint: .isDirectory)
        try FileManager.default.createDirectory(at: other, withIntermediateDirectories: true)
        await #expect(throws: AppError.notAPak) { try await PakArchiver.prepare(other, into: out) }
        let text = root.appending(path: "note.txt")
        try Data("x".utf8).write(to: text)
        await #expect(throws: AppError.notAPak) { try await PakArchiver.prepare(text, into: out) }
        let zip = root.appending(path: "Clock.pak.zip")
        try Data("PK".utf8).write(to: zip)
        #expect(try await PakArchiver.prepare(zip, into: out).name == "Clock.pak.zip")
    }

    // MARK: - Events

    @Test func anEventDuringTheFirstLoadIsNotLost() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let manager = app.session(for: pak.deviceId).paks
        _ = try await app.session(for: pak.deviceId).connectedClient()
        // The first answer is computed, then held; the event comes first.
        let first = pak.holdNextAnswer(method: "GET", path: "/api/v1/paks")
        let load = Task { await manager.load() }
        await waitUntil { first.reached }
        pak.installPakFromAnotherClient(name: "Radio")
        manager.paksChanged()
        await waitUntil { count(pak, "GET", "/paks") == 2 }
        await waitUntil { manager.paks?.contains { $0.name == "Radio" } == true }
        first.open()
        await load.value
        try? await Task.sleep(for: .milliseconds(100))
        // The older answer (without Radio) never replaces the newer one.
        #expect(manager.paks?.contains { $0.name == "Radio" } == true)
        #expect(count(pak, "GET", "/paks") == 2)
    }

    @Test func thePaksEventRefreshesTheList() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        app.becomeActive()
        app.openConsole(pak.deviceId)
        let session = app.session(for: pak.deviceId)
        await waitUntil { session.eventsLive && session.catchUpsDone >= 1 }
        #expect(session.eventsLive)
        let manager = session.paks
        await manager.load()
        let loads = count(pak, "GET", "/paks")
        let revision = session.libraryRevision
        pak.installPakFromAnotherClient(name: "Radio")
        await waitUntil { manager.paks?.contains { $0.name == "Radio" } == true }
        #expect(manager.paks?.contains { $0.name == "Radio" } == true)
        #expect(count(pak, "GET", "/paks") > loads)
        // The library did not reload for it.
        #expect(session.libraryRevision == revision)
    }

    // MARK: - SD browser

    @Test func browsesAndDownloadsAFileOfTheCard() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let rootFolder = try await session.perform { try await $0.browseSd() }
        #expect(rootFolder.isRoot)
        // Folders first, ReminisLink's data never listed.
        #expect(rootFolder.entries.prefix { $0.isFolder }.count == rootFolder.entries.filter(\.isFolder).count)
        let downloads = try #require(rootFolder.entries.first { $0.name == "Downloads" })
        let folder = try await session.perform { try await $0.browseSd(folderId: downloads.sdEntryId) }
        let readme = try #require(folder.entries.first { $0.name == "README.txt" })
        let progress = FileDownload(fileId: readme.sdEntryId, expected: readme.sizeBytes)
        let report = progress.reporter(interval: .zero)
        let id = readme.sdEntryId
        let exported = try await session.export { client, directory in
            try await client.downloadSdFile(id: id, fallbackName: "README.txt", into: directory, progress: report)
        }
        #expect(exported.file.url.lastPathComponent == "README.txt")
        #expect(exported.file.url.path.hasPrefix(app.exportDirectory.path))
        #expect(try Data(contentsOf: exported.file.url).count == 512)
        // A file gone since the listing: "this file changed".
        do {
            _ = try await session.export { client, directory in
                try await client.downloadSdFile(id: FakePak.sdEntryId("Downloads/Gone.txt"), into: directory)
            }
            Issue.record("downloaded")
        } catch {
            #expect(AppError.gameFile(error) == .gameFileChanged)
        }
        // A pak older than M12: an update is asked for.
        pak.paksSupported = false
        do {
            _ = try await session.perform { try await $0.browseSd() }
            Issue.record("listed")
        } catch {
            #expect(AppError.pak(error, routeMissing: true) == .needsNewerPak)
        }
    }
}
