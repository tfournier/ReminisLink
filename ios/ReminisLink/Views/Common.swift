import ReminisLinkKit
import SwiftUI
import UIKit

extension Int64 {
    /// A file size in the user's locale ("16,8 Mo").
    var byteString: String { formatted(.byteCount(style: .file)) }
}

extension DeviceModel {
    var symbolName: String { self == .desktop ? "desktopcomputer" : "gamecontroller" }

    /// Product names stay as they are; "Desktop" (the pak on a computer) is
    /// translated.
    var localizedName: String {
        self == .desktop ? String(localized: "Desktop") : displayName
    }
}

/// An error with the action of docs/specs/06-app-ios.md §5.
struct ErrorView: View {
    @Environment(AppModel.self) private var app
    let error: AppError
    var retry: (() async -> Void)?
    /// The console to pair again on "Pair Again" (default: the console of
    /// the current screen).
    var deviceId: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Label {
                Text(error.message)
                    .fixedSize(horizontal: false, vertical: true)
            } icon: {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
                    .accessibilityHidden(true)
            }
            switch error.action {
            case .openSettings:
                Button("Open Settings") {
                    if let url = URL(string: UIApplication.openSettingsURLString) {
                        UIApplication.shared.open(url)
                    }
                }
                .buttonStyle(.borderedProminent)
            case .retry:
                if let retry {
                    Button("Try Again") { Task { await retry() } }
                        .buttonStyle(.borderedProminent)
                        .accessibilityIdentifier("error.retry")
                }
            case .repair:
                Button("Pair Again") { app.showRepair(deviceId: deviceId) }
                    .buttonStyle(.borderedProminent)
            case nil:
                EmptyView()
            }
        }
        .padding(.vertical, 4)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("error")
    }
}

/// A small icon with a number, read as a whole by VoiceOver.
struct Badge: View {
    let systemImage: String
    var count: Int?
    let accessibilityLabel: Text

    var body: some View {
        HStack(spacing: 2) {
            Image(systemName: systemImage)
            if let count { Text(count, format: .number) }
        }
        .font(.caption)
        .foregroundStyle(.secondary)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityLabel)
    }
}

/// Art of a game, from the disk cache or the console.
struct GameArtView: View {
    @Environment(AppModel.self) private var app
    let deviceId: String
    let gameId: String
    let hasArt: Bool
    var size: CGFloat = 44
    /// UI tests (Debug, `RL_UITEST_ART_PICTURE=1`): the view is exposed as
    /// `game.art`, its value `image` or `none`.
    var exposedToUITests = false

    @State private var image: UIImage?

    private var isExposed: Bool {
        #if DEBUG
        exposedToUITests && ProcessInfo.processInfo.environment["RL_UITEST_ART_PICTURE"] == "1"
        #else
        false
        #endif
    }

    var body: some View {
        Group {
            if let image {
                Image(uiImage: image)
                    .resizable()
                    .scaledToFit()
            } else {
                Image(systemName: "gamecontroller")
                    .font(.system(size: size * 0.4))
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(.quaternary)
            }
        }
        .frame(width: size, height: size)
        .clipShape(RoundedRectangle(cornerRadius: size / 8))
        .accessibilityHidden(!isExposed)
        .accessibilityIdentifier(isExposed ? "game.art" : "")
        .accessibilityValue(Text(verbatim: isExposed ? (image == nil ? "none" : "image") : ""))
        // Loaded again when the art is set or removed (M8).
        .task(id: "\(gameId)|\(hasArt)|\(app.session(for: deviceId).artRevision)") { await load() }
    }

    /// Best effort: an art error never touches the session. The disk cache
    /// is read off the main actor.
    private func load() async {
        guard hasArt else {
            image = nil
            return
        }
        let cache = app.artCache
        let deviceId = deviceId
        let gameId = gameId
        let cached = await Task.detached(priority: .utility) {
            cache.entry(deviceId: deviceId, gameId: gameId)?.data
        }.value
        if let cached { image = UIImage(data: cached) }
        guard let client = app.session(for: deviceId).client,
              let data = try? await cache.load(gameId: gameId, client: client),
              data != cached
        else { return }
        image = UIImage(data: data)
    }
}

/// Storage bar: used space, with the share taken by the trash.
struct StorageBar: View {
    let storage: DeviceStatus.Storage
    let trashBytes: Int64?

    var body: some View {
        let total = max(Double(storage.totalBytes), 1)
        let used = Double(storage.usedBytes) / total
        let trash = min(Double(trashBytes ?? 0) / total, used)
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(storage.label).font(.headline)
                Spacer()
                Text("\(storage.freeBytes.byteString) free")
                    .foregroundStyle(.secondary)
            }
            GeometryReader { geometry in
                ZStack(alignment: .leading) {
                    Capsule().fill(.quaternary)
                    Capsule().fill(Color.accentColor).frame(width: geometry.size.width * used)
                    Capsule().fill(.orange).frame(width: geometry.size.width * trash)
                }
            }
            .frame(height: 10)
            if let trashBytes, trashBytes > 0 {
                Text("Including \(trashBytes.byteString) in the trash")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(storage.label))
        .accessibilityValue(Text("\(storage.usedBytes.byteString) used of \(storage.totalBytes.byteString)"))
    }
}
