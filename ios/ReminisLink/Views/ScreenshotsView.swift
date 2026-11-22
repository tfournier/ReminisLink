import ReminisLinkKit
import SwiftUI
import UIKit

/// The screenshots taken on a console (milestone M22,
/// docs/specs/06-app-ios.md): a grid of previews grouped by day, whose
/// columns follow the width of the window (iPhone, iPad, Mac). Tap one to
/// open it; Select picks several to share (Save Image, Save to Files,
/// AirDrop…), add to Photos or move to the trash of the console.
struct ScreenshotsView: View {
    @Environment(AppModel.self) private var app
    let deviceId: String

    @State private var width: Double = 0
    @State private var isSelecting = false
    @State private var selection = ScreenshotSelection()
    @State private var opened: Screenshot?
    @State private var exported: ExportedFile?
    @State private var toTrash: [Screenshot]?
    @State private var actionError: AppError?
    @State private var notice: String?
    @State private var isWorking = false
    @State private var demoPhotos = false

    var body: some View {
        let manager = app.session(for: deviceId).screenshots
        let columns = Array(
            repeating: GridItem(.flexible(), spacing: ScreenshotGridLayout.spacing),
            count: ScreenshotGridLayout.columns(forWidth: width)
        )
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                if let error = manager.loadError {
                    ErrorView(error: error, retry: { await manager.load() }).padding(.horizontal)
                }
                if let actionError {
                    ErrorView(error: actionError).padding(.horizontal)
                }
                if let notice {
                    NoticeView(text: notice).padding(.horizontal)
                }
                if manager.list?.truncated == true {
                    Text("Only the 5,000 newest screenshots are shown.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .padding(.horizontal)
                }
                LazyVGrid(columns: columns, alignment: .leading, spacing: ScreenshotGridLayout.spacing, pinnedViews: [.sectionHeaders]) {
                    ForEach(ScreenshotList.byDay(manager.screenshots), id: \.day) { group in
                        Section {
                            ForEach(group.screenshots) { shot in
                                cell(shot, manager: manager)
                            }
                        } header: {
                            Text(group.day.formatted(date: .complete, time: .omitted))
                                .font(.headline)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(.horizontal)
                                .padding(.vertical, 6)
                                .background(.bar)
                                .accessibilityAddTraits(.isHeader)
                        }
                    }
                }
            }
            .padding(.bottom, isSelecting ? 8 : 0)
        }
        .onGeometryChange(for: Double.self) { $0.size.width } action: { width = $0 }
        .overlay {
            if manager.list == nil && manager.loadError == nil {
                ProgressView()
            } else if manager.list?.screenshots.isEmpty == true {
                ContentUnavailableView {
                    Label("No Screenshots", systemImage: "camera.viewfinder")
                } description: {
                    Text("Screenshots taken on the console appear here.")
                }
            }
        }
        .navigationTitle("Screenshots")
        .toolbar { toolbar(manager) }
        .task { await manager.load() }
        .refreshable { await manager.load() }
        .onChange(of: manager.revision) { _, _ in selection.keep(manager.screenshots) }
        .navigationDestination(item: $opened) { shot in
            ScreenshotDetailView(deviceId: deviceId, screenshot: shot)
        }
        .exportSheet($exported)
        .confirmationDialog(
            trashTitle(toTrash ?? []),
            isPresented: Binding(get: { toTrash != nil }, set: { if !$0 { toTrash = nil } }),
            titleVisibility: .visible,
            presenting: toTrash
        ) { shots in
            Button("Move to Trash", role: .destructive) {
                Task { await run { try await manager.trash(shots) ; finishSelection() } }
            }
            .accessibilityIdentifier("screenshots.trash.confirm")
        } message: { _ in
            Text("They stay 30 days in the trash of the console, where you can restore them.")
        }
        .alert("Saved to Photos", isPresented: $demoPhotos) {
            Button("OK", role: .cancel) {}
                .accessibilityIdentifier("demo.photos.ok")
        } message: {
            Text("Demo mode: with a real console, the screenshots are added to Photos here; in the demo, nothing leaves the app.")
        }
    }

    // MARK: - Cells

    private func cell(_ shot: Screenshot, manager: ScreenshotManager) -> some View {
        let selected = selection.contains(shot.screenshotId)
        return Button {
            if isSelecting {
                selection.toggle(shot.screenshotId)
            } else {
                opened = shot
            }
        } label: {
            ScreenshotThumbnail(screenshot: shot, manager: manager)
                .overlay(alignment: .bottomTrailing) {
                    if isSelecting {
                        Image(systemName: selected ? "checkmark.circle.fill" : "circle")
                            .font(.title2)
                            .symbolRenderingMode(.palette)
                            .foregroundStyle(.white, selected ? Color.accentColor : Color.black.opacity(0.3))
                            .padding(6)
                            .accessibilityHidden(true)
                    }
                }
                .overlay {
                    if isSelecting && selected {
                        Rectangle().fill(Color.white.opacity(0.18))
                    }
                }
        }
        .buttonStyle(.plain)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(shot.title))
        .accessibilityValue(Text(shot.takenAt.dateTimeString))
        .accessibilityAddTraits(isSelecting && selected ? [.isButton, .isSelected] : [.isButton])
        .accessibilityHint(isSelecting ? Text("Selects or deselects the screenshot.") : Text("Opens the screenshot."))
        .accessibilityIdentifier("screenshots.cell")
        .contextMenu {
            if !isSelecting {
                Button("Share…", systemImage: "square.and.arrow.up") { share([shot]) }
                Button("Save to Photos", systemImage: "photo.badge.plus") { saveToPhotos([shot], manager: manager) }
                Button("Move to Trash…", systemImage: "trash", role: .destructive) { toTrash = [shot] }
            }
        }
    }

    // MARK: - Toolbar

    @ToolbarContentBuilder
    private func toolbar(_ manager: ScreenshotManager) -> some ToolbarContent {
        let shots = manager.screenshots
        let picked = selection.selected(in: shots)
        ToolbarItem(placement: .primaryAction) {
            Button(isSelecting ? "Done" : "Select") {
                if isSelecting { finishSelection() } else { isSelecting = true }
            }
            .disabled(shots.isEmpty && !isSelecting)
            .accessibilityIdentifier("screenshots.select")
        }
        if isSelecting {
            ToolbarItem(placement: .topBarLeading) {
                Button(selection.count == shots.count && !shots.isEmpty ? "Deselect All" : "Select All") {
                    selection.toggleAll(shots)
                }
                .accessibilityIdentifier("screenshots.selectAll")
            }
            ToolbarItemGroup(placement: .bottomBar) {
                Button { share(picked) } label: { Label("Share", systemImage: "square.and.arrow.up") }
                    .disabled(picked.isEmpty || isWorking)
                    .accessibilityIdentifier("screenshots.share")
                Spacer()
                if isWorking {
                    ProgressView()
                } else {
                    (picked.isEmpty ? Text("Select Screenshots") : Text("\(picked.count) Selected"))
                        .font(.footnote)
                        .accessibilityIdentifier("screenshots.count")
                }
                Spacer()
                Button { saveToPhotos(picked, manager: manager) } label: { Label("Save to Photos", systemImage: "photo.badge.plus") }
                    .disabled(picked.isEmpty || isWorking)
                    .accessibilityIdentifier("screenshots.photos")
                Button(role: .destructive) { toTrash = picked } label: { Label("Move to Trash", systemImage: "trash") }
                    .disabled(picked.isEmpty || isWorking)
                    .accessibilityIdentifier("screenshots.trash")
            }
        }
    }

    // MARK: - Actions

    private func trashTitle(_ shots: [Screenshot]) -> String {
        shots.count == 1
            ? String(localized: "Move this screenshot to the trash of the console?")
            : String(localized: "Move \(shots.count) screenshots to the trash of the console?")
    }

    private func finishSelection() {
        isSelecting = false
        selection.clear()
    }

    private func share(_ shots: [Screenshot]) {
        guard !shots.isEmpty else { return }
        let manager = app.session(for: deviceId).screenshots
        Task { await run { exported = try await manager.export(shots) } }
    }

    private func saveToPhotos(_ shots: [Screenshot], manager: ScreenshotManager) {
        guard !shots.isEmpty else { return }
        if app.isDemo {
            // M16: nothing leaves the app in the demo mode.
            demoPhotos = true
            return
        }
        Task {
            await run {
                let count = try await manager.saveToPhotos(shots)
                notice = count == 1
                    ? String(localized: "The screenshot was added to Photos.")
                    : String(localized: "\(count) screenshots were added to Photos.")
            }
        }
    }

    private func run(_ operation: () async throws -> Void) async {
        isWorking = true
        defer { isWorking = false }
        do {
            try await operation()
            actionError = nil
        } catch {
            let error = AppError(error)
            if !error.isCancellation { actionError = error }
        }
    }
}

/// A preview of the grid: 4:3, filled, from memory or the console; a
/// placeholder when the pak makes none (a BMP) or while it loads.
struct ScreenshotThumbnail: View {
    let screenshot: Screenshot
    let manager: ScreenshotManager
    @State private var image: UIImage?

    var body: some View {
        Color(.secondarySystemFill)
            .aspectRatio(4 / 3, contentMode: .fit)
            .overlay {
                if let image {
                    Image(uiImage: image)
                        .resizable()
                        .scaledToFill()
                } else {
                    Image(systemName: screenshot.hasThumbnail ? "photo" : "photo.badge.exclamationmark")
                        .font(.title2)
                        .foregroundStyle(.secondary)
                }
            }
            .clipped()
            .task(id: screenshot.thumbnailKey) {
                if let data = manager.cachedThumbnail(for: screenshot) {
                    image = UIImage(data: data)
                    return
                }
                let data = await manager.thumbnail(for: screenshot)
                guard !Task.isCancelled else { return }
                image = data.flatMap(UIImage.init(data:))
            }
    }
}

/// One screenshot, whole (milestone M22): its game, date, size and file
/// name, with Share, Save to Photos and Move to Trash.
struct ScreenshotDetailView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    let deviceId: String
    let screenshot: Screenshot

    @State private var image: UIImage?
    @State private var loadError: AppError?
    @State private var actionError: AppError?
    @State private var notice: String?
    @State private var exported: ExportedFile?
    @State private var confirmTrash = false
    @State private var isWorking = false
    @State private var demoPhotos = false

    var body: some View {
        let manager = app.session(for: deviceId).screenshots
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                Group {
                    if let image {
                        Image(uiImage: image)
                            .resizable()
                            .interpolation(.none) // pixel art stays sharp
                            .scaledToFit()
                            .accessibilityLabel(Text(screenshot.title))
                            .accessibilityIdentifier("screenshot.image")
                    } else if let loadError {
                        ErrorView(error: loadError, retry: { await load(manager) })
                    } else {
                        Color(.secondarySystemFill)
                            .aspectRatio(4 / 3, contentMode: .fit)
                            .overlay { ProgressView() }
                    }
                }
                .frame(maxWidth: .infinity)
                if let actionError { ErrorView(error: actionError) }
                if let notice { NoticeView(text: notice) }
                VStack(alignment: .leading, spacing: 6) {
                    Text(screenshot.title).font(.title2.bold())
                    LabeledContent("Taken", value: screenshot.takenAt.dateTimeString)
                    LabeledContent("Size", value: screenshot.sizeBytes.byteString)
                    LabeledContent("File", value: screenshot.name)
                }
                .readableWidthInline()
            }
            .padding()
        }
        .navigationTitle(screenshot.title)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItemGroup(placement: .primaryAction) {
                Button { share(manager) } label: { Label("Share", systemImage: "square.and.arrow.up") }
                    .disabled(isWorking)
                    .accessibilityIdentifier("screenshot.share")
                Menu {
                    Button("Save to Photos", systemImage: "photo.badge.plus") { saveToPhotos(manager) }
                        .accessibilityIdentifier("screenshot.photos")
                    Button("Move to Trash…", systemImage: "trash", role: .destructive) { confirmTrash = true }
                        .accessibilityIdentifier("screenshot.trash")
                } label: {
                    Label("More", systemImage: "ellipsis.circle")
                }
                .disabled(isWorking)
                .accessibilityIdentifier("screenshot.more")
            }
        }
        .task { await load(manager) }
        .exportSheet($exported)
        .confirmationDialog("Move this screenshot to the trash of the console?", isPresented: $confirmTrash, titleVisibility: .visible) {
            Button("Move to Trash", role: .destructive) {
                Task {
                    await run {
                        try await manager.trash([screenshot])
                        dismiss()
                    }
                }
            }
            .accessibilityIdentifier("screenshot.trash.confirm")
        } message: {
            Text("It stays 30 days in the trash of the console, where you can restore it.")
        }
        .alert("Saved to Photos", isPresented: $demoPhotos) {
            Button("OK", role: .cancel) {}
                .accessibilityIdentifier("demo.photos.ok")
        } message: {
            Text("Demo mode: with a real console, the screenshots are added to Photos here; in the demo, nothing leaves the app.")
        }
    }

    private func load(_ manager: ScreenshotManager) async {
        loadError = nil
        do {
            // At most the size of the screen, in pixels: a large image is
            // subsampled while it is decoded (Codex review of M22).
            let screen = UIScreen.main.bounds.size
            let maxPixels = Int(max(screen.width, screen.height) * UIScreen.main.scale)
            let picture = try await manager.displayImage(for: screenshot, maxPixels: min(maxPixels, 4096))
            image = UIImage(cgImage: picture)
        } catch {
            let error = AppError(error)
            if !error.isCancellation { loadError = error }
        }
    }

    private func share(_ manager: ScreenshotManager) {
        Task { await run { exported = try await manager.export([screenshot]) } }
    }

    private func saveToPhotos(_ manager: ScreenshotManager) {
        if app.isDemo {
            demoPhotos = true
            return
        }
        Task {
            await run {
                _ = try await manager.saveToPhotos([screenshot])
                notice = String(localized: "The screenshot was added to Photos.")
            }
        }
    }

    private func run(_ operation: () async throws -> Void) async {
        isWorking = true
        defer { isWorking = false }
        do {
            try await operation()
            actionError = nil
        } catch {
            let error = AppError(error)
            if !error.isCancellation { actionError = error }
        }
    }
}

private extension View {
    /// The text under the image keeps a readable width in a wide window.
    func readableWidthInline() -> some View {
        frame(maxWidth: ReadableWidth.maxWidth, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}
