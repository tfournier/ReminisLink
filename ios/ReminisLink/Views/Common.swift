import ReminisLinkKit
import SwiftUI
import UIKit

extension Int64 {
    /// A file size in the user's locale ("16,8 Mo").
    var byteString: String { formatted(.byteCount(style: .file)) }
}

extension DeviceModel {
    var symbolName: String { self == .desktop ? "desktopcomputer" : "gamecontroller" }
}

/// An error with the action of docs/specs/06-app-ios.md §5.
struct ErrorView: View {
    @Environment(AppModel.self) private var app
    let error: AppError
    var retry: (() async -> Void)?

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
                Button("Pair Again") { app.sheet = .addConsole }
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

    @State private var image: UIImage?

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
        .accessibilityHidden(true)
        .task(id: gameId) { await load() }
    }

    private func load() async {
        guard hasArt else { return }
        let session = app.session(for: deviceId)
        let cache = app.artCache
        if let cached = cache.entry(deviceId: deviceId, gameId: gameId) {
            image = UIImage(data: cached.data)
        }
        guard let data = try? await session.perform({ try await cache.load(gameId: gameId, client: $0) }) else { return }
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
