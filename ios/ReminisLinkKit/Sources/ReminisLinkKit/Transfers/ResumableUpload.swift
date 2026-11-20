import Foundation

/// Why an upload stopped, besides the errors of ``APIClient``.
public enum UploadFailure: Error, Equatable, Sendable {
    /// The connection was lost after `complete` was sent and the device no
    /// longer knows the upload: it may or may not have been written.
    case outcomeUnknown
    /// Stopped between two chunks on request (the app is going to the
    /// background). ``ResumableUpload/run(client:shouldPause:progress:)``
    /// resumes it.
    case paused
    /// The local file is shorter than when it was hashed.
    case fileChanged
    /// The device keeps forgetting the upload or rejecting the offset.
    case tooManyRestarts
    /// A cancel got no definite answer (lost connection, error of the
    /// device): the upload may still be on the device, it is kept and can
    /// be resumed or cancelled again.
    case cancelNotConfirmed
}

/// The file to send, already hashed.
public struct UploadSource: Sendable, Equatable {
    public let fileURL: URL
    public let sizeBytes: Int64
    public let sha256: String

    public init(fileURL: URL, sizeBytes: Int64, sha256: String) {
        self.fileURL = fileURL
        self.sizeBytes = sizeBytes
        self.sha256 = sha256
    }
}

/// How network failures are retried inside one run.
public struct RetryPolicy: Sendable {
    /// Delays before each retry; the run fails once they are used up.
    public var delays: [Duration]
    public var sleep: @Sendable (Duration) async throws -> Void

    public init(delays: [Duration] = [.milliseconds(500), .seconds(1), .seconds(2), .seconds(4)],
                sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) {
        self.delays = delays
        self.sleep = sleep
    }

    /// No wait: for tests.
    public static let immediate = RetryPolicy(sleep: { _ in })
}

/// Progress of the chunk being sent, between two confirmed offsets.
///
/// `started` is awaited before each attempt to send a chunk; `sent` then
/// reports the body bytes of that attempt handed to the network so far
/// (from the URLSession delegate queue, not awaited: a late report carries
/// its attempt number, to be ignored once the chunk ended). The offset
/// confirmed by the device still comes through `progress`, after every
/// attempt, successful or not.
public struct ChunkProgress: Sendable {
    public var started: @Sendable (_ attempt: Int, _ offset: Int64, _ length: Int64) async -> Void
    public var sent: @Sendable (_ attempt: Int, _ bytes: Int64) -> Void

    public init(
        started: @escaping @Sendable (_ attempt: Int, _ offset: Int64, _ length: Int64) async -> Void,
        sent: @escaping @Sendable (_ attempt: Int, _ bytes: Int64) -> Void
    ) {
        self.started = started
        self.sent = sent
    }
}

/// One chunked, resumable upload (docs/specs/03-protocole.md §7).
///
/// - Chunks of the size given by the device are copied to a scratch file
///   and streamed from it: the file is never loaded in memory.
/// - After a network error, the run waits, asks `GET /uploads/{id}` for
///   the offset received, and goes on from there. A `409 conflict` on a
///   chunk carries the expected offset (`details.offset`): the run goes on
///   from it.
/// - The pak keeps uploads in progress across its restarts (the offset
///   given by `GET /uploads/{id}` is recomputed from the bytes written). An
///   upload it no longer knows (`404`: purged after 24 h idle, or lost) is
///   created again from the start, unless `complete` was already sent.
/// - Once `complete` is sent, the outcome is only known from its answer:
///   if the answer is lost and the device then no longer knows the upload,
///   the file may or may not have been written. The run then fails with
///   ``UploadFailure/outcomeUnknown`` and never sends the file again by
///   itself (this state survives across runs).
/// - Every request is a new signed request (new nonce): a nonce is consumed
///   even when the request fails after the signature.
///
/// A failed run keeps its state: calling ``run(client:shouldPause:progress:)``
/// again resumes the upload.
public actor ResumableUpload {
    public static let maxChunkSize: Int64 = 16 << 20
    public static let defaultChunkSize: Int64 = 4 << 20

    public let source: UploadSource
    public private(set) var target: TransferTarget
    public private(set) var onConflict: OnConflict
    public private(set) var uploadId: String?
    /// Bytes confirmed by the device.
    public private(set) var offset: Int64 = 0
    public private(set) var chunkSize: Int64 = ResumableUpload.defaultChunkSize
    /// `complete` was sent and no definite answer came back yet.
    public private(set) var completeSent = false
    /// The id of the upload once completed: what `POST /cheats/import`
    /// takes (`source.type = "upload"`) for a staging upload.
    public private(set) var completedUploadId: String?

    private let scratchDirectory: URL
    private let retry: RetryPolicy
    private var needsSync = false
    /// Numbers the attempts to send a chunk (``ChunkProgress``).
    private var chunkAttempts = 0

    public init(source: UploadSource, target: TransferTarget, onConflict: OnConflict = .fail,
                scratchDirectory: URL, retry: RetryPolicy = RetryPolicy()) {
        self.source = source
        self.target = target
        self.onConflict = onConflict
        self.scratchDirectory = scratchDirectory
        self.retry = retry
    }

    /// Changes the conflict rule (and the target name). The rule is fixed
    /// when the upload is created, so the upload starts again; returns the
    /// id of the previous upload, for the caller to cancel it.
    @discardableResult
    public func restart(onConflict: OnConflict, target: TransferTarget? = nil) -> String? {
        let previous = uploadId
        self.onConflict = onConflict
        if let target { self.target = target }
        uploadId = nil
        offset = 0
        needsSync = false
        completeSent = false
        return previous
    }

    /// Sends what is left, then completes the upload.
    ///
    /// - Parameters:
    ///   - shouldPause: checked between chunks; true stops the run with
    ///     ``UploadFailure/paused``.
    ///   - progress: the offset confirmed by the device, after every change
    ///     and after every chunk attempt.
    ///   - chunkProgress: the bytes of the chunk being sent, before the
    ///     device confirms them.
    ///   - willComplete: called just before `complete` is sent.
    public func run(
        client: APIClient,
        shouldPause: @escaping @Sendable () -> Bool = { false },
        progress: @escaping @Sendable (Int64) async -> Void = { _ in },
        chunkProgress: ChunkProgress? = nil,
        willComplete: @escaping @Sendable () async -> Void = {}
    ) async throws -> UploadResult {
        var failures = 0
        var restarts = 0
        var conflicts = 0
        // A previous run sent `complete` and the device forgot the upload.
        if completeSent && uploadId == nil { throw UploadFailure.outcomeUnknown }
        if uploadId != nil { needsSync = true }
        try FileManager.default.createDirectory(at: scratchDirectory, withIntermediateDirectories: true)
        let chunkFile = scratchDirectory.appending(path: "chunk-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: chunkFile) }

        while true {
            try Task.checkCancellation()
            do {
                if uploadId == nil {
                    let created = try await client.createUpload(CreateUploadRequest(
                        target: target, sizeBytes: source.sizeBytes, sha256: source.sha256, onConflict: onConflict
                    ))
                    uploadId = created.uploadId
                    chunkSize = min(max(created.chunkSize, 1), Self.maxChunkSize)
                    offset = min(max(created.offset, 0), source.sizeBytes)
                    needsSync = false
                    await progress(offset)
                } else if needsSync, let id = uploadId {
                    let status = try await client.uploadStatus(id: id)
                    needsSync = false
                    guard status.sizeBytes == source.sizeBytes, (0...source.sizeBytes).contains(status.offset) else {
                        // Not the upload we know. After `complete` was sent
                        // the file may be written: never start over then.
                        if completeSent { uploadId = nil; throw UploadFailure.outcomeUnknown }
                        try forget(&restarts)
                        continue
                    }
                    offset = status.offset
                    await progress(offset)
                }
                guard let id = uploadId else { continue }

                while offset < source.sizeBytes {
                    if shouldPause() { throw UploadFailure.paused }
                    try Task.checkCancellation()
                    let length = min(chunkSize, source.sizeBytes - offset)
                    try FileDigest.writeChunk(of: source.fileURL, offset: offset, length: length, to: chunkFile)
                    chunkAttempts += 1
                    let attempt = chunkAttempts
                    await chunkProgress?.started(attempt, offset, length)
                    var onSent: (@Sendable (Int64) -> Void)?
                    if let observer = chunkProgress?.sent {
                        onSent = { bytes in observer(attempt, min(bytes, length)) }
                    }
                    do {
                        let next = try await client.sendChunk(uploadId: id, offset: offset, file: chunkFile, length: length, onSent: onSent)
                        offset = min(max(next, 0), source.sizeBytes)
                    } catch APIError.server(let error) where error.expectedOffset != nil {
                        // The device has another offset (e.g. a chunk whose
                        // answer was lost was stored after all).
                        conflicts += 1
                        guard conflicts <= 8, let expected = error.expectedOffset,
                              (0...source.sizeBytes).contains(expected)
                        else { throw UploadFailure.tooManyRestarts }
                        offset = expected
                    } catch {
                        // The chunk ended without confirmation: only the
                        // confirmed offset counts again.
                        await progress(offset)
                        throw error
                    }
                    failures = 0
                    await progress(offset)
                }

                if shouldPause() { throw UploadFailure.paused }
                await willComplete()
                let sentBefore = completeSent
                completeSent = true
                let result: UploadResult
                do {
                    result = try await client.completeUpload(id: id)
                } catch let error as APIError {
                    // A definite refusal (conflict, checksum, or an upload
                    // unknown to a first `complete`): nothing was written.
                    // A lost answer keeps the doubt.
                    if case .server(let server) = error, !Self.isTransient(error),
                       server.code != .notFound || !sentBefore {
                        completeSent = false
                    }
                    // M13: refused before it started (another write ran
                    // on the console for 10 s): nothing was written, it is
                    // sent again after `Retry-After`.
                    if error.isDeviceBusy { completeSent = sentBefore }
                    throw error
                }
                completeSent = false
                completedUploadId = id
                uploadId = nil
                return result
            } catch let error as APIError where Self.isTransient(error) {
                failures += 1
                guard failures <= retry.delays.count else { needsSync = uploadId != nil; throw error }
                var delay = retry.delays[failures - 1]
                if case .server(let server) = error, let seconds = server.retryAfter {
                    delay = max(delay, .seconds(seconds))
                }
                try await retry.sleep(delay)
                needsSync = uploadId != nil
            } catch let error as APIError {
                switch error {
                case .server(let server) where server.code == .notFound && uploadId != nil:
                    // The device lost the upload (purged, or lost).
                    if completeSent { uploadId = nil; throw UploadFailure.outcomeUnknown }
                    try forget(&restarts)
                case .server(let server) where server.code == .checksumMismatch:
                    // The device deleted the upload.
                    uploadId = nil
                    offset = 0
                    throw error
                default:
                    throw error
                }
            }
        }
    }

    /// What a cancellation achieved.
    public enum CancelOutcome: Sendable, Equatable {
        /// Nothing written on the device.
        case cancelled
        /// `complete` had been sent and the device no longer knows the
        /// upload: the file may have been written.
        case outcomeUnknown
        /// M10: the device refused (`401 disconnected`).
        /// Nothing changed there; the upload is kept and can resume.
        case refused(APIError)
        /// No definite answer (lost connection, timeout, `5xx`...): the
        /// upload may still be on the device; it is kept (resume, or cancel
        /// again). The error, when it came from the client.
        case notConfirmed(APIError?)
    }

    /// Cancels the upload on the device and forgets it, but only once the
    /// device confirmed: `2xx`, or `404` (already gone) before `complete`
    /// was sent. Otherwise the upload is kept, to resume or cancel again.
    @discardableResult
    public func cancel(client: APIClient) async -> CancelOutcome {
        guard let id = uploadId else { return completeSent ? .outcomeUnknown : .cancelled }
        let outcome: CancelOutcome
        do {
            try await client.cancelUpload(id: id)
            outcome = .cancelled
        } catch APIError.server(let error) where error.code == .notFound {
            // Already gone: before `complete`, nothing was written; after
            // it, the file may have been.
            outcome = completeSent ? .outcomeUnknown : .cancelled
        } catch let error as APIError where error.isDisconnected {
            return .refused(error)
        } catch {
            return .notConfirmed(error as? APIError)
        }
        uploadId = nil
        offset = 0
        if outcome == .cancelled { completeSent = false }
        return outcome
    }

    private func forget(_ restarts: inout Int) throws {
        restarts += 1
        guard restarts <= 3 else { throw UploadFailure.tooManyRestarts }
        uploadId = nil
        offset = 0
        needsSync = false
    }
}

extension ResumableUpload {
    /// Errors retried in the same run: the connection, or a busy device.
    static func isTransient(_ error: APIError) -> Bool {
        switch error {
        case .network: true
        case .server(let server): server.code == .tooManyAttempts || server.code == .shuttingDown
        default: false
        }
    }
}
