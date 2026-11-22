import ReminisLinkKit
import SwiftUI

/// Uploads and console jobs (downloads and patches, docs/specs/06-app-ios.md
/// §4.7): progress, speed, time left, cancel, resume. Jobs are polled every
/// second while the screen is visible.
struct TransfersView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.scenePhase) private var scenePhase
    let deviceId: String

    var body: some View {
        let queue = app.session(for: deviceId).transfers
        List {
            if !queue.uploads.isEmpty {
                Section("Uploads") {
                    ForEach(queue.uploads) { item in
                        UploadRow(item: item, queue: queue)
                    }
                }
            }
            if !queue.jobs.isEmpty {
                Section("Tasks on the Console") {
                    ForEach(queue.jobs) { item in
                        let paks = app.session(for: deviceId).paks
                        JobRow(item: item, queue: queue, pakInstall: paks.install(forJob: item.id), paks: paks)
                    }
                }
            }
        }
        .overlay {
            if queue.uploads.isEmpty && queue.jobs.isEmpty {
                ContentUnavailableView {
                    Label("No transfers", systemImage: "arrow.up.arrow.down")
                } description: {
                    Text("Files you add to the console appear here.")
                } actions: {
                    Button("Add") { app.showAdd(deviceId: deviceId) }
                }
            }
        }
        .navigationTitle("Transfers")
        // M12: a pak installation that meets an installed pak asks here too.
        .pakReplacementConfirmation(app.session(for: deviceId).paks)
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button {
                    app.showAdd(deviceId: deviceId)
                } label: {
                    Label("Add", systemImage: "plus")
                }
            }
            ToolbarItem(placement: .secondaryAction) {
                Button("Clear Finished") { queue.clearFinished() }
                    .disabled(!queue.uploads.contains { $0.phase.isFinished } && !queue.jobs.contains { $0.isFinished })
            }
        }
        // Every second while the screen is visible and the app active.
        .task(id: scenePhase) {
            guard scenePhase == .active else { return }
            while !Task.isCancelled {
                await queue.refreshJobs()
                try? await Task.sleep(for: .seconds(1))
            }
        }
    }
}

struct UploadRow: View {
    let item: UploadItem
    let queue: TransferQueue
    @State private var confirmResend = false

    /// What "Send Again" does after an unknown outcome. ROMs and staging
    /// files are sent under a new name (both copies kept); a save or a
    /// state always replaces the one on the console (the pak ignores the
    /// conflict rule for them), so it is said and confirmed first.
    enum Resend: Equatable {
        case keepBoth
        case replaceSave
        case replaceState
        /// M12: a pak archive is installed by a job, which asks before
        /// replacing a pak of the same name.
        case installAgain

        init(_ target: TransferTarget) {
            switch target {
            case .save: self = .replaceSave
            case .state: self = .replaceState
            case .rom, .staging, .sdFile: self = .keepBoth
            case .pak: self = .installAgain
            }
        }

        var onConflict: OnConflict {
            switch self {
            case .keepBoth: .rename
            case .installAgain: .fail
            case .replaceSave, .replaceState: .overwrite
            }
        }
        var needsConfirmation: Bool { self == .replaceSave || self == .replaceState }

        var explanation: LocalizedStringKey {
            switch self {
            case .installAgain: "Sending it again installs the app again; if it is already installed, you are asked before it is replaced."
            case .keepBoth: "If the file is already on the console, sending it again keeps both copies."
            case .replaceSave: "Sending it again replaces the save on the console, even if it was already received and you played since. The current save is kept as a backup copy first."
            case .replaceState: "Sending it again replaces this state on the console, even if it was already received. The current state goes to the trash first."
            }
        }
    }

    private var isConflict: Bool {
        (item.failure as? APIError)?.code == .conflict
    }

    /// M23: the folder of an sd-file upload became protected (`409
    /// details.protected`): no other name helps.
    private var protection: SdProtection? {
        if case .server(let server)? = item.failure as? APIError { return server.sdProtection }
        return nil
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline) {
                Text(item.fileName).font(.headline)
                Spacer()
                if !item.phase.isFinished && !item.isCompleting && item.phase != .cancelling {
                    Button {
                        queue.cancel(item)
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .foregroundStyle(.secondary)
                    }
                    .buttonStyle(.borderless)
                    .accessibilityLabel(Text("Cancel"))
                    .accessibilityIdentifier("upload.cancel")
                }
            }
            if !item.destination.isEmpty {
                Text(item.destination)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            if item.phase != .done && item.phase != .cancelled {
                ProgressView(value: item.fractionCompleted)
                    .accessibilityHidden(true)
            }
            Text(statusText)
                .font(.footnote)
                .foregroundStyle(item.phase == .failed ? .red : .secondary)
                .accessibilityIdentifier(item.phase == .done ? "upload.done" : "upload.status")
            if let failure = item.failure, item.phase == .failed || item.phase == .paused {
                Text(Self.failureText(failure, target: item.target))
                    .font(.footnote)
                    .fixedSize(horizontal: false, vertical: true)
            }
            actions
        }
        .padding(.vertical, 2)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("upload.\(item.fileName)")
        .swipeActions {
            if !item.phase.isFinished && !item.isCompleting && item.phase != .cancelling {
                Button("Cancel", role: .destructive) { queue.cancel(item) }
            }
        }
    }

    @ViewBuilder
    private var actions: some View {
        switch item.phase {
        case .paused:
            Button("Resume") { queue.resume(item) }
                .buttonStyle(.bordered)
        case .failed where (item.failure as? UploadFailure) == .outcomeUnknown:
            let resend = Resend(item.target)
            Button("Send Again") {
                if resend.needsConfirmation {
                    confirmResend = true
                } else {
                    Task { await queue.retry(item, onConflict: resend.onConflict) }
                }
            }
            .buttonStyle(.bordered)
            .accessibilityIdentifier("upload.sendAgain")
            .confirmationDialog(
                resend == .replaceSave ? "Replace the save on the console?" : "Replace the state on the console?",
                isPresented: $confirmResend, titleVisibility: .visible
            ) {
                Button("Replace", role: .destructive) {
                    Task { await queue.retry(item, onConflict: resend.onConflict) }
                }
                .accessibilityIdentifier("upload.sendAgain.replace")
            } message: {
                Text(resend.explanation)
            }
            Text(resend.explanation)
                .font(.caption)
                .foregroundStyle(.secondary)
        case .failed where protection != nil:
            Text("The console protects this folder: the file cannot be added there. Choose another folder in Files.")
                .font(.caption)
                .foregroundStyle(.secondary)
        case .failed where isConflict:
            if case .sdFile = item.target {
                // M23: a file of the SD browser is never replaced.
                Button("Keep Both") { Task { await queue.retry(item, onConflict: .rename) } }
                    .buttonStyle(.bordered)
                Text("A file of the same name is already in this folder. Keep Both adds a number to the new one.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                HStack {
                    Button("Keep Both") { Task { await queue.retry(item, onConflict: .rename) } }
                    Button("Replace") { Task { await queue.retry(item, onConflict: .overwrite) } }
                }
                .buttonStyle(.bordered)
                Text("Replace moves the file on the console to its trash.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        case .failed:
            Button("Try Again") { queue.resume(item) }
                .buttonStyle(.bordered)
        default:
            EmptyView()
        }
    }

    private var statusText: String {
        switch item.phase {
        case .hashing:
            let percent = item.fractionCompleted.formatted(.percent.precision(.fractionLength(0)))
            return String(localized: "Preparing… \(percent)")
        case .waiting:
            return String(localized: "Waiting")
        case .uploading:
            if item.sentBytes >= item.sizeBytes {
                return String(localized: "Finishing on the console…")
            }
            var parts = [String(localized: "\(item.displayedBytes.byteString) of \(item.sizeBytes.byteString)")]
            if let speed = item.bytesPerSecond {
                parts.append(String(localized: "\(Int64(speed).byteString)/s"))
            }
            if let seconds = item.secondsRemaining {
                parts.append(String(localized: "\(Self.duration(seconds)) left"))
            }
            return parts.joined(separator: " · ")
        case .paused:
            return String(localized: "Paused at \(item.sentBytes.byteString) of \(item.sizeBytes.byteString)")
        case .failed:
            return String(localized: "Not sent")
        case .done:
            if case .save = item.target, item.result?.backup != nil {
                return String(localized: "Sent. The previous save was kept as a backup copy.")
            }
            return String(localized: "Sent")
        case .cancelling:
            return String(localized: "Cancelling… Waiting for the console to confirm.")
        case .cancelled:
            return String(localized: "Cancelled")
        }
    }

    /// A save or state upload refused for its NextUI format gets the text
    /// of the save and state screens.
    static func failureText(_ failure: any Error, target: TransferTarget) -> String {
        if let error = failure as? APIError, error.isDisconnected {
            return String(localized: "Paused: disconnected by the console. The transfer resumes after reconnecting.")
        }
        return switch target {
        case .save, .state: AppError.gameData(failure).message
        case .pak: AppError.pak(failure).message
        default: AppError(failure).message
        }
    }

    static func duration(_ seconds: TimeInterval) -> String {
        Duration.seconds(max(1, seconds.rounded()))
            .formatted(.units(allowed: [.hours, .minutes, .seconds], width: .abbreviated, maximumUnitCount: 2))
    }
}

struct JobRow: View {
    let item: JobItem
    let queue: TransferQueue
    /// M12: the pak installation this job belongs to, if this app started
    /// it, and the manager that can replace the installed pak.
    var pakInstall: PakInstall?
    var paks: PakManager?
    @State private var error: AppError?

    /// A pak installation (its own job, or a download of a pak).
    private var isPak: Bool { item.kind == .pakInstall || pakInstall != nil || item.job?.resultPakId != nil }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline) {
                Text(item.title).font(.headline)
                Spacer()
                if !item.isFinished {
                    Button {
                        Task { await cancel() }
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .foregroundStyle(.secondary)
                    }
                    .buttonStyle(.borderless)
                    .accessibilityLabel(Text("Cancel"))
                }
            }
            if let progress = item.job?.progress, !item.isFinished {
                if let fraction = progress.fraction {
                    ProgressView(value: fraction).accessibilityHidden(true)
                } else {
                    ProgressView().accessibilityHidden(true)
                }
            }
            Text(statusText)
                .font(.footnote)
                .foregroundStyle(item.job?.state == .failed ? .red : .secondary)
                .accessibilityIdentifier("job.status")
            if let message = failureMessage {
                Text(message)
                    .font(.footnote)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let pakInstall, let paks, case .needsReplacement = pakInstall.phase {
                Button("Replace…") { paks.pendingReplacement = pakInstall }
                    .buttonStyle(.bordered)
                    .accessibilityIdentifier("job.replacePak")
            }
        }
        .padding(.vertical, 2)
        // Children kept apart: Cancel must not become the default action.
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("job.\(item.title)")
    }

    private var failureMessage: String? {
        if let error { return error.message }
        if let server = item.job?.serverError {
            if isPak {
                if let name = server.conflictingPakName, pakInstall?.replacing != true {
                    return String(localized: "\(name) is already installed on the console.")
                }
                return AppError.pak(APIError.server(server)).message
            }
            return item.kind == .patch ? AppError.patch(APIError.server(server)).message : AppError.api(.server(server)).message
        }
        if item.isLost {
            return item.kind == .patch
                ? String(localized: "The console was restarted: this patch was stopped.")
                : String(localized: "The console was restarted: this installation was stopped.")
        }
        return nil
    }

    private var statusText: String {
        guard let job = item.job else { return String(localized: "Waiting for the console…") }
        switch job.state {
        case .queued:
            return String(localized: "Waiting")
        case .running where item.kind == .patch:
            if let fraction = job.progress.fraction {
                return String(localized: "Applying the patch… \(fraction.formatted(.percent.precision(.fractionLength(0))))")
            }
            return String(localized: "Applying the patch…")
        case .running:
            if let total = job.progress.total {
                return String(localized: "\(job.progress.done.byteString) of \(total.byteString)")
            }
            return job.progress.done.byteString
        case .succeeded where item.kind == .patch:
            return String(localized: "Patch applied")
        case .running where item.kind == .pakInstall:
            return String(localized: "Installing on the console…")
        case .succeeded where isPak:
            return String(localized: "Installed")
        case .succeeded:
            return String(localized: "Done")
        case .failed:
            return String(localized: "Failed")
        case .cancelled:
            return String(localized: "Cancelled")
        default:
            return job.state.rawValue
        }
    }

    private func cancel() async {
        do {
            try await queue.cancel(item)
            error = nil
        } catch {
            let error = AppError(error)
            if !error.isCancellation { self.error = error }
        }
    }
}
