import Foundation
import ReminisLinkKit

/// Milestone M23: the writes of the Files tab (the SD card browser) and the
/// renaming of a game. Every place is an opaque identifier of the pak; the
/// pak decides what is protected and never replaces anything.
extension ConsoleSession {
    /// `POST /sd/folders`: a new folder in `parentId` (nil: the root).
    func createSdFolder(parentId: String?, name: String) async throws -> SdEntry {
        let request = SdFolderRequest(parentId: parentId, name: name)
        do {
            let entry = try await perform { try await $0.createSdFolder(request) }
            contentChanged()
            return entry
        } catch {
            throw AppError.sdWrite(error)
        }
    }

    /// `POST /sd/trash`: the entries go to the trash of the console, in one
    /// request (at most 100: the selection is limited, so the request stays
    /// all or nothing), each with the version shown (size, date, version):
    /// an entry changed since is refused (``AppError/sdEntryChanged``).
    /// Returns the number of files moved.
    @discardableResult
    func trashSdEntries(_ entries: [SdEntry]) async throws -> Int {
        let request = SdTrashRequest(entries: entries)
        guard !request.items.isEmpty else { return 0 }
        guard request.items.count <= SdTrashRequest.maxIds else { throw AppError.sdTooManySelected }
        defer { contentChanged() }
        do {
            return try await perform { try await $0.trashSdEntries(request) }.moved.count
        } catch {
            throw AppError.sdWrite(error)
        }
    }

    /// Queues files chosen on this device for a folder of the SD card
    /// (`sd-file` uploads, resumable, in the Transfers screen). A name
    /// taken on the console waits there for "Keep Both".
    @discardableResult
    func addSdFiles(_ files: [URL], folderId: String?, folderTitle: String, securityScoped: Bool) async throws -> Int {
        guard let app = liveApp else { return 0 }
        let root = app.transferDirectory
        var queued = 0
        for url in files {
            let name = url.lastPathComponent
            let copy = try await app.work.run {
                try await Task.detached(priority: .userInitiated) {
                    let directory = root.appending(path: UUID().uuidString, directoryHint: .isDirectory)
                    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
                    let destination = directory.appending(path: name)
                    let accessing = securityScoped && url.startAccessingSecurityScopedResource()
                    defer { if accessing { url.stopAccessingSecurityScopedResource() } }
                    try FileManager.default.copyItem(at: url, to: destination)
                    return destination
                }.value
            }
            try transfers.enqueue(
                fileURL: copy, fileName: name, target: .sdFile(folderId: folderId, fileName: name),
                onConflict: .fail, destination: folderTitle, ownsFile: true
            )
            queued += 1
        }
        return queued
    }

    /// `POST /games/{gameId}/rename`: the game and all its files under a
    /// new name; returns the game under its new identifier.
    func renameGame(id: String, name: String) async throws -> GameRenameResult {
        let new = GameNameRule.normalized(name)
        do {
            let result = try await perform { try await $0.renameGame(id: id, name: new) }
            contentChanged()
            return result
        } catch {
            throw AppError.rename(error)
        }
    }
}
