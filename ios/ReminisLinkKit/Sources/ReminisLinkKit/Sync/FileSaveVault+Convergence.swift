import Foundation

// Several devices write one vault (the iPhone and a Mac of the same Apple
// account, the pairing shared through iCloud Keychain). A device that
// syncs before iCloud delivered the folder made by the other creates its
// own; iCloud then renames one of them ("Game Boy Advance (GBA) 2").
//
// 1. Reads never depend on folders being merged: the versions of a game
//    are those of every folder of that game (same `gameId` in
//    `.game.json`, in every folder of the console, same `deviceId` in
//    `.console.json`). Identical versions published one after the other
//    (two devices synced the same save) are shown once, both kept.
//
// 2. Folders are then tidied, by moves only: nothing is deleted or
//    overwritten. An item moves into the canonical folder of its console,
//    system or game only if its name is free there and nothing in it is
//    still an iCloud placeholder (checked again inside the coordinated
//    move); a duplicate folder is removed only once empty (checked inside
//    the coordinated removal, `rmdir`). Folders whose identity is not
//    known yet (metadata not downloaded) are left where they are, and so
//    are the groups they could belong to: the canonical folder never
//    changes when that metadata arrives.
//
// The canonical folder is chosen by name only, the same on every device:
// see ``FileSaveVault/precedes(_:_:)``.

extension FileSaveVault {
    // MARK: - Canonical folder

    /// Whether the folder named `a` comes before the folder named `b`
    /// among folders holding the same item (the first is the canonical
    /// one): a name without duplicate suffix (` 2` from iCloud, ` (2)`
    /// from the vault) first, then the smaller suffix, then the name in
    /// code point order (Unicode-normalized): the same on every device.
    nonisolated static func precedes(_ a: String, _ b: String) -> Bool {
        let na = duplicateRank(a).number
        let nb = duplicateRank(b).number
        if na != nb { return na < nb }
        return a.precomposedStringWithCanonicalMapping.unicodeScalars
            .lexicographicallyPrecedes(b.precomposedStringWithCanonicalMapping.unicodeScalars)
    }

    /// `("X", 2)` for `X 2` and `X (2)`, `("X", 1)` for `X`.
    nonisolated static func duplicateRank(_ name: String) -> (base: String, number: Int) {
        let name = name.precomposedStringWithCanonicalMapping
        func number(_ digits: Substring) -> Int? {
            guard !digits.isEmpty, digits.allSatisfy({ $0.isASCII && $0.isNumber }),
                  let n = Int(digits), n >= 2 else { return nil }
            return n
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

    /// Whether a folder of unknown identity could belong to the group
    /// (same name without duplicate suffix): the group is then left alone.
    nonisolated static func mayBelong(_ unknown: [URL], to group: [URL]) -> Bool {
        let bases = Set(group.map { duplicateRank($0.lastPathComponent).base.lowercased() })
        return unknown.contains { bases.contains(duplicateRank($0.lastPathComponent).base.lowercased()) }
    }

    // MARK: - Tidying

    /// Tidies the folders of one console (its folders, canonical first).
    /// Best effort: an error leaves things where they are. Returns whether
    /// anything moved or was removed.
    func tidy(consoles: [URL], infos: GameInfoCache) -> Bool {
        guard let canonical = consoles.first else { return false }
        var changed = false
        cleanHoldingArea()
        let rootUnknown = ((try? subfolders(of: root)) ?? []).filter {
            (readJSON($0.appending(path: Self.consoleFile)) as VaultConsoleInfo?) == nil
        }
        let consolesKnown = consoles.count < 2 || !Self.mayBelong(rootUnknown, to: consoles)

        var games = (try? gameFolders(in: consolesKnown ? consoles : [canonical], infos: infos)) ?? [:]

        // Console folders: a system folder of known identity goes to the
        // canonical console folder: its games with no other folder join
        // the canonical system folder of the same identity if there is
        // one, else the whole folder moves if its name is free. A folder
        // of unknown identity, or whose name is taken by another system,
        // stays.
        if consolesKnown {
            for duplicate in consoles.dropFirst() {
                for system in (try? subfolders(of: duplicate)) ?? [] {
                    guard let identity = systemIdentity(system, infos) else { continue }
                    let targets = Self.ranked((try? subfolders(of: canonical)) ?? [])
                    if let target = targets.first(where: { systemIdentity($0, infos) == identity }) {
                        for game in (try? subfolders(of: system)) ?? [] {
                            guard let info = gameInfo(game, infos), games[info.gameId]?.count == 1 else { continue }
                            if (try? moveIfReady(game, into: target)) == true { changed = true }
                        }
                    } else if (try? moveIfReady(system, into: canonical)) == true {
                        changed = true
                    }
                }
            }
            if changed {
                infos.infos = [:]
                games = (try? gameFolders(in: consoles, infos: infos)) ?? games
            }
        }

        // System folders of the canonical console holding the same system.
        var systems: [String: [URL]] = [:]
        var unknownSystems: [URL] = []
        for system in Self.ranked((try? subfolders(of: canonical)) ?? []) {
            if let identity = systemIdentity(system, infos) {
                systems[identity, default: []].append(system)
            } else {
                unknownSystems.append(system)
            }
        }
        var emptied: [URL] = []
        for (_, folders) in systems.sorted(by: { $0.key < $1.key }) where folders.count > 1 {
            guard !Self.mayBelong(unknownSystems, to: folders) else { continue }
            for duplicate in folders.dropFirst() {
                // A game with no other folder moves whole.
                for game in (try? subfolders(of: duplicate)) ?? [] {
                    guard let info = gameInfo(game, infos), games[info.gameId]?.count == 1 else { continue }
                    if (try? moveIfReady(game, into: folders[0])) == true { changed = true }
                }
                emptied.append(duplicate)
            }
        }

        // Game folders holding the same game: their versions (and anything
        // else) move into the canonical game folder.
        let duplicated = games.filter { $0.value.count > 1 }
        var unknownGames: [URL] = []
        if !duplicated.isEmpty {
            for console in consolesKnown ? consoles : [canonical] {
                for system in (try? subfolders(of: console)) ?? [] {
                    unknownGames += ((try? subfolders(of: system)) ?? []).filter { gameInfo($0, infos) == nil }
                }
            }
        }
        for (_, folders) in duplicated.sorted(by: { $0.key < $1.key }) {
            guard !Self.mayBelong(unknownGames, to: folders) else { continue }
            for duplicate in folders.dropFirst() {
                for raw in (try? entries(of: duplicate)) ?? [] {
                    if let name = Self.placeholderName(raw) { Self.startDownloading(duplicate.appending(path: name)) }
                    guard !raw.hasPrefix(".") else { continue }
                    if (try? moveIfReady(duplicate.appending(path: raw), into: folders[0])) == true { changed = true }
                }
                if (try? removeIfEmpty(duplicate, metadata: Self.gameFile)) == true { changed = true }
            }
        }

        // Duplicate system folders, then console folders, left empty.
        for system in emptied where (try? removeIfEmpty(system, metadata: nil)) == true { changed = true }
        if consolesKnown {
            for duplicate in consoles.dropFirst() {
                for system in (try? subfolders(of: duplicate)) ?? [] where (try? removeIfEmpty(system, metadata: nil)) == true {
                    changed = true
                }
                if (try? removeIfEmpty(duplicate, metadata: Self.consoleFile)) == true { changed = true }
            }
        }
        return changed
    }

    /// The system a system folder holds: the folder name of the
    /// `systemName` of its games, when every game folder in it is readable
    /// and they agree (nil otherwise: unknown).
    func systemIdentity(_ system: URL, _ infos: GameInfoCache) -> String? {
        var names = Set<String>()
        for game in (try? subfolders(of: system)) ?? [] {
            guard let info = gameInfo(game, infos) else { return nil }
            names.insert(Self.folderName(info.systemName))
        }
        return names.count == 1 ? names.first : nil
    }

    /// The system folder for a new game (`name`: the folder name of its
    /// system): the folder with that name (also a placeholder still
    /// downloading: the canonical name), else the canonical folder holding
    /// that system under another name (an iCloud suffix), else a new one.
    func systemFolder(_ name: String, in console: URL) throws -> URL {
        let systems = Self.ranked(try subfolders(of: console))
        let infos = GameInfoCache()
        let folder = systems.first { $0.lastPathComponent.lowercased() == name.lowercased() }
            ?? systems.first { systemIdentity($0, infos) == name }
            ?? console.appending(path: name, directoryHint: .isDirectory)
        try coordinateWriting(folder) { try FileManager.default.createDirectory(at: $0, withIntermediateDirectories: true) }
        return folder
    }

    // MARK: - Moves and removals

    /// Moves `item` into `folder` under the same name, only if the name is
    /// free there and nothing in `item` is still an iCloud placeholder;
    /// both are checked again inside the coordinated move. Returns
    /// whether it moved. Never replaces anything.
    @discardableResult
    func moveIfReady(_ item: URL, into folder: URL) throws -> Bool {
        let name = item.lastPathComponent
        let taken = try entries(of: folder).map { (Self.placeholderName($0) ?? $0).lowercased() }
        guard !taken.contains(name.lowercased()), try isDownloaded(item) else { return false }
        let destination = folder.appending(path: name)
        var moved = false
        var coordinationError: NSError?
        var bodyError: Error?
        let coordinator = NSFileCoordinator(filePresenter: nil)
        coordinator.coordinate(
            writingItemAt: item, options: .forMoving, writingItemAt: destination, options: .forReplacing,
            error: &coordinationError
        ) { from, to in
            do {
                let manager = FileManager.default
                let names = (try? manager.contentsOfDirectory(atPath: to.deletingLastPathComponent().path(percentEncoded: false))) ?? []
                guard !names.contains(where: { (Self.placeholderName($0) ?? $0).lowercased() == name.lowercased() }),
                      !manager.fileExists(atPath: to.path(percentEncoded: false)),
                      Self.isFullyLocal(from) else { return }
                coordinator.item(at: from, willMoveTo: to)
                try manager.moveItem(at: from, to: to)
                coordinator.item(at: from, didMoveTo: to)
                moved = true
            } catch {
                bodyError = error
            }
        }
        if let bodyError { throw bodyError }
        if let coordinationError { throw coordinationError }
        return moved
    }

    /// Where a duplicate folder goes to be checked and removed: a hidden
    /// folder of the vault (same volume, never indexed).
    static let holdingFolder = ".ReminisLink-holding"

    /// Removes a duplicate folder left empty, without ever leaving a
    /// version without its identity file: inside one coordinated access,
    /// the folder (holding nothing but its `metadata` file and `.DS_Store`)
    /// is moved whole into the holding area; there, if it still holds
    /// nothing else, its files are removed, then the folder (`rmdir`);
    /// otherwise (anything arrived) it is moved back intact.
    @discardableResult
    func removeIfEmpty(_ folder: URL, metadata: String?) throws -> Bool {
        let allowed = Set([metadata, Self.finderFile].compactMap { $0 })
        guard try entries(of: folder).allSatisfy({ allowed.contains($0) }) else { return false }
        let holding = root.appending(path: Self.holdingFolder, directoryHint: .isDirectory)
        try FileManager.default.createDirectory(at: holding, withIntermediateDirectories: true)
        let held = holding.appending(path: UUID().uuidString, directoryHint: .isDirectory)
        let hook = holdingHook
        var removed = false
        var coordinationError: NSError?
        var bodyError: Error?
        let coordinator = NSFileCoordinator(filePresenter: nil)
        coordinator.coordinate(
            writingItemAt: folder, options: .forMoving, writingItemAt: held, options: .forReplacing,
            error: &coordinationError
        ) { from, to in
            let manager = FileManager.default
            guard let names = try? manager.contentsOfDirectory(atPath: from.path(percentEncoded: false)),
                  names.allSatisfy({ allowed.contains($0) }) else { return }
            do {
                coordinator.item(at: from, willMoveTo: to)
                try manager.moveItem(at: from, to: to)
                coordinator.item(at: from, didMoveTo: to)
            } catch {
                bodyError = error
                return
            }
            hook?(to)
            if Self.removeHeld(to, allowed: allowed, metadata: metadata) {
                removed = true
                return
            }
            // Something arrived: back where it was, intact.
            do {
                try Self.putBack(to, at: from, coordinator: coordinator)
            } catch {
                bodyError = error
            }
        }
        if let bodyError { throw bodyError }
        if let coordinationError { throw coordinationError }
        return removed
    }

    /// Removes a held folder if it holds nothing but `allowed` files: the
    /// others first, its metadata file last, then the folder (`rmdir`).
    /// If that fails, the metadata file is written back and false returned.
    nonisolated static func removeHeld(_ held: URL, allowed: Set<String>, metadata: String?) -> Bool {
        let manager = FileManager.default
        let path = held.path(percentEncoded: false)
        guard let names = try? manager.contentsOfDirectory(atPath: path), names.allSatisfy({ allowed.contains($0) }) else {
            return false
        }
        let metadataURL = metadata.map { held.appending(path: $0) }
        var saved: Data?
        if let metadataURL, names.contains(metadataURL.lastPathComponent) {
            guard let data = try? Data(contentsOf: metadataURL) else { return false }
            saved = data
        }
        for name in names where name != metadata { try? manager.removeItem(at: held.appending(path: name)) }
        if let metadataURL, saved != nil { try? manager.removeItem(at: metadataURL) }
        if rmdir(path) == 0 { return true }
        if let metadataURL, let saved, !manager.fileExists(atPath: metadataURL.path(percentEncoded: false)) {
            try? writeSynced(saved, to: metadataURL)
        }
        return false
    }

    /// Moves a held folder back to `original`, or next to it under a free
    /// name if that name was taken meanwhile.
    nonisolated static func putBack(_ held: URL, at original: URL, coordinator: NSFileCoordinator) throws {
        let manager = FileManager.default
        var target = original
        var n = 2
        while manager.fileExists(atPath: target.path(percentEncoded: false)) {
            target = original.deletingLastPathComponent().appending(path: "\(original.lastPathComponent) (\(n))", directoryHint: .isDirectory)
            n += 1
        }
        coordinator.item(at: held, willMoveTo: target)
        try manager.moveItem(at: held, to: target)
        coordinator.item(at: held, didMoveTo: target)
    }

    /// Folders left in the holding area by a removal that was stopped (the
    /// app quit): removed if they hold nothing but an identity file.
    func cleanHoldingArea() {
        let holding = root.appending(path: Self.holdingFolder, directoryHint: .isDirectory)
        let allowed: Set<String> = [Self.consoleFile, Self.gameFile, Self.finderFile]
        for name in (try? FileManager.default.contentsOfDirectory(atPath: holding.path(percentEncoded: false))) ?? [] {
            let held = holding.appending(path: name, directoryHint: .isDirectory)
            try? coordinateWriting(held) { url in
                let names = (try? FileManager.default.contentsOfDirectory(atPath: url.path(percentEncoded: false))) ?? []
                let metadata = names.first { $0 == Self.consoleFile || $0 == Self.gameFile }
                _ = Self.removeHeld(url, allowed: allowed, metadata: metadata)
            }
        }
    }

    /// Deletes a version beyond the limit (publication only). Inside the
    /// coordinated deletion, its `.version.json` must still be the one
    /// listed, and the folder must hold exactly its listed files,
    /// downloaded and matching their size and SHA-256; otherwise it stays.
    func deleteVersion(_ version: StoredVersion) throws {
        var coordinationError: NSError?
        var bodyError: Error?
        NSFileCoordinator(filePresenter: nil).coordinate(writingItemAt: version.folder, options: .forDeleting, error: &coordinationError) { url in
            guard let data = try? Data(contentsOf: url.appending(path: Self.versionFile)),
                  let info = try? ProtocolJSON.decoder().decode(SaveVersionInfo.self, from: data),
                  info == version.version.info, Self.versionCheck(url, info).exact else { return }
            do { try FileManager.default.removeItem(at: url) } catch { bodyError = error }
        }
        if let bodyError { throw bodyError }
        if let coordinationError { throw coordinationError }
    }

    /// ``versionCheck(_:_:)`` in a coordinated read.
    func checkVersion(_ version: StoredVersion) -> (restorable: Bool, exact: Bool) {
        var result = (restorable: false, exact: false)
        try? coordinateReading(version.folder) { url in result = Self.versionCheck(url, version.version.info) }
        return result
    }

    /// `restorable`: the listed files are there, downloaded, with their
    /// size and SHA-256. `exact`: also nothing else in the folder but the
    /// `.version.json` (and `.DS_Store`). A folder that cannot be read is
    /// neither.
    nonisolated static func versionCheck(_ folder: URL, _ info: SaveVersionInfo) -> (restorable: Bool, exact: Bool) {
        let manager = FileManager.default
        guard let names = try? manager.contentsOfDirectory(atPath: folder.path(percentEncoded: false)) else { return (false, false) }
        for file in [info.save, info.rtc].compactMap({ $0 }) {
            let url = folder.appending(path: file.name)
            guard names.contains(file.name), !isDirectory(url),
                  (try? url.resourceValues(forKeys: [.ubiquitousItemDownloadingStatusKey]).ubiquitousItemDownloadingStatus) != .notDownloaded,
                  (try? FileDigest.size(of: url)) == file.sizeBytes,
                  (try? FileDigest.sha256Sync(of: url)) == file.sha256
            else { return (false, false) }
        }
        let expected = Set([versionFile, finderFile, info.save.name] + (info.rtc.map { [$0.name] } ?? []))
        return (true, names.allSatisfy { expected.contains($0) })
    }

    // MARK: - Downloads

    /// Whether nothing in `item` (a file or a whole folder) is an iCloud
    /// placeholder or not downloaded; asks iCloud to download what is not.
    func isDownloaded(_ item: URL) throws -> Bool {
        var downloaded = true
        try coordinateReading(item) { url in
            downloaded = Self.isFullyLocal(url, startDownloads: true)
        }
        return downloaded
    }

    /// `isDownloaded` without coordination (inside a coordinated access).
    nonisolated static func isFullyLocal(_ item: URL, startDownloads: Bool = false) -> Bool {
        let manager = FileManager.default
        let path = item.path(percentEncoded: false)
        guard manager.fileExists(atPath: path) else { return false }
        var local = true
        func check(_ url: URL) {
            let status = try? url.resourceValues(forKeys: [.ubiquitousItemDownloadingStatusKey]).ubiquitousItemDownloadingStatus
            if status == .notDownloaded {
                if startDownloads { startDownloading(url) }
                local = false
            }
        }
        check(item)
        guard isDirectory(item) else { return local }
        // A folder that cannot be listed is not known to be local.
        guard let names = try? manager.contentsOfDirectory(atPath: path) else { return false }
        for name in names {
            let url = item.appending(path: name)
            if let real = placeholderName(name) {
                if startDownloads { startDownloading(item.appending(path: real)) }
                local = false
            } else if isDirectory(url) {
                if !isFullyLocal(url, startDownloads: startDownloads) { local = false }
            } else {
                check(url)
            }
        }
        return local
    }

    nonisolated static func isDirectory(_ url: URL) -> Bool {
        var isDirectory: ObjCBool = false
        return FileManager.default.fileExists(atPath: url.path(percentEncoded: false), isDirectory: &isDirectory) && isDirectory.boolValue
    }

    static let finderFile = ".DS_Store"
}
