import Foundation
import ReminisLinkDemo
import Testing
@testable import ReminisLinkKit

/// Records the background activities, and can make them expire.
@MainActor
final class FakeBackground: BackgroundActivity {
    var begun = 0
    var ended = Set<Int>()
    var expirations: [Int: @MainActor () -> Void] = [:]

    func begin(name: String, onExpiration: @escaping @MainActor () -> Void) -> Int {
        begun += 1
        expirations[begun] = onExpiration
        return begun
    }

    func end(_ token: Int) { ended.insert(token) }

    func expireAll() {
        for (token, handler) in expirations where !ended.contains(token) {
            handler()
            ended.insert(token)
        }
    }
}

@MainActor
@Suite("Transfer queue")
struct TransferQueueTests {
    let scratch = FileManager.default.temporaryDirectory.appending(path: "rl-queue-\(UUID().uuidString)")

    func makeQueue(_ client: APIClient, background: BackgroundActivity = NoBackgroundActivity()) -> TransferQueue {
        TransferQueue(scratchDirectory: scratch, background: background, retry: .immediate) { client }
    }

    @Test func uploadsRunInOrderAndReportProgress() async throws {
        let pak = makePak()
        pak.chunkSize = 32 * 1024
        let client = try await pairedClient(pak)
        let background = FakeBackground()
        let queue = makeQueue(client, background: background)
        var finished: [String] = []
        queue.onUploadFinished = { finished.append($0.fileName) }
        let a = try queue.enqueue(fileURL: try makeFile(size: 100_000, name: "A.gba"),
                                  target: .rom(systemId: gbaSystemId, fileName: "A.gba"), destination: "GBA", ownsFile: false)
        let b = try queue.enqueue(fileURL: try makeFile(size: 50_000, name: "B.gba"),
                                  target: .rom(systemId: gbaSystemId, fileName: "B.gba"), destination: "GBA", ownsFile: false)
        #expect(a.sizeBytes == 100_000)
        await queue.waitUntilIdle()
        #expect(a.phase == .done)
        #expect(b.phase == .done)
        #expect(a.sentBytes == 100_000)
        #expect(a.fractionCompleted == 1)
        #expect(a.result?.gameId != nil)
        #expect(finished == ["A.gba", "B.gba"])
        #expect(background.begun == 2)
        #expect(background.ended == [1, 2])
        // A then B, never interleaved.
        let uploads = pak.chunkLog.map(\.uploadId)
        #expect(uploads == uploads.sorted { uploads.firstIndex(of: $0)! < uploads.firstIndex(of: $1)! })
    }

    @Test func ownedCopyIsDeletedOnceDone() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let queue = makeQueue(client)
        let file = try makeFile(size: 1000, name: "C.gba")
        try queue.enqueue(fileURL: file, target: .rom(systemId: gbaSystemId, fileName: "C.gba"), destination: "GBA", ownsFile: true)
        await queue.waitUntilIdle()
        try await Task.sleep(for: .milliseconds(200))
        #expect(!FileManager.default.fileExists(atPath: file.deletingLastPathComponent().path))
    }

    @Test func lostConnectionPausesThenResumes() async throws {
        let pak = makePak()
        pak.chunkSize = 32 * 1024
        let client = try await pairedClient(pak)
        let queue = makeQueue(client)
        // More failures in a row than the retry policy allows.
        pak.injectChunkFaults(Array(repeating: .dropBeforeStoring, count: 5))
        let item = try queue.enqueue(fileURL: try makeFile(size: 100_000), target: .rom(systemId: gbaSystemId, fileName: "D.gba"),
                                     destination: "GBA", ownsFile: false)
        await queue.waitUntilIdle()
        #expect(item.phase == .paused)
        #expect(item.failure as? APIError == .network(.networkConnectionLost))
        queue.resumePaused()
        await queue.waitUntilIdle()
        #expect(item.phase == .done)
        #expect(item.failure == nil)
    }

    @Test func backgroundExpirationPausesAfterTheChunk() async throws {
        let pak = makePak()
        pak.chunkSize = 16 * 1024
        pak.responseDelay = 0.02
        let client = try await pairedClient(pak)
        let background = FakeBackground()
        let queue = makeQueue(client, background: background)
        let item = try queue.enqueue(fileURL: try makeFile(size: 400_000), target: .rom(systemId: gbaSystemId, fileName: "E.gba"),
                                     destination: "GBA", ownsFile: false)
        while item.sentBytes == 0 { try await Task.sleep(for: .milliseconds(10)) }
        background.expireAll()
        await queue.waitUntilIdle()
        #expect(item.phase == .paused)
        #expect(queue.isSuspended)
        #expect(item.sentBytes > 0 && item.sentBytes < 400_000)
        queue.resumeAfterSuspension()
        await queue.waitUntilIdle()
        #expect(item.phase == .done)
    }

    /// Regression: in the background, the next upload must not start.
    @Test func waitingItemStartsInBackground() async throws {
        let pak = makePak()
        pak.chunkSize = 16 * 1024
        pak.responseDelay = 0.02
        let client = try await pairedClient(pak)
        let queue = makeQueue(client)
        let first = try queue.enqueue(fileURL: try makeFile(size: 200_000, name: "G1.gba"),
                                      target: .rom(systemId: gbaSystemId, fileName: "G1.gba"), destination: "", ownsFile: false)
        let second = try queue.enqueue(fileURL: try makeFile(size: 50_000, name: "G2.gba"),
                                       target: .rom(systemId: gbaSystemId, fileName: "G2.gba"), destination: "", ownsFile: false)
        while first.sentBytes == 0 { try await Task.sleep(for: .milliseconds(10)) }
        queue.suspend()
        await queue.waitUntilIdle()
        try await Task.sleep(for: .milliseconds(200))
        #expect(first.phase == .paused)
        #expect(second.phase == .waiting)
        #expect(second.hashedBytes == 0)
        #expect(Set(pak.chunkLog.map(\.uploadId)).count == 1)
        queue.resumeAfterSuspension()
        await queue.waitUntilIdle()
        #expect(first.phase == .done)
        #expect(second.phase == .done)
    }

    /// Regression: back in the foreground before the chunk ends, the upload
    /// must not stay paused.
    @Test func quickReturnLeavesUploadPaused() async throws {
        let pak = makePak()
        pak.chunkSize = 16 * 1024
        pak.responseDelay = 0.05
        let client = try await pairedClient(pak)
        let queue = makeQueue(client)
        let item = try queue.enqueue(fileURL: try makeFile(size: 100_000), target: .rom(systemId: gbaSystemId, fileName: "H.gba"),
                                     destination: "", ownsFile: false)
        while item.sentBytes == 0 { try await Task.sleep(for: .milliseconds(5)) }
        queue.suspend()
        queue.resumeAfterSuspension()
        await queue.waitUntilIdle()
        #expect(item.phase == .done)
        // Also when the run already stopped on the flag.
        let other = try queue.enqueue(fileURL: try makeFile(size: 100_000, name: "I.gba"),
                                      target: .rom(systemId: gbaSystemId, fileName: "I.gba"), destination: "", ownsFile: false)
        while other.sentBytes == 0 { try await Task.sleep(for: .milliseconds(5)) }
        queue.suspend()
        try await Task.sleep(for: .milliseconds(120))
        queue.resumeAfterSuspension()
        await queue.waitUntilIdle()
        #expect(other.phase == .done)
    }

    /// Regression: suspended between `pump` and `process`, nothing starts.
    @Test func processDoesNothingWhileSuspended() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let queue = makeQueue(client)
        queue.suspend()
        let item = try queue.enqueue(fileURL: try makeFile(size: 10_000), target: .rom(systemId: gbaSystemId, fileName: "K.gba"),
                                     destination: "", ownsFile: false)
        let requests = pak.log.count
        await queue.process(item)
        #expect(item.phase == .waiting)
        #expect(item.hashedBytes == 0)
        #expect(pak.log.count == requests)
        queue.resumeAfterSuspension()
        await queue.waitUntilIdle()
        #expect(item.phase == .done)
    }

    @Test func suspensionBetweenTwoItems() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let queue = makeQueue(client)
        // Suspended right when the first upload ends.
        queue.onUploadFinished = { _ in queue.suspend() }
        let first = try queue.enqueue(fileURL: try makeFile(size: 10_000, name: "L1.gba"),
                                      target: .rom(systemId: gbaSystemId, fileName: "L1.gba"), destination: "", ownsFile: false)
        let second = try queue.enqueue(fileURL: try makeFile(size: 10_000, name: "L2.gba"),
                                       target: .rom(systemId: gbaSystemId, fileName: "L2.gba"), destination: "", ownsFile: false)
        await queue.waitUntilIdle()
        #expect(first.phase == .done)
        #expect(second.phase == .waiting)
        #expect(second.hashedBytes == 0)
        queue.onUploadFinished = nil
        queue.resumeAfterSuspension()
        await queue.waitUntilIdle()
        #expect(second.phase == .done)
    }

    /// After a lost `complete` request (nothing written), the pak still has
    /// the upload: cancelling deletes it and shows it cancelled.
    @Test func cancelAfterALostCompleteRequestWhenThePakStillHasTheUpload() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let queue = TransferQueue(scratchDirectory: scratch, retry: RetryPolicy(delays: [], sleep: { _ in })) { client }
        pak.loseCompleteRequests(1)
        let item = try queue.enqueue(fileURL: try makeFile(size: 10_000), target: .rom(systemId: gbaSystemId, fileName: "M.gba"),
                                     destination: "", ownsFile: false)
        await queue.waitUntilIdle()
        #expect(item.phase == .paused)
        #expect(item.completeSent)
        #expect(pak.uploadIds.count == 1)
        queue.cancel(item)
        for _ in 0..<50 where item.phase != .cancelled { try await Task.sleep(for: .milliseconds(20)) }
        #expect(item.phase == .cancelled)
        #expect(pak.uploadIds.isEmpty)
        #expect(try await !client.games(systemId: gbaSystemId).games.contains { $0.fileName == "M.gba" })
    }

    @Test func retryClearsTheCompleteSentMirror() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let queue = makeQueue(client)
        pak.dropCompleteAnswers(1)
        let item = try queue.enqueue(fileURL: try makeFile(size: 10_000), target: .rom(systemId: gbaSystemId, fileName: "N.gba"),
                                     destination: "", ownsFile: false)
        await queue.waitUntilIdle()
        #expect(item.completeSent)
        await queue.retry(item, onConflict: .rename)
        await queue.waitUntilIdle()
        #expect(item.phase == .done)
        #expect(!item.completeSent)
    }

    @Test func cancelDuringCompleteShowsTheRealOutcome() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let queue = makeQueue(client)
        pak.dropCompleteAnswers(1)
        let item = try queue.enqueue(fileURL: try makeFile(size: 10_000), target: .rom(systemId: gbaSystemId, fileName: "J.gba"),
                                     destination: "", ownsFile: false)
        await queue.waitUntilIdle()
        #expect(item.phase == .failed)
        #expect(item.failure as? UploadFailure == .outcomeUnknown)
        #expect(item.completeSent)
        queue.cancel(item)
        try await Task.sleep(for: .milliseconds(200))
        #expect(item.phase == .failed, "never shown cancelled: the file may be on the console")
    }

    @Test func conflictFailsThenReplaceOrKeepBoth() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let queue = makeQueue(client)
        let item = try queue.enqueue(fileURL: try makeFile(size: 1000), target: .rom(systemId: gbaSystemId, fileName: "Pixel Runner.gba"),
                                     destination: "GBA", ownsFile: false)
        await queue.waitUntilIdle()
        #expect(item.phase == .failed)
        #expect((item.failure as? APIError)?.code == .conflict)
        await queue.retry(item, onConflict: .rename)
        await queue.waitUntilIdle()
        #expect(item.phase == .done)
        #expect(item.onConflict == .rename)
        #expect(item.result?.gameId == DemoLibrary.id("Game Boy Advance (GBA)/Pixel Runner (2).gba"))
    }

    @Test func cancelStopsAndDeletesOnTheDevice() async throws {
        let pak = makePak()
        pak.chunkSize = 16 * 1024
        pak.responseDelay = 0.02
        let client = try await pairedClient(pak)
        let queue = makeQueue(client)
        let item = try queue.enqueue(fileURL: try makeFile(size: 400_000), target: .rom(systemId: gbaSystemId, fileName: "F.gba"),
                                     destination: "GBA", ownsFile: false)
        while item.sentBytes == 0 { try await Task.sleep(for: .milliseconds(10)) }
        queue.cancel(item)
        // M10: final once the device confirms (cancelling until then).
        #expect(item.phase == .cancelling)
        await queue.waitUntilIdle()
        for _ in 0..<50 where item.phase != .cancelled { try await Task.sleep(for: .milliseconds(20)) }
        #expect(item.phase == .cancelled)
        for _ in 0..<50 where !pak.uploadIds.isEmpty { try await Task.sleep(for: .milliseconds(20)) }
        #expect(pak.uploadIds.isEmpty)
        #expect(try await !client.games(systemId: gbaSystemId).games.contains { $0.fileName == "F.gba" })
        queue.clearFinished()
        #expect(queue.uploads.isEmpty)
    }

    @Test func jobsArePolledUntilTheyEnd() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let queue = makeQueue(client)
        var ended: [String] = []
        queue.onJobFinished = { ended.append($0.id) }
        let accepted = try await client.createDownload(DownloadRequest(url: "https://example.org/a.gba",
                                                                       target: .rom(systemId: gbaSystemId, fileName: nil)))
        let item = queue.track(jobId: accepted.jobId, title: "a.gba")
        #expect(queue.track(jobId: accepted.jobId, title: "again") === item)
        #expect(queue.isBusy)
        for _ in 0..<10 where !item.isFinished { await queue.refreshJobs() }
        #expect(item.job?.state == .succeeded)
        #expect(ended == [accepted.jobId])
        #expect(!queue.isBusy)
    }

    @Test func jobLostWhenThePakRestarts() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let queue = makeQueue(client)
        let accepted = try await client.createDownload(DownloadRequest(url: "https://example.org/a.gba", target: .staging(fileName: nil)))
        let item = queue.track(jobId: accepted.jobId, title: "a.gba")
        pak.restart()
        // The client re-pairs nothing: restart only forgets jobs and nonces.
        await queue.refreshJobs()
        #expect(item.isLost)
        #expect(item.isFinished)
    }

    @Test func cancelAJob() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let queue = makeQueue(client)
        let accepted = try await client.createDownload(DownloadRequest(url: "https://example.org/a.gba", target: .staging(fileName: nil)))
        let item = queue.track(jobId: accepted.jobId, title: "a.gba")
        try await queue.cancel(item)
        #expect(item.job?.state == .cancelled)
        #expect(item.isFinished)
    }

    @Test func rate() {
        var rate = TransferRate(window: 5)
        #expect(rate.bytesPerSecond == nil)
        rate.record(bytes: 0, at: 0)
        rate.record(bytes: 1000, at: 1)
        rate.record(bytes: 3000, at: 2)
        #expect(rate.bytesPerSecond == 1500)
        #expect(rate.secondsRemaining(total: 6000) == 2)
        // Old samples leave the window.
        rate.record(bytes: 13000, at: 7)
        #expect(rate.bytesPerSecond == 2000)
        // Going back (upload started again) resets.
        rate.record(bytes: 0, at: 8)
        #expect(rate.bytesPerSecond == nil)
    }
}

@Suite("Add suggestions and share inbox")
struct AddSuggestionTests {
    let systems = [
        GameSystem(id: "a", label: "Game Boy Advance", folder: "Game Boy Advance (GBA)", tag: "GBA", gameCount: 0),
        GameSystem(id: "b", label: "Game Boy", folder: "01) Game Boy (GB)", tag: "GB", gameCount: 0),
        GameSystem(id: "c", label: "Super Nintendo", folder: "Super Nintendo (SUPA)", tag: "SUPA", gameCount: 0),
    ]

    @Test func kindFromTheExtension() {
        #expect(AddKind.suggested(forFileName: "Hack.BPS") == .patch)
        #expect(AddKind.suggested(forFileName: "a.ips") == .patch)
        #expect(AddKind.suggested(forFileName: "a.ups") == .patch)
        #expect(AddKind.suggested(forFileName: "Game.gba.cht") == .cheats)
        #expect(AddKind.suggested(forFileName: "Game.sav") == .save)
        #expect(AddKind.suggested(forFileName: "Game.gba") == .rom)
        #expect(AddKind.suggested(forFileName: ".ips") == .rom)
        #expect(AddKind.suggested(forFileName: "README") == .rom)
    }

    @Test func systemFromTheExtension() {
        #expect(SystemSuggestion.system(forFileName: "x.GBA", in: systems)?.id == "a")
        #expect(SystemSuggestion.system(forFileName: "x.gbc", in: systems)?.id == "b", "falls back to GB")
        #expect(SystemSuggestion.system(forFileName: "x.smc", in: systems)?.id == "c")
        #expect(SystemSuggestion.system(forFileName: "x.zip", in: systems) == nil)
        #expect(SystemSuggestion.system(forFileName: "x.nes", in: systems) == nil)
    }

    @Test func downloadURLs() {
        #expect(DownloadURL.validated("  https://example.org/a.bps \n")?.absoluteString == "https://example.org/a.bps")
        #expect(DownloadURL.validated("http://192.168.1.10:8000/x") != nil)
        for bad in ["ftp://example.org/a", "file:///etc/hosts", "example.org/a", "javascript:alert(1)", "https://", ""] {
            #expect(DownloadURL.validated(bad) == nil, "\(bad)")
        }
        #expect(DownloadURL.displayName(URL(string: "https://example.org/dir/a.bps")!) == "a.bps")
        #expect(DownloadURL.displayName(URL(string: "https://example.org/")!) == "example.org")
    }

    @Test func inboxRoundTrip() throws {
        let root = FileManager.default.temporaryDirectory.appending(path: "rl-inbox-\(UUID().uuidString)")
        let inbox = ShareInbox(root: root)
        let a = try makeFile(size: 1000, name: "Game.gba")
        let b = try makeFile(size: 10, name: "Game.gba", seed: 3)
        let request = try inbox.write(
            files: [a, b], urls: [URL(string: "https://example.org/x.ips")!, URL(string: "ftp://example.org/y")!]
        )
        #expect(request.files == ["Game.gba", "Game (2).gba"])
        #expect(request.urls == ["https://example.org/x.ips"])
        #expect(inbox.pending() == [request])
        #expect(try Data(contentsOf: inbox.fileURL("Game (2).gba", in: request)) == Data(contentsOf: b))
        // The link of the request.
        let url = ShareInbox.url(for: request)
        #expect(url.absoluteString.hasPrefix("reminislink://add?request="))
        #expect(ShareInbox.requestId(from: url) == request.id)
        #expect(ShareInbox.requestId(from: URL(string: "reminislink://pair?request=\(request.id)")!) == nil)
        inbox.remove(request.id)
        #expect(inbox.pending().isEmpty)
    }

    @Test func inboxIgnoresAndPurgesIncompleteRequests() throws {
        let root = FileManager.default.temporaryDirectory.appending(path: "rl-inbox-\(UUID().uuidString)")
        let inbox = ShareInbox(root: root)
        let partial = root.appending(path: "partial")
        try FileManager.default.createDirectory(at: partial, withIntermediateDirectories: true)
        #expect(inbox.pending().isEmpty)
        let old = try inbox.write(files: [], urls: [URL(string: "https://example.org/a")!])
        inbox.purge(olderThan: 60, now: Date().addingTimeInterval(3 * 3600))
        #expect(inbox.request(id: old.id) == nil)
        #expect(!FileManager.default.fileExists(atPath: partial.path))
    }

    @Test func inboxRejectsTamperedRequests() throws {
        let root = FileManager.default.temporaryDirectory.appending(path: "rl-inbox-\(UUID().uuidString)")
        let inbox = ShareInbox(root: root)
        let good = try inbox.write(files: [try makeFile(size: 10, name: "a.gba")], urls: [])
        for names in [["../../etc/passwd"], ["sub/a.gba"], ["request.json"], [".."]] {
            let id = UUID()
            let tampered = ShareRequest(id: id, files: names, urls: [])
            try FileManager.default.createDirectory(at: inbox.directory(for: id), withIntermediateDirectories: true)
            try ProtocolJSON.encoder().encode(tampered).write(to: inbox.directory(for: id).appending(path: "request.json"))
        }
        // A request stored under another request's directory.
        let other = ShareRequest(id: UUID(), files: [], urls: ["https://example.org/a"])
        try ProtocolJSON.encoder().encode(other).write(to: inbox.directory(for: good.id).appending(path: "../x.json"))
        #expect(inbox.pending().map(\.id) == [good.id])
        let destination = root.appending(path: "moved.gba")
        try inbox.takeFile("a.gba", from: good, to: destination)
        #expect(FileManager.default.fileExists(atPath: destination.path))
        #expect(!FileManager.default.fileExists(atPath: inbox.fileURL("a.gba", in: good).path))
        #expect(throws: CocoaError.self) {
            try inbox.takeFile("../x.json", from: ShareRequest(id: good.id, files: ["../x.json"], urls: []), to: destination)
        }
    }

    /// Regression: on iPhone the container is under `/private/var`; a
    /// partly used request must stay valid with its remaining files. Here
    /// the root goes through the `/tmp` → `/private/tmp` link.
    @Test func partlyTakenRequestUnderALinkedRootStays() throws {
        let root = URL(fileURLWithPath: "/tmp/rl-inbox-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: root) }
        let inbox = ShareInbox(root: root)
        let request = try inbox.write(
            files: [try makeFile(size: 10, name: "a.gba"), try makeFile(size: 20, name: "a.gba", seed: 2)], urls: []
        )
        #expect(request.files == ["a.gba", "a (2).gba"])
        try inbox.takeFile("a.gba", from: request, to: root.appending(path: "moved.gba"))
        #expect(inbox.pending().map(\.id) == [request.id])
        inbox.purge()
        #expect(inbox.request(id: request.id) != nil)
        #expect(inbox.remainingFiles(of: request) == ["a (2).gba"])
        #expect(throws: CocoaError.self) { try inbox.takeFile("a.gba", from: request, to: root.appending(path: "again.gba")) }
    }

    /// Regression: the app stopped while the Patch screen was open, after
    /// it took the only file: the request must not open again.
    @Test func requestWithAllItsFilesTakenIsFinished() throws {
        let root = URL(fileURLWithPath: "/tmp/rl-inbox-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: root) }
        let inbox = ShareInbox(root: root)
        let request = try inbox.write(files: [try makeFile(size: 10, name: "fix.ips")], urls: [])
        let links = try inbox.write(files: [], urls: [URL(string: "https://example.org/a.zip")!])
        try inbox.takeFile("fix.ips", from: request, to: root.appending(path: "moved.ips"))
        #expect(inbox.isUsedUp(request))
        #expect(inbox.pending().map(\.id) == [links.id])
        #expect(inbox.request(id: request.id) == nil)
        inbox.purge()
        #expect(!FileManager.default.fileExists(atPath: inbox.directory(for: request.id).path))
        #expect(FileManager.default.fileExists(atPath: inbox.directory(for: links.id).path))
    }

    /// A share with a ROM and a link: the ROM is queued, then the app is
    /// stopped before the link is sent. The request stays with its link
    /// only, until the link is sent.
    @Test func requestWithALinkLeftStaysUntilTheLinkIsSent() throws {
        let root = URL(fileURLWithPath: "/tmp/rl-inbox-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: root) }
        let inbox = ShareInbox(root: root)
        let link = "https://example.org/b.zip"
        let request = try inbox.write(
            files: [try makeFile(size: 10, name: "a.gba")], urls: [URL(string: "https://example.org/a.zip")!, URL(string: link)!]
        )
        try inbox.takeFile("a.gba", from: request, to: root.appending(path: "moved.gba"))
        try inbox.consumeLink("https://example.org/a.zip", of: request.id)
        inbox.purge()
        let left = try #require(inbox.request(id: request.id))
        #expect(left.pendingURLs == [link])
        #expect(!inbox.isUsedUp(left))
        #expect(throws: CocoaError.self) { try inbox.consumeLink("https://example.org/other", of: request.id) }
        try inbox.consumeLink(link, of: request.id)
        #expect(inbox.pending().isEmpty)
        inbox.purge()
        #expect(!FileManager.default.fileExists(atPath: inbox.directory(for: request.id).path))
    }

    /// The user replaced the offered link by another URL: the shared link
    /// is not sent, so it stays to offer (and is never recorded as sent).
    @Test func onlyTheSubmittedSharedLinkIsConsumed() {
        let request = ShareRequest(files: [], urls: ["https://example.org/a.zip", "https://example.org/b.zip"])
        #expect(request.sharedLink(matching: "https://example.org/b.zip") == "https://example.org/b.zip")
        #expect(request.sharedLink(matching: "https://example.org/other.zip") == nil)
        // a.zip offered, other.zip sent: a.zip stays, after b.zip.
        #expect(ShareRequest.linksLeft(["https://example.org/b.zip"], offered: "https://example.org/a.zip", sent: nil)
            == ["https://example.org/b.zip", "https://example.org/a.zip"])
        // a.zip offered, b.zip typed and sent: b.zip leaves, a.zip stays.
        #expect(ShareRequest.linksLeft(["https://example.org/b.zip"], offered: "https://example.org/a.zip", sent: "https://example.org/b.zip")
            == ["https://example.org/a.zip"])
        // The offered link sent as it is.
        #expect(ShareRequest.linksLeft(["https://example.org/b.zip"], offered: "https://example.org/a.zip", sent: "https://example.org/a.zip")
            == ["https://example.org/b.zip"])
    }

    /// A request.json written before M8 (no `consumedURLs`) is still read.
    @Test func requestWithoutConsumedLinksIsRead() throws {
        let data = Data(#"{"id":"6F9619FF-8B86-D011-B42D-00C04FC964FF","createdAt":"2026-11-19T10:00:00Z","files":[],"urls":["https://example.org/a"]}"#.utf8)
        let request = try ProtocolJSON.decoder().decode(ShareRequest.self, from: data)
        #expect(request.pendingURLs == ["https://example.org/a"])
    }

    @Test func plainNamesFromUniqueNameAreAccepted() {
        #expect(ShareInbox.isPlainName(" (2).gba"))
        #expect(ShareInbox.isPlainName("Game (2).gba"))
        for bad in ["", ".", "..", "request.json", "a/b", "a\\b", "a\u{1}b"] {
            #expect(!ShareInbox.isPlainName(bad), "\(bad)")
        }
    }

    @Test func requestDirectoryAsASymbolicLinkIsRefused() throws {
        let root = URL(fileURLWithPath: "/tmp/rl-inbox-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: root) }
        let inbox = ShareInbox(root: root)
        // A real request elsewhere, linked into the inbox under another id.
        let elsewhere = ShareInbox(root: root.appending(path: "elsewhere"))
        let real = try elsewhere.write(files: [try makeFile(size: 10, name: "a.gba")], urls: [])
        let id = real.id
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        try FileManager.default.createSymbolicLink(at: inbox.directory(for: id), withDestinationURL: elsewhere.directory(for: id))
        #expect(inbox.pending().isEmpty)
        #expect(throws: CocoaError.self) { try inbox.takeFile("a.gba", from: real, to: root.appending(path: "x.gba")) }
    }

    @Test func safeNames() {
        #expect(ShareInbox.safeName("../a/b.gba") == ".._a_b.gba")
        #expect(ShareInbox.safeName("request.json") == "file")
        #expect(ShareInbox.safeName("a\u{1}b") == "ab")
        #expect(ShareInbox.uniqueName("a", taken: ["a", "a (2)"]) == "a (3)")
    }
}
