import Foundation
import Observation
import ReminisLinkKit

/// The game a patch is applied to.
struct PatchGame: Equatable, Hashable {
    let id: String
    let name: String
    /// The ROM file name (gives the suggested name and its extension).
    let fileName: String
    let kind: GameKind
    let systemLabel: String

    /// Why this game cannot be patched (several discs, an archive), or nil.
    var refusal: PatchRefusal? { PatchNaming.refusal(kind: kind, fileName: fileName) }
}

/// A thread-safe flag, read by the patch upload between two chunks.
nonisolated final class PatchPauseFlag: @unchecked Sendable {
    private let lock = NSLock()
    private var value = false
    var isSet: Bool { lock.withLock { value } }
    func set() { lock.withLock { value = true } }
}

/// Applies a patch to a game (docs/specs/06-app-ios.md §4.5, milestone M6).
///
/// The patch comes from Files (sent to the staging area with a resumable
/// upload, then used by its `stagingId`), from a link (downloaded by the
/// console to the staging area in a job, then used by the `stagingId` of
/// the job's result) or from the SD card (`GET /sd/files?type=patch`).
/// `POST /games/{gameId}/patch` then starts a job.
///
/// Jobs go through the transfer queue of the console (they also show on
/// the Transfers screen). The screen calls ``tick()`` every second while
/// it is visible and the app is active (docs/specs/03-protocole.md §8):
/// nothing is polled otherwise.
///
/// A cancellation never leads to a patch: a download cancelled too late
/// ends "downloaded, nothing applied" (the staged patch is kept for an
/// explicit new try); the final state of a cancelled job is always the
/// one the console reports.
@MainActor
@Observable
final class PatchFlow {
    enum Source: Hashable {
        case files
        case link
        case sdCard
    }

    enum Step: Equatable {
        case idle
        /// Computing the SHA-256 of the file.
        case preparing
        /// Sending the file to the staging area (0...1).
        case sending(Double)
        /// The console downloads the patch of the link.
        case downloading
        /// `POST /games/{gameId}/patch` is in flight.
        case starting
        /// The console applies the patch.
        case patching
        /// The download of the link ended although it was cancelled: the
        /// patch is in the staging area, nothing was applied.
        case downloaded
        /// `gameId` of the produced ROM; `late`: the cancellation reached
        /// the console after the patch was in place.
        case succeeded(gameId: String?, late: Bool)
        case failed(AppError)
        /// Confirmed: nothing was applied.
        case cancelled
        /// M10: the cancel of the upload is waiting for the console's
        /// answer. Nothing is final yet (refused, the upload pauses): the
        /// screen and the local copy stay.
        case cancellingUpload

        var isSending: Bool {
            if case .sending = self { return true }
            return false
        }

        /// Something runs: the inputs are locked.
        var isBusy: Bool {
            switch self {
            case .preparing, .sending, .downloading, .starting, .patching, .cancellingUpload: true
            case .idle, .downloaded, .succeeded, .failed, .cancelled: false
            }
        }

        /// The app side runs: closing the screen would stop it.
        var holdsTheScreen: Bool {
            switch self {
            case .preparing, .sending, .downloading, .starting, .cancellingUpload: true
            default: false
            }
        }
    }

    let deviceId: String
    let session: ConsoleSession

    var game: PatchGame? {
        didSet {
            guard game != oldValue else { return }
            fileName = game.map { PatchNaming.suggestedFileName(forROM: $0.fileName) } ?? ""
            inputChanged()
        }
    }
    var source: Source = .files { didSet { inputChanged() } }
    /// The patch chosen in Files (or shared), copied into the app.
    var file: PickedFile? {
        didSet {
            if let oldValue, oldValue.id != file?.id { oldValue.remove() }
            inputChanged()
        }
    }
    var linkText = "" { didSet { inputChanged() } }
    var sdFileId: String? { didSet { inputChanged() } }
    var mode: PatchOutputMode = .newFile { didSet { inputChanged() } }
    /// The name of the new file, suggested as `<name> (patched).<ext>`.
    var fileName = "" { didSet { inputChanged() } }

    private(set) var step: Step = .idle
    /// The job followed now (download of the patch, then the patch).
    private(set) var job: JobItem?
    /// A cancellation was asked and the console has not confirmed it yet.
    private(set) var isCancelling = false
    /// The last polling error of the job (the job itself goes on).
    private(set) var pollError: AppError?
    /// The cancellation could not be sent.
    private(set) var cancelError: AppError?
    /// The ROM the patch expects and the one on the console, after a
    /// `source-mismatch` that carries them.
    private(set) var mismatchDetails: PatchMismatchDetails?
    /// `result.verified` of the patch job: false for IPS.
    private(set) var verified: Bool?
    /// M10: the upload of the patch is paused until the user reconnects
    /// (the console disconnected this app); it then goes on by itself.
    var isWaitingForReconnection: Bool { step.isSending && session.reconnectionWaits > 0 }

    /// The patch already in the staging area, by source.
    @ObservationIgnored private var staged: (key: String, stagingId: String)?
    /// The upload of the file, kept to resume it.
    @ObservationIgnored private var upload: (key: String, upload: ResumableUpload)?
    /// The source the running download is for.
    @ObservationIgnored private var downloadKey: String?
    /// Jobs the user cancelled: their end never leads further.
    @ObservationIgnored private var cancelledJobs: Set<String> = []
    /// A cancellation asked while a request was in flight.
    @ObservationIgnored private var cancelRequested = false
    @ObservationIgnored private var pauseFlag = PatchPauseFlag()
    @ObservationIgnored private var runTask: Task<Void, Never>?
    /// Waits between two polls of a cancelled job (tests shorten it).
    @ObservationIgnored var cancelPollInterval: Duration = .seconds(1)

    init(session: ConsoleSession, game: PatchGame?) {
        self.session = session
        deviceId = session.deviceId
        self.game = game
        fileName = game.map { PatchNaming.suggestedFileName(forROM: $0.fileName) } ?? ""
        // M14: a `job` event moves the flow on at once (no polling while
        // the event stream is live).
        session.transfers.observeJobs { [weak self] item in
            guard let self else { return false }
            self.jobUpdated(item)
            return true
        }
    }

    // MARK: Inputs

    var isPatchable: Bool { game != nil && game?.refusal == nil }

    /// What the typed name becomes (the ROM's extension added if missing).
    var outputName: PatchNaming.OutputName {
        guard let game else { return .invalid }
        return PatchNaming.outputName(fileName, romFileName: game.fileName)
    }

    var validatedLink: URL? { DownloadURL.validated(linkText) }

    /// Something was sent to this console: it can no longer be changed.
    var isBoundToConsole: Bool { step.isBusy || staged != nil || upload != nil }

    /// Identifies the chosen patch, to reuse what is already staged.
    private var sourceKey: String? {
        switch source {
        case .files: file.map { "file:\($0.id)" }
        case .link: validatedLink.map { "link:\($0.absoluteString)" }
        case .sdCard: sdFileId.map { "sd:\($0)" }
        }
    }

    var canStart: Bool {
        guard isPatchable, !step.isBusy, !isCancelling, sourceKey != nil else { return false }
        if mode == .replace { return true }
        if case .valid = outputName { return true }
        return false
    }

    private func inputChanged() {
        switch step {
        case .failed, .cancelled, .downloaded: step = .idle
        default: break
        }
    }

    // MARK: Running

    /// Starts (or resumes) the patch.
    func start() {
        guard canStart else { return }
        pauseFlag = PatchPauseFlag()
        cancelRequested = false
        cancelError = nil
        pollError = nil
        mismatchDetails = nil
        verified = nil
        // Busy at once: the inputs lock before the task starts.
        step = .starting
        runTask = Task { await run() }
    }

    private func run() async {
        guard let key = sourceKey else {
            step = .idle
            return
        }
        job = nil
        do {
            let patch: PatchSource
            if let staged, staged.key == key {
                patch = .staging(stagingId: staged.stagingId)
            } else {
                switch source {
                case .sdCard:
                    guard let sdFileId else {
                        step = .idle
                        return
                    }
                    patch = .sd(sdFileId: sdFileId)
                case .files:
                    patch = .staging(stagingId: try await stageFile(key: key))
                case .link:
                    try await startDownload(key: key)
                    return
                }
            }
            try await startPatch(patch)
        } catch {
            fail(error)
        }
    }

    /// Sends the file to the staging area and returns its `stagingId`. The
    /// local copy is deleted once the console has it.
    private func stageFile(key: String) async throws -> String {
        guard let file else { throw AppError.file }
        if cancelRequested { throw CancellationError() }
        let upload: ResumableUpload
        if let current = self.upload, current.key == key {
            upload = current.upload
        } else {
            step = .preparing
            let sha = try await FileDigest.sha256(of: file.url)
            try Task.checkCancellation()
            upload = ResumableUpload(
                source: UploadSource(fileURL: file.url, sizeBytes: file.sizeBytes, sha256: sha),
                target: .staging(fileName: file.name), onConflict: .rename,
                scratchDirectory: session.app.transferDirectory.appending(path: "chunks-\(deviceId)")
            )
            self.upload = (key, upload)
        }
        step = .sending(0)
        let flag = pauseFlag
        let background = session.app.background
        // Out of background time: stop after the current chunk.
        let token = background.begin(name: "Patch upload") { flag.set() }
        defer { background.end(token) }
        let size = file.sizeBytes
        // M10: refused while the console disconnected this app, the upload
        // waits (kept, never restarted) and goes on once reconnected.
        let session = session
        let result = try await session.pausingForReconnection {
            try await upload.run(client: try await session.connectedClient(), shouldPause: { flag.isSet }, progress: { [weak self] offset in
                await self?.reportSent(offset, of: size)
            })
        }
        guard let stagingId = result.stagingId else { throw AppError.api(.invalidResponse("no stagingId")) }
        staged = (key, stagingId)
        self.upload = nil
        file.remove()
        // Cancelled while the upload ended: go no further.
        try Task.checkCancellation()
        return stagingId
    }

    private func reportSent(_ offset: Int64, of size: Int64) {
        guard case .sending = step else { return }
        step = .sending(size > 0 ? min(1, Double(offset) / Double(size)) : 1)
    }

    /// `POST /downloads` to the staging area; ``tick()`` follows the job.
    private func startDownload(key: String) async throws {
        guard let url = validatedLink else { return }
        guard !cancelRequested else { return finishCancelledRequest() }
        step = .downloading
        let request = DownloadRequest(url: url.absoluteString, target: .staging(fileName: nil))
        let accepted = try await session.perform { try await $0.createDownload(request) }
        downloadKey = key
        let item = session.transfers.track(jobId: accepted.jobId, title: DownloadURL.displayName(url), kind: .download)
        job = item
        // Cancelled while the request was in flight: cancel the job now.
        if cancelRequested { await cancelJob(item) }
    }

    private func startPatch(_ patch: PatchSource) async throws {
        guard let game else { return }
        // Never after a cancellation.
        guard !cancelRequested, step != .cancelled, !Task.isCancelled else { return finishCancelledRequest() }
        step = .starting
        let request: PatchRequest
        switch mode {
        case .replace:
            request = PatchRequest(patch: patch, output: PatchOutput(mode: .replace))
        case .newFile:
            guard case .valid(let name) = outputName else { throw AppError.unexpected }
            request = PatchRequest(patch: patch, output: PatchOutput(mode: .newFile, fileName: name))
        }
        let gameId = game.id
        let accepted = try await session.perform { try await $0.patchGame(gameId: gameId, request) }
        let item = session.transfers.track(jobId: accepted.jobId, title: String(localized: "Patch: \(game.name)"), kind: .patch)
        job = item
        step = .patching
        if cancelRequested { await cancelJob(item) }
    }

    /// A cancellation asked before any job existed: nothing was applied.
    private func finishCancelledRequest() {
        step = .cancelled
        isCancelling = false
    }

    /// Polls the job followed now and moves on when it ends. Called every
    /// second by the screen while it is visible and the app is active;
    /// paused while a cancellation is in progress.
    func tick() async {
        guard !isCancelling, let job, step == .downloading || step == .patching else { return }
        let polled = step
        if !job.isFinished {
            await session.transfers.refreshJobs()
            // Another poll ("Try Again" and the timer) may have moved on
            // meanwhile: this one is stale then.
            guard self.job === job, step == polled, !isCancelling else { return }
            pollError = !job.isLost ? job.failure.map(AppError.init) : nil
        }
        // Cancelled elsewhere (the Transfers screen): never go further.
        if job.job?.cancelRequested == true { cancelledJobs.insert(job.id) }
        advance()
    }

    /// A job changed (an event, or a poll of another screen): the step
    /// that follows it moves on as ``tick()`` would.
    private func jobUpdated(_ item: JobItem) {
        guard !isCancelling, item === job, step == .downloading || step == .patching else { return }
        pollError = nil
        if item.job?.cancelRequested == true { cancelledJobs.insert(item.id) }
        advance()
    }

    private func advance() {
        guard let item = job, item.isFinished else { return }
        // Only the step that follows this job moves on, once: a second
        // poll of the same finished job changes nothing (e.g. while the
        // patch request it started is in flight).
        guard step == (item.kind == .download ? .downloading : .patching) else { return }
        if item.isLost {
            step = .failed(.jobLost)
            return
        }
        guard let job = item.job else { return }
        pollError = nil
        if cancelledJobs.contains(item.id) {
            finishAfterCancel(item, job)
            return
        }
        switch (step, job.state) {
        case (.downloading, .succeeded):
            guard let stagingId = job.resultStagingId, let key = downloadKey else {
                step = .failed(.api(.invalidResponse("no stagingId")))
                return
            }
            staged = (key, stagingId)
            step = .starting
            // Not tied to the polling task: leaving the screen must not
            // stop the request half-way.
            runTask = Task {
                do { try await startPatch(.staging(stagingId: stagingId)) } catch { fail(error) }
            }
        case (.patching, .succeeded):
            verified = job.resultVerified
            step = .succeeded(gameId: job.resultGameId, late: false)
        case (_, .failed):
            fail(job: job, kind: item.kind)
        case (_, .cancelled):
            step = .cancelled
        default:
            step = .failed(.unexpected)
        }
    }

    /// The end of a job the user cancelled, as the console reports it.
    private func finishAfterCancel(_ item: JobItem, _ job: Job) {
        isCancelling = false
        switch job.state {
        case .cancelled:
            step = .cancelled
        case .succeeded where item.kind == .download:
            // Downloaded before the cancellation arrived: keep it for an
            // explicit new try, apply nothing.
            if let stagingId = job.resultStagingId, let key = downloadKey { staged = (key, stagingId) }
            step = .downloaded
        case .succeeded:
            verified = job.resultVerified
            step = .succeeded(gameId: job.resultGameId, late: true)
            session.contentChanged()
        case .failed:
            fail(job: job, kind: item.kind)
        default:
            step = .failed(.unexpected)
        }
    }

    private func fail(job: Job, kind: JobKind) {
        if let server = job.serverError {
            mismatchDetails = server.patchMismatchDetails
            step = .failed(AppError.patchJob(server, kind: kind))
        } else {
            step = .failed(.unexpected)
        }
    }

    /// The app goes to the background: the upload stops after its current
    /// chunk (docs/specs/06-app-ios.md §7).
    func suspend() {
        if case .sending = step { pauseFlag.set() }
        if step == .preparing { pauseFlag.set() }
    }

    /// The app is active again: a paused upload goes on.
    func resumeIfPaused() {
        if step == .failed(.upload(.paused)) { start() }
    }

    /// Stops what runs: the upload (also on the console), or the job
    /// (`DELETE /jobs/{id}`). A request in flight is cancelled as soon as
    /// it answers. A patch job cancelled while the console installs its
    /// file may still end `succeeded`: that is what is shown then.
    func cancel() async {
        guard !isCancelling else { return }
        cancelError = nil
        switch step {
        case .preparing, .sending, .failed(.upload(.cancelNotConfirmed)):
            cancelRequested = true
            runTask?.cancel()
            // Not final until the console answers: the screen and the
            // local copy stay meanwhile.
            step = .cancellingUpload
            switch await cancelUpload() {
            case .confirmed:
                step = .cancelled
            case .refused:
                // M10: refused by the console (it disconnected this app):
                // nothing was cancelled there. The upload and its local
                // copy stay, paused until the user reconnects.
                cancelRequested = false
                step = .idle
                start()
            case .notConfirmed:
                // No definite answer: the upload and its copy stay, to
                // resume or cancel again.
                cancelRequested = false
                step = .failed(.upload(.cancelNotConfirmed))
            }
        case .starting:
            // The request answers first; its job is then cancelled.
            cancelRequested = true
            isCancelling = true
        case .downloading, .patching:
            cancelRequested = true
            if let job {
                await cancelJob(job)
            } else {
                isCancelling = true
            }
        default:
            break
        }
    }

    /// `DELETE /jobs/{id}`, then the final state the console reports
    /// (polled until it is final).
    private func cancelJob(_ item: JobItem) async {
        isCancelling = true
        cancelledJobs.insert(item.id)
        do {
            try await session.transfers.cancel(item)
        } catch {
            let error = AppError(error)
            isCancelling = false
            if !error.isCancellation { cancelError = error }
            // The job goes on: its end is still handled as cancelled.
            return
        }
        for _ in 0..<30 where !item.isFinished {
            try? await Task.sleep(for: cancelPollInterval)
            await session.transfers.refreshJobs()
        }
        guard job === item else { return }
        if item.isFinished {
            advance()
        } else {
            // Not confirmed yet: the screen polls on, the end is handled
            // as a cancellation.
            isCancelling = false
        }
    }

    /// Cancels the upload on the console (best effort). Refused by the
    /// console (M10: it disconnected this app), the upload is kept: Apply
    /// resumes it.
    private enum CancelResult { case confirmed, refused, notConfirmed }

    /// Cancels the upload on the console. Unless the console confirmed, the
    /// upload is kept (to resume it, or cancel again).
    @discardableResult
    private func cancelUpload() async -> CancelResult {
        guard let current = upload else { return .confirmed }
        self.upload = nil
        let outcome: ResumableUpload.CancelOutcome
        do {
            outcome = await current.upload.cancel(client: try await session.connectedClient())
        } catch {
            outcome = .notConfirmed(error as? APIError)
        }
        switch outcome {
        case .cancelled, .outcomeUnknown:
            return .confirmed
        case .refused:
            if self.upload == nil { self.upload = current }
            return .refused
        case .notConfirmed:
            if self.upload == nil { self.upload = current }
            return .notConfirmed
        }
    }

    private func fail(_ error: Error) {
        let error = AppError.patch(error)
        // The cancel of the upload decides the step itself.
        if step == .cancellingUpload { return }
        // Stopped by a cancellation asked before any job existed.
        if cancelRequested && error.isCancellation && step != .cancelled {
            return finishCancelledRequest()
        }
        // A cancellation sets its own step.
        guard !error.isCancellation, step != .cancelled else { return }
        isCancelling = false
        step = .failed(error)
    }

    /// The screen goes away: the local copy of a file not sent is deleted,
    /// and an upload left paused or failed is cancelled on the console.
    func discardFile() {
        guard !step.holdsTheScreen else { return }
        file?.remove()
        if upload != nil {
            Task { await cancelUpload() }
        }
    }
}
