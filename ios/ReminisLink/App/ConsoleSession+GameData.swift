import Foundation
import ReminisLinkKit

/// What happened to a save sent from Files.
enum SaveReplacement {
    /// Replaced with `PUT /save`; `backup` is the copy of the previous save
    /// (nil if there was none).
    case replaced(backup: SaveBackup?)
    /// Larger than 64 MiB: sent through the transfer queue
    /// (`POST /uploads`, target `save`); the copy is made when it completes.
    case queued(UploadItem)
}

extension ConsoleSession {
    /// Replaces the save of a game with a file already copied into the app
    /// (its directory is deleted once used). Up to 64 MiB, `PUT /save`
    /// signed with the SHA-256 of the file; above, a resumable upload.
    /// Either way the pak copies the current save first (`before-replace`).
    func replaceSave(gameId: String, gameName: String, with file: PickedFile) async throws -> SaveReplacement {
        if file.sizeBytes > APIClient.maxSaveBodyBytes {
            let item = try transfers.enqueue(
                fileURL: file.url, fileName: file.name, target: .save(gameId: gameId),
                destination: gameName, ownsFile: true
            )
            return .queued(item)
        }
        defer { file.remove() }
        let url = file.url
        let sha = try await FileDigest.sha256(of: url)
        let result = try await perform { try await $0.replaceSave(gameId: gameId, file: url, sha256: sha) }
        contentChanged()
        return .replaced(backup: result.backup)
    }

    /// Sends a `.cht` chosen in Files to the staging area, then imports it
    /// (`source.type = "staging"`, with the `stagingId` of the upload).
    func importCheats(gameId: String, from file: PickedFile, mode: CheatImportMode) async throws -> CheatImportResult {
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
            // upload waits (its local copy kept) and goes on from the
            // confirmed offset once reconnected.
            result = try await pausingForReconnection {
                try await upload.run(client: try await connectedClient())
            }
        } catch {
            if let client { _ = await upload.cancel(client: client) }
            throw AppError(error)
        }
        guard let stagingId = result.stagingId else { throw AppError.api(.invalidResponse("no stagingId")) }
        return try await importCheats(gameId: gameId, CheatImportRequest(source: .staging(stagingId: stagingId), mode: mode))
    }

    func importCheats(gameId: String, _ request: CheatImportRequest) async throws -> CheatImportResult {
        let result = try await perform { try await $0.importCheats(gameId: gameId, request) }
        contentChanged()
        return result
    }
}
