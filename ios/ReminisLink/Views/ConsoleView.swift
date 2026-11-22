import ReminisLinkKit
import SwiftUI

/// Dashboard of a console (docs/specs/06-app-ios.md §4.3), its Console tab
/// (M11: the library, transfers and trash have their own tabs, and nothing
/// is added from here).
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
                // M12: the apps of the console, and its SD card (read only).
                Section {
                    NavigationLink(value: Route.paks(deviceId)) {
                        Label("Apps", systemImage: "square.stack.3d.down.right")
                    }
                    .accessibilityIdentifier("console.paks")
                    // M22: the screenshots taken on the console.
                    NavigationLink(value: Route.screenshots(deviceId)) {
                        Label("Screenshots", systemImage: "camera.viewfinder")
                    }
                    .accessibilityIdentifier("console.screenshots")
                } footer: {
                    Text("The tools and emulators installed on the console, and the screenshots taken on it.")
                }
                Section {
                    ForEach(status.storage) { storage in
                        NavigationLink(value: Route.sdFolder(SdFolderRoute(deviceId: deviceId, folderId: nil, title: storage.label))) {
                            StorageBar(storage: storage, trashBytes: session.trash?.totalBytes)
                        }
                        .accessibilityHint(Text("Browse the SD card"))
                        .accessibilityIdentifier("console.storage")
                    }
                } header: {
                    Text("Storage")
                } footer: {
                    Text("Tap to browse the SD card (read only).")
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
                            Text("Expires no earlier than \(entry.expiresAt.formatted(date: .abbreviated, time: .omitted))")
                                .font(.footnote)
                                .foregroundStyle(.secondary)
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
