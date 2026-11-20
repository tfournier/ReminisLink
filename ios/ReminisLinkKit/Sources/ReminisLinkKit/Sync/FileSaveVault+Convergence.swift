import Foundation

// Several devices write one vault (the iPhone and a Mac of the same Apple
// account, the pairing shared through iCloud Keychain). A device that
// syncs before iCloud delivered the folder made by the other creates its
// own; iCloud then renames one of them ("Game Boy Advance (GBA) 2"). Each
// load merges the folders that hold the same item into one canonical
// folder, chosen the same way on every device:
//
// - console folders with the same `deviceId` (`.console.json`);
// - system folders of a console whose games (`.game.json`) all name the
//   same system (`systemName`), whatever the folder names;
// - game folders of a console with the same `gameId` (`.game.json`).
//
// The content of a duplicate is moved into the canonical folder (a free
// name when it is taken); a version identical to one of the canonical
// folder (same files, same SHA-256, checked on disk) is dropped instead,
// each canonical version absorbing at most one. Then the version limit
// applies. A duplicate is deleted only once it holds nothing but its
// metadata. What is not downloaded yet (iCloud placeholder) is neither
// moved nor deleted: its download is asked and the next load retries.
// Merging is best effort and idempotent: an error leaves the folders as
// they are (no version is lost), the next load goes on.

extension FileSaveVault {
    // MARK: - Canonical folder

    /// Whether the folder named `a` comes before the folder named `b`
    /// among folders holding the same item (the first is the canonical
    /// one): by name without a duplicate suffix (` 2` from iCloud, ` (2)`
    /// from the vault), then by that suffix (none first), then by name.
    /// Plain code point order: the same on every device.
    nonisolated static func precedes(_ a: String, _ b: String) -> Bool {
        let ra = duplicateRank(a)
        let rb = duplicateRank(b)
        if ra.base != rb.base { return ra.base.unicodeScalars.lexicographicallyPrecedes(rb.base.unicodeScalars) }
        if ra.number != rb.number { return ra.number < rb.number }
        return a.precomposedStringWithCanonicalMapping.unicodeScalars
            .lexicographicallyPrecedes(b.precomposedStringWithCanonicalMapping.unicodeScalars)
    }

    /// `("X", 2)` for `X 2` and `X (2)`, `("X", 1)` for `X`.
    nonisolated static func duplicateRank(_ name: String) -> (base: String, number: Int) {
        let name = name.precomposedStringWithCanonicalMapping
        func number(_ digits: Substring) -> Int? {
            guard !digits.isEmpty, digits.allSatisfy({ $0.isASCII && $0.isNumber }) else { return nil }
            return Int(digits)
        }
        if name.hasSuffix(")"), let open = name.range(of: " (", options: .backwards) {
            let digits = name[open.upperBound..<name.index(before: name.endIndex)]
            if let n = number(digits), open.lowerBound > name.startIndex { return (String(name[..<open.lowerBound]), n) }
        }
        if let space = name.lastIndex(of: " "), space > name.startIndex,
           let n = number(name[name.index(after: space)...]) {
            return (String(name[..<space]), n)
        }
        return (name, 1)
    }

    nonisolated static func ranked(_ folders: [URL]) -> [URL] {
        folders.sorted { precedes($0.lastPathComponent, $1.lastPathComponent) }
    }

    // MARK: - Merging

    /// Merges the console folders of one device.
    func convergeConsoles() {
        guard let groups = try? consoleFolders() else { return }
        for (_, folders) in groups.sorted(by: { $0.key < $1.key }) where folders.count > 1 {
            for duplicate in folders.dropFirst() {
                _ = try? mergeFolder(duplicate, into: folders[0], metadata: Self.consoleFile, mergeSameNamed: true)
            }
        }
    }

    /// Merges the system folders, then the game folders, of a console folder.
    func convergeConsole(_ console: URL) {
        var systems: [String: [URL]] = [:]
        for system in Self.ranked((try? subfolders(of: console)) ?? []) {
            if let identity = systemIdentity(system) { systems[identity, default: []].append(system) }
        }
        for (identity, folders) in systems.sorted(by: { $0.key < $1.key }) where folders.count > 1 {
            // The folder named after the system first (then the ranking).
            let ordered = folders.filter { $0.lastPathComponent.lowercased() == identity.lowercased() }
                + folders.filter { $0.lastPathComponent.lowercased() != identity.lowercased() }
            for duplicate in ordered.dropFirst() {
                _ = try? mergeFolder(duplicate, into: ordered[0], metadata: nil, mergeSameNamed: false)
            }
        }
        guard let games = try? gameFolders(in: [console]) else { return }
        for (_, folders) in games.sorted(by: { $0.key < $1.key }) where folders.count > 1 {
            for duplicate in folders.dropFirst() {
                try? mergeGame(duplicate, into: folders[0])
            }
        }
    }

    /// The system a system folder holds: the folder name of the
    /// `systemName` of its games, when they all agree (nil without a
    /// readable game, or with games of several systems).
    func systemIdentity(_ system: URL) -> String? {
        var names = Set<String>()
        for game in (try? subfolders(of: system)) ?? [] {
            if let info: VaultGameInfo = readJSON(game.appending(path: Self.gameFile)) {
                names.insert(Self.folderName(info.systemName))
            }
        }
        return names.count == 1 ? names.first : nil
    }

    /// The system folder for a new game (`name`: the folder name of its
    /// system): the canonical folder holding that system if there is one
    /// (it may carry an iCloud suffix), else the folder with that name
    /// (also a placeholder still downloading), else a new one.
    func systemFolder(_ name: String, in console: URL) throws -> URL {
        var byIdentity: [URL] = []
        var byName: URL?
        for system in Self.ranked(try subfolders(of: console)) {
            if system.lastPathComponent.lowercased() == name.lowercased() { byName = system }
            if systemIdentity(system) == name { byIdentity.append(system) }
        }
        let folder = byIdentity.first(where: { $0 == byName }) ?? byIdentity.first ?? byName
            ?? console.appending(path: name, directoryHint: .isDirectory)
        try coordinateWriting(folder) { try FileManager.default.createDirectory(at: $0, withIntermediateDirectories: true) }
        return folder
    }

    /// Moves the content of `duplicate` into `target`, then deletes
    /// `duplicate` if nothing but its `metadata` file is left. With
    /// `mergeSameNamed`, a folder whose name is taken in `target` by a
    /// folder is merged into it (one level). Returns whether everything
    /// was moved.
    func mergeFolder(_ duplicate: URL, into target: URL, metadata: String?, mergeSameNamed: Bool) throws -> Bool {
        var complete = true
        for raw in try entries(of: duplicate) where raw != metadata && raw != Self.finderFile {
            if let name = Self.placeholderName(raw) {
                Self.startDownloading(duplicate.appending(path: name))
                complete = false
                continue
            }
            // A hidden file of the user: kept, and its folder with it.
            guard !raw.hasPrefix(".") else { complete = false; continue }
            let item = duplicate.appending(path: raw)
            if mergeSameNamed, Self.isDirectory(item), let existing = try existingChild(raw, in: target), Self.isDirectory(existing) {
                if try !mergeFolder(item, into: existing, metadata: nil, mergeSameNamed: false) { complete = false }
                continue
            }
            try coordinateMove(from: item, to: target.appending(path: try uniqueName(raw, in: target)))
        }
        if complete { try deleteIfEmpty(duplicate, metadata: metadata) }
        return complete
    }

    /// Moves the versions of a duplicate game folder into `target`,
    /// dropping those identical to a version of `target`, then applies
    /// the version limit to `target`.
    func mergeGame(_ duplicate: URL, into target: URL) throws {
        // Each version of the canonical folder absorbs at most one identical
        // duplicate: a save restored to an older content stays a version.
        var twins = try versions(in: target)
        var complete = true
        for raw in try entries(of: duplicate) where raw != Self.gameFile && raw != Self.finderFile {
            if let name = Self.placeholderName(raw) {
                Self.startDownloading(duplicate.appending(path: name))
                complete = false
                continue
            }
            guard !raw.hasPrefix(".") else { complete = false; continue }
            let item = duplicate.appending(path: raw)
            if Self.isDirectory(item) {
                guard try isDownloaded(item) else { complete = false; continue }
                if let info: SaveVersionInfo = readJSON(item.appending(path: Self.versionFile)),
                   let index = twins.firstIndex(where: { Self.sameContent($0.info, info) }) {
                    let twin = target.appending(path: twins[index].id, directoryHint: .isDirectory)
                    guard try isDownloaded(twin) else { complete = false; continue }
                    // Dropped only if the copy kept is really there.
                    if hasFiles(of: twins[index].info, in: twin) {
                        try coordinateDeleting(item)
                        twins.remove(at: index)
                        continue
                    }
                }
            }
            try coordinateMove(from: item, to: target.appending(path: try uniqueName(raw, in: target)))
        }
        if complete { try deleteIfEmpty(duplicate, metadata: Self.gameFile) }
        try rotate(target, keeping: nil)
    }

    /// Whether two versions hold the same files (names, sizes, SHA-256).
    nonisolated static func sameContent(_ a: SaveVersionInfo, _ b: SaveVersionInfo) -> Bool {
        a.gameId == b.gameId && a.save == b.save && a.rtc == b.rtc
    }

    /// Whether the files of a version are in `folder`, with their SHA-256.
    func hasFiles(of info: SaveVersionInfo, in folder: URL) -> Bool {
        for file in [info.save, info.rtc].compactMap({ $0 }) {
            var sha: String?
            try? coordinateReading(folder.appending(path: file.name)) { sha = try FileDigest.sha256Sync(of: $0) }
            guard sha == file.sha256 else { return false }
        }
        return true
    }

    /// Whether every item of a folder is downloaded; asks iCloud to
    /// download the others.
    func isDownloaded(_ folder: URL) throws -> Bool {
        var downloaded = true
        for raw in try entries(of: folder) {
            if let name = Self.placeholderName(raw) {
                Self.startDownloading(folder.appending(path: name))
                downloaded = false
                continue
            }
            let url = folder.appending(path: raw)
            let status = try? url.resourceValues(forKeys: [.ubiquitousItemDownloadingStatusKey]).ubiquitousItemDownloadingStatus
            if status == .notDownloaded {
                Self.startDownloading(url)
                downloaded = false
            }
        }
        return downloaded
    }

    /// Deletes a folder emptied by a merge: nothing but its metadata file
    /// (and the Finder's `.DS_Store`) is left in it.
    func deleteIfEmpty(_ folder: URL, metadata: String?) throws {
        let left = try entries(of: folder).filter { $0 != metadata && $0 != Self.finderFile }
        guard left.isEmpty else { return }
        try coordinateDeleting(folder)
    }

    /// The folder named `name` in `folder` (whatever its case), if it is
    /// on this device.
    func existingChild(_ name: String, in folder: URL) throws -> URL? {
        guard let match = try entries(of: folder).first(where: { $0.lowercased() == name.lowercased() }) else { return nil }
        return folder.appending(path: match, directoryHint: .isDirectory)
    }

    nonisolated static func isDirectory(_ url: URL) -> Bool {
        var isDirectory: ObjCBool = false
        return FileManager.default.fileExists(atPath: url.path(percentEncoded: false), isDirectory: &isDirectory) && isDirectory.boolValue
    }

    static let finderFile = ".DS_Store"
}
