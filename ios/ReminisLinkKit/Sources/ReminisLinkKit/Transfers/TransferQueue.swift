import Foundation
import Observation

/// Extra running time when the app goes to the background
/// (`UIApplication.beginBackgroundTask` in the app; nothing in tests).
@MainActor
public protocol BackgroundActivity: AnyObject {
    /// Starts an activity. `onExpiration` is called if the system ends it;
    /// the activity is then over.
    func begin(name: String, onExpiration: @escaping @MainActor () -> Void) -> Int
    /// Ends an activity (idempotent).
    func end(_ token: Int)
}

/// No background time (tests, previews).
@MainActor
public final class NoBackgroundActivity: BackgroundActivity {
    public init() {}
    public func begin(name: String, onExpiration: @escaping @MainActor () -> Void) -> Int { 0 }
    public func end(_ token: Int) {}
}

/// A thread-safe flag, read by an upload between two chunks.
final class PauseFlag: @unchecked Sendable {
    private let lock = NSLock()
    private var value = false
    var isSet: Bool { lock.withLock { value } }
    func set() { lock.withLock { value = true } }
    func clear() { lock.withLock { value = false } }
}

/// One upload of the queue.
@MainActor
@Observable
public final class UploadItem: Identifiable {
    public enum Phase: Equatable, Sendable {
        /// Computing the SHA-256 of the file.
        case hashing
        /// Waiting for its turn.
        case waiting
        case uploading
        /// Stopped (app in the background, connection lost): resumes at
        /// the offset confirmed by the device.
        case paused
        case failed
        case done
        case cancelled

        public var isFinished: Bool { self == .done || self == .cancelled }
    }

    public let id = UUID()
    public let fileName: String
    public let sizeBytes: Int64
    /// Where it goes, for display (e.g. the system name).
    public let destination: String
    public private(set) var target: TransferTarget
    public private(set) var onConflict: OnConflict
    public internal(set) var phase: Phase = .waiting
    public internal(set) var hashedBytes: Int64 = 0
    /// Bytes confirmed by the device.
    public internal(set) var sentBytes: Int64 = 0
    /// Bytes of the chunk being sent, not confirmed yet (0 between chunks).
    public internal(set) var inFlightBytes: Int64 = 0
    /// The chunk attempt `inFlightBytes` belongs to, and its length.
    private var inFlight: (attempt: Int, length: Int64)?
    public internal(set) var bytesPerSecond: Double?
    public internal(set) var secondsRemaining: TimeInterval?
    /// Why it is paused or failed.
    public internal(set) var failure: (any Error)?
    public internal(set) var result: UploadResult?
    /// `complete` is in flight: the outcome is up to the device now, the
    /// upload can no longer be cancelled.
    public internal(set) var isCompleting = false
    /// `complete` was sent without a definite answer yet.
    public internal(set) var completeSent = false
    /// A cancellation after a lost `complete` answer is checking the device.
    var isCancelling = false

    let fileURL: URL
    /// The file is a copy made for this upload: deleted once finished.
    let ownsFile: Bool
    var sha256: String?
    var upload: ResumableUpload?
    var task: Task<Void, Never>?
    var pauseFlag = PauseFlag()
    @ObservationIgnored var rate = TransferRate()

    init(fileURL: URL, fileName: String, sizeBytes: Int64, destination: String,
         target: TransferTarget, onConflict: OnConflict, ownsFile: Bool) {
        self.fileURL = fileURL
        self.fileName = fileName
        self.sizeBytes = sizeBytes
        self.destination = destination
        self.target = target
        self.onConflict = onConflict
        self.ownsFile = ownsFile
    }

    /// The bytes shown: the confirmed offset, plus the chunk being sent
    /// while it is sent. Once a chunk ends, only what the device confirmed
    /// is shown; while it is sent, the value only grows.
    public var displayedBytes: Int64 {
        min(sizeBytes, sentBytes + (phase == .uploading ? inFlightBytes : 0))
    }

    /// 0...1 of the current step (hashing, then sending).
    public var fractionCompleted: Double {
        guard sizeBytes > 0 else { return phase == .done ? 1 : 0 }
        let bytes = phase == .hashing ? hashedBytes : displayedBytes
        return min(1, Double(bytes) / Double(sizeBytes))
    }

    /// A chunk attempt starts at the confirmed offset.
    func chunkStarted(attempt: Int, length: Int64) {
        inFlight = (attempt, length)
        inFlightBytes = 0
    }

    /// Bytes of a chunk attempt sent so far. Ignored once that attempt
    /// ended (a late report), never going back, at most the chunk length.
    func chunkSent(attempt: Int, bytes: Int64) {
        guard let inFlight, inFlight.attempt == attempt else { return }
        inFlightBytes = min(inFlight.length, max(inFlightBytes, bytes))
    }

    func setConflictRule(_ rule: OnConflict) {
        onConflict = rule
    }

    func report(offset: Int64, at time: TimeInterval) {
        sentBytes = offset
        inFlight = nil
        inFlightBytes = 0
        rate.record(bytes: offset, at: time)
        bytesPerSecond = rate.bytesPerSecond
        secondsRemaining = rate.secondsRemaining(total: sizeBytes)
    }
}

/// A job started by this app and followed on the device.
@MainActor
@Observable
public final class JobItem: Identifiable {
    public let id: String
    /// What it is, for display (the URL, the file name...).
    public let title: String
    /// `download` or `patch`, known before the first answer.
    public let kind: JobKind
    public internal(set) var job: Job?
    /// The last polling error (the job itself goes on).
    public internal(set) var failure: (any Error)?
    /// The device no longer knows the job (the pak was restarted).
    public internal(set) var isLost = false

    init(id: String, title: String, kind: JobKind) {
        self.id = id
        self.title = title
        self.kind = kind
    }

    public var isFinished: Bool { isLost || (job?.state.isFinished ?? false) }
}

/// The uploads and jobs of one console (docs/specs/06-app-ios.md §4.7).
///
/// Uploads run one at a time, in order. Each one is hashed (streamed), then
/// sent by a ``ResumableUpload``. While it runs, a background activity
/// keeps the app alive to finish the current chunk if the app goes to the
/// background; a lost connection pauses the upload, which resumes at the
/// offset confirmed by the device.
@MainActor
@Observable
public final class TransferQueue {
    public private(set) var uploads: [UploadItem] = []
    public private(set) var jobs: [JobItem] = []
    /// The app is in the background: no upload or hashing starts, the
    /// running one stops after its current chunk.
    public private(set) var isSuspended = false

    /// Called on the main actor when an upload is done.
    @ObservationIgnored public var onUploadFinished: ((UploadItem) -> Void)?
    /// Called on the main actor when a job reaches its end.
    @ObservationIgnored public var onJobFinished: ((JobItem) -> Void)?

    @ObservationIgnored private let client: () async throws -> APIClient
    @ObservationIgnored private let scratchDirectory: URL
    @ObservationIgnored private let background: BackgroundActivity
    @ObservationIgnored private let retry: RetryPolicy
    @ObservationIgnored private let clock = ContinuousClock()
    @ObservationIgnored private let start = ContinuousClock.now
    @ObservationIgnored private var worker: Task<Void, Never>?

    /// - Parameter client: a signed client for the console, connecting if
    ///   needed.
    public init(
        scratchDirectory: URL,
        background: BackgroundActivity = NoBackgroundActivity(),
        retry: RetryPolicy = RetryPolicy(),
        client: @escaping () async throws -> APIClient
    ) {
        self.scratchDirectory = scratchDirectory
        self.background = background
        self.retry = retry
        self.client = client
    }

    /// Uploads or jobs still running.
    public var isBusy: Bool {
        uploads.contains { !$0.phase.isFinished && $0.phase != .failed && $0.phase != .paused }
            || jobs.contains { !$0.isFinished }
    }

    // MARK: - Uploads

    /// Adds an upload at the end of the queue.
    /// - Parameter ownsFile: the file is a copy, deleted once the upload is
    ///   done or cancelled.
    @discardableResult
    public func enqueue(
        fileURL: URL, fileName: String? = nil, target: TransferTarget, onConflict: OnConflict = .fail,
        destination: String, ownsFile: Bool
    ) throws -> UploadItem {
        let size = try FileDigest.size(of: fileURL)
        let item = UploadItem(
            fileURL: fileURL, fileName: fileName ?? target.fileName ?? fileURL.lastPathComponent,
            sizeBytes: size, destination: destination, target: target, onConflict: onConflict, ownsFile: ownsFile
        )
        uploads.append(item)
        pump()
        return item
    }

    /// Cancels an upload, on the device too (best effort). Ignored while
    /// `complete` is in flight: its real outcome is shown instead. After a
    /// `complete` whose answer was lost, the upload is only shown cancelled
    /// if the device still had it; otherwise the outcome is unknown.
    public func cancel(_ item: UploadItem) {
        guard !item.phase.isFinished, !item.isCompleting else { return }
        if item.completeSent, let upload = item.upload {
            item.isCancelling = true
            item.task?.cancel()
            let client = client
            Task {
                let outcome: ResumableUpload.CancelOutcome
                if let api = try? await client() {
                    outcome = await upload.cancel(client: api)
                } else {
                    outcome = .outcomeUnknown
                }
                item.isCancelling = false
                if outcome == .cancelled {
                    item.phase = .cancelled
                    item.failure = nil
                    item.completeSent = false
                    self.cleanUp(item)
                } else {
                    item.phase = .failed
                    item.failure = UploadFailure.outcomeUnknown
                }
            }
            return
        }
        item.phase = .cancelled
        item.failure = nil
        item.task?.cancel()
        let upload = item.upload
        let client = client
        Task {
            if let upload, let api = try? await client() { await upload.cancel(client: api) }
        }
        cleanUp(item)
    }

    /// Resumes a paused or failed upload.
    public func resume(_ item: UploadItem) {
        guard item.phase == .paused || item.phase == .failed else { return }
        item.phase = .waiting
        item.failure = nil
        pump()
    }

    /// Starts a failed upload again with another conflict rule ("Keep
    /// Both" is `rename`, "Replace" is `overwrite`).
    public func retry(_ item: UploadItem, onConflict: OnConflict) async {
        guard item.phase == .failed || item.phase == .paused else { return }
        item.setConflictRule(onConflict)
        // An explicit new attempt: the previous `complete` no longer counts.
        item.completeSent = false
        if let upload = item.upload, let previous = await upload.restart(onConflict: onConflict),
           let api = try? await client() {
            try? await api.cancelUpload(id: previous)
        }
        resume(item)
    }

    /// Uploads paused because another client has write access (M10).
    public var pausedForWriteAccess: [UploadItem] {
        uploads.filter { item in
            guard item.phase == .paused, let error = item.failure as? APIError else { return false }
            return error.isReadOnly || error.isDisconnected
        }
    }

    /// Resumes the uploads paused by `423 read_only` or `401 disconnected`
    /// (the write lease is back).
    public func resumePausedForWriteAccess() {
        for item in pausedForWriteAccess { resume(item) }
    }

    /// Resumes every paused upload.
    public func resumePaused() {
        for item in uploads where item.phase == .paused { resume(item) }
    }

    /// The app goes to the background: the running upload stops after its
    /// current chunk (or hashing buffer) and nothing else starts.
    public func suspend() {
        isSuspended = true
        for item in uploads where item.phase == .uploading || item.phase == .hashing { item.pauseFlag.set() }
    }

    /// The app is active again: a running upload that has not stopped yet
    /// goes on, and paused uploads resume.
    public func resumeAfterSuspension() {
        isSuspended = false
        for item in uploads where item.phase == .uploading || item.phase == .hashing { item.pauseFlag.clear() }
        resumePaused()
        pump()
    }

    /// Removes the finished uploads and jobs from the lists.
    public func clearFinished() {
        uploads.removeAll { $0.phase.isFinished }
        jobs.removeAll { $0.isFinished }
    }

    /// Waits until no upload is running (tests).
    public func waitUntilIdle() async {
        while let worker { await worker.value }
    }

    private func pump() {
        guard worker == nil, !isSuspended else { return }
        worker = Task {
            while !isSuspended, let item = uploads.first(where: { $0.phase == .waiting }) {
                let task = Task { await self.process(item) }
                item.task = task
                await task.value
            }
            worker = nil
        }
    }

    func process(_ item: UploadItem) async {
        // Suspended between `pump` picking the item and now: start nothing.
        guard !isSuspended else {
            item.phase = .waiting
            return
        }
        let flag = PauseFlag()
        item.pauseFlag = flag
        let token = background.begin(name: "Upload \(item.fileName)") { [weak self] in
            // Out of background time: stop after the current chunk.
            self?.suspend()
            flag.set()
        }
        defer { background.end(token) }
        do {
            if item.sha256 == nil {
                item.phase = .hashing
                item.hashedBytes = 0
                let sha = try await FileDigest.sha256(of: item.fileURL, shouldPause: { flag.isSet }) { bytes in
                    Task { @MainActor in item.hashedBytes = bytes }
                }
                item.sha256 = sha
            }
            guard !flag.isSet else { throw UploadFailure.paused }
            let upload: ResumableUpload
            if let existing = item.upload {
                upload = existing
            } else {
                upload = ResumableUpload(
                    source: UploadSource(fileURL: item.fileURL, sizeBytes: item.sizeBytes, sha256: item.sha256 ?? ""),
                    target: item.target, onConflict: item.onConflict,
                    scratchDirectory: scratchDirectory, retry: retry
                )
                item.upload = upload
            }
            item.phase = .uploading
            item.rate.reset()
            item.bytesPerSecond = nil
            item.secondsRemaining = nil
            let api = try await client()
            let start = start
            let clock = clock
            let result: UploadResult
            do {
                result = try await upload.run(client: api, shouldPause: { flag.isSet }, progress: { offset in
                    let elapsed = clock.now - start
                    let seconds = Double(elapsed.components.seconds) + Double(elapsed.components.attoseconds) / 1e18
                    await item.report(offset: offset, at: seconds)
                }, chunkProgress: ChunkProgress(
                    started: { attempt, _, length in await item.chunkStarted(attempt: attempt, length: length) },
                    sent: { attempt, bytes in Task { @MainActor in item.chunkSent(attempt: attempt, bytes: bytes) } }
                ), willComplete: {
                    await MainActor.run {
                        item.isCompleting = true
                        item.completeSent = true
                    }
                })
            } catch {
                item.isCompleting = false
                item.completeSent = await upload.completeSent
                throw error
            }
            item.isCompleting = false
            item.completeSent = false
            guard item.phase != .cancelled else { return }
            item.result = result
            item.sentBytes = item.sizeBytes
            item.phase = .done
            item.bytesPerSecond = nil
            item.secondsRemaining = nil
            cleanUp(item)
            onUploadFinished?(item)
        } catch {
            // A cancellation in progress decides the phase itself.
            guard item.phase != .cancelled, !item.isCancelling else { return }
            item.bytesPerSecond = nil
            item.secondsRemaining = nil
            switch error {
            case UploadFailure.paused, is CancellationError:
                // Back in the foreground before the chunk ended: go on.
                item.phase = isSuspended || error is CancellationError ? .paused : .waiting
                item.failure = nil
            case APIError.network:
                item.phase = .paused
                item.failure = error
            case let error as APIError where error.isReadOnly || error.isDisconnected:
                // M10: another client has write access, or the console
                // disconnected this iPhone. Nothing was lost: the upload
                // resumes at the confirmed offset once writing is possible.
                item.phase = .paused
                item.failure = error
            default:
                item.phase = .failed
                item.failure = error
            }
        }
    }

    private func cleanUp(_ item: UploadItem) {
        guard item.ownsFile else { return }
        let url = item.fileURL
        Task.detached(priority: .utility) {
            // The copy lives in a directory of its own.
            try? FileManager.default.removeItem(at: url.deletingLastPathComponent())
        }
    }

    // MARK: - Jobs

    /// Follows a job started on the device.
    @discardableResult
    public func track(jobId: String, title: String, kind: JobKind = .download) -> JobItem {
        if let existing = jobs.first(where: { $0.id == jobId }) { return existing }
        let item = JobItem(id: jobId, title: title, kind: kind)
        jobs.append(item)
        return item
    }

    /// `GET /jobs/{id}` for every job not finished. The screens call it every
    /// second while they are visible (docs/specs/03-protocole.md §8).
    public func refreshJobs() async {
        let pending = jobs.filter { !$0.isFinished }
        guard !pending.isEmpty else { return }
        let api: APIClient
        do {
            api = try await client()
        } catch {
            for item in pending { item.failure = error }
            return
        }
        for item in pending {
            do {
                let job = try await api.job(id: item.id)
                item.job = job
                item.failure = nil
                if job.state.isFinished { onJobFinished?(item) }
            } catch APIError.server(let error) where error.code == .notFound {
                item.isLost = true
                item.failure = APIError.server(error)
            } catch is CancellationError {
                return
            } catch {
                item.failure = error
            }
        }
    }

    /// `DELETE /jobs/{id}`.
    public func cancel(_ item: JobItem) async throws {
        let api = try await client()
        item.job = try await api.cancelJob(id: item.id)
    }
}
