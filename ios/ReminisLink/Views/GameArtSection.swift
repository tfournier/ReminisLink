import PhotosUI
import ReminisLinkKit
import SwiftUI
import UniformTypeIdentifiers

/// The image of a game in the game sheet (milestone M8): change it from
/// Photos or Files, or remove it. The previous image goes to the trash of
/// the console.
struct GameArtSection: View {
    @Environment(AppModel.self) private var app
    let deviceId: String
    let gameId: String
    let hasArt: Bool

    @State private var choosingSource = false
    @State private var showPhotos = false
    @State private var showFiles = false
    @State private var photo: PhotosPickerItem?
    @State private var confirmRemove = false
    @State private var isWorking = false
    @State private var error: AppError?
    @State private var notice: String?

    var body: some View {
        Section {
            if let error {
                ErrorView(error: error)
            }
            if let notice {
                NoticeView(text: notice)
            }
            Button {
                choosingSource = true
            } label: {
                HStack {
                    Label("Change Image…", systemImage: "photo")
                    if isWorking {
                        Spacer()
                        ProgressView()
                    }
                }
            }
            .disabled(isWorking)
            .accessibilityIdentifier("game.changeArt")
            .confirmationDialog("Change the Image", isPresented: $choosingSource, titleVisibility: .visible) {
                Button("Photo Library") { showPhotos = true }
                    .accessibilityIdentifier("art.photos")
                Button("Files") { showFiles = true }
                    .accessibilityIdentifier("art.files")
                #if DEBUG
                if ProcessInfo.processInfo.environment["RL_UITEST_ART_PICTURE"] == "1" {
                    // UI tests: Photos and Files cannot be driven by XCUITest.
                    Button("Test Picture") { Task { await set(.data(Self.testPicture())) } }
                        .accessibilityIdentifier("art.test")
                }
                #endif
            } message: {
                Text("The picture is scaled down to at most 1024 pixels and sent as a PNG.")
            }
            if hasArt {
                Button("Remove Image", role: .destructive) { confirmRemove = true }
                    .disabled(isWorking)
                    .accessibilityIdentifier("game.removeArt")
                    .confirmationDialog("Remove the image?", isPresented: $confirmRemove, titleVisibility: .visible) {
                        Button("Remove Image", role: .destructive) { Task { await remove() } }
                            .accessibilityIdentifier("art.removeConfirm")
                    } message: {
                        Text("The current image goes to the trash of the console.")
                    }
            }
        } header: {
            Text("Image")
        } footer: {
            Text("Shown by NextUI in the game list. The previous image goes to the trash of the console.")
        }
        .photosPicker(isPresented: $showPhotos, selection: $photo, matching: .images)
        .onChange(of: photo) { _, item in
            guard let item else { return }
            photo = nil
            Task { await setFromPhotos(item) }
        }
        .fileImporter(isPresented: $showFiles, allowedContentTypes: [.image]) { result in
            switch result {
            case .success(let url): Task { await set(.file(url)) }
            case .failure: error = .file
            }
        }
    }

    private func setFromPhotos(_ item: PhotosPickerItem) async {
        isWorking = true
        defer { isWorking = false }
        do {
            guard let data = try await item.loadTransferable(type: Data.self) else { throw AppError.unreadablePicture }
            try await apply { try await $0.setArt(gameId: gameId, from: .data(data)) }
            notice = String(localized: "Image changed.")
        } catch {
            show(error)
        }
    }

    private func set(_ picture: ArtPicture) async {
        isWorking = true
        defer { isWorking = false }
        do {
            try await apply { try await $0.setArt(gameId: gameId, from: picture) }
            notice = String(localized: "Image changed.")
        } catch {
            show(error)
        }
    }

    private func remove() async {
        isWorking = true
        defer { isWorking = false }
        do {
            try await apply { try await $0.removeArt(gameId: gameId) }
            notice = String(localized: "Image removed. It is in the trash of the console.")
        } catch {
            show(error)
        }
    }

    private func apply(_ action: (ConsoleSession) async throws -> Void) async throws {
        error = nil
        notice = nil
        try await action(app.session(for: deviceId))
    }

    private func show(_ error: Error) {
        let error = AppError(error)
        if !error.isCancellation { self.error = error }
    }

    #if DEBUG
    /// A drawn 1600 x 1200 picture (no game art), as a JPEG.
    static func testPicture() -> Data {
        let size = CGSize(width: 1600, height: 1200)
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        let image = UIGraphicsImageRenderer(size: size, format: format).image { context in
            UIColor.systemTeal.setFill()
            context.fill(CGRect(origin: .zero, size: size))
            UIColor.systemYellow.setFill()
            context.cgContext.fillEllipse(in: CGRect(x: 500, y: 300, width: 600, height: 600))
        }
        return image.jpegData(compressionQuality: 0.9) ?? Data()
    }
    #endif
}
