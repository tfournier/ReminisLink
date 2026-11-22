import Foundation
import ReminisLinkKit

// Milestone M22 of the fake pak: the screenshots of the console
// (`GET /screenshots`, `GET /screenshots/{id}`, its thumbnail,
// `POST /screenshots/trash`, the `screenshots` event) and their restore
// from the trash (FakePakTrash.swift). The images are invented, drawn in
// code (``DemoArtwork``) when the fake draws its artwork (the app's demo
// mode), else a 1-pixel PNG. A BMP screenshot has no preview (`404`), as
// on the real pak.

/// A screenshot of the fake console.
struct FakeScreenshot: Equatable {
    var name: String
    var takenAt: Date
    var gameName: String?
    var sizeBytes: Int64

    var id: String { "sc_" + Base64URL.encode(Data(name.utf8)) }

    var format: String {
        switch (name as NSString).pathExtension.lowercased() {
        case "jpg", "jpeg": "jpeg"
        case "bmp": "bmp"
        default: "png"
        }
    }

    /// What is drawn: the game, else the name.
    var seed: String { gameName ?? name }
}

/// The screenshots of the fake console.
struct FakeScreenshotsState {
    var screenshots: [FakeScreenshot] = FakeScreenshotsState.sample
    /// `/screenshots` exists (false: a pak older than M22).
    var supported = true

    /// NextUI names (`<game>.<YYYY-MM-DD-HH-MM-SS>.png`) of the made-up
    /// games of the demo library, over three days, and one BMP.
    static let sample: [FakeScreenshot] = {
        let base = ProtocolJSON.parseDate("2026-11-21T18:30:00Z") ?? Date(timeIntervalSince1970: 1_795_300_000)
        func shot(_ game: String?, _ name: String, minutesBefore: Double, size: Int64 = 182_000) -> FakeScreenshot {
            FakeScreenshot(name: name, takenAt: base.addingTimeInterval(-minutesBefore * 60), gameName: game, sizeBytes: size)
        }
        return [
            shot("Thistledown Isles", "Thistledown Isles.2026-11-21-18-30-00.png", minutesBefore: 0),
            shot("Thistledown Isles", "Thistledown Isles.2026-11-21-18-12-41.png", minutesBefore: 17.3),
            shot("Mosslight Maze 🌙", "Mosslight Maze 🌙.2026-11-21-09-05-12.png", minutesBefore: 565),
            shot("Quillwork Tactics", "Quillwork Tactics.2026-11-20-21-44-03.png", minutesBefore: 1246),
            shot("Orrery of Nine Tides", "Orrery of Nine Tides.2026-11-20-20-10-30.png", minutesBefore: 1340),
            shot("Cartographe Égaré", "Cartographe Égaré.2026-11-19-16-02-58.png", minutesBefore: 3027),
            shot("Brambleway Dash", "Brambleway Dash.2026-11-19-15-58-20.png", minutesBefore: 3032),
            shot(nil, "capture-0042.bmp", minutesBefore: 3100, size: 2_359_350),
        ]
    }()
}

extension FakePak {
    // MARK: - Controls

    /// Whether `/screenshots` exists (false: a pak older than M22, `404`).
    public var screenshotsSupported: Bool {
        get { lock.withLock { screenshotsState.supported } }
        set { lock.withLock { screenshotsState.supported = newValue } }
    }

    /// The file names of the screenshots, newest first.
    public var screenshotNames: [String] {
        lock.withLock { sortedScreenshots().map(\.name) }
    }

    /// The identifier of a screenshot file name, as `GET /screenshots`
    /// gives it.
    public static func screenshotId(_ name: String) -> String {
        "sc_" + Base64URL.encode(Data(name.utf8))
    }

    /// As if the console took a screenshot meanwhile (tests: a place taken
    /// before a restore, or a new screenshot after a `screenshots` event).
    public func addScreenshot(name: String, gameName: String? = nil, takenAt: Date = Date()) {
        lock.withLock {
            screenshotsState.screenshots.append(FakeScreenshot(name: name, takenAt: takenAt, gameName: gameName, sizeBytes: 1024))
        }
    }

    /// As if another client moved screenshots to the trash (a
    /// `screenshots` event follows).
    public func trashScreenshotsFromAnotherClient(names: [String]) {
        _ = trashScreenshots(ids: names.map(Self.screenshotId))
    }

    // MARK: - Routes

    func routeScreenshots(method: String, segments: [String], body: Data) -> FakeResponse? {
        guard segments.first == "screenshots" else { return nil }
        guard lock.withLock({ screenshotsState.supported }) else {
            return .error(404, code: "not_found", message: "No route")
        }
        switch (method, segments) {
        case ("GET", ["screenshots"]):
            return listScreenshots()
        case ("POST", ["screenshots", "trash"]):
            guard let object = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
                  let ids = object["screenshotIds"] as? [String], (1...500).contains(ids.count)
            else { return .error(400, code: "invalid_request", message: "screenshotIds must hold 1 to 500 identifiers") }
            return trashScreenshots(ids: ids)
        case ("GET", let s) where s.count == 2:
            return screenshotContent(id: s[1], thumbnail: false)
        case ("GET", let s) where s.count == 3 && s[2] == "thumbnail":
            return screenshotContent(id: s[1], thumbnail: true)
        default:
            return nil
        }
    }

    /// Newest first, then by name (lock held).
    private func sortedScreenshots() -> [FakeScreenshot] {
        screenshotsState.screenshots.sorted { $0.takenAt != $1.takenAt ? $0.takenAt > $1.takenAt : $0.name < $1.name }
    }

    private func listScreenshots() -> FakeResponse {
        let shots = lock.withLock { sortedScreenshots() }
        return .json(200, [
            "screenshots": shots.map { shot -> [String: Any] in
                [
                    "screenshotId": shot.id, "name": shot.name, "takenAt": ProtocolJSON.formatDate(shot.takenAt),
                    "sizeBytes": shot.sizeBytes, "format": shot.format,
                    "gameName": shot.gameName.map { $0 as Any } ?? NSNull(),
                    "hasThumbnail": shot.format != "bmp", "futureField": "ignored by the app",
                ]
            },
            "totalBytes": shots.reduce(Int64(0)) { $0 + $1.sizeBytes }, "truncated": false,
        ])
    }

    private func screenshotContent(id: String, thumbnail: Bool) -> FakeResponse {
        let found = lock.withLock { () -> (FakeScreenshot, Data)? in
            guard let shot = screenshotsState.screenshots.first(where: { $0.id == id }) else { return nil }
            if thumbnail && shot.format == "bmp" { return (shot, Data()) }
            guard _drawsArtwork else { return (shot, Self.artPNG) }
            let key = "screenshot#\(shot.name)#\(thumbnail)"
            let data = drawnPicture(key: key) {
                let slot = 100 + Int(shot.takenAt.timeIntervalSince1970.truncatingRemainder(dividingBy: 997))
                return thumbnail
                    ? DemoArtwork.screen(for: shot.seed, slot: slot, width: 320, height: 240, jpeg: true)
                    : DemoArtwork.screen(for: shot.seed, slot: slot, width: 1024, height: 768)
            }
            return (shot, data)
        }
        guard let (shot, data) = found else {
            if !id.hasPrefix("sc_") || Base64URL.decode(String(id.dropFirst(3))) == nil {
                return .error(400, code: "invalid_id", message: "Invalid identifier")
            }
            return .error(404, code: "not_found", message: "Screenshot not found")
        }
        if thumbnail {
            guard !data.isEmpty else { return .error(404, code: "not_found", message: "Screenshot not found") }
            return FakeResponse(status: 200, headers: ["Content-Type": "image/jpeg"], body: data)
        }
        return FakeResponse(status: 200, headers: [
            "Content-Type": shot.format == "bmp" ? "image/bmp" : "image/png",
            "Content-Disposition": "attachment; filename=\"\(shot.name.replacingOccurrences(of: "\"", with: "\\\""))\"",
        ], body: data)
    }

    /// Moves screenshots to the trash in one entry, all or nothing.
    func trashScreenshots(ids: [String]) -> FakeResponse {
        var seen = Set<String>()
        let unique = ids.filter { seen.insert($0).inserted }
        for id in unique where !id.hasPrefix("sc_") || Base64URL.decode(String(id.dropFirst(3))) == nil {
            return .error(400, code: "invalid_id", message: "Invalid identifier")
        }
        let response = lock.withLock { () -> FakeResponse in
            let missing = unique.filter { id in !screenshotsState.screenshots.contains { $0.id == id } }
            guard missing.isEmpty else {
                return .error(404, code: "not_found", message: "Screenshot not found", details: ["missing": missing])
            }
            let moved = unique.compactMap { id in screenshotsState.screenshots.first { $0.id == id } }
            screenshotsState.screenshots.removeAll { unique.contains($0.id) }
            let now = Date()
            let stamp = ProtocolJSON.formatDate(now).replacingOccurrences(of: "-", with: "").replacingOccurrences(of: ":", with: "")
            let entryId = "tr_\(stamp)_\(UUID().uuidString.prefix(4).lowercased())"
            let files = moved.map { "Screenshots/" + $0.name }
            let bytes = moved.reduce(Int64(0)) { $0 + $1.sizeBytes }
            trashEntries.insert([
                "id": entryId, "trashedAt": ProtocolJSON.formatDate(now), "reason": "delete-screenshot",
                "label": moved.count == 1 ? moved[0].name : "\(moved.count) screenshots",
                "files": files, "sizeBytes": bytes, "expiresAt": ProtocolJSON.formatDate(now.addingTimeInterval(30 * 86_400)),
            ], at: 0)
            trashPayloads[entryId] = .screenshots(moved)
            return .json(200, ["trashEntryId": entryId, "moved": files, "movedBytes": bytes])
        }
        if response.status == 200 { emitScreenshots() }
        return response
    }

    /// The `screenshots` event (M14, M22).
    func emitScreenshots() {
        emit("screenshots", [String: Any]())
    }

    /// Whether a screenshot name is taken (ignoring case, lock held).
    func screenshotTaken(_ name: String) -> Bool {
        screenshotsState.screenshots.contains { $0.name.lowercased() == name.lowercased() }
    }

    /// A screenshot name with " (n)" before its extension.
    static func screenshotName(_ name: String, number: Int?) -> String {
        guard let number else { return name }
        let ext = (name as NSString).pathExtension
        let base = ext.isEmpty ? name : String(name.dropLast(ext.count + 1))
        return ext.isEmpty ? "\(base) (\(number))" : "\(base) (\(number)).\(ext)"
    }
}
