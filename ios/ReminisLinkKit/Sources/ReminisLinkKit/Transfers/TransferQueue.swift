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
    public internal(set) var sentBytes: Int64 = 0
    public internal(set) var bytesPerSecond: Double?
    public internal(set) var secondsRemaining: TimeInterval?
    /// Why it is paused or failed.
    public internal(set) var failure: (any Error)?
    public internal(set) var result: UploadResult?

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

    /// 0...1 of the current step (hashing, then sending).
    public var fractionCompleted: Double {
        guard sizeBytes > 0 else { return phase == .done ? 1 : 0 }
        let bytes = phase == .hashing ? hashedBytes : sentBytes
        return min(1, Double(bytes) / Double(sizeBytes))
    }

    func setConflictRule(_ rule: OnConflict) {
        onConflict = rule
    }

    func report(offset: Int64, at time: TimeInterval) {
        sentBytes = offset
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
    public internal(set) var job: Job?
    /// The last polling error (the job itself goes on).
    public internal(set) var failure: (any Error)?
    /// The device no longer knows the job (the pak was restarted).
    public internal(set) var isLost = false

    init(id: String, title: String) {
        self.id = id
        self.title = title
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

    /// Cancels an upload, on the device too (best effort).
    public func cancel(_ item: UploadItem) {
        guard !item.phase.isFinished else { return }
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
        if let upload = item.upload, let previous = await upload.restart(onConflict: onConflict),
           let api = try? await client() {
            try? await api.cancelUpload(id: previous)
        }
        resume(item)
    }

    /// Resumes every paused upload (the app is active again).
    public func resumePaused() {
        for item in uploads where item.phase == .paused { resume(item) }
    }

    /// The app goes to the background: the running upload stops after its
    /// current chunk.
    public func pauseAfterCurrentChunk() {
        for item in uploads where item.phase == .uploading || item.phase == .hashing { item.pauseFlag.set() }
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
        guard worker == nil else { return }
        worker = Task {
            while let item = uploads.first(where: { $0.phase == .waiting }) {
                let task = Task { await self.process(item) }
                item.task = task
                await task.value
            }
            worker = nil
        }
    }

    private func process(_ item: UploadItem) async {
        let flag = PauseFlag()
        item.pauseFlag = flag
        let token = background.begin(name: "Upload \(item.fileName)") { flag.set() }
        defer { background.end(token) }
        do {
            if item.sha256 == nil {
                item.phase = .hashing
                item.hashedBytes = 0
                let sha = try await FileDigest.sha256(of: item.fileURL) { bytes in
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
            let result = try await upload.run(client: api, shouldPause: { flag.isSet }, progress: { offset in
                let elapsed = clock.now - start
                let seconds = Double(elapsed.components.seconds) + Double(elapsed.components.attoseconds) / 1e18
                await item.report(offset: offset, at: seconds)
            })
            guard item.phase != .cancelled else { return }
            item.result = result
            item.sentBytes = item.sizeBytes
            item.phase = .done
            item.bytesPerSecond = nil
            item.secondsRemaining = nil
            cleanUp(item)
            onUploadFinished?(item)
        } catch {
            guard item.phase != .cancelled else { return }
            item.bytesPerSecond = nil
            item.secondsRemaining = nil
            switch error {
            case UploadFailure.paused, is CancellationError:
                item.phase = .paused
                item.failure = nil
            case APIError.network:
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
    public func track(jobId: String, title: String) -> JobItem {
        if let existing = jobs.first(where: { $0.id == jobId }) { return existing }
        let item = JobItem(id: jobId, title: title)
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
