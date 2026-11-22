import ReminisLinkKit
import SwiftUI

/// Dashboard of a console (docs/specs/06-app-ios.md §4.3), its Console tab
/// (M11: the library has its own tab, and nothing is added from here; M23:
/// the transfers and the trash open from here, the apps and the files of
/// the SD card have their own tabs).
struct ConsoleView: View {
    @Environment(AppModel.self) private var app
    let deviceId: String
    @State private var confirmUnpair = false
    @State private var actionError: AppError?
    @State private var isVisible = false

    var body: some View {
        let session = app.session(for: deviceId)
        let device = app.store.device(id: deviceId)
        List {
            switch session.state {
            case .idle, .connecting:
                Section {
                    HStack(spacing: 12) {
                        ProgressView()
                        Text("Connecting to the console…").foregroundStyle(.secondary)
                    }
                }
            case .failed(let error):
                Section { ErrorView(error: error, retry: { await session.connect() }, deviceId: deviceId) }
            case .connected:
                EmptyView()
            }

            SaveSyncSection(deviceId: deviceId, isConnected: session.state == .connected)

            // M23: the transfers and the trash (no longer tabs, shown
            // even while offline: paused uploads), and the screenshots (M22).
            Section {
                NavigationLink(value: Route.transfers(deviceId)) {
                    LabeledContent {
                        if activeTransfers(session) > 0 {
                            CountBadge(count: activeTransfers(session))
                        }
                    } label: {
                        Label("Transfers", systemImage: "arrow.up.arrow.down")
                    }
                }
                .accessibilityIdentifier("console.transfers")
                NavigationLink(value: Route.trash(deviceId)) {
                    LabeledContent {
                        if let count = session.trash?.entries.count, count > 0 {
                            Text(count, format: .number).foregroundStyle(.secondary)
                        }
                    } label: {
                        Label("Trash", systemImage: "trash")
                    }
                }
                .accessibilityIdentifier("console.trash")
                NavigationLink(value: Route.screenshots(deviceId)) {
                    Label("Screenshots", systemImage: "camera.viewfinder")
                }
                .accessibilityIdentifier("console.screenshots")
            }

            if let status = session.status {
                Section("Console") {
                    LabeledContent("Name", value: status.deviceName)
                    LabeledContent("Model", value: status.model.localizedName)
                    // The release string of the OS as the console shows it
                    // (e.g. NextUI-20261013-039a06de), else the OS id.
                    LabeledContent("System", value: status.osVersion ?? status.os)
                    LabeledContent("ReminisLink version", value: status.pakVersion)
                    LabeledContent("IP address", value: status.network.ipv4)
                    if let ssid = status.network.ssid {
                        LabeledContent("Wi-Fi", value: ssid)
                    }
                    if let battery = status.battery {
                        LabeledContent("Battery") {
                            Text(battery.charging ? "\(battery.percent)%, charging" : "\(battery.percent)%")
                        }
                    }
                }
                Section {
                    ForEach(status.storage) { storage in
                        Button {
                            app.consoleTab = .files
                        } label: {
                            StorageBar(storage: storage, trashBytes: session.trash?.totalBytes)
                                .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .accessibilityHint(Text("Opens the Files tab"))
                        .accessibilityIdentifier("console.storage")
                    }
                } header: {
                    Text("Storage")
                } footer: {
                    Text("Tap to browse the SD card in the Files tab.")
                }
                ConnectedClientsSection(clients: status.connectedClients)
            }

            Section {
                Button("Unpair", role: .destructive) { confirmUnpair = true }
                    .accessibilityIdentifier("console.unpair")
            }

            if let actionError {
                Section { ErrorView(error: actionError) }
            }
        }
        .navigationTitle(device?.name ?? "")
        .task { await session.connectIfNeeded() }
        .onAppear { isVisible = true }
        .onDisappear { isVisible = false }
        // A connection cancelled by another screen leaves .idle: never stay
        // on "Connecting…" without trying again.
        .onChange(of: session.state) { _, state in
            if state == .idle && isVisible {
                Task { await session.connectIfNeeded() }
            }
        }
        .refreshable { await session.refresh() }
        // The count of the Trash row.
        .task(id: session.state == .connected) {
            if session.state == .connected { await session.loadTrash() }
        }
        .confirmationDialog("Unpair this console?", isPresented: $confirmUnpair, titleVisibility: .visible) {
            Button("Unpair", role: .destructive) {
                Task {
                    do {
                        try await session.unpair()
                    } catch {
                        let error = AppError(error)
                        if !error.isCancellation { actionError = error }
                    }
                }
            }
        } message: {
            Text("The console will forget this iPhone. To reconnect, pair it again: on the console, open Settings > Pair a New Device and scan the QR code.")
        }
    }
}

extension ConsoleView {
    /// The uploads and jobs still running (the badge of the Transfers row).
    func activeTransfers(_ session: ConsoleSession) -> Int {
        session.transfers.uploads.filter { !$0.phase.isFinished }.count
            + session.transfers.jobs.filter { !$0.isFinished }.count
    }
}

/// A count in a capsule, as a tab badge (the Transfers row, M23).
struct CountBadge: View {
    let count: Int

    var body: some View {
        Text(count, format: .number)
            .font(.caption.bold().monospacedDigit())
            .foregroundStyle(.white)
            .padding(.horizontal, 7)
            .padding(.vertical, 2)
            .background(Capsule().fill(Color.red))
            .accessibilityLabel(Text("\(count) in progress"))
    }
}

/// The trash of the console: entries, restore (M18: tap an entry, or swipe
/// it), permanent deletion with confirmation.
struct TrashView: View {
    @Environment(AppModel.self) private var app
    let deviceId: String
    @State private var confirmEmpty = false
    @State private var entryToDelete: TrashEntry?
    @State private var entryToRestore: TrashEntry?
    @State private var error: AppError?

    var body: some View {
        let session = app.session(for: deviceId)
        let entries = session.trash?.entries ?? []
        List {
            if let error {
                Section { ErrorView(error: error, retry: { await session.loadTrash() }) }
            }
            Section {
                ForEach(entries) { entry in
                    Button {
                        entryToRestore = entry
                    } label: {
                        VStack(alignment: .leading, spacing: 4) {
                            Text(entry.displayLabel).font(.headline)
                            Text(entry.sizeBytes.byteString)
                            if entry.held == true {
                                // M22: never purged automatically.
                                Text("Kept until you delete it: it holds an item moved by mistake.")
                                    .font(.footnote)
                                    .foregroundStyle(.secondary)
                            } else {
                                Text("Expires no earlier than \(entry.expiresAt.formatted(date: .abbreviated, time: .omitted))")
                                    .font(.footnote)
                                    .foregroundStyle(.secondary)
                            }
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityElement(children: .combine)
                    .accessibilityHint(Text("Shows what comes back, then restores it."))
                    .accessibilityIdentifier("trash.entry")
                    .swipeActions(edge: .leading) {
                        Button("Restore") { entryToRestore = entry }
                            .tint(.accentColor)
                    }
                    .swipeActions {
                        Button("Delete", role: .destructive) { entryToDelete = entry }
                    }
                    .contextMenu {
                        Button("Restore…", systemImage: "arrow.uturn.backward") { entryToRestore = entry }
                        Button("Delete…", systemImage: "trash", role: .destructive) { entryToDelete = entry }
                    }
                }
            } footer: {
                Text("Files stay in the trash of the console until they are removed automatically after 30 days, or until the trash is emptied. Tap an entry to restore it: its files go back to their original place, never over a file already on the SD card.")
            }
        }
        .overlay {
            if entries.isEmpty {
                ContentUnavailableView("The trash is empty", systemImage: "trash")
            }
        }
        .navigationTitle("Trash")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button("Empty Trash", role: .destructive) { confirmEmpty = true }
                    .disabled(entries.isEmpty)
            }
        }
        .task { await session.loadTrash() }
        .refreshable { await session.loadTrash() }
        .sheet(item: $entryToRestore) { entry in
            TrashRestoreView(deviceId: deviceId, entry: entry)
        }
        .confirmationDialog("Empty the trash?", isPresented: $confirmEmpty, titleVisibility: .visible) {
            Button("Empty Trash", role: .destructive) {
                Task { await run { try await session.emptyTrash() } }
            }
        } message: {
            Text("\((session.trash?.totalBytes ?? 0).byteString) will be permanently deleted from the SD card. This cannot be undone.")
        }
        .confirmationDialog(
            "Delete permanently?",
            isPresented: Binding(get: { entryToDelete != nil }, set: { if !$0 { entryToDelete = nil } }),
            titleVisibility: .visible,
            presenting: entryToDelete
        ) { entry in
            Button("Delete", role: .destructive) {
                Task { await run { try await session.deleteTrashEntry(entry.id) } }
            }
        } message: { entry in
            Text("\(entry.displayLabel) will be permanently deleted from the SD card. This cannot be undone.")
        }
    }

    private func run(_ operation: () async throws -> Void) async {
        do {
            try await operation()
            error = nil
        } catch {
            let error = AppError(error)
            if !error.isCancellation { self.error = error }
        }
    }
}

extension TrashEntry {
    /// The label shown: the pak's, except for several screenshots (M22),
    /// counted in the user's language.
    var displayLabel: String {
        if let count = screenshotCount, count > 1 {
            return String(localized: "\(count) screenshots")
        }
        return label
    }
}

/// `connectedClients` of `GET /device` (docs/specs/06-app-ios.md §4.3):
/// the paired devices seen by the console in the last 30 seconds.
struct ConnectedClientsSection: View {
    let clients: [ConnectedClient]

    var body: some View {
        Section {
            if clients.isEmpty {
                Text("No device connected")
                    .foregroundStyle(.secondary)
            }
            ForEach(clients, id: \.self) { client in
                let seen = client.lastSeenAt.formatted(.relative(presentation: .named))
                HStack {
                    Image(systemName: "iphone")
                        .foregroundStyle(.secondary)
                        .accessibilityHidden(true)
                    Text(client.clientName)
                    Spacer()
                    Text(seen)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(Text(client.clientName))
                .accessibilityValue(Text("Last seen \(seen)"))
            }
        } header: {
            Text("Connected Devices")
        } footer: {
            Text("Paired devices that talked to the console in the last 30 seconds.")
        }
        .accessibilityIdentifier("console.connectedClients")
    }
}
