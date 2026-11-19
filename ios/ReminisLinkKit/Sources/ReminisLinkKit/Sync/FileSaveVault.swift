import Foundation

/// A ``SaveVault`` in a folder of the file system.
///
/// - Every access goes through `NSFileCoordinator` (required in an iCloud
///   ubiquity container, harmless elsewhere): a coordinated read also
///   waits for iCloud to download an item that is not on the iPhone yet.
/// - A version is assembled in `stagingDirectory` (outside the vault),
///   each file synced to disk, then moved into the game folder in one
///   rename: a version is never seen half written.
/// - Nothing is ever deleted, except the oldest versions beyond
///   ``SaveVaultLimits/maxVersions`` once a new version is in place. A
///   folder without a readable `.version.json` is not a version: it is
///   never counted nor deleted. Versions are ordered by their publication
///   `sequence`, never by date (the iPhone clock can go back): the version
///   just published is always kept.
/// - Console and game folders are found by the identifier in their
///   `.console.json` / `.game.json`, never by name: renaming a console or
///   two games with the same name do not mix histories.
public actor FileSaveVault: SaveVault {
    public nonisolated let location: SaveVaultLocation
    public nonisolated let root: URL
    public nonisolated let stagingDirectory: URL
    private let maxVersions: Int
    private let now: @Sendable () -> Date
    private let timeZone: TimeZone

    /// Console folders by device id, and game folders by game id, found
    /// at the first use (and kept up to date by this instance).
    private var consoleIndex: [String: URL]?
    private var gameIndex: [String: [String: URL]] = [:]

    static let consoleFile = ".console.json"
    static let gameFile = ".game.json"
    static let versionFile = ".version.json"

    public init(
        location: SaveVaultLocation, root: URL, stagingDirectory: URL,
        maxVersions: Int = SaveVaultLimits.maxVersions,
        timeZone: TimeZone = .current,
        now: @escaping @Sendable () -> Date = { Date() }
    ) {
        self.location = location
        self.root = root
        self.stagingDirectory = stagingDirectory
        self.maxVersions = max(1, maxVersions)
        self.timeZone = timeZone
        self.now = now
    }

    // MARK: - Reading

    public func consoles() throws -> [VaultConsole] {
        consoleIndex = nil
        return try loadConsoleIndex().compactMap { deviceId, folder in
            guard let info: VaultConsoleInfo = readJSON(folder.appending(path: Self.consoleFile)) else { return nil }
            return VaultConsole(info: VaultConsoleInfo(deviceId: deviceId, name: info.name), folderName: folder.lastPathComponent)
        }
        .sorted { $0.folderName.localizedStandardCompare($1.folderName) == .orderedAscending }
    }

    public func games(deviceId: String) throws -> [VaultGame] {
        gameIndex[deviceId] = nil
        var games: [VaultGame] = []
        for (_, folder) in try loadGameIndex(deviceId: deviceId) {
            guard let info: VaultGameInfo = readJSON(folder.appending(path: Self.gameFile)) else { continue }
            let versions = try versions(in: folder)
            guard !versions.isEmpty else { continue }
            let path = folder.deletingLastPathComponent().lastPathComponent + "/" + folder.lastPathComponent
            games.append(VaultGame(info: info, folderPath: path, latest: versions.first, versionCount: versions.count))
        }
        return games.sorted {
            let system = $0.info.systemName.localizedStandardCompare($1.info.systemName)
            if system != .orderedSame { return system == .orderedAscending }
            let name = $0.info.name.localizedStandardCompare($1.info.name)
            if name != .orderedSame { return name == .orderedAscending }
            return $0.folderPath.localizedStandardCompare($1.folderPath) == .orderedAscending
        }
    }

    public func versions(deviceId: String, gameId: String) throws -> [SaveVersion] {
        guard let folder = try gameFolder(deviceId: deviceId, gameId: gameId) else { return [] }
        return try versions(in: folder)
    }

    public func export(deviceId: String, gameId: String, versionId: String, to directory: URL) throws -> ExportedVersion {
        guard let game = try gameFolder(deviceId: deviceId, gameId: gameId),
              let version = try versions(in: game).first(where: { $0.id == versionId })
        else { throw SaveVaultError.versionNotFound }
        let source = game.appending(path: version.id, directoryHint: .isDirectory)
        let destination = directory.appending(path: UUID().uuidString, directoryHint: .isDirectory)
        let manager = FileManager.default
        try manager.createDirectory(at: destination, withIntermediateDirectories: true)
        do {
            var copied: [URL] = []
            for file in [version.info.save, version.info.rtc].compactMap({ $0 }) {
                let target = destination.appending(path: file.name)
                try coordinateReading(source.appending(path: file.name)) { url in
                    try manager.copyItem(at: url, to: target)
                }
                copied.append(target)
            }
            return ExportedVersion(save: copied[0], rtc: copied.count > 1 ? copied[1] : nil)
        } catch {
            try? manager.removeItem(at: destination)
            throw error
        }
    }

    // MARK: - Writing

    public func addVersion(
        console: VaultConsoleInfo, game: VaultGameInfo,
        save: NewVersionFile, rtc: NewVersionFile?, modifiedAt: Date?
    ) throws -> SaveVersion {
        let saveName = try Self.checkedFileName(save.name)
        let rtcName = try rtc.map { try Self.checkedFileName($0.name) }
        if let rtcName, rtcName.lowercased() == saveName.lowercased() { throw SaveVaultError.invalidName(rtcName) }

        // 1. Assemble the version outside the vault, numbered after the
        //    versions already there.
        let gameFolder = try ensureGameFolder(console: console, game: game)
        let sequence = (try versions(in: gameFolder).map(\.info.sequence).max() ?? 0) + 1
        let manager = FileManager.default
        let stage = stagingDirectory.appending(path: UUID().uuidString, directoryHint: .isDirectory)
        try manager.createDirectory(at: stage, withIntermediateDirectories: true)
        defer { try? manager.removeItem(at: stage) }
        let saveFile = try stageFile(save, as: saveName, in: stage)
        let rtcFile = try rtc.map { try stageFile($0, as: rtcName!, in: stage) }
        let info = SaveVersionInfo(
            gameId: game.gameId, sequence: sequence, syncedAt: now(), modifiedAt: modifiedAt, save: saveFile, rtc: rtcFile
        )
        try Self.writeSynced(Self.encoder().encode(info), to: stage.appending(path: Self.versionFile))

        // 2. Move it into the game folder in one rename.
        let name = try uniqueName(Self.versionName(info.syncedAt, timeZone: timeZone), in: gameFolder)
        let destination = gameFolder.appending(path: name, directoryHint: .isDirectory)
        try coordinateMove(from: stage, to: destination)

        // 3. Only then, rotate (the new version has the highest sequence;
        //    it is also excluded by name, whatever happens).
        let version = SaveVersion(id: name, info: info)
        try rotate(gameFolder, keeping: version.id)
        return version
    }

    public func retainedVersions(deviceId: String, gameId: String) throws -> [SaveVersion] {
        guard let folder = try gameFolder(deviceId: deviceId, gameId: gameId) else { return [] }
        try rotate(folder, keeping: nil)
        return try versions(in: folder)
    }

    /// Deletes the versions beyond the limit, oldest published first,
    /// never `kept`.
    private func rotate(_ gameFolder: URL, keeping kept: String?) throws {
        for old in try versions(in: gameFolder).dropFirst(maxVersions) where old.id != kept {
            try coordinateDeleting(gameFolder.appending(path: old.id, directoryHint: .isDirectory))
        }
    }

    private func stageFile(_ file: NewVersionFile, as name: String, in stage: URL) throws -> SaveVersionFile {
        let target = stage.appending(path: name)
        try FileManager.default.moveItem(at: file.url, to: target)
        try Self.fsync(target)
        return SaveVersionFile(name: name, sha256: file.sha256, sizeBytes: try FileDigest.size(of: target))
    }

    // MARK: - Folders

    private func loadConsoleIndex() throws -> [String: URL] {
        if let consoleIndex { return consoleIndex }
        var index: [String: URL] = [:]
        for folder in try subfolders(of: root) {
            guard let info: VaultConsoleInfo = readJSON(folder.appending(path: Self.consoleFile)) else { continue }
            // Two folders claiming one console (copied by hand): the first by name wins.
            if index[info.deviceId] == nil { index[info.deviceId] = folder }
        }
        consoleIndex = index
        return index
    }

    private func loadGameIndex(deviceId: String) throws -> [String: URL] {
        if let games = gameIndex[deviceId] { return games }
        var index: [String: URL] = [:]
        if let console = try loadConsoleIndex()[deviceId] {
            for system in try subfolders(of: console) {
                for folder in try subfolders(of: system) {
                    guard let info: VaultGameInfo = readJSON(folder.appending(path: Self.gameFile)) else { continue }
                    if index[info.gameId] == nil { index[info.gameId] = folder }
                }
            }
        }
        gameIndex[deviceId] = index
        return index
    }

    private func gameFolder(deviceId: String, gameId: String) throws -> URL? {
        try loadGameIndex(deviceId: deviceId)[gameId]
    }

    private func ensureConsoleFolder(_ console: VaultConsoleInfo) throws -> URL {
        if let folder = try loadConsoleIndex()[console.deviceId] { return folder }
        try coordinateWriting(root) { try FileManager.default.createDirectory(at: $0, withIntermediateDirectories: true) }
        let folder = root.appending(path: try uniqueName(Self.folderName(console.name), in: root), directoryHint: .isDirectory)
        try createFolder(folder, metadata: Self.encoder().encode(console), file: Self.consoleFile)
        consoleIndex?[console.deviceId] = folder
        return folder
    }

    private func ensureGameFolder(console: VaultConsoleInfo, game: VaultGameInfo) throws -> URL {
        if let folder = try gameFolder(deviceId: console.deviceId, gameId: game.gameId) { return folder }
        let consoleFolder = try ensureConsoleFolder(console)
        let system = consoleFolder.appending(path: Self.folderName(game.systemName), directoryHint: .isDirectory)
        try coordinateWriting(system) { try FileManager.default.createDirectory(at: $0, withIntermediateDirectories: true) }
        let folder = system.appending(path: try uniqueName(Self.folderName(game.name), in: system), directoryHint: .isDirectory)
        try createFolder(folder, metadata: Self.encoder().encode(game), file: Self.gameFile)
        var games = try loadGameIndex(deviceId: console.deviceId)
        games[game.gameId] = folder
        gameIndex[console.deviceId] = games
        return folder
    }

    /// A folder and its metadata file, staged then moved in one rename.
    private func createFolder(_ folder: URL, metadata: Data, file: String) throws {
        let manager = FileManager.default
        let stage = stagingDirectory.appending(path: UUID().uuidString, directoryHint: .isDirectory)
        try manager.createDirectory(at: stage, withIntermediateDirectories: true)
        defer { try? manager.removeItem(at: stage) }
        try Self.writeSynced(metadata, to: stage.appending(path: file))
        try coordinateMove(from: stage, to: folder)
    }

    /// The version folders of a game, last published first.
    private func versions(in game: URL) throws -> [SaveVersion] {
        var versions: [SaveVersion] = []
        for folder in try subfolders(of: game) {
            guard let info: SaveVersionInfo = readJSON(folder.appending(path: Self.versionFile)) else { continue }
            versions.append(SaveVersion(id: folder.lastPathComponent, info: info))
        }
        return versions.sorted {
            if $0.info.sequence != $1.info.sequence { return $0.info.sequence > $1.info.sequence }
            if $0.info.syncedAt != $1.info.syncedAt { return $0.info.syncedAt > $1.info.syncedAt }
            return $0.id > $1.id
        }
    }

    /// The folders in a folder, iCloud placeholders included (an item not
    /// downloaded yet shows as `.<name>.icloud`), hidden ones excluded.
    private func subfolders(of folder: URL) throws -> [URL] {
        var names: [String] = []
        var exists = true
        try coordinateReading(folder) { url in
            do {
                names = try FileManager.default.contentsOfDirectory(atPath: url.path(percentEncoded: false))
            } catch CocoaError.fileReadNoSuchFile {
                exists = false
            }
        }
        guard exists else { return [] }
        var result: [URL] = []
        for raw in names.sorted() {
            if raw.hasPrefix("."), raw.hasSuffix(".icloud") {
                // Only a placeholder of a folder can be a folder: keep it,
                // the coordinated read of its metadata downloads it.
                let name = String(raw.dropFirst().dropLast(".icloud".count))
                if !name.hasPrefix(".") && !names.contains(name) {
                    result.append(folder.appending(path: name, directoryHint: .isDirectory))
                }
                continue
            }
            guard !raw.hasPrefix(".") else { continue }
            var isDirectory: ObjCBool = false
            let url = folder.appending(path: raw, directoryHint: .isDirectory)
            if FileManager.default.fileExists(atPath: url.path(percentEncoded: false), isDirectory: &isDirectory), isDirectory.boolValue {
                result.append(url)
            }
        }
        return result
    }

    private func uniqueName(_ base: String, in folder: URL) throws -> String {
        var taken = Set<String>()
        try coordinateReading(folder) { url in
            let names = (try? FileManager.default.contentsOfDirectory(atPath: url.path(percentEncoded: false))) ?? []
            for name in names {
                let plain = name.hasPrefix(".") && name.hasSuffix(".icloud") ? String(name.dropFirst().dropLast(7)) : name
                taken.insert(plain.lowercased())
            }
        }
        if !taken.contains(base.lowercased()) { return base }
        var n = 2
        while taken.contains("\(base) (\(n))".lowercased()) { n += 1 }
        return "\(base) (\(n))"
    }

    // MARK: - Coordination

    private func coordinateReading(_ url: URL, _ body: (URL) throws -> Void) throws {
        var coordinationError: NSError?
        var bodyError: Error?
        NSFileCoordinator(filePresenter: nil).coordinate(readingItemAt: url, options: [], error: &coordinationError) { url in
            do { try body(url) } catch { bodyError = error }
        }
        if let bodyError { throw bodyError }
        if let coordinationError {
            if coordinationError.domain == NSCocoaErrorDomain && coordinationError.code == NSFileReadNoSuchFileError {
                try body(url)
                return
            }
            throw coordinationError
        }
    }

    private func coordinateWriting(_ url: URL, _ body: (URL) throws -> Void) throws {
        var coordinationError: NSError?
        var bodyError: Error?
        NSFileCoordinator(filePresenter: nil).coordinate(writingItemAt: url, options: [], error: &coordinationError) { url in
            do { try body(url) } catch { bodyError = error }
        }
        if let bodyError { throw bodyError }
        if let coordinationError { throw coordinationError }
    }

    private func coordinateMove(from source: URL, to destination: URL) throws {
        var coordinationError: NSError?
        var bodyError: Error?
        let coordinator = NSFileCoordinator(filePresenter: nil)
        coordinator.coordinate(
            writingItemAt: source, options: .forMoving, writingItemAt: destination, options: .forReplacing,
            error: &coordinationError
        ) { from, to in
            do {
                // Never replace: the name was chosen free.
                guard !FileManager.default.fileExists(atPath: to.path(percentEncoded: false)) else {
                    throw CocoaError(.fileWriteFileExists)
                }
                coordinator.item(at: from, willMoveTo: to)
                try FileManager.default.moveItem(at: from, to: to)
                coordinator.item(at: from, didMoveTo: to)
            } catch {
                bodyError = error
            }
        }
        if let bodyError { throw bodyError }
        if let coordinationError { throw coordinationError }
    }

    private func coordinateDeleting(_ url: URL) throws {
        var coordinationError: NSError?
        var bodyError: Error?
        NSFileCoordinator(filePresenter: nil).coordinate(writingItemAt: url, options: .forDeleting, error: &coordinationError) { url in
            do { try FileManager.default.removeItem(at: url) } catch { bodyError = error }
        }
        if let bodyError { throw bodyError }
        if let coordinationError { throw coordinationError }
    }

    private func readJSON<T: Decodable>(_ url: URL) -> T? {
        var data: Data?
        try? coordinateReading(url) { data = try Data(contentsOf: $0) }
        return data.flatMap { try? ProtocolJSON.decoder().decode(T.self, from: $0) }
    }

    // MARK: - Helpers

    static func encoder() -> JSONEncoder {
        let encoder = ProtocolJSON.encoder()
        encoder.outputFormatting.insert(.prettyPrinted)
        return encoder
    }

    /// Writes a new file and syncs it to disk before returning.
    static func writeSynced(_ data: Data, to url: URL) throws {
        guard FileManager.default.createFile(atPath: url.path(percentEncoded: false), contents: nil) else {
            throw CocoaError(.fileWriteUnknown)
        }
        let handle = try FileHandle(forWritingTo: url)
        defer { try? handle.close() }
        try handle.write(contentsOf: data)
        try handle.synchronize()
    }

    static func fsync(_ url: URL) throws {
        let handle = try FileHandle(forUpdating: url)
        defer { try? handle.close() }
        try handle.synchronize()
    }

    /// `2026-11-19 15.42.10`: sorts by date, valid on every file system
    /// (no colon).
    public static func versionName(_ date: Date, timeZone: TimeZone) -> String {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = timeZone
        let c = calendar.dateComponents([.year, .month, .day, .hour, .minute, .second], from: date)
        return String(
            format: "%04d-%02d-%02d %02d.%02d.%02d",
            c.year ?? 0, c.month ?? 0, c.day ?? 0, c.hour ?? 0, c.minute ?? 0, c.second ?? 0
        )
    }

    /// A name made safe for a folder in Files and iCloud Drive: no path
    /// separator or reserved character, no leading dot, not empty, at most
    /// 100 characters.
    public static func folderName(_ name: String) -> String {
        let reserved = Set("/\\:*?\"<>|")
        var result = String(name.precomposedStringWithCanonicalMapping.map { character in
            character.unicodeScalars.contains(where: { $0.properties.generalCategory == .control }) || reserved.contains(character)
                ? "_" : character
        })
        result = result.trimmingCharacters(in: .whitespacesAndNewlines)
        while result.hasPrefix(".") { result.removeFirst() }
        while result.hasSuffix(".") { result.removeLast() }
        if result.count > 100 { result = String(result.prefix(100)).trimmingCharacters(in: .whitespaces) }
        return result.isEmpty ? "_" : result
    }

    /// A file name taken from the console: a single, visible path component.
    static func checkedFileName(_ name: String) throws -> String {
        let safe = ContentDisposition.safe(name) ?? ""
        guard !safe.isEmpty, safe == name, !name.hasPrefix("."), name != Self.versionFile else {
            throw SaveVaultError.invalidName(name)
        }
        return name
    }
}
