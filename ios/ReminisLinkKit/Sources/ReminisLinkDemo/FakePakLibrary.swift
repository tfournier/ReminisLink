import Foundation
import ReminisLinkKit

// Milestone M11 of the fake pak: `GET /games` (every game in one flat
// list), the `fileId` of each file of `GET /games/{id}` and
// `GET /games/{id}/files/{fileId}`, and the closing of the pak
// (`503 shutting_down`, then nothing; its Bonjour service withdrawn).

extension FakePak {
    /// Whether the pak is open.
    public enum Availability: Sendable, Equatable {
        case open
        /// Quitting: every request gets `503 shutting_down`.
        case shuttingDown
        /// Gone: every request fails to connect.
        case gone
    }

    /// The M11 routes exist (false: a pak older than M11: `GET /games` and
    /// the file downloads answer `404`, and files have no `fileId`).
    public var allGamesSupported: Bool {
        get { lock.withLock { _allGamesSupported } }
        set { lock.withLock { _allGamesSupported = newValue } }
    }

    /// At most this many games in `GET /games` (beyond, `truncated`).
    public var allGamesLimit: Int {
        get { lock.withLock { _allGamesLimit } }
        set { lock.withLock { _allGamesLimit = newValue } }
    }

    public var availability: Availability {
        get { lock.withLock { _availability } }
        set {
            let callback = lock.withLock { () -> (@Sendable (Availability) -> Void)? in
                guard _availability != newValue else { return nil }
                _availability = newValue
                return _onAvailabilityChange
            }
            // M14: `shutdown`, then the streams end, as the pak quits.
            if newValue != .open { endEventStreams(shutdown: "quit") }
            callback?(newValue)
        }
    }

    /// Called when the pak quits or opens again (the demo mode withdraws
    /// or announces its Bonjour service).
    public var onAvailabilityChange: (@Sendable (Availability) -> Void)? {
        get { lock.withLock { _onAvailabilityChange } }
        set { lock.withLock { _onAvailabilityChange = newValue } }
    }

    #if DEBUG
    /// Demo mode and UI tests: the pak quits when it receives this request
    /// (`method` and the path under `/api/v1`, e.g. `GET /games`), which
    /// gets `503 shutting_down`, then opens again after `reopenAfter`
    /// seconds (never if nil).
    public func closeOnRequest(method: String, path: String, reopenAfter: TimeInterval?) {
        lock.withLock {
            _closeTrigger = (method.uppercased(), APIClient.apiPrefix + path)
            _reopenDelay = reopenAfter
        }
    }
    #endif

    /// `503 shutting_down` while quitting, a connection failure once gone.
    func availabilityRefusal(method: String, path: String) -> FakeResponse? {
        let (triggered, delay) = lock.withLock { () -> (Bool, TimeInterval?) in
            guard let trigger = _closeTrigger, trigger.method == method, trigger.path == path else { return (false, nil) }
            _closeTrigger = nil
            return (true, _reopenDelay)
        }
        if triggered {
            availability = .shuttingDown
            if let delay {
                DispatchQueue.global().asyncAfter(deadline: .now() + delay) { [self] in availability = .open }
            }
        }
        switch availability {
        case .open: return nil
        case .shuttingDown: return .error(503, code: "shutting_down", message: "Shutting down")
        case .gone: return .networkFailure(.cannotConnectToHost)
        }
    }

    // MARK: - GET /games

    /// Every game, sorted by system folder then path (case-insensitive),
    /// at most ``allGamesLimit``.
    func allGames() -> FakeResponse {
        let (supported, limit) = lock.withLock { (_allGamesSupported, _allGamesLimit) }
        guard supported else { return .error(404, code: "not_found", message: "No route") }
        var games: [[String: Any]] = []
        let systems = library.systems.sorted { $0.folder.localizedCaseInsensitiveCompare($1.folder) == .orderedAscending }
        for system in systems {
            let sorted = system.games.sorted { $0.path.localizedCaseInsensitiveCompare($1.path) == .orderedAscending }
            for game in sorted {
                var json = summaryJSON(system: system, game: game)
                json["systemId"] = system.id
                json["folderId"] = game.parent.isEmpty ? NSNull() : DemoLibrary.id(system.folder + "/" + game.parent)
                json["folderPath"] = game.parent.isEmpty ? NSNull() : game.parent
                games.append(json)
            }
        }
        let truncated = games.count > limit
        return .json(200, [
            "games": Array(games.prefix(limit)), "truncated": truncated,
            "generatedAt": ProtocolJSON.formatDate(Date()),
        ])
    }

    // MARK: - Files

    /// The `fileId` of a file: base64url of `f_<role>` (`f_state<slot>`
    /// for a state), unique in the list of one game, as the contract's
    /// example (`Zl9yb20` is `f_rom`). The app never decodes it.
    static func fileId(role: String, slot: Int?) -> String {
        Base64URL.encode(Data(("f_" + role + (slot.map(String.init) ?? "")).utf8))
    }

    static func withFileIds(_ files: [[String: Any]]) -> [[String: Any]] {
        files.map { file in
            var file = file
            file["fileId"] = fileId(role: file["role"] as? String ?? "", slot: file["slot"] as? Int)
            return file
        }
    }

    /// `GET /games/{gameId}/files/{fileId}`: only a file of the current
    /// list of the game.
    func downloadFile(gameId: String, fileId: String) -> FakeResponse {
        guard allGamesSupported else { return .error(404, code: "not_found", message: "No route") }
        guard let (system, game) = library.game(id: gameId) else {
            return .error(404, code: "not_found", message: "Game not found")
        }
        guard let file = files(system: system, game: game).first(where: { $0["fileId"] as? String == fileId }),
              let role = file["role"] as? String, let path = file["path"] as? String
        else { return .error(404, code: "not_found", message: "File not found") }
        let name = String(path.split(separator: "/").last ?? "file")
        let data = currentData(system: system, game: game)
        let content: Data
        switch role {
        case "save":
            content = data.save?.content ?? Data()
        case "state":
            content = (file["slot"] as? Int).flatMap { data.slots[$0]?.state?.content } ?? Data()
        case "cheats":
            content = Self.chtText(data.cheats)
        case "art":
            content = currentArt(gameId: gameId, game: game)?.data ?? Data()
        default:
            content = Self.madeUpContent(size: Self.size(file["sizeBytes"]), tag: path)
        }
        var response = fileResponse(name: name, content: content)
        response.headers["Content-Length"] = String(content.count)
        #if DEBUG
        response.delay = fileDownloadDelay
        #endif
        return response
    }

    #if DEBUG
    /// Every file download is answered this many seconds late: a download
    /// still in progress, for the UI tests (M15).
    public var fileDownloadDelay: TimeInterval {
        get { lock.withLock { _fileDownloadDelay } }
        set { lock.withLock { _fileDownloadDelay = newValue } }
    }
    #endif

    /// `size` bytes, the whole file as listed (the contract sends files
    /// whole): the tag, then zeros. Demo games are small; it is held in
    /// memory.
    static func madeUpContent(size: Int64, tag: String) -> Data {
        var data = Data(count: Int(size))
        let prefix = Data(tag.utf8.prefix(data.count))
        data.replaceSubrange(0..<prefix.count, with: prefix)
        return data
    }

    /// The cheats of a game as a RetroArch `.cht` file.
    static func chtText(_ cheats: [FakeCheat]) -> Data {
        var lines = ["cheats = \(cheats.count)", ""]
        for (index, cheat) in cheats.enumerated() {
            func quoted(_ s: String) -> String { "\"" + s.replacingOccurrences(of: "\"", with: "\\\"") + "\"" }
            lines.append("cheat\(index)_desc = \(quoted(cheat.description))")
            lines.append("cheat\(index)_code = \(quoted(cheat.code))")
            lines.append("cheat\(index)_enable = \(cheat.enabled)")
            for key in cheat.extra.keys.sorted() {
                lines.append("cheat\(index)_\(key) = \(quoted(cheat.extra[key] ?? ""))")
            }
            lines.append("")
        }
        return Data(lines.joined(separator: "\n").utf8)
    }
}
