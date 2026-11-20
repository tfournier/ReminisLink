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
///   ``SaveVaultLimits/maxVersions`` when a new version is published. A
///   folder without a readable `.version.json` is not a version: it is
///   never counted nor deleted. Versions are ordered by their publication
///   `sequence`, never by date (the iPhone clock can go back): the version
///   just published is always kept.
/// - Console and game folders are found by the identifier in their
///   `.console.json` / `.game.json`, never by name: renaming a console or
///   two games with the same name do not mix histories.
/// - Several devices can write the same vault (iCloud Drive), and create
///   the same folder twice: everything reads the union of the folders of
///   a console or game, and folders are tidied by moves only (see
///   `FileSaveVault+Convergence.swift`).
public actor FileSaveVault: SaveVault {
    public nonisolated let location: SaveVaultLocation
    public nonisolated let root: URL
    public nonisolated let stagingDirectory: URL
    private let maxVersions: Int
    private let now: @Sendable () -> Date
    private let timeZone: TimeZone

    /// Console folders by device id, and game folders by game id, found
    /// at the first use (and kept up to date by this instance). Several
    /// folders for one id are duplicates (several devices): the canonical
    /// one first (see ``precedes(_:_:)``).
    private var consoleIndex: [String: [URL]]?
    private var gameIndex: [String: [String: [URL]]] = [:]

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
        return try loadConsoleIndex().compactMap { deviceId, folders in
            guard let folder = folders.first, let info: VaultConsoleInfo = readJSON(folder.appending(path: Self.consoleFile)) else { return nil }
            return VaultConsole(info: VaultConsoleInfo(deviceId: deviceId, name: info.name), folderName: folder.lastPathComponent)
        }
        .sorted { $0.folderName.localizedStandardCompare($1.folderName) == .orderedAscending }
    }

    public func games(deviceId: String) throws -> [VaultGame] {
        gameIndex[deviceId] = nil
        var games: [VaultGame] = []
        for (_, folders) in try loadGameIndex(deviceId: deviceId) {
            guard let folder = folders.first, let info: VaultGameInfo = readJSON(folder.appending(path: Self.gameFile)) else { continue }
            let versions = Self.shown(try storedVersions(in: folders))
            guard !versions.isEmpty else { continue }
            let path = folder.deletingLastPathComponent().lastPathComponent + "/" + folder.lastPathComponent
            games.append(VaultGame(info: info, folderPath: path, latest: versions.first?.version, versionCount: versions.count))
        }
        return games.sorted {
            let system = $0.info.systemName.localizedStandardCompare($1.info.systemName)
            if system != .orderedSame { return system == .orderedAscending }
            let name = $0.info.name.localizedStandardCompare($1.info.name)
            if name != .orderedSame { return name == .orderedAscending }
            return $0.folderPath.localizedStandardCompare($1.folderPath) == .orderedAscending
        }
    }

    /// The versions of every folder of the game; a version identical to
    /// the one published just after it (another device synced the same
    /// save) is shown once, never deleted for that.
    public func versions(deviceId: String, gameId: String) throws -> [SaveVersion] {
        Self.shown(try storedVersions(deviceId: deviceId, gameId: gameId)).map(\.version)
    }

    /// The version is found by what it is (its `.version.json`), wherever
    /// it is now: a version moved by a tidy-up since it was listed is
    /// still found. Its files are checked (SHA-256): when they are not
    /// those of the version (changed by hand), an identical version is
    /// tried. Last resort, the folder with the identifier of `version`
    /// (``exportVerified`` then refuses what does not match).
    public func export(deviceId: String, gameId: String, version: SaveVersion, to directory: URL) throws -> ExportedVersion {
        func candidates() throws -> [StoredVersion] {
            let stored = try storedVersions(deviceId: deviceId, gameId: gameId)
            return stored.filter { $0.version.info == version.info }
                + stored.filter { $0.version.info != version.info && Self.sameContent($0.version.info, version.info) }
                + stored.filter { $0.version.id == version.id && !Self.sameContent($0.version.info, version.info) }
        }
        var found = try candidates()
        if found.isEmpty {
            // Moved by another instance since this one indexed the folders.
            gameIndex[deviceId] = nil
            found = try candidates()
        }
        var unverified: ExportedVersion?
        for candidate in found {
            guard let copy = try? copyFiles(of: candidate, to: directory) else { continue }
            let info = candidate.version.info
            let save = try? FileDigest.sha256Sync(of: copy.save)
            let rtc = try? copy.rtc.map { try FileDigest.sha256Sync(of: $0) }
            if save == info.save.sha256, rtc == info.rtc?.sha256, Self.sameContent(info, version.info) {
                if let unverified { try? FileManager.default.removeItem(at: unverified.directory) }
                return copy
            }
            if unverified == nil { unverified = copy } else { try? FileManager.default.removeItem(at: copy.directory) }
        }
        guard let unverified else { throw SaveVaultError.versionNotFound }
        return unverified
    }

    private func copyFiles(of stored: StoredVersion, to directory: URL) throws -> ExportedVersion {
        let info = stored.version.info
        let destination = directory.appending(path: UUID().uuidString, directoryHint: .isDirectory)
        let manager = FileManager.default
        try manager.createDirectory(at: destination, withIntermediateDirectories: true)
        do {
            var copied: [URL] = []
            for file in [info.save, info.rtc].compactMap({ $0 }) {
                let target = destination.appending(path: file.name)
                try coordinateReading(stored.folder.appending(path: file.name)) { url in
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
        //    versions already there (in every folder of the game).
        let gameFolder = try ensureGameFolder(console: console, game: game)
        let folders = try loadGameIndex(deviceId: console.deviceId)[game.gameId] ?? [gameFolder]
        let sequence = (try storedVersions(in: folders).map(\.version.info.sequence).max() ?? 0) + 1
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
        //    it is also excluded by its folder, whatever happens).
        try rotate(folders, keeping: destination)
        return SaveVersion(id: name, info: info)
    }

    /// Deletes the versions beyond the limit, counted as shown (identical
    /// versions published one after the other count once), oldest
    /// published first, in every folder of the game. Never `kept`, never a
    /// version not downloaded (iCloud), never the newest version that is
    /// downloaded (so one stays restorable on this device).
    private func rotate(_ folders: [URL], keeping kept: URL) throws {
        let stored = try storedVersions(in: folders)
        let groups = Self.groups(stored)
        guard groups.count > maxVersions else { return }
        let restorable = try stored.first { try isDownloaded($0.folder) }
        for group in groups.dropFirst(maxVersions) {
            for version in group where version.folder != kept && version.folder != restorable?.folder {
                try deleteVersion(version)
            }
        }
    }

    private func stageFile(_ file: NewVersionFile, as name: String, in stage: URL) throws -> SaveVersionFile {
        let target = stage.appending(path: name)
        try FileManager.default.moveItem(at: file.url, to: target)
        try Self.fsync(target)
        return SaveVersionFile(name: name, sha256: file.sha256, sizeBytes: try FileDigest.size(of: target))
    }

    // MARK: - Folders

    private func loadConsoleIndex() throws -> [String: [URL]] {
        if let consoleIndex { return consoleIndex }
        gameIndex = [:]
        let index = try consoleFolders()
        consoleIndex = index
        return index
    }

    /// Tidies the folders of the console first (moves only, see
    /// ``tidy(consoles:)``).
    private func loadGameIndex(deviceId: String) throws -> [String: [URL]] {
        if let games = gameIndex[deviceId] { return games }
        let consoles = try loadConsoleIndex()[deviceId] ?? []
        tidy(consoles: consoles)
        let index = try gameFolders(in: consoles)
        gameIndex[deviceId] = index
        return index
    }

    /// The console folders by device id, canonical first.
    func consoleFolders() throws -> [String: [URL]] {
        var index: [String: [URL]] = [:]
        for folder in Self.ranked(try subfolders(of: root)) {
            guard let info: VaultConsoleInfo = readJSON(folder.appending(path: Self.consoleFile)) else { continue }
            index[info.deviceId, default: []].append(folder)
        }
        return index
    }

    /// The game folders of console folders by game id, canonical first
    /// (by console, then system, then game folder, each by ``precedes(_:_:)``).
    func gameFolders(in consoles: [URL]) throws -> [String: [URL]] {
        var index: [String: [URL]] = [:]
        for console in consoles {
            for system in Self.ranked(try subfolders(of: console)) {
                for folder in Self.ranked(try subfolders(of: system)) {
                    guard let info: VaultGameInfo = readJSON(folder.appending(path: Self.gameFile)) else { continue }
                    index[info.gameId, default: []].append(folder)
                }
            }
        }
        return index
    }

    private func storedVersions(deviceId: String, gameId: String) throws -> [StoredVersion] {
        try storedVersions(in: loadGameIndex(deviceId: deviceId)[gameId] ?? [])
    }

    private func ensureConsoleFolder(_ console: VaultConsoleInfo) throws -> URL {
        if let folder = try loadConsoleIndex()[console.deviceId]?.first { return folder }
        try coordinateWriting(root) { try FileManager.default.createDirectory(at: $0, withIntermediateDirectories: true) }
        let folder = root.appending(path: try uniqueName(Self.folderName(console.name), in: root), directoryHint: .isDirectory)
        try createFolder(folder, metadata: Self.encoder().encode(console), file: Self.consoleFile)
        consoleIndex?[console.deviceId] = [folder]
        return folder
    }

    private func ensureGameFolder(console: VaultConsoleInfo, game: VaultGameInfo) throws -> URL {
        if let folder = try loadGameIndex(deviceId: console.deviceId)[game.gameId]?.first { return folder }
        let consoleFolder = try ensureConsoleFolder(console)
        let system = try systemFolder(Self.folderName(game.systemName), in: consoleFolder)
        let folder = system.appending(path: try uniqueName(Self.folderName(game.name), in: system), directoryHint: .isDirectory)
        try createFolder(folder, metadata: Self.encoder().encode(game), file: Self.gameFile)
        var games = try loadGameIndex(deviceId: console.deviceId)
        games[game.gameId] = [folder]
        gameIndex[console.deviceId] = games
        return folder
    }

    /// A folder and its metadata file, staged then moved in one rename.
    func createFolder(_ folder: URL, metadata: Data, file: String) throws {
        let manager = FileManager.default
        let stage = stagingDirectory.appending(path: UUID().uuidString, directoryHint: .isDirectory)
        try manager.createDirectory(at: stage, withIntermediateDirectories: true)
        defer { try? manager.removeItem(at: stage) }
        try Self.writeSynced(metadata, to: stage.appending(path: file))
        try coordinateMove(from: stage, to: folder)
    }

    /// A version and the folder holding it.
    struct StoredVersion {
        let version: SaveVersion
        let folder: URL
    }

    /// The versions of the folders of one game, last published first.
    /// A version of the canonical folder (the first) is identified by its
    /// folder name; one of another folder by its path in the vault, so
    /// that identifiers stay unique.
    func storedVersions(in games: [URL]) throws -> [StoredVersion] {
        var stored: [StoredVersion] = []
        for (index, game) in games.enumerated() {
            for folder in try subfolders(of: game) {
                guard let info: SaveVersionInfo = readJSON(folder.appending(path: Self.versionFile)) else { continue }
                let id = index == 0 ? folder.lastPathComponent : relativePath(folder)
                stored.append(StoredVersion(version: SaveVersion(id: id, info: info), folder: folder))
            }
        }
        return stored.sorted { Self.publishedAfter($0.version, $1.version) }
    }

    /// Publication order: sequence, then sync date, then identifier.
    nonisolated static func publishedAfter(_ a: SaveVersion, _ b: SaveVersion) -> Bool {
        if a.info.sequence != b.info.sequence { return a.info.sequence > b.info.sequence }
        if a.info.syncedAt != b.info.syncedAt { return a.info.syncedAt > b.info.syncedAt }
        return a.id > b.id
    }

    /// Versions in publication order, those identical to the version just
    /// before them grouped with it.
    nonisolated static func groups(_ stored: [StoredVersion]) -> [[StoredVersion]] {
        var groups: [[StoredVersion]] = []
        for version in stored {
            if let first = groups.last?.first, sameContent(first.version.info, version.version.info) {
                groups[groups.count - 1].append(version)
            } else {
                groups.append([version])
            }
        }
        return groups
    }

    /// One version per group (the last published).
    nonisolated static func shown(_ stored: [StoredVersion]) -> [StoredVersion] {
        groups(stored).map { $0[0] }
    }

    /// Whether two versions hold the same files (names, sizes, SHA-256).
    nonisolated static func sameContent(_ a: SaveVersionInfo, _ b: SaveVersionInfo) -> Bool {
        a.gameId == b.gameId && a.save == b.save && a.rtc == b.rtc
    }

    private func relativePath(_ url: URL) -> String {
        let base = root.standardizedFileURL.pathComponents
        let parts = url.standardizedFileURL.pathComponents
        guard parts.starts(with: base) else { return url.path(percentEncoded: false) }
        return parts.dropFirst(base.count).joined(separator: "/")
    }

    /// The folders in a folder, iCloud placeholders included (an item not
    /// downloaded yet shows as `.<name>.icloud`), hidden ones excluded.
    func subfolders(of folder: URL) throws -> [URL] {
        let names = try entries(of: folder)
        var result: [URL] = []
        for raw in names {
            if let name = Self.placeholderName(raw) {
                // Only a placeholder of a folder can be a folder: keep it,
                // the coordinated read of its metadata downloads it.
                if !name.hasPrefix(".") && !names.contains(name) {
                    let url = folder.appending(path: name, directoryHint: .isDirectory)
                    Self.startDownloading(url)
                    result.append(url)
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

    /// The names in a folder, sorted, placeholders as they are
    /// (`.<name>.icloud`); empty when the folder does not exist.
    func entries(of folder: URL) throws -> [String] {
        var names: [String] = []
        try coordinateReading(folder) { url in
            do {
                names = try FileManager.default.contentsOfDirectory(atPath: url.path(percentEncoded: false))
            } catch CocoaError.fileReadNoSuchFile {
                names = []
            }
        }
        return names.sorted()
    }

    /// `name` for the iCloud placeholder `.name.icloud` of an item not
    /// downloaded yet, else nil.
    nonisolated static func placeholderName(_ raw: String) -> String? {
        guard raw.hasPrefix("."), raw.hasSuffix(".icloud"), raw.count > ".icloud".count + 1 else { return nil }
        return String(raw.dropFirst().dropLast(".icloud".count))
    }

    /// Asks iCloud to download an item (no effect outside iCloud Drive).
    nonisolated static func startDownloading(_ url: URL) {
        try? FileManager.default.startDownloadingUbiquitousItem(at: url)
    }

    func uniqueName(_ base: String, in folder: URL) throws -> String {
        var taken = Set<String>()
        try coordinateReading(folder) { url in
            let names = (try? FileManager.default.contentsOfDirectory(atPath: url.path(percentEncoded: false))) ?? []
            for name in names {
                let plain = Self.placeholderName(name) ?? name
                taken.insert(plain.lowercased())
            }
        }
        if !taken.contains(base.lowercased()) { return base }
        var n = 2
        while taken.contains("\(base) (\(n))".lowercased()) { n += 1 }
        return "\(base) (\(n))"
    }

    // MARK: - Coordination

    func coordinateReading(_ url: URL, _ body: (URL) throws -> Void) throws {
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

    func coordinateWriting(_ url: URL, _ body: (URL) throws -> Void) throws {
        var coordinationError: NSError?
        var bodyError: Error?
        NSFileCoordinator(filePresenter: nil).coordinate(writingItemAt: url, options: [], error: &coordinationError) { url in
            do { try body(url) } catch { bodyError = error }
        }
        if let bodyError { throw bodyError }
        if let coordinationError { throw coordinationError }
    }

    func coordinateMove(from source: URL, to destination: URL) throws {
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

    func readJSON<T: Decodable>(_ url: URL) -> T? {
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
