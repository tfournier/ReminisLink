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
    /// Tests only: runs once a duplicate folder is in the holding area,
    /// before it is checked (see ``removeIfEmpty(_:metadata:)``).
    var holdingHook: (@Sendable (URL) -> Void)?

    func setHoldingHook(_ hook: (@Sendable (URL) -> Void)?) { holdingHook = hook }

    static let consoleFile = ".console.json"
    static let gameFile = ".game.json"
    static let versionFile = SaveVersionInfo.manifestName
    static let stateVersionFile = StateVersionInfo.manifestName

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
        // From disk: console folders delivered since the last load too.
        consoleIndex = nil
        var games: [VaultGame] = []
        for (_, folders) in try loadGameIndex(deviceId: deviceId) {
            guard let folder = folders.first, let info: VaultGameInfo = readJSON(folder.appending(path: Self.gameFile)) else { continue }
            let versions = Self.shown(try storedVersions(in: folders))
            let states = Self.shownBySeries(try stored(StateVersionInfo.self, in: folders))
            guard !versions.isEmpty || !states.isEmpty else { continue }
            let path = folder.deletingLastPathComponent().lastPathComponent + "/" + folder.lastPathComponent
            let slots = Array(Set(states.map(\.info.slot))).sorted()
            let latestState = states.max { $0.info.syncedAt < $1.info.syncedAt }.map { StateVersion(id: $0.id, info: $0.info) }
            games.append(VaultGame(
                info: info, folderPath: path, latest: versions.first?.version, versionCount: versions.count,
                stateSlots: slots, latestState: latestState
            ))
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
        Self.shown(try storedVersions(in: currentGameFolders(deviceId: deviceId, gameId: gameId, name: nil))).map(\.version)
    }

    /// The version is found by what it is (its `.version.json`), wherever
    /// it is now: a version moved since it was listed is still found. The
    /// copy is checked (SHA-256) against `version`: when it does not match
    /// (a file changed by hand), an identical version is tried, then every
    /// candidate again after reloading the folders from disk. Never
    /// returns bytes that are not those of `version`.
    public func export(deviceId: String, gameId: String, version: SaveVersion, to directory: URL) throws -> ExportedVersion {
        let copied = try exportRecord(deviceId: deviceId, gameId: gameId, info: version.info, id: version.id, to: directory)
        return ExportedVersion(save: copied[0], rtc: copied.count > 1 ? copied[1] : nil)
    }

    public func stateVersions(deviceId: String, gameId: String) throws -> [StateVersion] {
        Self.shownBySeries(try stored(StateVersionInfo.self, in: currentGameFolders(deviceId: deviceId, gameId: gameId, name: nil)))
            .sorted { $0.info.slot != $1.info.slot ? $0.info.slot < $1.info.slot : Self.publishedAfter($0, $1) }
            .map { StateVersion(id: $0.id, info: $0.info) }
    }

    public func exportState(deviceId: String, gameId: String, version: StateVersion, to directory: URL) throws -> ExportedStateVersion {
        let copied = try exportRecord(deviceId: deviceId, gameId: gameId, info: version.info, id: version.id, to: directory)
        return ExportedStateVersion(state: copied[0], thumbnail: copied.count > 1 ? copied[1] : nil)
    }

    /// The version is found by what it is (its manifest), wherever it is
    /// now: a version moved since it was listed is still found. The copy
    /// is checked (SHA-256) against `info`: when it does not match (a file
    /// changed by hand), an identical version is tried, then every
    /// candidate again after reloading the folders from disk. Never
    /// returns bytes that are not those of `info`. Returns the copies of
    /// its files, in the order of ``VaultVersionRecord/files``.
    private func exportRecord<I: VaultVersionRecord>(deviceId: String, gameId: String, info: I, id: String, to directory: URL) throws -> [URL] {
        /// A verified copy, or whether a candidate (or a folder with the
        /// identifier of the version) was seen.
        func attempt(_ folders: [URL]) throws -> (copy: [URL]?, seen: Bool) {
            let stored = try stored(I.self, in: folders)
            let candidates = stored.filter { $0.info == info }
                + stored.filter { $0.info != info && Self.sameContent($0.info, info) }
            for candidate in candidates {
                guard let copy = try? copyFiles(of: candidate, to: directory) else { continue }
                let sums = copy.map { try? FileDigest.sha256Sync(of: $0) }
                if sums == info.files.map(\.sha256) { return (copy, true) }
                try? FileManager.default.removeItem(at: copy[0].deletingLastPathComponent())
            }
            return (nil, !candidates.isEmpty || stored.contains { $0.id == id })
        }
        let first = try attempt(currentGameFolders(deviceId: deviceId, gameId: gameId, name: nil))
        if let copy = first.copy { return copy }
        // Moved by another instance, or a good copy arrived since: reload.
        consoleIndex = nil
        let second = try attempt(loadGameIndex(deviceId: deviceId)[gameId] ?? [])
        if let copy = second.copy { return copy }
        throw first.seen || second.seen ? SaveVaultError.versionMismatch : SaveVaultError.versionNotFound
    }

    private func copyFiles<I: VaultVersionRecord>(of stored: StoredRecord<I>, to directory: URL) throws -> [URL] {
        let destination = directory.appending(path: UUID().uuidString, directoryHint: .isDirectory)
        let manager = FileManager.default
        try manager.createDirectory(at: destination, withIntermediateDirectories: true)
        do {
            var copied: [URL] = []
            for file in stored.info.files {
                let target = destination.appending(path: file.name)
                try coordinateReading(stored.folder.appending(path: file.name)) { url in
                    try manager.copyItem(at: url, to: target)
                }
                copied.append(target)
            }
            guard !copied.isEmpty else { throw SaveVaultError.versionNotFound }
            return copied
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
        let (id, info) = try publish(
            console: console, game: game, series: "save", files: [save] + (rtc.map { [$0] } ?? []),
            folderName: { Self.versionName($0, timeZone: self.timeZone) }
        ) { sequence, syncedAt, files in
            SaveVersionInfo(
                gameId: game.gameId, sequence: sequence, syncedAt: syncedAt, modifiedAt: modifiedAt,
                save: files[0], rtc: files.count > 1 ? files[1] : nil
            )
        }
        return SaveVersion(id: id, info: info)
    }

    public func addStateVersion(
        console: VaultConsoleInfo, game: VaultGameInfo, slot: Int,
        state: NewVersionFile, thumbnail: NewVersionFile?, modifiedAt: Date?
    ) throws -> StateVersion {
        let (id, info) = try publish(
            console: console, game: game, series: "state/\(slot)", files: [state] + (thumbnail.map { [$0] } ?? []),
            folderName: { Self.stateVersionName($0, slot: slot, timeZone: self.timeZone) }
        ) { sequence, syncedAt, files in
            StateVersionInfo(
                gameId: game.gameId, slot: slot, sequence: sequence, syncedAt: syncedAt, modifiedAt: modifiedAt,
                state: files[0], thumbnail: files.count > 1 ? files[1] : nil
            )
        }
        return StateVersion(id: id, info: info)
    }

    /// Publishes a new version of a series of a game (the files are moved
    /// in): assembled outside the vault, numbered after the versions of the
    /// series already there (in every folder of the game, as on disk now:
    /// another instance or device may have written since), moved into the
    /// game folder in one rename, then the series is rotated.
    private func publish<I: VaultVersionRecord>(
        console: VaultConsoleInfo, game: VaultGameInfo, series: String, files: [NewVersionFile],
        folderName: (Date) -> String,
        makeInfo: (_ sequence: Int, _ syncedAt: Date, _ files: [SaveVersionFile]) -> I
    ) throws -> (id: String, info: I) {
        let names = try files.map { try Self.checkedFileName($0.name) }
        if Set(names.map { $0.lowercased() }).count != names.count { throw SaveVaultError.invalidName(names.last ?? "") }

        // 1. Assemble the version outside the vault.
        var folders = try currentGameFolders(deviceId: console.deviceId, gameId: game.gameId, name: game.name)
        if folders.isEmpty { folders = [try ensureGameFolder(console: console, game: game)] }
        let gameFolder = folders[0]
        let sequence = (try stored(I.self, in: folders).filter { $0.info.series == series }.map(\.info.sequence).max() ?? 0) + 1
        let manager = FileManager.default
        let stage = stagingDirectory.appending(path: UUID().uuidString, directoryHint: .isDirectory)
        try manager.createDirectory(at: stage, withIntermediateDirectories: true)
        defer { try? manager.removeItem(at: stage) }
        let staged = try zip(files, names).map { try stageFile($0, as: $1, in: stage) }
        let info = makeInfo(sequence, now(), staged)
        try Self.writeSynced(Self.encoder().encode(info), to: stage.appending(path: I.manifestName))

        // 2. Move it into the game folder in one rename.
        let name = try uniqueName(folderName(info.syncedAt), in: gameFolder)
        let destination = gameFolder.appending(path: name, directoryHint: .isDirectory)
        try coordinateMove(from: stage, to: destination)

        // 3. Only then, rotate (the new version has the highest sequence;
        //    it is also excluded by its folder, whatever happens).
        try rotate(I.self, series: series, in: folders, keeping: destination)
        return (name, info)
    }

    /// Deletes the versions of a series beyond the limit (the M9 retention
    /// policy), counted as shown (identical versions published one after
    /// the other count once), oldest published first, in every folder of
    /// the game. Never `kept`, never the newest restorable version (files
    /// present, downloaded and matching their SHA-256), and only a version
    /// whose folder holds exactly its listed files, downloaded and
    /// matching (checked again inside the coordinated deletion): anything
    /// else (a placeholder, a file changed or added by hand, a folder that
    /// cannot be read) is kept.
    private func rotate<I: VaultVersionRecord>(_ type: I.Type, series: String, in folders: [URL], keeping kept: URL) throws {
        let stored = try stored(I.self, in: folders).filter { $0.info.series == series }
        let groups = Self.groups(stored)
        guard groups.count > maxVersions else { return }
        let restorable = stored.first { checkVersion($0).restorable }
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
        cleanHoldingArea()
        let index = try consoleFolders()
        consoleIndex = index
        return index
    }

    /// Re-lists the console folders from disk (cheap): the game index of a
    /// device whose console folders changed (one delivered by iCloud since
    /// the last load) is dropped.
    private func refreshConsoles() throws {
        if consoleIndex == nil { cleanHoldingArea() }
        let fresh = try consoleFolders()
        if let consoleIndex {
            for deviceId in Set(consoleIndex.keys).union(fresh.keys) where consoleIndex[deviceId] != fresh[deviceId] {
                gameIndex[deviceId] = nil
            }
        } else {
            gameIndex = [:]
        }
        consoleIndex = fresh
    }

    /// The folders of a game as on disk now: those of the index, plus any
    /// folder of the console with the same name (without duplicate suffix)
    /// as one of them or as `name` that holds the game (a folder another
    /// instance or device wrote since the index was made). Canonical first.
    private func currentGameFolders(deviceId: String, gameId: String, name: String?) throws -> [URL] {
        try refreshConsoles()
        let indexed = try loadGameIndex(deviceId: deviceId)[gameId] ?? []
        let names = indexed.map(\.lastPathComponent) + (name.map { [Self.folderName($0)] } ?? [])
        let bases = Set(names.map { Self.duplicateRank($0).base.lowercased() })
        guard !bases.isEmpty else { return indexed }
        let infos = GameInfoCache()
        var found: [URL] = []
        for console in consoleIndex?[deviceId] ?? [] {
            for system in Self.ranked(try subfolders(of: console)) {
                for folder in Self.ranked(try subfolders(of: system))
                where bases.contains(Self.duplicateRank(folder.lastPathComponent).base.lowercased()) {
                    if gameInfo(folder, infos)?.gameId == gameId { found.append(folder) }
                }
            }
        }
        guard !found.isEmpty else { return indexed }
        gameIndex[deviceId]?[gameId] = found
        return found
    }

    /// Tidies the folders of the console first (moves only, see
    /// ``tidy(consoles:infos:)``).
    private func loadGameIndex(deviceId: String) throws -> [String: [URL]] {
        if let games = gameIndex[deviceId] { return games }
        let consoles = try loadConsoleIndex()[deviceId] ?? []
        var infos = GameInfoCache()
        if tidy(consoles: consoles, infos: infos) { infos = GameInfoCache() }
        let index = try gameFolders(in: consoles, infos: infos)
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
    func gameFolders(in consoles: [URL], infos: GameInfoCache) throws -> [String: [URL]] {
        var index: [String: [URL]] = [:]
        for console in consoles {
            for system in Self.ranked(try subfolders(of: console)) {
                for folder in Self.ranked(try subfolders(of: system)) {
                    guard let info = gameInfo(folder, infos) else { continue }
                    index[info.gameId, default: []].append(folder)
                }
            }
        }
        return index
    }

    /// The `.game.json` of game folders, read once per load.
    final class GameInfoCache {
        var infos: [URL: VaultGameInfo?] = [:]
    }

    func gameInfo(_ folder: URL, _ cache: GameInfoCache) -> VaultGameInfo? {
        if let cached = cache.infos[folder] { return cached }
        let info: VaultGameInfo? = readJSON(folder.appending(path: Self.gameFile))
        cache.infos[folder] = info
        return info
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
    struct StoredRecord<Info: VaultVersionRecord> {
        /// The name of its folder (the canonical game folder), or its path
        /// in the vault (another folder of the game).
        let id: String
        let info: Info
        let folder: URL
    }

    typealias StoredVersion = StoredRecord<SaveVersionInfo>

    /// The save versions of the folders of one game, last published first.
    func storedVersions(in games: [URL]) throws -> [StoredVersion] {
        try stored(SaveVersionInfo.self, in: games)
    }

    /// The versions of one kind of the folders of one game, last published
    /// first. A version of the canonical folder (the first) is identified
    /// by its folder name; one of another folder by its path in the vault,
    /// so that identifiers stay unique.
    func stored<I: VaultVersionRecord>(_ type: I.Type, in games: [URL]) throws -> [StoredRecord<I>] {
        var stored: [StoredRecord<I>] = []
        for (index, game) in games.enumerated() {
            for folder in try subfolders(of: game) {
                guard let info: I = readJSON(folder.appending(path: I.manifestName)) else { continue }
                let id = index == 0 ? folder.lastPathComponent : relativePath(folder)
                stored.append(StoredRecord(id: id, info: info, folder: folder))
            }
        }
        return stored.sorted { Self.publishedAfter($0, $1) }
    }

    /// Publication order: sequence, then sync date, then version folder
    /// name, then the manifest: nothing that changes when a version moves
    /// to another game folder.
    nonisolated static func publishedAfter<I>(_ a: StoredRecord<I>, _ b: StoredRecord<I>) -> Bool {
        let x = a.info, y = b.info
        if x.sequence != y.sequence { return x.sequence > y.sequence }
        if x.syncedAt != y.syncedAt { return x.syncedAt > y.syncedAt }
        let nx = a.folder.lastPathComponent, ny = b.folder.lastPathComponent
        if nx != ny { return nx > ny }
        return x.manifestKey > y.manifestKey
    }

    /// Versions in publication order, those identical to the version just
    /// before them grouped with it.
    nonisolated static func groups<I>(_ stored: [StoredRecord<I>]) -> [[StoredRecord<I>]] {
        var groups: [[StoredRecord<I>]] = []
        for version in stored {
            if let first = groups.last?.first, sameContent(first.info, version.info) {
                groups[groups.count - 1].append(version)
            } else {
                groups.append([version])
            }
        }
        return groups
    }

    /// One version per group (the last published).
    nonisolated static func shown<I>(_ stored: [StoredRecord<I>]) -> [StoredRecord<I>] {
        groups(stored).map { $0[0] }
    }

    /// ``shown(_:)`` for each series (state slot), in publication order.
    nonisolated static func shownBySeries<I>(_ stored: [StoredRecord<I>]) -> [StoredRecord<I>] {
        let series = Dictionary(grouping: stored, by: \.info.series)
        return series.values.flatMap { shown($0) }.sorted { publishedAfter($0, $1) }
    }

    /// Whether two versions hold the same files (names, sizes, SHA-256) of
    /// the same series of the same game.
    nonisolated static func sameContent<I: VaultVersionRecord>(_ a: I, _ b: I) -> Bool {
        a.gameId == b.gameId && a.series == b.series && a.files == b.files
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

    /// `2026-11-19 15.42.10 state 3`: the folder of a state version (M19),
    /// next to the save versions of the game.
    public static func stateVersionName(_ date: Date, slot: Int, timeZone: TimeZone) -> String {
        "\(versionName(date, timeZone: timeZone)) state \(slot)"
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
        guard !safe.isEmpty, safe == name, !name.hasPrefix("."), name != Self.versionFile, name != Self.stateVersionFile else {
            throw SaveVaultError.invalidName(name)
        }
        return name
    }
}

extension FileSaveVault.StoredRecord where Info == SaveVersionInfo {
    var version: SaveVersion { SaveVersion(id: id, info: info) }
}
