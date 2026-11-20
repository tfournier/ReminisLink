import CryptoKit
import Foundation
import ReminisLinkDemo
import Testing
@testable import ReminisLinkKit

// Milestone M11: the flat library (`GET /games`), the download of one file
// of a game, the filters of the library and the closing of the pak.

private func decode<T: Decodable>(_ type: T.Type, _ json: String) throws -> T {
    try ProtocolJSON.decoder().decode(type, from: Data(json.utf8))
}

/// Thread-safe list of progress reports.
private final class Progress: @unchecked Sendable {
    private let lock = NSLock()
    private var items: [(Int64, Int64?)] = []
    func add(_ received: Int64, _ expected: Int64?) { lock.withLock { items.append((received, expected)) } }
    var all: [(Int64, Int64?)] { lock.withLock { items } }
}

@Suite("Flat library models")
struct LibraryModelTests {
    @Test func decodesTheContractExampleOfAllGames() throws {
        let all = try decode(AllGames.self, """
        {"games":[
          {"id":"R2FtZQ","systemId":"R0JB","folderId":null,"folderPath":null,"name":"Thistledown Isles",
           "fileName":"Thistledown Isles.gba","kind":"file","sizeBytes":16777216,"modifiedAt":"2026-10-02T09:12:00Z",
           "hasSave":true,"stateCount":3,"lockedStateCount":1,"cheatCount":12,"hasArt":true},
          {"id":"VGlueQ","systemId":"R0JB","folderId":"SG9tZQ","folderPath":"Homebrew/2026","name":"Tiny",
           "fileName":"Tiny.gba","kind":"file","sizeBytes":1,"modifiedAt":"2026-10-02T09:12:00Z",
           "hasSave":false,"stateCount":0,"lockedStateCount":0,"cheatCount":0,"hasArt":false,"futureField":1}
         ],"truncated":false,"generatedAt":"2026-11-19T21:00:00Z"}
        """)
        #expect(all.games.count == 2)
        #expect(!all.truncated)
        #expect(all.generatedAt == ProtocolJSON.parseDate("2026-11-19T21:00:00Z"))
        let sky = all.games[0]
        #expect(sky.id == "R2FtZQ")
        #expect(sky.systemId == "R0JB")
        #expect(sky.folderId == nil && sky.folderPath == nil)
        #expect(sky.summary.name == "Thistledown Isles")
        #expect(sky.summary.cheatCount == 12)
        #expect(all.games[1].folderId == "SG9tZQ")
        #expect(all.games[1].folderPath == "Homebrew/2026")
        #expect(LibraryMode.from(all) == .flat(all))
    }

    @Test func aTruncatedListMeansBrowsingBySystem() throws {
        let all = try decode(AllGames.self, #"{"games":[],"truncated":true,"generatedAt":"2026-11-19T21:00:00Z"}"#)
        #expect(LibraryMode.from(all) == .bySystem(truncated: true))
    }

    @Test func requiredFieldsAreRequired() {
        // `systemId` is required in a LibraryGame (`fileId` of a GameFile
        // is not, for paks older than M11: see aFileWithoutFileIdDecodes).
        #expect(throws: DecodingError.self) {
            try decode(LibraryGame.self, """
            {"id":"a","folderId":null,"folderPath":null,"name":"x","fileName":"x.gba","kind":"file","sizeBytes":1,
             "modifiedAt":"2026-10-02T09:12:00Z","hasSave":false,"stateCount":0,"lockedStateCount":0,"cheatCount":0,"hasArt":false}
            """)
        }
    }

    @Test func only404Or405MeansAnOlderPak() {
        #expect(LibraryMode.meansUnsupported(APIError.server(ServerError(status: 404, code: .notFound, message: "x"))))
        #expect(LibraryMode.meansUnsupported(APIError.unexpectedStatus(404)))
        #expect(LibraryMode.meansUnsupported(APIError.unexpectedStatus(405)))
        #expect(!LibraryMode.meansUnsupported(APIError.server(ServerError(status: 503, code: .shuttingDown, message: "x"))))
        #expect(!LibraryMode.meansUnsupported(APIError.network(.timedOut)))
        #expect(!LibraryMode.meansUnsupported(CancellationError()))
    }
}

@Suite("Flat library against the fake pak")
struct LibraryAPITests {
    @Test func listsEveryGameOfEverySystem() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let all = try await client.allGames()
        #expect(!all.truncated)
        let expected = pak.library.systems.reduce(0) { $0 + $1.games.count }
        #expect(all.games.count == expected)
        // Sorted by system folder, then by path.
        #expect(all.games.first?.summary.name == "Mosslight Maze 🌙")
        let tiny = try #require(all.games.first { $0.summary.name == "Quillwork Tactics" })
        #expect(tiny.folderPath == "Homebrew")
        #expect(tiny.folderId == DemoLibrary.id("Handheld Advance (GBA)/Homebrew"))
        #expect(tiny.systemId == DemoLibrary.id("Handheld Advance (GBA)"))
        let sky = try #require(all.games.first { $0.id == skyGarden })
        #expect(sky.folderId == nil && sky.folderPath == nil)
        #expect(pak.log.last?.path == "/api/v1/games")
    }

    @Test func aLongListIsTruncated() async throws {
        let pak = makePak()
        pak.allGamesLimit = 2
        let client = try await pairedClient(pak)
        let all = try await client.allGames()
        #expect(all.truncated)
        #expect(all.games.count == 2)
    }

    @Test func anOlderPakAnswers404() async throws {
        let pak = makePak()
        pak.allGamesSupported = false
        let client = try await pairedClient(pak)
        await #expect {
            _ = try await client.allGames()
        } throws: { LibraryMode.meansUnsupported($0) }
    }

    @Test func everyFileHasAFileIdAndCanBeDownloaded() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let detail = try await client.game(id: skyGarden)
        #expect(Set(detail.files.compactMap(\.fileId)).count == detail.files.count)
        let root = downloadsDirectory()
        defer { try? FileManager.default.removeItem(at: root) }

        let rom = try #require(detail.files.first { $0.role == .rom })
        let progress = Progress()
        let romId = try #require(rom.fileId)
        let file = try await client.downloadGameFile(gameId: skyGarden, fileId: romId, into: root) {
            progress.add($0, $1)
        }
        #expect(file.url.lastPathComponent == "Thistledown Isles.gba")
        #expect(try FileDigest.size(of: file.url) == rom.sizeBytes)
        #expect(file.directory.deletingLastPathComponent().standardizedFileURL == root.standardizedFileURL)
        #expect(pak.log.last?.path == "/api/v1/games/\(skyGarden)/files/\(romId)")
        let reports = progress.all
        #expect(!reports.isEmpty)
        #expect(reports.map(\.0) == reports.map(\.0).sorted())
        #expect(reports.last?.0 == rom.sizeBytes)

        let cheats = try #require(detail.files.first { $0.role == .cheats })
        let cht = try await client.downloadGameFile(gameId: skyGarden, fileId: try #require(cheats.fileId), into: root)
        #expect(cht.url.lastPathComponent == "Thistledown Isles.gba.cht")
        #expect(try FileDigest.size(of: cht.url) == cheats.sizeBytes)
        let text = try String(contentsOf: cht.url, encoding: .utf8)
        #expect(text.hasPrefix("cheats = 12"))

        let save = try #require(detail.files.first { $0.role == .save })
        let saveFile = try await client.downloadGameFile(gameId: skyGarden, fileId: try #require(save.fileId), into: root)
        #expect(try FileDigest.size(of: saveFile.url) == save.sizeBytes)
    }

    @Test func aFileNoLongerInTheListIs404AndLeavesNothing() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let root = downloadsDirectory()
        defer { try? FileManager.default.removeItem(at: root) }
        // Brambleway Dash has no save: the save's fileId of another game is not in its list.
        let sky = try await client.game(id: skyGarden)
        let save = try #require(sky.files.first { $0.role == .save })
        await #expect(throws: APIError.server(ServerError(status: 404, code: .notFound, message: "File not found"))) {
            try await client.downloadGameFile(gameId: pixelRunner, fileId: try #require(save.fileId), into: root)
        }
        let left = (try? FileManager.default.contentsOfDirectory(atPath: root.path)) ?? []
        #expect(left.isEmpty)
    }

    /// A real connection: the bytes are written to disk as they arrive, with
    /// the size announced by `Content-Length`.
    @Test func aDownloadReportsItsProgressOverARealConnection() async throws {
        let length = 3 << 20
        let server = try LocalHTTPServer { request in
            .init(status: 200, headers: [
                "Content-Type": "application/octet-stream",
                "Content-Disposition": #"attachment; filename="Big Game.gba""#,
            ], body: Data(repeating: 7, count: length))
        }
        let port = try await server.start()
        defer { server.stop() }
        let client = APIClient(
            scheme: "http", // A plain local test server: no TLS.
            endpoint: Endpoint(host: "127.0.0.1", port: port),
            credentials: DeviceCredentials.random(deviceId: FakePak.demoDeviceId),
            session: APIClient.makeSession()
        )
        let root = downloadsDirectory()
        defer { try? FileManager.default.removeItem(at: root) }
        let progress = Progress()
        let file = try await client.downloadGameFile(gameId: "g", fileId: "f", into: root) { progress.add($0, $1) }
        #expect(file.url.lastPathComponent == "Big Game.gba")
        #expect(try FileDigest.size(of: file.url) == Int64(length))
        let reports = progress.all
        #expect(reports.last?.0 == Int64(length))
        #expect(reports.last?.1 == Int64(length))
    }

    /// The contract sends files whole: every file of every demo game is
    /// served with the size the game sheet lists.
    @Test func everyFileIsServedWithItsListedSize() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let root = downloadsDirectory()
        defer { try? FileManager.default.removeItem(at: root) }
        var checked = 0
        for game in try await client.allGames().games {
            for file in try await client.game(id: game.id).files {
                let downloaded = try await client.downloadGameFile(gameId: game.id, fileId: try #require(file.fileId), into: root)
                #expect(try FileDigest.size(of: downloaded.url) == file.sizeBytes, "\(file.path)")
                try FileManager.default.removeItem(at: downloaded.directory)
                checked += 1
            }
        }
        #expect(checked > 10)
    }

    /// A pak older than M11: no `GET /games` (`404`, the app browses per
    /// system), and files without `fileId`, which must still decode (the
    /// game sheet keeps working, without downloads).
    @Test func aPakOlderThanM11StillOpensItsGames() async throws {
        let pak = makePak()
        pak.allGamesSupported = false
        let client = try await pairedClient(pak)
        await #expect {
            _ = try await client.allGames()
        } throws: { LibraryMode.meansUnsupported($0) }
        let listing = try await client.games(systemId: DemoLibrary.id("Handheld Advance (GBA)"))
        #expect(listing.games.contains { $0.id == skyGarden })
        let detail = try await client.game(id: skyGarden)
        #expect(!detail.files.isEmpty)
        #expect(detail.files.allSatisfy { $0.fileId == nil })
        #expect(detail.hasSave && detail.hasCheats)
    }

    @Test func aFileWithoutFileIdDecodes() throws {
        let file = try decode(GameFile.self, #"{"role":"rom","path":"Roms/GBA/x.gba","sizeBytes":1,"modifiedAt":"2026-10-02T09:12:00Z"}"#)
        #expect(file.fileId == nil)
        #expect(file.name == "x.gba")
    }

    /// Cancelled while the answer is on its way, through the path the game
    /// sheet uses (with progress): cancellation, and nothing left behind.
    @Test func aCancelledDownloadThrowsCancellationAndLeavesNothing() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let detail = try await client.game(id: skyGarden)
        let romId = try #require(detail.files.first { $0.role == .rom }?.fileId)
        let path = "/api/v1/games/\(skyGarden)/files/\(romId)"
        let gate = pak.holdNextAnswer(method: "GET", path: path)
        let root = downloadsDirectory()
        defer { try? FileManager.default.removeItem(at: root) }
        let progress = Progress()
        let task = Task {
            try await client.downloadGameFile(gameId: skyGarden, fileId: romId, into: root) { progress.add($0, $1) }
        }
        let deadline = ContinuousClock.now + .seconds(5)
        while !gate.reached && ContinuousClock.now < deadline { try await Task.sleep(for: .milliseconds(10)) }
        #expect(gate.reached)
        task.cancel()
        gate.open()
        await #expect(throws: CancellationError.self) { try await task.value }
        let left = (try? FileManager.default.contentsOfDirectory(atPath: root.path)) ?? []
        #expect(left.isEmpty)
    }
}

@Suite("Library filters")
struct LibraryFilterTests {
    static let date = Date(timeIntervalSince1970: 1_790_000_000)

    static func game(
        _ name: String, system: String, file: String? = nil, size: Int64 = 1, days: Double = 0,
        save: Bool = false, states: Int = 0, cheats: Int = 0
    ) -> LibraryGame {
        LibraryGame(
            summary: GameSummary(
                id: system + "/" + name, name: name, fileName: file ?? name + ".rom", kind: .file, sizeBytes: size,
                modifiedAt: date.addingTimeInterval(days * 86_400), hasSave: save, stateCount: states,
                lockedStateCount: 0, cheatCount: cheats, hasArt: false
            ),
            systemId: system
        )
    }

    static let systems = [
        GameSystem(id: "gba", label: "Game Boy Advance", folder: "Game Boy Advance (GBA)", tag: "GBA", gameCount: 2),
        GameSystem(id: "mgba", label: "Game Boy Advance", folder: "Game Boy Advance (MGBA)", tag: "MGBA", gameCount: 1),
        GameSystem(id: "gb", label: "Game Boy", folder: "Game Boy (GB)", tag: "GB", gameCount: 1),
        GameSystem(id: "ps", label: "Sony PlayStation", folder: "Sony PlayStation (PS)", tag: "PS", gameCount: 0),
    ]

    static let games = [
        game("Thistledown Isles", system: "gba", size: 16, days: 0, save: true, states: 3, cheats: 12),
        game("pixel runner", system: "gba", file: "Brambleway Dash (Europe).gba", size: 4, days: 3),
        game("Cartographe Égaré", system: "mgba", size: 2, days: 2, states: 1, cheats: 2),
        game("Mosslight Maze", system: "gb", size: 1, days: 1, save: true),
    ]

    var groups: [SystemGroup] { LibraryFiltering.groups(systems: Self.systems, games: Self.games) }

    func names(_ query: LibraryQuery) -> [String] {
        LibraryFiltering.apply(query, to: Self.games, groups: groups).map(\.summary.name)
    }

    @Test func systemsWithTheSameLabelAreOneConsole() {
        #expect(groups.map(\.label) == ["Game Boy", "Game Boy Advance"])
        let gba = groups[1]
        #expect(gba.systemIds == ["gba", "mgba"])
        #expect(gba.tags == ["GBA", "MGBA"])
        #expect(gba.gameCount == 3)
        // A system without games is not offered.
        #expect(!groups.contains { $0.label == "Sony PlayStation" })
        #expect(LibraryFiltering.labels(Self.systems)["mgba"] == "Game Boy Advance")
    }

    /// The same game in GBA and MGBA: rows say which system; the filter
    /// keeps one console.
    @Test func rowsOfSystemsSharingALabelShowTheirTag() {
        let labels = LibraryFiltering.rowLabels(systems: Self.systems, games: Self.games)
        #expect(labels["gba"] == "Game Boy Advance (GBA)")
        #expect(labels["mgba"] == "Game Boy Advance (MGBA)")
        #expect(labels["gb"] == "Game Boy")
        // A label shared with a system without games: no tag needed.
        let onlyGBA = Self.games.filter { $0.systemId != "mgba" }
        #expect(LibraryFiltering.rowLabels(systems: Self.systems, games: onlyGBA)["gba"] == "Game Boy Advance")
        #expect(groups.filter { $0.label == "Game Boy Advance" }.count == 1)
    }

    @Test func sortsByNameDateOrSize() {
        #expect(names(LibraryQuery()) == ["Cartographe Égaré", "Mosslight Maze", "pixel runner", "Thistledown Isles"])
        #expect(names(LibraryQuery(sort: .date)) == ["pixel runner", "Cartographe Égaré", "Mosslight Maze", "Thistledown Isles"])
        #expect(names(LibraryQuery(sort: .size)) == ["Thistledown Isles", "pixel runner", "Cartographe Égaré", "Mosslight Maze"])
    }

    @Test func searchesTheNameAndTheFileName() {
        #expect(names(LibraryQuery(text: "thistle")) == ["Thistledown Isles"])
        // Accents and case do not matter.
        #expect(names(LibraryQuery(text: "egare")) == ["Cartographe Égaré"])
        // The file name counts too.
        #expect(names(LibraryQuery(text: "Europe")) == ["pixel runner"])
        #expect(names(LibraryQuery(text: "  maze ")) == ["Mosslight Maze"])
        #expect(names(LibraryQuery(text: "nothing")).isEmpty)
    }

    @Test func filtersByConsoleAndContents() {
        #expect(names(LibraryQuery(systemLabel: "Game Boy Advance")) == ["Cartographe Égaré", "pixel runner", "Thistledown Isles"])
        #expect(names(LibraryQuery(systemLabel: "Game Boy")) == ["Mosslight Maze"])
        #expect(names(LibraryQuery(hasSave: true)) == ["Mosslight Maze", "Thistledown Isles"])
        #expect(names(LibraryQuery(hasStates: true)) == ["Cartographe Égaré", "Thistledown Isles"])
        #expect(names(LibraryQuery(hasCheats: true)) == ["Cartographe Égaré", "Thistledown Isles"])
        #expect(names(LibraryQuery(systemLabel: "Game Boy Advance", hasSave: true, hasStates: true, hasCheats: true)) == ["Thistledown Isles"])
        #expect(names(LibraryQuery(text: "moon", systemLabel: "Game Boy Advance")).isEmpty)
        // A console no longer in the list matches nothing.
        #expect(names(LibraryQuery(systemLabel: "Atari")).isEmpty)
    }

    @Test func clearingFiltersKeepsTheSearchAndTheSort() {
        var query = LibraryQuery(text: "a", systemLabel: "Game Boy", hasSave: true, hasStates: true, hasCheats: true, sort: .size)
        #expect(query.isFiltered)
        query.clearFilters()
        #expect(!query.isFiltered)
        #expect(query == LibraryQuery(text: "a", sort: .size))
    }
}

@Suite("Closing of the pak")
struct PakClosureTests {
    @Test func shuttingDownAlwaysMeansClosed() {
        let error = APIError.server(ServerError(status: 503, code: .shuttingDown, message: "x"))
        #expect(PakClosure.isClosed(error, serviceVisible: true, browsingFor: nil))
        #expect(PakClosure.isClosed(error, serviceVisible: true, browsingFor: .seconds(60)))
    }

    @Test func aConnectionFailureNeedsTheServiceGone() {
        let error = APIError.network(.cannotConnectToHost)
        // A Wi-Fi glitch alone: the console is still announced.
        #expect(!PakClosure.isClosed(error, serviceVisible: true, browsingFor: .seconds(60)))
        // The service is gone, and the browser has been running long enough.
        #expect(PakClosure.isClosed(error, serviceVisible: false, browsingFor: .seconds(5)))
        #expect(PakClosure.isClosed(APIError.network(.timedOut), serviceVisible: false, browsingFor: .seconds(5)))
        // Just started (back from the background): nothing is known yet.
        #expect(!PakClosure.isClosed(error, serviceVisible: false, browsingFor: .seconds(1)))
        // Browser stopped, or local network denied.
        #expect(!PakClosure.isClosed(error, serviceVisible: false, browsingFor: nil))
        #expect(!PakClosure.isClosed(APIError.network(.cancelled), serviceVisible: false, browsingFor: .seconds(5)))
    }

    @Test func otherErrorsNeverMeanClosed() {
        let errors: [APIError] = [
            .server(ServerError(status: 404, code: .notFound, message: "x")),
            .server(ServerError(status: 429, code: .tooManyAttempts, message: "x")),
            .server(ServerError(status: 401, code: .disconnected, message: "x")),
            .unexpectedStatus(502),
            .invalidResponse("x"),
        ]
        for error in errors {
            #expect(!PakClosure.isClosed(error, serviceVisible: false, browsingFor: .seconds(60)))
        }
    }

    /// The `/hello` probe sent when the service disappeared (M11).
    @Test func theProbeMeansClosedOnlyWithoutAnAnswer() {
        #expect(PakClosure.probeMeansClosed(APIError.server(ServerError(status: 503, code: .shuttingDown, message: "x"))))
        #expect(PakClosure.probeMeansClosed(APIError.network(.cannotConnectToHost)))
        #expect(PakClosure.probeMeansClosed(APIError.network(.timedOut)))
        #expect(PakClosure.probeMeansClosed(APIError.network(.networkConnectionLost)))
        #expect(!PakClosure.probeMeansClosed(APIError.network(.cancelled)))
        #expect(!PakClosure.probeMeansClosed(APIError.server(ServerError(status: 500, code: .internal, message: "x"))))
        #expect(!PakClosure.probeMeansClosed(APIError.unexpectedStatus(502)))
        #expect(!PakClosure.probeMeansClosed(CancellationError()))
    }

    @Test func theFakePakQuitsThenIsGone() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let changes = Changes()
        pak.onAvailabilityChange = { changes.add($0) }
        pak.closeOnRequest(method: "GET", path: "/games", reopenAfter: nil)
        _ = try await client.systems()
        await #expect(throws: APIError.server(ServerError(status: 503, code: .shuttingDown, message: "Shutting down"))) {
            try await client.allGames()
        }
        await #expect(throws: APIError.server(ServerError(status: 503, code: .shuttingDown, message: "Shutting down"))) {
            try await client.hello()
        }
        pak.availability = .gone
        await #expect(throws: APIError.network(.cannotConnectToHost)) { try await client.systems() }
        pak.availability = .open
        _ = try await client.systems()
        #expect(changes.all == [.shuttingDown, .gone, .open])
    }

    @Test @MainActor func aFixedDeviceCanWithdrawItsService() async throws {
        let pak = makePak()
        let discovery = Discovery(fixedDevices: [pak.discoveredDevice])
        #expect(discovery.browsingDuration == nil)
        discovery.start()
        #expect(discovery.browsingDuration != nil)
        #expect(discovery.devices.count == 1)
        discovery.setFixedDevice(pak.discoveredDevice, visible: false)
        #expect(discovery.devices.isEmpty)
        discovery.setFixedDevice(pak.discoveredDevice, visible: true)
        #expect(discovery.devices.count == 1)
        discovery.stop()
        #expect(discovery.browsingDuration == nil)
    }

    final class Changes: @unchecked Sendable {
        private let lock = NSLock()
        private var items: [FakePak.Availability] = []
        func add(_ item: FakePak.Availability) { lock.withLock { items.append(item) } }
        var all: [FakePak.Availability] { lock.withLock { items } }
    }
}
