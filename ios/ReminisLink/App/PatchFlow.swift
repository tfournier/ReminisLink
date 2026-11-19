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
        /// `gameId` of the produced ROM.
        case succeeded(gameId: String?)
        case failed(AppError)
        case cancelled

        /// Something runs: the inputs are locked.
        var isBusy: Bool {
            switch self {
            case .preparing, .sending, .downloading, .starting, .patching: true
            case .idle, .succeeded, .failed, .cancelled: false
            }
        }

        /// The app side runs: closing the screen would stop it.
        var holdsTheScreen: Bool {
            switch self {
            case .preparing, .sending, .downloading, .starting: true
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

    /// The patch already in the staging area, by source.
    @ObservationIgnored private var staged: (key: String, stagingId: String)?
    /// The upload of the file, kept to resume it.
    @ObservationIgnored private var upload: (key: String, upload: ResumableUpload)?
    /// The source the running download is for.
    @ObservationIgnored private var downloadKey: String?
    @ObservationIgnored private var pauseFlag = PatchPauseFlag()
    @ObservationIgnored private var runTask: Task<Void, Never>?

    init(session: ConsoleSession, game: PatchGame?) {
        self.session = session
        deviceId = session.deviceId
        self.game = game
        fileName = game.map { PatchNaming.suggestedFileName(forROM: $0.fileName) } ?? ""
    }

    // MARK: Inputs

    var isPatchable: Bool { game?.kind.isPatchable ?? false }

    /// The new file name, or nil if the pak would refuse it.
    var validatedFileName: String? { PatchNaming.validatedFileName(fileName) }

    var validatedLink: URL? { DownloadURL.validated(linkText) }

    /// The new name does not keep the extension of the ROM.
    var changesExtension: Bool {
        guard let game, mode == .newFile, let name = validatedFileName else { return false }
        return !PatchNaming.sameExtension(name, game.fileName)
    }

    /// Identifies the chosen patch, to reuse what is already staged.
    private var sourceKey: String? {
        switch source {
        case .files: file.map { "file:\($0.id)" }
        case .link: validatedLink.map { "link:\($0.absoluteString)" }
        case .sdCard: sdFileId.map { "sd:\($0)" }
        }
    }

    var canStart: Bool {
        guard isPatchable, !step.isBusy, sourceKey != nil else { return false }
        return mode == .replace || validatedFileName != nil
    }

    private func inputChanged() {
        switch step {
        case .failed, .cancelled: step = .idle
        default: break
        }
    }

    // MARK: Running

    /// Starts (or resumes) the patch.
    func start() {
        guard canStart else { return }
        pauseFlag = PatchPauseFlag()
        // Busy at once: the inputs lock before the task starts.
        step = .starting
        runTask = Task { await run() }
    }

    private func run() async {
        guard let key = sourceKey else { return }
        job = nil
        do {
            let patch: PatchSource
            if let staged, staged.key == key {
                patch = .staging(stagingId: staged.stagingId)
            } else {
                switch source {
                case .sdCard:
                    guard let sdFileId else { return }
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
        let upload: ResumableUpload
        if let current = self.upload, current.key == key {
            upload = current.upload
        } else {
            step = .preparing
            let sha = try await FileDigest.sha256(of: file.url)
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
        let client = try await session.connectedClient()
        let size = file.sizeBytes
        let result = try await upload.run(client: client, shouldPause: { flag.isSet }, progress: { [weak self] offset in
            await self?.reportSent(offset, of: size)
        })
        guard let stagingId = result.stagingId else { throw AppError.api(.invalidResponse("no stagingId")) }
        staged = (key, stagingId)
        self.upload = nil
        file.remove()
        return stagingId
    }

    private func reportSent(_ offset: Int64, of size: Int64) {
        guard case .sending = step else { return }
        step = .sending(size > 0 ? min(1, Double(offset) / Double(size)) : 1)
    }

    /// `POST /downloads` to the staging area; ``tick()`` follows the job.
    private func startDownload(key: String) async throws {
        guard let url = validatedLink else { return }
        step = .downloading
        let request = DownloadRequest(url: url.absoluteString, target: .staging(fileName: nil))
        let accepted = try await session.perform { try await $0.createDownload(request) }
        downloadKey = key
        job = session.transfers.track(jobId: accepted.jobId, title: DownloadURL.displayName(url), kind: .download)
    }

    private func startPatch(_ patch: PatchSource) async throws {
        guard let game else { return }
        step = .starting
        let request = PatchRequest(patch: patch, output: PatchOutput(mode: mode, fileName: validatedFileName))
        let gameId = game.id
        let accepted = try await session.perform { try await $0.patchGame(gameId: gameId, request) }
        job = session.transfers.track(jobId: accepted.jobId, title: String(localized: "Patch: \(game.name)"), kind: .patch)
        step = .patching
    }

    /// Polls the job followed now and moves on when it ends. Called every
    /// second by the screen while it is visible and the app is active.
    func tick() async {
        guard let job, step == .downloading || step == .patching else { return }
        if !job.isFinished { await session.transfers.refreshJobs() }
        advance()
    }

    private func advance() {
        guard let item = job, item.isFinished else { return }
        if item.isLost {
            step = .failed(.jobLost)
            return
        }
        guard let job = item.job else { return }
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
            step = .succeeded(gameId: job.resultGameId)
        case (_, .failed):
            if let server = job.serverError {
                step = .failed(AppError.patchJob(server, kind: item.kind))
            } else {
                step = .failed(.unexpected)
            }
        case (_, .cancelled):
            step = .cancelled
        default:
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
    /// (`DELETE /jobs/{id}`). A patch job cancelled while the console
    /// installs its file may still end `succeeded`.
    func cancel() async {
        switch step {
        case .preparing, .sending:
            runTask?.cancel()
            step = .cancelled
            if let upload = upload?.upload, let client = try? await session.connectedClient() {
                _ = await upload.cancel(client: client)
            }
            self.upload = nil
        case .downloading, .patching:
            guard let job else { return }
            do {
                try await session.transfers.cancel(job)
                if job.job?.state == .succeeded { session.contentChanged() }
                advance()
            } catch {
                let error = AppError(error)
                if !error.isCancellation { step = .failed(error) }
            }
        default:
            break
        }
    }

    private func fail(_ error: Error) {
        let error = AppError.patch(error)
        // A cancellation sets its own step.
        guard !error.isCancellation, step != .cancelled else { return }
        step = .failed(error)
    }

    /// The screen goes away: the local copy of a file not sent is deleted.
    func discardFile() {
        guard !step.holdsTheScreen else { return }
        file?.remove()
    }
}
