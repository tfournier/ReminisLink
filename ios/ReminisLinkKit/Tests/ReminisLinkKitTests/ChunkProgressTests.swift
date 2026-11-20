import CryptoKit
import Foundation
import ReminisLinkDemo
import Testing
@testable import ReminisLinkKit

/// Smooth upload progress (M8): the bytes of the chunk being sent are
/// reported while it is sent; only the confirmed offset counts once it ends.
@Suite("Chunk progress")
struct ChunkProgressTests {
    /// Thread-safe list of reports.
    final class Reports: @unchecked Sendable {
        private let lock = NSLock()
        private var items: [String] = []
        private var sent: [Int64] = []
        func add(_ item: String) { lock.withLock { items.append(item) } }
        func addSent(_ bytes: Int64) { lock.withLock { sent.append(bytes) } }
        var all: [String] { lock.withLock { items } }
        var sentBytes: [Int64] { lock.withLock { sent } }
    }

    func client(port: UInt16) -> APIClient {
        APIClient(
            scheme: "http", // A plain local test server: no TLS.
            endpoint: Endpoint(host: "127.0.0.1", port: port),
            credentials: DeviceCredentials.random(deviceId: FakePak.demoDeviceId),
            session: APIClient.makeSession()
        )
    }

    /// A real connection reports the bytes sent while the chunk goes out.
    @Test func aChunkReportsItsBytesWhileItIsSent() async throws {
        let length = 4 << 20
        let server = try LocalHTTPServer { request in
            .init(status: 204, headers: ["Upload-Offset": String(request.bodyLength)])
        }
        let port = try await server.start()
        defer { server.stop() }
        let file = FileManager.default.temporaryDirectory.appending(path: "chunk-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: file) }
        try Data(count: length).write(to: file)
        let reports = Reports()
        let next = try await client(port: port).sendChunk(
            uploadId: "up_1", offset: 0, file: file, length: Int64(length), onSent: { reports.addSent($0) }
        )
        #expect(next == Int64(length))
        let sent = reports.sentBytes
        #expect(!sent.isEmpty)
        #expect(sent == sent.sorted())
        #expect(sent.last == Int64(length))
    }

    /// The session reports the bytes sent asynchronously: the delegate
    /// closes them when the request ends, with the whole body, and drops
    /// late or out-of-order callbacks.
    @Test func theDelegateClosesTheReportsWhenTheRequestEnds() {
        let reports = Reports()
        let delegate = PakTaskDelegate(pin: nil, onSent: { reports.addSent($0) })
        let session = URLSession(configuration: .ephemeral)
        defer { session.invalidateAndCancel() }
        let task = session.dataTask(with: URL(string: "http://127.0.0.1:9/")!)
        func send(_ total: Int64) {
            delegate.urlSession(session, task: task, didSendBodyData: 1, totalBytesSent: total, totalBytesExpectedToSend: 10)
        }
        send(3)
        send(2) // out of order
        delegate.finish(totalBytesSent: 10)
        send(7) // late
        delegate.finish(totalBytesSent: 12) // once only
        #expect(reports.sentBytes == [3, 10])
    }

    /// The upload numbers its chunk attempts, reports their bytes, and the
    /// confirmed offset after each attempt, also after a failed one.
    @Test func theUploadReportsTheChunksAndTheConfirmedOffsets() async throws {
        let size = 3 << 20
        let failures = Reports()
        let server = try LocalHTTPServer { request in
            switch (request.method, request.path) {
            case ("POST", "/api/v1/uploads"):
                let body: [String: Any] = ["uploadId": "up_1", "chunkSize": 1 << 20, "offset": 0, "expiresAt": "2026-11-20T00:00:00Z"]
                return .init(status: 201, headers: ["Content-Type": "application/json"],
                             body: try! JSONSerialization.data(withJSONObject: body))
            case ("GET", "/api/v1/uploads/up_1"):
                let body: [String: Any] = ["uploadId": "up_1", "sizeBytes": size, "offset": 1 << 20, "expiresAt": "2026-11-20T00:00:00Z"]
                return .init(status: 200, headers: ["Content-Type": "application/json"],
                             body: try! JSONSerialization.data(withJSONObject: body))
            case ("PUT", "/api/v1/uploads/up_1"):
                let offset = Int(request.headers["upload-offset"] ?? "") ?? 0
                // The second chunk fails once (the device is closing).
                if offset == 1 << 20 && failures.all.isEmpty {
                    failures.add("503")
                    let error = ["error": ["code": "shutting_down", "message": "x", "details": [String: Any]()]]
                    return .init(status: 503, headers: ["Content-Type": "application/json"],
                                 body: try! JSONSerialization.data(withJSONObject: error))
                }
                return .init(status: 204, headers: ["Upload-Offset": String(offset + request.bodyLength)])
            default:
                return .init(status: 200, headers: ["Content-Type": "application/json"], body: Data(#"{"stagingId":"st_1"}"#.utf8))
            }
        }
        let port = try await server.start()
        defer { server.stop() }
        let directory = FileManager.default.temporaryDirectory.appending(path: "up-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: directory) }
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let file = directory.appending(path: "file.bin")
        try Data(count: size).write(to: file)
        let upload = ResumableUpload(
            source: UploadSource(fileURL: file, sizeBytes: Int64(size), sha256: String(repeating: "0", count: 64)),
            target: .staging(fileName: "file.bin"), scratchDirectory: directory, retry: .immediate
        )
        let reports = Reports()
        let sentByAttempt = Reports()
        let result = try await upload.run(
            client: client(port: port),
            progress: { reports.add("confirmed \($0)") },
            chunkProgress: ChunkProgress(
                started: { attempt, offset, length in reports.add("start \(attempt) \(offset) \(length)") },
                sent: { attempt, bytes in sentByAttempt.add("\(attempt) \(bytes)") }
            )
        )
        #expect(result.stagingId == "st_1")
        let mb = 1 << 20
        #expect(reports.all == [
            "confirmed 0",
            "start 1 0 \(mb)", "confirmed \(mb)",
            // Failed: the confirmed offset is reported again, then synced.
            "start 2 \(mb) \(mb)", "confirmed \(mb)", "confirmed \(mb)",
            "start 3 \(mb) \(mb)", "confirmed \(2 * mb)",
            "start 4 \(2 * mb) \(mb)", "confirmed \(3 * mb)",
        ])
        // Every attempt sent reports, never more than its chunk.
        let byAttempt = sentByAttempt.all.map { $0.split(separator: " ").map { Int64($0)! } }
        for attempt in [1, 3, 4] {
            #expect(byAttempt.contains { $0[0] == Int64(attempt) && $0[1] == Int64(mb) })
        }
        #expect(byAttempt.allSatisfy { $0[1] <= Int64(mb) })
    }

    /// What the Transfers screen shows: confirmed bytes plus the chunk in
    /// flight, growing only, capped, and dropped once the chunk ends.
    @MainActor
    @Test func theItemShowsTheChunkInFlightWithoutGoingBack() {
        let item = UploadItem(
            fileURL: FileManager.default.temporaryDirectory.appending(path: "a.bin"), fileName: "a.bin", sizeBytes: 10,
            destination: "", target: .staging(fileName: "a.bin"), onConflict: .rename, ownsFile: false
        )
        item.phase = .uploading
        item.report(offset: 0, at: 0)
        item.chunkStarted(attempt: 1, length: 4)
        item.chunkSent(attempt: 1, bytes: 3)
        #expect(item.displayedBytes == 3)
        item.chunkSent(attempt: 1, bytes: 2) // out of order: never back
        #expect(item.displayedBytes == 3)
        item.chunkSent(attempt: 1, bytes: 9) // never more than the chunk
        #expect(item.displayedBytes == 4)
        item.report(offset: 4, at: 1)
        #expect(item.displayedBytes == 4)
        item.chunkSent(attempt: 1, bytes: 4) // late: ignored
        #expect(item.displayedBytes == 4)
        item.chunkStarted(attempt: 2, length: 4)
        item.chunkSent(attempt: 2, bytes: 3)
        #expect(item.displayedBytes == 7)
        #expect(abs(item.fractionCompleted - 0.7) < 0.0001)
        // The chunk failed: only the confirmed offset is shown.
        item.report(offset: 4, at: 2)
        #expect(item.displayedBytes == 4)
        item.chunkSent(attempt: 2, bytes: 4)
        #expect(item.displayedBytes == 4)
    }
}
