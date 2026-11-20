import CryptoKit
import Darwin
import Foundation
import ReminisLinkDemo
import Testing
@testable import ReminisLinkKit

/// A file of `size` pseudo-random bytes in a fresh directory.
func makeFile(size: Int, name: String = "Test Game.gba", seed: UInt8 = 7) throws -> URL {
    let directory = FileManager.default.temporaryDirectory.appending(path: "rl-tests-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    let url = directory.appending(path: name)
    var bytes = [UInt8](repeating: 0, count: size)
    var x = UInt32(seed) &* 2_654_435_761
    for i in 0..<size {
        x = x &* 1_103_515_245 &+ 12345
        bytes[i] = UInt8(truncatingIfNeeded: x >> 16)
    }
    try Data(bytes).write(to: url)
    return url
}

func sha256Hex(_ data: Data) -> String {
    SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
}

let gbaSystemId = DemoLibrary.id("Handheld Advance (GBA)")

/// Records the offsets reported by an upload.
final class OffsetLog: @unchecked Sendable {
    private let lock = NSLock()
    private var _values: [Int64] = []
    var values: [Int64] { lock.withLock { _values } }
    func append(_ value: Int64) { lock.withLock { _values.append(value) } }
}

/// Records the waits of a retry policy.
final class SleepLog: @unchecked Sendable {
    private let lock = NSLock()
    private var _values: [Duration] = []
    var values: [Duration] { lock.withLock { _values } }
    func append(_ value: Duration) { lock.withLock { _values.append(value) } }
}

@Suite("Transfers")
struct TransferTests {
    let scratch = FileManager.default.temporaryDirectory.appending(path: "rl-scratch-\(UUID().uuidString)")

    func upload(_ file: URL, name: String = "Test Game.gba", onConflict: OnConflict = .fail, sha: String? = nil,
                retry: RetryPolicy = .immediate) throws -> ResumableUpload {
        let data = try Data(contentsOf: file)
        return ResumableUpload(
            source: UploadSource(fileURL: file, sizeBytes: Int64(data.count), sha256: sha ?? sha256Hex(data)),
            target: .rom(systemId: gbaSystemId, fileName: name), onConflict: onConflict,
            scratchDirectory: scratch, retry: retry
        )
    }

    func gameNames(_ client: APIClient) async throws -> [String] {
        try await client.games(systemId: gbaSystemId).games.map(\.fileName)
    }

    // MARK: Streaming SHA-256

    @Test func streamingDigestMatchesCryptoKit() throws {
        let file = try makeFile(size: 3 * FileDigest.bufferSize + 12345)
        let expected = sha256Hex(try Data(contentsOf: file))
        #expect(try FileDigest.sha256Sync(of: file) == expected)
        #expect(try FileDigest.sha256Sync(of: file, bufferSize: 1000) == expected)
        let empty = try makeFile(size: 0)
        #expect(try FileDigest.sha256Sync(of: empty) == sha256Hex(Data()))
    }

    @Test func chunkFileIsTheExactSlice() throws {
        let file = try makeFile(size: 5_000_000)
        let data = try Data(contentsOf: file)
        let chunk = scratch.appending(path: "c")
        try FileManager.default.createDirectory(at: scratch, withIntermediateDirectories: true)
        try FileDigest.writeChunk(of: file, offset: 1_234_567, length: 2_000_000, to: chunk)
        #expect(try Data(contentsOf: chunk) == data[1_234_567..<3_234_567])
        #expect(throws: UploadFailure.fileChanged) {
            try FileDigest.writeChunk(of: file, offset: 4_000_000, length: 2_000_000, to: chunk)
        }
    }

    /// Hashing a 100 MiB file must not hold it in memory. Method: the
    /// physical footprint of the process (`task_info(TASK_VM_INFO)
    /// .phys_footprint`, the figure iOS uses for its memory limits) is
    /// sampled after every 1 MiB buffer; the peak minus the footprint
    /// before hashing must stay under 50 MiB. The file is written in 1 MiB
    /// pieces, so the test itself never holds it either.
    @Test func streamingDigestOf100MiBStaysUnder50MiB() async throws {
        let directory = FileManager.default.temporaryDirectory.appending(path: "rl-big-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let url = directory.appending(path: "big.bin")
        FileManager.default.createFile(atPath: url.path, contents: nil)
        let handle = try FileHandle(forWritingTo: url)
        var reference = SHA256()
        let piece = Data((0..<(1 << 20)).map { UInt8(truncatingIfNeeded: $0 &* 31) })
        for i in 0..<100 {
            var block = piece
            block[0] = UInt8(i)
            try handle.write(contentsOf: block)
            reference.update(data: block)
        }
        try handle.close()
        let expected = reference.finalize().map { String(format: "%02x", $0) }.joined()

        // The footprint is the whole process's: tests running in parallel
        // allocate too. A spike of theirs can only add to the growth, so
        // the smallest of three runs is the digest's own (a digest holding
        // the file would grow by 100 MiB every time).
        var smallest = Int.max
        for _ in 0..<3 where smallest >= 50 << 20 {
            let baseline = try #require(physicalFootprint())
            let peak = PeakMemory(baseline)
            let digest = try await FileDigest.sha256(of: url, progress: { _ in peak.sample() })
            #expect(digest == expected)
            smallest = min(smallest, Int(peak.value - baseline))
        }
        #expect(smallest < 50 << 20, "memory grew by \(smallest >> 20) MiB")
    }

    // MARK: Uploads against the fake pak

    @Test func uploadInChunksThenComplete() async throws {
        let pak = makePak()
        pak.chunkSize = 64 * 1024
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 300 * 1024 + 17)
        let upload = try upload(file)
        let offsets = OffsetLog()
        let result = try await upload.run(client: client, progress: { offsets.append($0) })
        #expect(result.gameId == DemoLibrary.id("Handheld Advance (GBA)/Test Game.gba"))
        #expect(try await gameNames(client).contains("Test Game.gba"))
        let chunks = pak.chunkLog
        #expect(chunks.count == 5)
        #expect(chunks.map(\.offset) == [0, 65536, 131072, 196608, 262144])
        #expect(chunks.allSatisfy { $0.length <= 65536 })
        #expect(chunks.allSatisfy { $0.contentSHA256 == "UNSIGNED" })
        #expect(Set(chunks.map(\.nonce)).count == chunks.count)
        #expect(offsets.values.last == Int64(307_217))
        #expect(pak.uploadIds.isEmpty)
        // The other requests carry the hash of their body.
        let posts = pak.log.filter { $0.method == "POST" && $0.path.hasSuffix("/uploads") }
        #expect(posts.count == 1)
    }

    @Test func resumesAfterAConnectionLostBeforeTheChunkIsStored() async throws {
        let pak = makePak()
        pak.chunkSize = 64 * 1024
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 256 * 1024)
        let upload = try upload(file)
        // Two chunks pass, then the connection drops twice.
        let offsets = OffsetLog()
        let result = try await upload.run(client: client, progress: { offset in
            offsets.append(offset)
            if offset == 128 * 1024 && offsets.values.filter({ $0 == offset }).count == 1 {
                pak.injectChunkFaults([.dropBeforeStoring, .dropBeforeStoring])
            }
        })
        #expect(result.gameId != nil)
        // The status was asked after the failures, and nothing was sent twice.
        #expect(pak.log.contains { $0.method == "GET" && $0.path.contains("/uploads/") })
        #expect(pak.chunkLog.map(\.offset) == [0, 65536, 131072, 196608])
    }

    @Test func resumesAfterALostAnswerWithTheConflictOffset() async throws {
        let pak = makePak()
        pak.chunkSize = 64 * 1024
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 200 * 1024)
        let upload = try upload(file, retry: RetryPolicy(delays: [.zero], sleep: { _ in }))
        // The chunk is stored but its answer is lost, twice in a row: the
        // run gives up, keeping its state.
        pak.injectChunkFaults([.dropAfterStoring, .dropAfterStoring])
        await #expect(throws: APIError.network(.networkConnectionLost)) {
            try await upload.run(client: client)
        }
        #expect(await upload.uploadId != nil)
        // Resuming asks the status first (offset 128 KiB), then goes on.
        let result = try await upload.run(client: client)
        #expect(result.gameId != nil)
        #expect(pak.chunkLog.map(\.offset) == [0, 65536, 131072, 196608])
    }

    @Test func conflictOnAChunkMovesToTheExpectedOffset() async throws {
        let pak = makePak()
        pak.chunkSize = 64 * 1024
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 200 * 1024)
        // No retry delay left: the lost answer is retried at once, and the
        // device answers 409 with details.offset.
        let upload = try upload(file, retry: RetryPolicy(delays: [.zero], sleep: { _ in }))
        pak.injectChunkFaults([.dropAfterStoring])
        let result = try await upload.run(client: client)
        #expect(result.gameId != nil)
        #expect(pak.chunkLog.map(\.offset) == [0, 65536, 131072, 196608])
    }

    @Test func restartedPakForgetsTheUploadWhichIsCreatedAgain() async throws {
        let pak = makePak()
        pak.chunkSize = 64 * 1024
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 256 * 1024)
        let upload = try upload(file)
        let restarted = OffsetLog()
        let result = try await upload.run(client: client, progress: { offset in
            if offset == 128 * 1024 && restarted.values.isEmpty {
                restarted.append(offset)
                pak.restart()
            }
        })
        #expect(result.gameId != nil)
        let creates = pak.log.filter { $0.method == "POST" && $0.path.hasSuffix("/uploads") && $0.status == 201 }
        #expect(creates.count == 2)
        #expect(pak.chunkLog.map(\.offset) == [0, 65536, 0, 65536, 131072, 196608])
    }

    /// `429 device_busy` on `complete`: the run waits the `Retry-After` of
    /// the pak when it is longer than its own delay, then sends it again.
    @Test func aBusyCompleteWaitsTheRetryAfterOfThePak() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 10 * 1024)
        let waits = SleepLog()
        let upload = try upload(file, retry: RetryPolicy(delays: [.milliseconds(500)], sleep: { waits.append($0) }))
        pak.busyNextWrites(1, retryAfter: 9)
        let result = try await upload.run(client: client)
        #expect(result.gameId != nil)
        #expect(pak.busyRefusals == 1)
        #expect(waits.values == [.seconds(9)])
    }

    /// A `complete` refused as busy wrote nothing: if the pak then forgets
    /// the upload, it is created again; the outcome is never "unknown".
    @Test func aBusyCompleteNeverMakesTheOutcomeUnknown() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 10 * 1024)
        let upload = try upload(file, retry: RetryPolicy(delays: [], sleep: { _ in }))
        pak.busyNextWrites(1)
        do {
            _ = try await upload.run(client: client)
            Issue.record("expected device busy")
        } catch let error as APIError {
            #expect(error.isDeviceBusy)
            #expect(error.code == .deviceBusy)
        }
        pak.restart()
        let result = try await upload.run(client: client)
        #expect(result.gameId != nil)
        let creates = pak.log.filter { $0.method == "POST" && $0.path.hasSuffix("/uploads") && $0.status == 201 }
        #expect(creates.count == 2)
    }

    /// Regression: the "complete was sent" state must survive across runs.
    /// The answer of `complete` is lost, the run gives up; the next runs
    /// must report an unknown outcome, never create the upload again.
    @Test func outcomeUnknownLostAcrossRuns() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 10_000)
        let upload = try upload(file, retry: RetryPolicy(delays: [], sleep: { _ in }))
        pak.dropCompleteAnswers(1)
        await #expect(throws: APIError.network(.networkConnectionLost)) { try await upload.run(client: client) }
        #expect(await upload.completeSent)
        await #expect(throws: UploadFailure.outcomeUnknown) { try await upload.run(client: client) }
        await #expect(throws: UploadFailure.outcomeUnknown) { try await upload.run(client: client) }
        let names = try await gameNames(client)
        #expect(names.filter { $0.hasPrefix("Test Game") } == ["Test Game.gba"])
        #expect(pak.log.filter { $0.method == "POST" && $0.path.hasSuffix("/uploads") }.count == 1)
        // Cancelling now cannot claim nothing was written.
        #expect(await upload.cancel(client: client) == .outcomeUnknown)
    }

    @Test func lostCompleteAnswerWithinOneRunIsAnUnknownOutcome() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let upload = try upload(try makeFile(size: 10_000))
        pak.dropCompleteAnswers(1)
        await #expect(throws: UploadFailure.outcomeUnknown) { try await upload.run(client: client) }
        #expect(pak.log.filter { $0.method == "POST" && $0.path.hasSuffix("/uploads") }.count == 1)
    }

    @Test func definiteRefusalOfCompleteClearsTheDoubt() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let upload = try upload(try makeFile(size: 10_000), sha: String(repeating: "0", count: 64))
        await #expect(throws: APIError.self) { try await upload.run(client: client) }
        #expect(await !upload.completeSent)
    }

    @Test func sizeMismatchAfterCompleteIsAnUnknownOutcome() async throws {
        // The answer of `complete` is lost; the status then reports another
        // size (a different upload under that id): never start over.
        let pak = makePak()
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 10_000)
        let upload = try upload(file, retry: RetryPolicy(delays: [], sleep: { _ in }))
        pak.loseCompleteRequests(1)
        await #expect(throws: APIError.self) { try await upload.run(client: client) }
        #expect(await upload.completeSent)
        let id = try #require(await upload.uploadId)
        pak.enqueue(.json(200, ["uploadId": id, "offset": 0, "sizeBytes": 1]))
        await #expect(throws: UploadFailure.outcomeUnknown) { try await upload.run(client: client) }
        #expect(pak.log.filter { $0.method == "POST" && $0.path.hasSuffix("/uploads") }.count == 1)
    }

    @Test func hashingStopsOnPause() throws {
        let file = try makeFile(size: 3 * FileDigest.bufferSize)
        #expect(throws: UploadFailure.paused) { try FileDigest.sha256Sync(of: file, shouldPause: { true }) }
    }

    @Test func checksumMismatchIsReportedAndForgotten() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 10_000)
        let upload = try upload(file, sha: String(repeating: "0", count: 64))
        await #expect(throws: APIError.self) { try await upload.run(client: client) }
        #expect(await upload.uploadId == nil)
        #expect(pak.log.last?.status == 409)
        #expect(try await !gameNames(client).contains("Test Game.gba"))
    }

    @Test func insufficientStorage() async throws {
        let pak = makePak()
        pak.freeBytes = 1000
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 10_000)
        do {
            _ = try await upload(file).run(client: client)
            Issue.record("no error")
        } catch let APIError.server(error) {
            #expect(error.status == 507)
            #expect(error.code == .insufficientStorage)
            #expect(error.missingBytes == Int64(9500))
        }
    }

    @Test func existingNameFailsThenKeepsBoth() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 10_000)
        let upload = try upload(file, name: "Brambleway Dash.gba")
        do {
            _ = try await upload.run(client: client)
            Issue.record("no error")
        } catch let APIError.server(error) {
            #expect(error.code == .conflict)
            #expect(error.expectedOffset == nil)
        }
        await upload.restart(onConflict: .rename)
        let result = try await upload.run(client: client)
        #expect(result.gameId == DemoLibrary.id("Handheld Advance (GBA)/Brambleway Dash (2).gba"))
    }

    @Test func overwriteSendsTheOldFileToTheTrash() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 10_000)
        _ = try await upload(file, name: "Brambleway Dash.gba", onConflict: .overwrite).run(client: client)
        let trash = try await client.trash()
        #expect(trash.entries.first?.reason == "overwrite")
        #expect(trash.entries.first?.label == "Brambleway Dash")
    }

    @Test func pauseBetweenChunksThenResume() async throws {
        let pak = makePak()
        pak.chunkSize = 64 * 1024
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 256 * 1024)
        let upload = try upload(file)
        let flag = OffsetLog()
        await #expect(throws: UploadFailure.paused) {
            try await upload.run(client: client, shouldPause: { !flag.values.isEmpty }) { offset in
                if offset >= 64 * 1024 { flag.append(offset) }
            }
        }
        #expect(await upload.offset == 64 * 1024)
        let result = try await upload.run(client: client)
        #expect(result.gameId != nil)
        #expect(pak.chunkLog.map(\.offset) == [0, 65536, 131072, 196608])
    }

    @Test func shuttingDownIsRetried() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 10_000)
        pak.injectChunkFaults([.shuttingDown])
        let result = try await upload(file).run(client: client)
        #expect(result.gameId != nil)
    }

    @Test func cancelDeletesTheUploadOnTheDevice() async throws {
        let pak = makePak()
        pak.chunkSize = 64 * 1024
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 256 * 1024)
        let upload = try upload(file)
        await #expect(throws: UploadFailure.paused) {
            try await upload.run(client: client, shouldPause: { true })
        }
        #expect(pak.uploadIds.count == 1)
        await upload.cancel(client: client)
        #expect(pak.uploadIds.isEmpty)
        #expect(pak.log.last?.method == "DELETE")
    }

    @Test func unsignedIsRefusedOutsideUploadChunks() async throws {
        let pak = makePak()
        // A POST signed as UNSIGNED is refused by the (fake) pak.
        let identity = ClientIdentity.generate()
        pak.addClient(identity)
        var request = URLRequest(url: URL(string: "https://\(pak.endpoint.authority)/api/v1/uploads")!)
        request.httpMethod = "POST"
        request.httpBody = Data("{}".utf8)
        let signed = try Signer(identity: identity).sign(request, time: Int64(Date().timeIntervalSince1970), unsignedBody: true)
        let (_, response) = try await pak.makeSession().data(for: signed)
        #expect((response as? HTTPURLResponse)?.statusCode == 401)
    }

    @Test func uploadRequestEncoding() throws {
        let request = CreateUploadRequest(
            target: .rom(systemId: "R0JB", fileName: "a.gba"), sizeBytes: 3, sha256: "ab", onConflict: .rename
        )
        let json = String(decoding: try ProtocolJSON.encoder().encode(request), as: UTF8.self)
        #expect(json == #"{"onConflict":"rename","sha256":"ab","sizeBytes":3,"target":{"fileName":"a.gba","kind":"rom","systemId":"R0JB"}}"#)
        let staging = String(decoding: try ProtocolJSON.encoder().encode(TransferTarget.staging(fileName: nil)), as: UTF8.self)
        #expect(staging == #"{"kind":"staging"}"#)
        let state = String(decoding: try ProtocolJSON.encoder().encode(TransferTarget.state(gameId: "g", slot: 2)), as: UTF8.self)
        #expect(state == #"{"gameId":"g","kind":"state","slot":2}"#)
    }

    // MARK: Delete, trash, downloads, jobs

    @Test func deleteGameMovesEverythingToTheTrash() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let gameId = DemoLibrary.id("Handheld Advance (GBA)/Sky Garden Quest.gba")
        let detail = try await client.game(id: gameId)
        let result = try await client.deleteGame(id: gameId, keepSave: false)
        #expect(result.moved == detail.files.map(\.path))
        #expect(result.movedBytes == detail.totalBytes)
        #expect(pak.log.last?.query == "keepSave=false")
        let trash = try await client.trash()
        #expect(trash.entries.first?.id == result.trashEntryId)
        #expect(trash.entries.first?.files == detail.files.map(\.path))
        await #expect(throws: APIError.self) { try await client.game(id: gameId) }
        let freed = try await client.deleteTrashEntry(id: result.trashEntryId)
        #expect(freed.freedBytes == detail.totalBytes)
    }

    @Test func deleteGameKeepingTheSave() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let gameId = DemoLibrary.id("Handheld Advance (GBA)/Sky Garden Quest.gba")
        let result = try await client.deleteGame(id: gameId, keepSave: true)
        #expect(pak.log.last?.query == "keepSave=true")
        #expect(!result.moved.contains { $0.hasPrefix("Saves/") || $0.contains(".st") })
        #expect(result.moved.contains { $0.hasPrefix("Roms/") })
    }

    /// M8: the preview is the selection the deletion then makes.
    @Test func deletionPreviewIsWhatTheDeletionMoves() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let gameId = DemoLibrary.id("Handheld Advance (GBA)/Sky Garden Quest.gba")
        let kept = try await client.deletionPreview(gameId: gameId, keepSave: true)
        #expect(pak.log.last?.query == "keepSave=true")
        #expect(kept.kept.contains { $0.role == .save } && kept.kept.contains { $0.role == .state })
        #expect(!kept.moved.contains { $0.role == .save || $0.role == .state })
        #expect(kept.moved.contains { $0.name == "Sky Garden Quest.gba" && $0.role == .rom })
        let preview = try await client.deletionPreview(gameId: gameId, keepSave: false)
        #expect(preview.kept.isEmpty)
        #expect(preview.movedBytes == preview.moved.reduce(0) { $0 + $1.size })
        let result = try await client.deleteGame(id: gameId, keepSave: false)
        #expect(result.movedBytes == preview.movedBytes)
        #expect(result.moved.count == preview.moved.count)
        await #expect(throws: APIError.self) { try await client.deletionPreview(gameId: gameId, keepSave: false) }
    }

    /// The Keep option is offered when only a lock or a resume history is
    /// left (no save, no state): the pak keeps them with `keepSave`.
    @Test func keepSaveIsOfferedForTheCopiesOfThePakToo() {
        let rom = DeletionItem(name: "a.gba", role: .rom, size: 1)
        let lock = DeletionItem(name: "ReminisLink/locks/GBA/a.gba.st1", role: .other, size: 1)
        let bak = DeletionItem(name: "Cheats/GBA/a.gba.cht.bak", role: .other, size: 1)
        #expect(DeletionPreview(moved: [rom, lock], kept: [], movedBytes: 2).hasDataKeptWithSave(keepSave: false))
        #expect(!DeletionPreview(moved: [rom, bak], kept: [], movedBytes: 2).hasDataKeptWithSave(keepSave: false))
        #expect(DeletionPreview(moved: [rom], kept: [lock], movedBytes: 1).hasDataKeptWithSave(keepSave: true))
        for role in [GameFileRole.save, .rtc, .state] {
            #expect(DeletionPreview(moved: [rom, DeletionItem(name: "x", role: role, size: 1)], kept: [], movedBytes: 2)
                .hasDataKeptWithSave(keepSave: false))
        }
    }

    @Test func deletionPreviewDecodesTheContractExample() throws {
        let data = Data(#"{"moved":[{"name":"Sky Garden Quest.gba","role":"rom","size":16777216},{"name":"ReminisLink/locks/x","role":"other","size":10}],"kept":[{"name":"Discs/CD1.chd","role":"rom","size":5}],"movedBytes":16777226}"#.utf8)
        let preview = try ProtocolJSON.decoder().decode(DeletionPreview.self, from: data)
        #expect(preview.moved.last?.role == .other)
        #expect(preview.moved.map(DeletionPreview.isPakCopy) == [false, true])
        #expect(!DeletionPreview.isPakCopy(DeletionItem(name: "ReminisLink/x", role: .rom, size: 1)))
        #expect(preview.kept == [DeletionItem(name: "Discs/CD1.chd", role: .rom, size: 5)])
        #expect(preview.movedBytes == 16_777_226)
    }

    @Test func jobDecodingIsLenient() throws {
        let job = try ProtocolJSON.decoder().decode(Job.self, from: Data("""
        {"id":"job_1","kind":"future","state":"paused","progress":{"done":5,"total":null},
         "createdAt":"2026-11-18T17:00:00Z","updatedAt":"2026-11-18T17:00:12Z","result":null,"error":null,"x":1}
        """.utf8))
        #expect(job.kind.rawValue == "future")
        #expect(job.state.isFinished, "an unknown state must not be polled forever")
        #expect(job.progress.fraction == nil)
    }
}

// MARK: - Memory measurement

/// `phys_footprint` of this process, in bytes.
func physicalFootprint() -> Int64? {
    var info = task_vm_info_data_t()
    var count = mach_msg_type_number_t(MemoryLayout<task_vm_info_data_t>.size / MemoryLayout<natural_t>.size)
    let result = withUnsafeMutablePointer(to: &info) {
        $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
            task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), $0, &count)
        }
    }
    return result == KERN_SUCCESS ? Int64(info.phys_footprint) : nil
}

final class PeakMemory: @unchecked Sendable {
    private let lock = NSLock()
    private var peak: Int64

    init(_ start: Int64) { peak = start }

    var value: Int64 { lock.withLock { peak } }

    func sample() {
        guard let now = physicalFootprint() else { return }
        lock.withLock { peak = max(peak, now) }
    }
}
