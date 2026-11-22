import Foundation

// Codable models and pure logic of milestone M22 (protocol/openapi.yaml,
// tag Screenshots): the screenshots taken on the console. Identifiers are
// opaque: sent back as received.

/// `png`, `jpeg` or `bmp`, from the extension of the file. Open: an unknown
/// format from a newer pak is kept (shown as a generic image).
public struct ScreenshotFormat: RawRepresentable, Codable, Hashable, Sendable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public init(from decoder: Decoder) throws {
        rawValue = try decoder.singleValueContainer().decode(String.self)
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        try c.encode(rawValue)
    }

    public static let png = ScreenshotFormat(rawValue: "png")
    public static let jpeg = ScreenshotFormat(rawValue: "jpeg")
    public static let bmp = ScreenshotFormat(rawValue: "bmp")
}

/// A screenshot of `GET /screenshots`.
public struct Screenshot: Codable, Sendable, Equatable, Hashable, Identifiable {
    public let screenshotId: String
    /// The file name on the card (informative, never sent back).
    public let name: String
    /// From the file name when the OS writes a date in it, else the date
    /// of the file.
    public let takenAt: Date
    public let sizeBytes: Int64
    public let format: ScreenshotFormat
    /// The game the file name gives (informative), nil when none.
    public let gameName: String?
    /// `GET /screenshots/{id}/thumbnail` may make a preview.
    public let hasThumbnail: Bool

    public var id: String { screenshotId }

    public init(
        screenshotId: String, name: String, takenAt: Date, sizeBytes: Int64, format: ScreenshotFormat = .png,
        gameName: String? = nil, hasThumbnail: Bool = true
    ) {
        self.screenshotId = screenshotId
        self.name = name
        self.takenAt = takenAt
        self.sizeBytes = sizeBytes
        self.format = format
        self.gameName = gameName
        self.hasThumbnail = hasThumbnail
    }

    /// Lenient: `sizeBytes`, `format`, `gameName` and `hasThumbnail` may be
    /// missing (0, `png`, nil, false); unknown fields are ignored.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        screenshotId = try c.decode(String.self, forKey: .screenshotId)
        name = try c.decode(String.self, forKey: .name)
        takenAt = try c.decode(Date.self, forKey: .takenAt)
        sizeBytes = try c.decodeIfPresent(Int64.self, forKey: .sizeBytes) ?? 0
        format = try c.decodeIfPresent(ScreenshotFormat.self, forKey: .format) ?? .png
        gameName = try c.decodeIfPresent(String.self, forKey: .gameName)
        hasThumbnail = try c.decodeIfPresent(Bool.self, forKey: .hasThumbnail) ?? false
    }

    private enum CodingKeys: String, CodingKey {
        case screenshotId, name, takenAt, sizeBytes, format, gameName, hasThumbnail
    }

    /// What the app shows as the title: the game, else the file name
    /// without its extension.
    public var title: String {
        if let gameName, !gameName.isEmpty { return gameName }
        let stem = (name as NSString).deletingPathExtension
        return stem.isEmpty ? name : stem
    }

    /// The key of a cached preview: a screenshot replaced under the same
    /// name (another size or date) gets a new one.
    public var thumbnailKey: String { "\(screenshotId)|\(sizeBytes)|\(Int64(takenAt.timeIntervalSince1970))" }
}

/// `GET /screenshots`.
public struct ScreenshotList: Codable, Sendable, Equatable {
    public let screenshots: [Screenshot]
    public let totalBytes: Int64
    /// More than 5000 screenshots: the newest are listed.
    public let truncated: Bool

    public init(screenshots: [Screenshot], totalBytes: Int64? = nil, truncated: Bool = false) {
        self.screenshots = screenshots
        self.totalBytes = totalBytes ?? screenshots.reduce(0) { $0 + $1.sizeBytes }
        self.truncated = truncated
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        screenshots = try c.decode([Screenshot].self, forKey: .screenshots)
        totalBytes = try c.decodeIfPresent(Int64.self, forKey: .totalBytes) ?? screenshots.reduce(0) { $0 + $1.sizeBytes }
        truncated = try c.decodeIfPresent(Bool.self, forKey: .truncated) ?? false
    }

    private enum CodingKeys: String, CodingKey { case screenshots, totalBytes, truncated }

    /// The screenshots grouped by day of `takenAt` in `calendar` (its time
    /// zone), newest day first, each newest first.
    public static func byDay(_ screenshots: [Screenshot], calendar: Calendar = .current) -> [(day: Date, screenshots: [Screenshot])] {
        let sorted = screenshots.sorted {
            $0.takenAt != $1.takenAt ? $0.takenAt > $1.takenAt : $0.name < $1.name
        }
        var groups: [(day: Date, screenshots: [Screenshot])] = []
        for shot in sorted {
            let day = calendar.startOfDay(for: shot.takenAt)
            if let last = groups.indices.last, groups[last].day == day {
                groups[last].screenshots.append(shot)
            } else {
                groups.append((day, [shot]))
            }
        }
        return groups
    }
}

/// `screenshots` event (M22): screenshots went to the trash or came back;
/// reload `GET /screenshots`.
public struct EventScreenshots: Codable, Sendable, Equatable {
    public init() {}
}

/// `POST /screenshots/trash`.
public struct ScreenshotTrashRequest: Codable, Sendable, Equatable {
    public let screenshotIds: [String]

    public init(screenshotIds: [String]) { self.screenshotIds = screenshotIds }

    /// The most identifiers of one request.
    public static let maxIds = 500

    /// The requests that move `ids` (duplicates once, order kept), at most
    /// ``maxIds`` each.
    public static func batches(_ ids: [String]) -> [ScreenshotTrashRequest] {
        var seen = Set<String>()
        let unique = ids.filter { seen.insert($0).inserted }
        return stride(from: 0, to: unique.count, by: maxIds).map {
            ScreenshotTrashRequest(screenshotIds: Array(unique[$0..<min($0 + maxIds, unique.count)]))
        }
    }
}

/// The columns of the grid of screenshots for a width (universal layout,
/// M15: iPhone, iPad and Mac windows are told apart by their width, never
/// by the device).
public enum ScreenshotGridLayout {
    /// The narrowest a cell gets, and the spacing between cells (points).
    public static let minimumCell: Double = 104
    public static let spacing: Double = 4

    /// How many columns fit in `width` (at least 2, at most 8).
    public static func columns(forWidth width: Double) -> Int {
        guard width.isFinite, width > 0 else { return 2 }
        let fit = Int((width + spacing) / (minimumCell + spacing))
        return min(8, max(2, fit))
    }
}

/// What a selection of screenshots holds (multiple selection, M22).
public struct ScreenshotSelection: Equatable, Sendable {
    public private(set) var ids: Set<String> = []

    public init(ids: Set<String> = []) { self.ids = ids }

    public var isEmpty: Bool { ids.isEmpty }
    public var count: Int { ids.count }

    public func contains(_ id: String) -> Bool { ids.contains(id) }

    public mutating func toggle(_ id: String) {
        if ids.remove(id) == nil { ids.insert(id) }
    }

    /// Selects every screenshot, or none when all are selected already.
    public mutating func toggleAll(_ all: [Screenshot]) {
        let every = Set(all.map(\.screenshotId))
        ids = every.isSubset(of: ids) && !every.isEmpty ? [] : every
    }

    public mutating func clear() { ids = [] }

    /// Keeps only the screenshots still listed (after a reload).
    public mutating func keep(_ listed: [Screenshot]) {
        ids.formIntersection(listed.map(\.screenshotId))
    }

    /// The selected screenshots, in the order of `all`.
    public func selected(in all: [Screenshot]) -> [Screenshot] {
        all.filter { ids.contains($0.screenshotId) }
    }
}

extension TrashEntry {
    /// The reason of screenshots moved to the trash (M22).
    public static let screenshotReason = "delete-screenshot"

    /// How many screenshots the entry holds (nil for another reason): the
    /// pak labels several in English (`3 screenshots`), the app says it in
    /// the user's language instead.
    public var screenshotCount: Int? { reason == Self.screenshotReason ? files.count : nil }
}
