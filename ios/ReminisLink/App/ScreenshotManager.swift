import Foundation
import ImageIO
import Observation
import Photos
import ReminisLinkKit

/// The screenshots of one console (milestone M22): the list, the previews
/// (kept in memory), downloads for the share sheet and Photos, and moves
/// to the trash of the console.
@MainActor
@Observable
final class ScreenshotManager {
    /// Nil until loaded.
    private(set) var list: ScreenshotList?
    private(set) var loadError: AppError?
    private(set) var isLoading = false
    /// Changes when a screenshot moved or came back: the screens drop what
    /// they selected and is gone.
    private(set) var revision = 0

    /// Strong, like ``PakManager``: the session lets this manager go when
    /// it is dropped.
    @ObservationIgnored private let session: ConsoleSession
    @ObservationIgnored private var loads = LoadGeneration()
    /// The list was asked for once: a `screenshots` event reloads it.
    @ObservationIgnored private var wanted = false
    /// Previews by ``Screenshot/thumbnailKey`` (nil: the pak makes none),
    /// the most recent last.
    @ObservationIgnored private var thumbnails: [String: Data?] = [:]
    @ObservationIgnored private var thumbnailOrder: [String] = []
    @ObservationIgnored private var thumbnailLoads: [String: Task<Data?, Never>] = [:]
    /// The most previews kept (a few tens of KB each).
    nonisolated static let thumbnailLimit = 600

    init(session: ConsoleSession) {
        self.session = session
    }

    var screenshots: [Screenshot] { list?.screenshots ?? [] }

    // MARK: - List

    /// `GET /screenshots`. Only the last load started publishes. A pak older
    /// than M22 is ``AppError/screenshotsNeedNewerPak``.
    func load() async {
        wanted = true
        let generation = loads.begin()
        isLoading = true
        defer { if loads.isCurrent(generation) { isLoading = false } }
        do {
            let list = try await session.perform { try await $0.screenshots() }
            guard loads.isCurrent(generation) else { return }
            self.list = list
            loadError = nil
            revision += 1
        } catch {
            guard loads.isCurrent(generation) else { return }
            let error = AppError.screenshots(error, list: true)
            if !error.isCancellation { loadError = error }
        }
    }

    /// A `screenshots` event (M14, M22): another client (or this one) moved
    /// screenshots to the trash or restored some. Reloaded once the list
    /// was asked for.
    func screenshotsChanged() {
        guard wanted else { return }
        Task { await load() }
    }

    // MARK: - Previews

    /// The preview of a screenshot, from memory or the console (nil when
    /// the pak makes none, or the request failed: a placeholder is shown).
    func thumbnail(for screenshot: Screenshot) async -> Data? {
        let key = screenshot.thumbnailKey
        if let cached = thumbnails[key] {
            touch(key)
            return cached
        }
        guard screenshot.hasThumbnail else { return nil }
        if let running = thumbnailLoads[key] { return await running.value }
        let id = screenshot.screenshotId
        let task = Task { [session] () -> Data? in
            (try? await session.perform { try await $0.screenshotThumbnail(id: id) }) ?? nil
        }
        thumbnailLoads[key] = task
        let data = await task.value
        thumbnailLoads[key] = nil
        if !Task.isCancelled {
            thumbnails[key] = .some(data)
            touch(key)
        }
        return data
    }

    /// The preview already in memory (shown at once, without a task).
    func cachedThumbnail(for screenshot: Screenshot) -> Data? {
        thumbnails[screenshot.thumbnailKey] ?? nil
    }

    private func touch(_ key: String) {
        thumbnailOrder.removeAll { $0 == key }
        thumbnailOrder.append(key)
        while thumbnailOrder.count > Self.thumbnailLimit {
            thumbnails[thumbnailOrder.removeFirst()] = nil
        }
    }

    // MARK: - Downloads

    /// The largest screenshot the detail screen shows (Codex review of
    /// M22): a larger one can still be shared or saved.
    nonisolated static let maxPreviewBytes: Int64 = 64 << 20

    /// The image of a screenshot for the detail screen (Codex review of
    /// M22): downloaded to a file (streamed, never in memory), then decoded
    /// off the main actor by ImageIO at most `maxPixels` on its longest
    /// side (a large image is subsampled while it is read), and the file
    /// deleted. Sharing and saving download the original again.
    func displayImage(for screenshot: Screenshot, maxPixels: Int) async throws -> CGImage {
        guard screenshot.sizeBytes <= Self.maxPreviewBytes else { throw AppError.screenshotTooLarge }
        let directory = FileManager.default.temporaryDirectory.appending(path: "rl-shot-\(UUID().uuidString)", directoryHint: .isDirectory)
        defer { try? FileManager.default.removeItem(at: directory) }
        let file: DownloadedFile
        do {
            file = try await session.perform {
                try await $0.downloadScreenshot(
                    id: screenshot.screenshotId, fallbackName: screenshot.name, into: directory, maxBytes: Self.maxPreviewBytes
                )
            }
        } catch {
            // Larger than shown here (replaced since it was listed), or no
            // room left on the iPhone: it can still be shared.
            if Self.meansTooLarge(error) { throw AppError.screenshotTooLarge }
            throw failure(error)
        }
        let url = file.url
        return try await Task.detached(priority: .userInitiated) {
            try Self.downsampled(url, maxPixels: maxPixels)
        }.value
    }

    /// The download stopped at the limit, or the iPhone ran out of room.
    nonisolated static func meansTooLarge(_ error: Error) -> Bool {
        if error is DownloadLimitExceeded || (error as? AppError) == .screenshotTooLarge { return true }
        let noRoom: Set<URLError.Code> = [.cannotWriteToFile, .cannotCreateFile]
        if let url = error as? URLError, noRoom.contains(url.code) { return true }
        if case .network(let code)? = error as? APIError, noRoom.contains(code) { return true }
        if case .api(.network(let code))? = error as? AppError, noRoom.contains(code) { return true }
        let ns = error as NSError
        if ns.domain == NSCocoaErrorDomain && ns.code == NSFileWriteOutOfSpaceError { return true }
        if ns.domain == NSPOSIXErrorDomain && ns.code == Int(ENOSPC) { return true }
        return false
    }

    /// Decodes an image file at most `maxPixels` on its longest side;
    /// a file larger than ``maxPreviewBytes`` or that is no image fails.
    nonisolated static func downsampled(_ url: URL, maxPixels: Int) throws -> CGImage {
        let size = (try? url.resourceValues(forKeys: [.fileSizeKey]).fileSize).map(Int64.init) ?? 0
        guard size <= maxPreviewBytes else { throw AppError.screenshotTooLarge }
        guard let source = CGImageSourceCreateWithURL(url as CFURL, [kCGImageSourceShouldCache: false] as CFDictionary) else {
            throw AppError.unexpected
        }
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceShouldCacheImmediately: true,
            kCGImageSourceThumbnailMaxPixelSize: max(1, maxPixels),
        ]
        guard let image = CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary) else {
            throw AppError.unexpected
        }
        return image
    }

    /// Downloads screenshots into one directory of the export area, for
    /// the share sheet (Save Image, Save to Files, AirDrop…). Two names
    /// the iPhone cannot keep apart (another case only) get " (2)".
    func export(_ screenshots: [Screenshot]) async throws -> ExportedFile {
        guard !screenshots.isEmpty else { throw AppError.unexpected }
        let directory = session.app.exportDirectory
        let files: [DownloadedFile]
        do {
            files = try await session.app.work.run { [session] in
                var files: [DownloadedFile] = []
                do {
                    for shot in screenshots {
                        try Task.checkCancellation()
                        files.append(try await session.perform {
                            try await $0.downloadScreenshot(id: shot.screenshotId, fallbackName: shot.name, into: directory)
                        })
                    }
                } catch {
                    for file in files { ExportedFiles.remove(file) }
                    throw error
                }
                return files
            }
        } catch {
            throw failure(error)
        }
        guard files.count > 1 else { return ExportedFile(file: files[0]) }
        // One directory for all of them: the share sheet gets the files,
        // and its end deletes that directory.
        let target = files[0].directory
        var others: [URL] = []
        var taken: Set<String> = [files[0].url.lastPathComponent.lowercased()]
        for file in files.dropFirst() {
            let name = Self.freeName(file.url.lastPathComponent, taken: &taken)
            let destination = target.appending(path: name)
            do {
                try FileManager.default.moveItem(at: file.url, to: destination)
                others.append(destination)
            } catch {
                for file in files { ExportedFiles.remove(file) }
                throw AppError(error)
            }
            ExportedFiles.remove(file)
        }
        return ExportedFile(file: files[0], others: others)
    }

    /// `name`, or `name (n)` before its extension, not in `taken`
    /// (ignoring case, as the file system of the iPhone).
    nonisolated static func freeName(_ name: String, taken: inout Set<String>) -> String {
        var candidate = name
        var n = 2
        while taken.contains(candidate.lowercased()) {
            let ext = (name as NSString).pathExtension
            let stem = (name as NSString).deletingPathExtension
            candidate = ext.isEmpty ? "\(stem) (\(n))" : "\(stem) (\(n)).\(ext)"
            n += 1
        }
        taken.insert(candidate.lowercased())
        return candidate
    }

    /// Adds screenshots to the photo library (add-only access, asked the
    /// first time). Returns how many were added.
    func saveToPhotos(_ screenshots: [Screenshot]) async throws -> Int {
        let status = await PHPhotoLibrary.requestAuthorization(for: .addOnly)
        guard status == .authorized || status == .limited else { throw AppError.photosDenied }
        let exported = try await export(screenshots)
        defer { ExportedFiles.remove(exported.file) }
        let urls = [exported.file.url] + exported.others
        do {
            try await PHPhotoLibrary.shared().performChanges {
                for url in urls {
                    PHAssetCreationRequest.forAsset().addResource(with: .photo, fileURL: url, options: nil)
                }
            }
        } catch {
            throw AppError(error)
        }
        return urls.count
    }

    // MARK: - Trash

    /// `POST /screenshots/trash`: the screenshots go to the trash of the
    /// console (one entry per 500). The list and the trash reload.
    func trash(_ screenshots: [Screenshot]) async throws {
        let batches = ScreenshotTrashRequest.batches(screenshots.map(\.screenshotId))
        var moved = 0
        do {
            for batch in batches {
                _ = try await session.app.work.run { [session] in
                    try await session.perform { try await $0.trashScreenshots(batch) }
                }
                moved += batch.screenshotIds.count
            }
        } catch {
            // Codex review of M22: earlier batches may have moved, and a
            // failure of the card may leave a file in a trash entry: both
            // views are read again, and a partial move says so.
            // The screenshots the pak says stayed in the trash count as
            // moved (Codex review round 2: a failure inside one batch).
            if case .api(.server(let server))? = AppError(error) as AppError?, server.status == 500,
               case .array(let kept)? = server.details["kept"] {
                moved += kept.count
            }
            var error = AppError.screenshots(error)
            await load()
            await session.loadTrash()
            if moved > 0 && !error.isCancellation { error = .screenshotsPartlyMoved(moved) }
            throw error
        }
        let gone = Set(screenshots.map(\.screenshotId))
        if let list {
            let kept = list.screenshots.filter { !gone.contains($0.screenshotId) }
            self.list = ScreenshotList(screenshots: kept, truncated: list.truncated)
            revision += 1
        }
        await load()
        await session.loadTrash()
    }

    /// A screenshot gone meanwhile reloads the list (the trash reloads the
    /// list itself).
    private func failure(_ error: Error) -> AppError {
        let error = AppError.screenshots(error)
        if error == .screenshotGone { Task { await load() } }
        return error
    }
}
