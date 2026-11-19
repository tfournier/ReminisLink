import Foundation
import Observation
import ReminisLinkKit

/// The download of one file of a game to the iPhone (milestone M11), shown
/// in its row of the game sheet: bytes received and the expected total.
@MainActor
@Observable
final class FileDownload {
    let fileId: String
    private(set) var received: Int64 = 0
    /// `Content-Length`, else the size listed by the console.
    private(set) var expected: Int64?

    init(fileId: String, expected: Int64?) {
        self.fileId = fileId
        self.expected = expected
    }

    /// Between 0 and 1, nil while the size is unknown.
    var fraction: Double? {
        guard let expected, expected > 0 else { return nil }
        return min(1, Double(received) / Double(expected))
    }

    func update(received: Int64, expected: Int64?) {
        self.received = received
        if let expected { self.expected = expected }
    }

    /// A progress callback for the client: at most one report every
    /// `interval` reaches this object, and always the last one.
    nonisolated func reporter(interval: Duration = .milliseconds(100)) -> @Sendable (Int64, Int64?) -> Void {
        let gate = ProgressGate(interval: interval)
        return { [weak self] received, expected in
            guard gate.shouldReport(received: received, expected: expected) else { return }
            Task { @MainActor in self?.update(received: received, expected: expected) }
        }
    }
}

/// Drops the reports that come too close to each other (the session
/// reports every few kilobytes), but never the one that ends the file.
nonisolated private final class ProgressGate: @unchecked Sendable {
    private let lock = NSLock()
    private let interval: Duration
    private var last: ContinuousClock.Instant?

    init(interval: Duration) {
        self.interval = interval
    }

    func shouldReport(received: Int64, expected: Int64?) -> Bool {
        lock.withLock {
            let now = ContinuousClock.now
            let isLast = expected.map { received >= $0 } ?? false
            if !isLast, let last, now - last < interval { return false }
            last = now
            return true
        }
    }
}

extension ConsoleSession {
    /// `GET /games/{gameId}/files/{fileId}` (M11): the file is written to
    /// the export directory for the share sheet (Save to Files, AirDrop…),
    /// streamed to disk. A read: allowed while another device has write
    /// access. `404` (the file is no longer in the game's list) is
    /// ``AppError/gameFileChanged``.
    func downloadGameFile(gameId: String, file: GameFile, progress: FileDownload) async throws -> ExportedFile {
        // A pak older than M11 lists no `fileId`: no download is offered.
        guard let fileId = file.fileId else { throw AppError.unexpected }
        let report = progress.reporter()
        do {
            return try await export { client, directory in
                try await client.downloadGameFile(gameId: gameId, fileId: fileId, into: directory, progress: report)
            }
        } catch {
            throw AppError.gameFile(error)
        }
    }

    /// `GET /games/{gameId}`; `404` (the game was deleted or renamed on the
    /// console) is ``AppError/gameRemoved``.
    func gameDetail(id: String) async throws -> GameDetail {
        do {
            return try await perform { try await $0.game(id: id) }
        } catch {
            let error = AppError(error)
            if case .api(.server(let server)) = error, server.code == .notFound { throw AppError.gameRemoved }
            throw error
        }
    }
}
