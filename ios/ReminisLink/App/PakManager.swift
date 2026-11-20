import Foundation
import Observation
import ReminisLinkKit

/// One installation of a pak (milestone M12), from its source to its end.
///
/// The three sources use the mechanisms that exist: a file of the iPhone
/// is uploaded through the transfer queue (target `pak`; its `complete`
/// starts a `pak-install` job), a link is downloaded by the console (a
/// `download` job, target `pak`), a zip or `.pak` folder of the SD card is
/// installed by `POST /paks/install` (a `pak-install` job). A job that
/// fails with `409 conflict` and `details.existing` waits for the user:
/// "Replace" starts the same source again with `overwrite`/`replace`.
@MainActor
@Observable
final class PakInstall: Identifiable {
    enum Source: Equatable {
        /// A copy of the file in the app (a directory of its own, deleted
        /// once the installation ends): kept until then, so that a
        /// replacement can send it again.
        case file(PickedFile)
        case link(URL)
        case sdCard(SdEntry)
    }

    enum Phase: Equatable {
        /// Uploading through the transfer queue (see ``upload``).
        case sending
        /// Asked to the console (a job), or its job runs.
        case installing
        /// A pak of this name is installed: waiting for the user.
        case needsReplacement(name: String)
        case installed
        case failed(AppError)
        case cancelled

        var isFinished: Bool {
            switch self {
            case .installed, .failed, .cancelled: true
            default: false
            }
        }
    }

    let id = UUID()
    /// What is shown: the file name, the link's file name, the SD entry.
    let name: String
    let source: Source
    let kind: PakInstallKind
    var phase: Phase = .installing
    /// The upload of a file source (the current attempt).
    var upload: UploadItem?
    /// The job of the current attempt.
    var jobId: String?
    /// The current attempt replaces the installed pak (after the user
    /// confirmed): a new conflict is an error, never asked again.
    var replacing = false

    init(name: String, source: Source, kind: PakInstallKind) {
        self.name = name
        self.source = source
        self.kind = kind
    }

    /// The phase as shown: an upload that failed, paused or was cancelled
    /// says so.
    var displayPhase: Phase {
        guard phase == .sending, let upload else { return phase }
        switch upload.phase {
        case .cancelled: return .cancelled
        case .failed: return .failed(upload.failure.map { AppError.pak($0) } ?? .unexpected)
        default: return .sending
        }
    }

    var isFinished: Bool { displayPhase.isFinished }
}

/// The paks of one console (milestone M12): the list, deletions to the
/// trash, and the installations in progress.
@MainActor
@Observable
final class PakManager {
    /// Nil until loaded.
    private(set) var paks: [Pak]?
    private(set) var loadError: AppError?
    private(set) var isLoading = false
    private(set) var installs: [PakInstall] = []
    /// The installation whose replacement the user is asked to confirm.
    var pendingReplacement: PakInstall?

    @ObservationIgnored private unowned let session: ConsoleSession
    @ObservationIgnored private var loads = LoadGeneration()
    @ObservationIgnored private var observing = false

    init(session: ConsoleSession) {
        self.session = session
    }

    /// Follows the jobs of the transfer queue (once).
    private func observeJobs() {
        guard !observing else { return }
        observing = true
        session.transfers.observeJobs { [weak self] item in
            guard let self else { return false }
            self.jobUpdated(item)
            return true
        }
    }

    // MARK: - List

    /// `GET /paks`. Only the last load started publishes. A pak older than
    /// M12 is ``AppError/needsNewerPak``.
    func load() async {
        prune()
        let generation = loads.begin()
        isLoading = true
        defer { if loads.isCurrent(generation) { isLoading = false } }
        do {
            let paks = try await session.perform { try await $0.paks() }
            guard loads.isCurrent(generation) else { return }
            self.paks = paks
            loadError = nil
        } catch {
            guard loads.isCurrent(generation) else { return }
            let error = AppError.pak(error, routeMissing: true)
            if !error.isCancellation { loadError = error }
        }
    }

    /// A `paks` event (M14): another client (or this one) installed or
    /// deleted a pak. Reloaded if the list was shown once.
    func paksChanged() {
        guard paks != nil || loadError != nil else { return }
        Task { await load() }
    }

    /// `DELETE /paks/{pakId}`: the pak goes to the trash of the console.
    /// ReminisLink itself is never sent (and the pak refuses it).
    func delete(_ pak: Pak) async throws {
        guard !pak.isSelf else { throw AppError.pakSelf }
        do {
            _ = try await session.perform { try await $0.deletePak(id: pak.pakId) }
        } catch {
            throw AppError.pak(error)
        }
        paks?.removeAll { $0.pakId == pak.pakId }
        await load()
        await session.loadTrash()
    }

    // MARK: - Installations

    /// Starts an installation with `onConflict: fail`.
    @discardableResult
    func install(_ source: PakInstall.Source, name: String, kind: PakInstallKind) async -> PakInstall {
        observeJobs()
        let install = PakInstall(name: name, source: source, kind: kind)
        installs.append(install)
        await start(install)
        return install
    }

    /// The user confirmed: the same source again, replacing the pak.
    func replace(_ install: PakInstall) async {
        guard case .needsReplacement = install.phase else { return }
        if pendingReplacement === install { pendingReplacement = nil }
        install.replacing = true
        await start(install)
    }

    /// The user kept the installed pak.
    func keepInstalled(_ install: PakInstall) {
        if pendingReplacement === install { pendingReplacement = nil }
        guard case .needsReplacement = install.phase else { return }
        install.phase = .cancelled
        cleanUp(install)
    }

    /// Removes one finished installation from the list.
    func remove(_ install: PakInstall) {
        guard install.isFinished else { return }
        cleanUp(install)
        installs.removeAll { $0 === install }
    }

    /// Removes the finished installations from the list.
    func clearFinished() {
        prune()
        for install in installs where install.isFinished { cleanUp(install) }
        installs.removeAll { $0.isFinished }
    }

    private func start(_ install: PakInstall) async {
        install.jobId = nil
        install.upload = nil
        let replace = install.replacing
        do {
            switch install.source {
            case .file(let file):
                install.phase = .sending
                install.upload = try session.transfers.enqueue(
                    fileURL: file.url, fileName: file.name, target: .pak(kind: install.kind, fileName: file.name),
                    onConflict: replace ? .overwrite : .fail, destination: String(localized: "Apps"), ownsFile: false
                )
            case .link(let url):
                install.phase = .installing
                let request = DownloadRequest(
                    url: url.absoluteString, target: .pak(kind: install.kind, fileName: nil),
                    onConflict: replace ? .overwrite : .fail
                )
                let accepted = try await session.perform { try await $0.createDownload(request) }
                track(install, jobId: accepted.jobId, kind: .download)
            case .sdCard(let entry):
                install.phase = .installing
                let request = PakInstallRequest(sdEntryId: entry.sdEntryId, kind: install.kind, onConflict: replace ? .replace : .fail)
                let accepted = try await session.perform { try await $0.installPak(request) }
                track(install, jobId: accepted.jobId, kind: .pakInstall)
            }
        } catch {
            let error = AppError.pak(error)
            install.phase = error.isCancellation ? .cancelled : .failed(error)
            cleanUp(install)
        }
    }

    /// An upload of the queue is done: when it is the archive of an
    /// installation, its `pak-install` job is followed.
    func uploadFinished(_ item: UploadItem) {
        guard let install = installs.first(where: { $0.upload === item }) else { return }
        guard let jobId = item.result?.jobId else {
            install.phase = .failed(.api(.invalidResponse("no jobId")))
            cleanUp(install)
            return
        }
        install.phase = .installing
        track(install, jobId: jobId, kind: .pakInstall)
    }

    private func track(_ install: PakInstall, jobId: String, kind: JobKind) {
        install.jobId = jobId
        let item = session.transfers.track(jobId: jobId, title: install.name, kind: kind)
        // The job may have ended before it was tracked (its event came
        // first): it would never be updated again.
        jobUpdated(item)
    }

    /// A job of the queue changed (event or poll).
    func jobUpdated(_ item: JobItem) {
        guard let install = installs.first(where: { $0.jobId == item.id }), !install.phase.isFinished else { return }
        if item.isLost {
            install.phase = .failed(.jobLost)
            cleanUp(install)
            return
        }
        guard let job = item.job else { return }
        switch job.state {
        case .succeeded:
            install.phase = .installed
            cleanUp(install)
            // The `paks` event may come later (or not at all without a
            // stream): the list is read now.
            Task { await load() }
            Task { await session.loadTrash() }
        case .failed:
            let server = job.serverError
            if let server, let name = server.conflictingPakName, !install.replacing {
                install.phase = .needsReplacement(name: name)
                if pendingReplacement == nil { pendingReplacement = install }
            } else {
                install.phase = .failed(server.map { AppError.pak(APIError.server($0)) } ?? .unexpected)
                cleanUp(install)
            }
        case .cancelled:
            install.phase = .cancelled
            cleanUp(install)
        default:
            if install.phase != .installing { install.phase = .installing }
        }
    }

    /// The installation of a job, if this manager started it.
    func install(forJob jobId: String) -> PakInstall? {
        installs.first { $0.jobId == jobId }
    }

    /// Installations whose upload was cancelled in the Transfers tab end
    /// here (their copy is deleted).
    private func prune() {
        for install in installs where install.phase == .sending && install.upload?.phase == .cancelled {
            install.phase = .cancelled
            cleanUp(install)
        }
    }

    /// Deletes the copy of a file source.
    private func cleanUp(_ install: PakInstall) {
        if case .file(let file) = install.source { file.remove() }
    }
}

/// A `.pak` folder chosen on the iPhone is zipped before it is uploaded
/// (`NSFileCoordinator` with `.forUploading`): the archive holds the folder
/// itself, `<Name>.pak/…`, as the pak expects.
enum PakArchiver {
    /// Copies the source into a new directory under `root`: a zip file as
    /// it is, a `.pak` folder zipped as `<Name>.pak.zip`. Anything else is
    /// ``AppError/notAPak``.
    nonisolated static func prepare(_ source: URL, into root: URL) async throws -> PickedFile {
        let accessing = source.startAccessingSecurityScopedResource()
        defer { if accessing { source.stopAccessingSecurityScopedResource() } }
        let isDirectory = (try? source.resourceValues(forKeys: [.isDirectoryKey]).isDirectory) ?? false
        let name = source.lastPathComponent
        if !isDirectory {
            guard name.lowercased().hasSuffix(".zip") else { throw AppError.notAPak }
            return try await PickedFile.copy(source, into: root)
        }
        guard name.lowercased().hasSuffix(".pak") else { throw AppError.notAPak }
        return try await Task.detached(priority: .userInitiated) {
            try zip(folder: source, into: root)
        }.value
    }

    /// Zips a folder into a new directory under `root` (synchronous: the
    /// file coordinator blocks).
    nonisolated static func zip(folder: URL, into root: URL) throws -> PickedFile {
        let directory = root.appending(path: UUID().uuidString, directoryHint: .isDirectory)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let name = ContentDisposition.localName(folder.lastPathComponent + ".zip")
        let destination = directory.appending(path: name)
        var coordinationError: NSError?
        var copyError: Error?
        NSFileCoordinator().coordinate(readingItemAt: folder, options: .forUploading, error: &coordinationError) { zipped in
            // The zip only exists inside this block.
            do {
                try FileManager.default.copyItem(at: zipped, to: destination)
            } catch {
                copyError = error
            }
        }
        if let error = coordinationError ?? copyError {
            try? FileManager.default.removeItem(at: directory)
            throw error
        }
        return PickedFile(url: destination, name: name, sizeBytes: try FileDigest.size(of: destination))
    }
}
