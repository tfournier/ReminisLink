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
    /// (`source.type = "upload"`, with the id of the completed upload).
    func importCheats(gameId: String, from file: PickedFile, mode: CheatImportMode) async throws -> CheatImportResult {
        defer { file.remove() }
        let url = file.url
        let sha = try await FileDigest.sha256(of: url)
        let upload = ResumableUpload(
            source: UploadSource(fileURL: url, sizeBytes: file.sizeBytes, sha256: sha),
            target: .staging(fileName: file.name), onConflict: .rename,
            scratchDirectory: app.transferDirectory.appending(path: "chunks-\(deviceId)")
        )
        let client = try await connectedClient()
        do {
            _ = try await upload.run(client: client)
        } catch {
            await upload.cancel(client: client)
            throw AppError(error)
        }
        guard let uploadId = await upload.completedUploadId else { throw AppError.unexpected }
        return try await importCheats(gameId: gameId, CheatImportRequest(source: .upload(uploadId: uploadId), mode: mode))
    }

    func importCheats(gameId: String, _ request: CheatImportRequest) async throws -> CheatImportResult {
        let result = try await perform { try await $0.importCheats(gameId: gameId, request) }
        contentChanged()
        return result
    }
}
