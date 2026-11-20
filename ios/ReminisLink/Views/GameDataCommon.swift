import ReminisLinkKit
import SwiftUI
import UIKit

// Pieces shared by the Save, States and Cheats screens (milestone M5).

/// A file downloaded from the console, waiting for the share sheet.
struct ExportedFile: Identifiable {
    let id = UUID()
    let file: DownloadedFile
    /// Files shared with it, in the same directory (the `.rtc` of a save).
    var others: [URL] = []
}

/// `UIActivityViewController` for one file (and its companions).
struct ShareSheet: UIViewControllerRepresentable {
    let url: URL
    var others: [URL] = []
    let onFinish: () -> Void

    func makeUIViewController(context: Context) -> UIActivityViewController {
        let controller = UIActivityViewController(activityItems: [url] + others, applicationActivities: nil)
        controller.completionWithItemsHandler = { _, _, _, _ in onFinish() }
        return controller
    }

    func updateUIViewController(_ controller: UIActivityViewController, context: Context) {}
}

/// Shows the iOS share sheet for a downloaded file, then deletes the file
/// (and its directory) however the sheet closes.
///
/// In the demo mode (M16) the file never reaches the share sheet (iCloud
/// Drive, other apps, the share extension): the download is confirmed and
/// the file deleted.
struct ExportSheetModifier: ViewModifier {
    @Environment(AppModel.self) private var app
    @Binding var exported: ExportedFile?
    @State private var shown: ExportedFile?

    @ViewBuilder
    func body(content: Content) -> some View {
        if app.isDemo {
            content.alert(
                "Downloaded",
                isPresented: Binding(get: { exported != nil }, set: { if !$0 { dismissDemoExport() } })
            ) {
                Button("OK", role: .cancel) {}
                    .accessibilityIdentifier("demo.export.ok")
            } message: {
                Text("Demo mode: the file was downloaded from the demo console. With a real console, the share sheet opens here to save or send it; in the demo, nothing leaves the app.")
            }
        } else {
            shareSheet(content)
        }
    }

    private func dismissDemoExport() {
        if let exported { ExportedFiles.remove(exported.file) }
        exported = nil
    }

    private func shareSheet(_ content: Content) -> some View {
        content.sheet(item: $exported, onDismiss: {
            if let shown { ExportedFiles.remove(shown.file) }
            shown = nil
        }) { item in
            ShareSheet(url: item.file.url, others: item.others) { exported = nil }
                .presentationDetents([.medium, .large])
                .ignoresSafeArea()
                .onAppear { shown = item }
        }
    }
}

extension View {
    func exportSheet(_ exported: Binding<ExportedFile?>) -> some View {
        modifier(ExportSheetModifier(exported: exported))
    }
}

enum ExportedFiles {
    /// Deletes a downloaded file and its directory, off the main actor.
    nonisolated static func remove(_ file: DownloadedFile) {
        let directory = file.directory
        Task.detached(priority: .utility) {
            try? FileManager.default.removeItem(at: directory)
        }
    }
}

/// A file chosen in Files, copied into the app so it can be read after
/// the document picker's access ends (a directory per file).
struct PickedFile: Identifiable, Equatable {
    let id = UUID()
    let url: URL
    let name: String
    let sizeBytes: Int64

    var directory: URL { url.deletingLastPathComponent() }

    /// Copies `source` (security-scoped) into a new directory under `root`;
    /// the file system copies it, never memory.
    nonisolated static func copy(_ source: URL, into root: URL) async throws -> PickedFile {
        try await Task.detached(priority: .userInitiated) {
            let accessing = source.startAccessingSecurityScopedResource()
            defer { if accessing { source.stopAccessingSecurityScopedResource() } }
            let directory = root.appending(path: UUID().uuidString, directoryHint: .isDirectory)
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            let name = ContentDisposition.localName(source.lastPathComponent)
            let destination = directory.appending(path: name)
            do {
                try FileManager.default.copyItem(at: source, to: destination)
                return PickedFile(url: destination, name: name, sizeBytes: try FileDigest.size(of: destination))
            } catch {
                try? FileManager.default.removeItem(at: directory)
                throw error
            }
        }.value
    }

    /// Deletes the copy (when it was not handed to the transfer queue).
    func remove() {
        let directory = directory
        Task.detached(priority: .utility) { try? FileManager.default.removeItem(at: directory) }
    }
}

/// A short confirmation of what an action did.
struct NoticeView: View {
    let text: String

    var body: some View {
        Label {
            Text(text).fixedSize(horizontal: false, vertical: true)
        } icon: {
            Image(systemName: "checkmark.circle.fill")
                .foregroundStyle(.green)
                .accessibilityHidden(true)
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("notice")
    }
}

/// A capsule with a short word ("Restored", "Resume").
struct TagView: View {
    let text: Text
    var tint: Color = .accentColor

    var body: some View {
        text
            .font(.caption2.bold())
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(tint.opacity(0.18), in: Capsule())
            .foregroundStyle(tint)
    }
}

extension Date {
    /// "18 nov. 2026 à 17:00" in the user's locale.
    var dateTimeString: String { formatted(date: .abbreviated, time: .shortened) }
}

extension ConsoleSession {
    /// Downloads a file of the console into the export directory.
    func export(_ operation: @escaping @Sendable (APIClient, URL) async throws -> DownloadedFile) async throws -> ExportedFile {
        let directory = app.exportDirectory
        let file = try await app.work.run { try await perform { try await operation($0, directory) } }
        return ExportedFile(file: file)
    }
}
