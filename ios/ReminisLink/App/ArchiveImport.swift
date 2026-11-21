import Foundation
import Observation
import ReminisLinkKit

// A game's data as an archive (milestone M19, docs/specs/06-app-ios.md §4.5):
// export a zip of a game to the iPhone, import it into the same console or
// another one. The console builds and checks the archive; the app only
// moves it and shows what the import will do.

extension ConsoleSession {
    /// `GET /games/{gameId}/archive`: the archive is written to the export
    /// directory for the share sheet (Save to Files, AirDrop…). A read:
    /// allowed while another device writes.
    func downloadGameArchive(gameId: String, includeROM: Bool, progress: FileDownload) async throws -> ExportedFile {
        let report = progress.reporter()
        do {
            return try await export { client, directory in
                try await client.downloadGameArchive(gameId: gameId, includeROM: includeROM, into: directory, progress: report)
            }
        } catch {
            throw AppError.archive(error, routeMissing: true)
        }
    }

    /// Sends an archive chosen in Files to the staging area and returns its
    /// `stagingId`. The local copy is deleted once the console has it.
    func stageArchive(_ file: PickedFile, progress: @escaping @Sendable (Int64) async -> Void) async throws -> String {
        defer { file.remove() }
        let url = file.url
        let sha = try await FileDigest.sha256(of: url)
        let upload = ResumableUpload(
            source: UploadSource(fileURL: url, sizeBytes: file.sizeBytes, sha256: sha),
            target: .staging(fileName: file.name), onConflict: .rename,
            scratchDirectory: app.transferDirectory.appending(path: "chunks-\(deviceId)")
        )
        let result: UploadResult
        do {
            // M10: refused while the console disconnected this app, the
            // upload waits and goes on once reconnected.
            result = try await pausingForReconnection {
                try await upload.run(client: try await connectedClient(), progress: progress)
            }
        } catch {
            if let client { _ = await upload.cancel(client: client) }
            throw AppError(error)
        }
        guard let stagingId = result.stagingId else { throw AppError.api(.invalidResponse("no stagingId")) }
        return stagingId
    }

    /// Restores a state version of the vault to its slot on the console
    /// (`target.kind = "state"`, through the transfer queue): the console
    /// first moves the current state to its trash; a locked slot refuses
    /// it. Only the state is sent (the contract has no route to write a
    /// thumbnail).
    func restore(_ version: StateVersion, gameId: String, gameName: String, from vault: any SaveVault) async throws -> UploadItem {
        let files = try await app.work.run {
            try await vault.exportStateVerified(deviceId: deviceId, gameId: gameId, version: version, to: app.exportDirectory)
        }
        if let thumbnail = files.thumbnail { try? FileManager.default.removeItem(at: thumbnail) }
        do {
            return try transfers.enqueue(
                fileURL: files.state, fileName: version.info.state.name, target: .state(gameId: gameId, slot: version.info.slot),
                destination: gameName, ownsFile: true
            )
        } catch {
            try? FileManager.default.removeItem(at: files.directory)
            throw error
        }
    }
}

/// What opens the archive import screen.
struct ArchiveImportContext: Identifiable, Equatable {
    let id = UUID()
    /// The console; nil lets the user choose (a shared archive).
    var deviceId: String?
    /// The game the data go to; nil: the console suggests one.
    var gameId: String?
    var gameName: String?
    /// A share request holding one archive.
    var request: ShareRequest?
}

/// Imports a game archive into a console (milestone M19): the archive is
/// sent to the staging area, the console checks it and says what its
/// import would do (`GET /archives/{stagingId}`), the user confirms, and an
/// `archive-import` job replaces the files (the current ones go to the
/// console's trash, the save to its backup copies first).
@MainActor
@Observable
final class ArchiveImportFlow {
    enum Step: Equatable {
        case idle
        /// Sending the archive to the console (0...1).
        case sending(Double)
        /// The console checks the archive.
        case inspecting
        /// What the import would do.
        case ready
        /// `POST /archives/{stagingId}/import` is in flight.
        case starting
        /// The console imports.
        case importing
        case succeeded(ArchiveImportResult)
        case failed(AppError)

        var isBusy: Bool {
            switch self {
            case .sending, .inspecting, .starting, .importing: true
            default: false
            }
        }
    }

    let session: ConsoleSession
    private(set) var step: Step = .idle
    private(set) var plan: ArchivePlan?
    /// The game a `409` named as already holding the archive's ROM.
    private(set) var existingGameId: String?
    private(set) var job: JobItem?
    private var stagingId: String?
    /// The target asked for (nil: the console suggests one).
    private(set) var target: ArchiveTarget?
    private var task: Task<Void, Never>?

    init(session: ConsoleSession, target: ArchiveTarget?) {
        self.session = session
        self.target = target
    }

    /// Sends the archive, then asks what its import would do.
    func start(with file: PickedFile) {
        task?.cancel()
        plan = nil
        existingGameId = nil
        step = .sending(0)
        let size = file.sizeBytes
        let session = session
        task = Task { [weak self] in
            do {
                let id = try await session.stageArchive(file) { [weak self] offset in
                    await self?.reportSent(offset, of: size)
                }
                self?.stagingId = id
                await self?.inspect()
            } catch {
                let error = AppError(error)
                if !error.isCancellation { self?.step = .failed(error) }
            }
        }
    }

    private func reportSent(_ offset: Int64, of size: Int64) {
        guard case .sending = step else { return }
        step = .sending(size > 0 ? min(1, Double(offset) / Double(size)) : 1)
    }

    /// Asks again what the import would do, into `target`.
    func retarget(_ target: ArchiveTarget?) {
        guard stagingId != nil else { return }
        self.target = target
        existingGameId = nil
        step = .inspecting
        task = Task { await inspect() }
    }

    private func inspect() async {
        guard let stagingId else { return }
        step = .inspecting
        let target = target
        do {
            plan = try await session.perform { try await $0.inspectArchive(stagingId: stagingId, target: target) }
            step = .ready
        } catch {
            fail(error)
        }
    }

    /// Starts the import into the target of the plan.
    func confirm() {
        guard let stagingId, let target = plan?.target, step == .ready else { return }
        step = .starting
        let title = String(localized: "Import: \(plan?.targetName ?? plan?.manifest.source.gameName ?? "")")
        task = Task {
            do {
                let accepted = try await session.perform { try await $0.importArchive(stagingId: stagingId, target: target) }
                job = session.transfers.track(jobId: accepted.jobId, title: title, kind: .archiveImport)
                step = .importing
                await tick()
            } catch {
                fail(error)
            }
        }
    }

    /// Polls the job and moves on when it ends (called every second by the
    /// screen; events update the job meanwhile).
    func tick() async {
        guard let job, step == .importing else { return }
        if !job.isFinished { await session.transfers.refreshJobs() }
        guard job.isFinished, step == .importing else { return }
        if job.isLost { return step = .failed(.jobLost) }
        guard let result = job.job else { return }
        switch result.state {
        case .succeeded:
            if let parsed = ArchiveImportResult(result.result) {
                step = .succeeded(parsed)
                session.contentChanged()
            } else {
                step = .failed(.unexpected)
            }
        case .failed:
            if let server = result.serverError {
                fail(APIError.server(server))
            } else {
                step = .failed(.unexpected)
            }
        default:
            step = .failed(.cancelled)
        }
    }

    private func fail(_ error: Error) {
        if case .api(.server(let server)) = AppError(error), server.status == 409,
           case .string(let gameId)? = server.details["gameId"] {
            existingGameId = gameId
        }
        let error = AppError.archive(error)
        if !error.isCancellation { step = .failed(error) }
    }

    func cancel() {
        task?.cancel()
        task = nil
    }
}

extension AppError {
    /// The error of an archive action (M19): a refused archive, a ROM name
    /// already taken, or (`routeMissing`) a pak older than M19.
    static func archive(_ error: Error, routeMissing: Bool = false) -> AppError {
        let error = AppError(error)
        guard case .api(let api) = error else { return error }
        let server: ServerError? = if case .server(let s) = api { s } else { nil }
        if routeMissing {
            // A pak older than M19 has no such route (`404 No such route`,
            // or `405`); a `404` of the route itself is the game, or a game
            // with nothing to export.
            let status: Int? = if case .unexpectedStatus(let s) = api { s } else { server?.status }
            if status == 405 || (status == 404 && (server?.message.localizedCaseInsensitiveContains("route") ?? true)) {
                return .archivesNeedNewerPak
            }
            if status == 404 { return server?.message == "Nothing to export" ? .archiveEmpty : .gameRemoved }
        }
        switch server?.code {
        case .unsupportedFormat: return .invalidArchive
        case .conflict where server?.status == 409: return .archiveGameExists
        default: return error
        }
    }
}
