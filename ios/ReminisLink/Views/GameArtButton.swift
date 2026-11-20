import PhotosUI
import ReminisLinkKit
import SwiftUI
import UniformTypeIdentifiers

/// The image of a game in the header of the game sheet (milestone M8;
/// M11: tapping it changes it). Change it from Photos or Files, or remove
/// it; the previous image goes to the trash of the console.
struct GameArtButton: View {
    @Environment(AppModel.self) private var app
    let deviceId: String
    let gameId: String
    let hasArt: Bool
    /// The game is no longer on the console: the image is shown without
    /// any action.
    var isGameGone = false
    /// What the last action did, or its error, shown by the game sheet.
    @Binding var notice: String?
    @Binding var error: AppError?

    @State private var choosingSource = false
    @State private var showPhotos = false
    @State private var showFiles = false
    @State private var photo: PhotosPickerItem?
    @State private var confirmRemove = false
    @State private var isWorking = false
    @State private var isImageShown = false


    /// UI tests (Debug, `RL_UITEST_ART_PICTURE=1`): the value of the button
    /// is `image` or `none`.
    private var exposesValue: Bool {
        #if DEBUG
        ProcessInfo.processInfo.environment["RL_UITEST_ART_PICTURE"] == "1"
        #else
        false
        #endif
    }

    var body: some View {
        Button {
            choosingSource = true
        } label: {
            GameArtView(deviceId: deviceId, gameId: gameId, hasArt: hasArt, size: 96, onImageShown: { isImageShown = $0 })
                .overlay(alignment: .bottomTrailing) {
                    Group {
                        if isWorking {
                            ProgressView()
                                .padding(6)
                        } else if !isGameGone {
                            Image(systemName: "pencil.circle.fill")
                                .font(.title2)
                                .symbolRenderingMode(.palette)
                                .foregroundStyle(.white, Color.accentColor)
                        }
                    }
                    .background(.regularMaterial, in: Circle())
                    .offset(x: 6, y: 6)
                }
        }
        .buttonStyle(.plain)
        .disabled(!Self.isEnabled(isWorking: isWorking, isGameGone: isGameGone))
        .accessibilityLabel(Text(hasArt ? "Image of the game" : "No image"))
        .accessibilityHint(isGameGone ? Text(verbatim: "") : Text("Change or remove the image"))
        .accessibilityValue(Text(verbatim: exposesValue ? (isImageShown ? "image" : "none") : ""))
        .accessibilityIdentifier("game.changeArt")
        .confirmationDialog("Image of the Game", isPresented: $choosingSource, titleVisibility: .visible) {
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
            if hasArt {
                Button("Remove Image", role: .destructive) { confirmRemove = true }
                    .accessibilityIdentifier("game.removeArt")
            }
        } message: {
            Text("Shown by NextUI in the game list. The picture is scaled down to at most 1024 pixels and sent as a PNG; the previous image goes to the trash of the console.")
        }
        .confirmationDialog("Remove the image?", isPresented: $confirmRemove, titleVisibility: .visible) {
            Button("Remove Image", role: .destructive) { Task { await remove() } }
                .accessibilityIdentifier("art.removeConfirm")
        } message: {
            Text("The current image goes to the trash of the console.")
        }
        // The game went away with a dialog open: no action left.
        .onChange(of: isGameGone) { _, gone in
            if gone {
                choosingSource = false
                confirmRemove = false
                showPhotos = false
                showFiles = false
            }
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

    /// Whether the image can be changed: not during a change, not once
    /// the game is gone.
    static func isEnabled(isWorking: Bool, isGameGone: Bool) -> Bool {
        !isWorking && !isGameGone
    }

    private func setFromPhotos(_ item: PhotosPickerItem) async {
        isWorking = true
        defer { isWorking = false }
        do {
            guard let data = try await item.loadTransferable(type: Data.self) else { throw AppError.unreadablePicture }
            if try await apply({ try await $0.setArt(gameId: gameId, from: .data(data)) }) {
                notice = String(localized: "Image changed.")
            }
        } catch {
            show(error)
        }
    }

    private func set(_ picture: ArtPicture) async {
        isWorking = true
        defer { isWorking = false }
        do {
            if try await apply({ try await $0.setArt(gameId: gameId, from: picture) }) {
                notice = String(localized: "Image changed.")
            }
        } catch {
            show(error)
        }
    }

    private func remove() async {
        isWorking = true
        defer { isWorking = false }
        do {
            if try await apply({ try await $0.removeArt(gameId: gameId) }) {
                notice = String(localized: "Image removed. It is in the trash of the console.")
            }
        } catch {
            show(error)
        }
    }

    /// Runs a change of the image; a picker result arriving once the game
    /// is gone is ignored (nothing sent). Returns whether it ran.
    func apply(_ action: (ConsoleSession) async throws -> Void) async throws -> Bool {
        guard !isGameGone else { return false }
        error = nil
        notice = nil
        try await action(app.session(for: deviceId))
        return true
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
