import ReminisLinkKit
import SwiftUI

// Save sync to the iPhone (milestone M9): the dashboard section, the
// synced saves of a console and the versions of one save, with restore
// and share.

/// Where the synced saves are, said in Files' own words.
struct VaultLocationNotice: View {
    let location: SaveVaultLocation

    var body: some View {
        switch location {
        case .iCloud:
            Label("Saves are copied to your iCloud Drive, in the ReminisLink folder of the Files app.", systemImage: "icloud")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .accessibilityIdentifier("sync.location.icloud")
        case .local:
            Label {
                Text("iCloud Drive is not available: saves are kept on this iPhone only (Files > On My iPhone > ReminisLink). Turn on iCloud Drive for ReminisLink in Settings to keep them in iCloud.")
            } icon: {
                Image(systemName: "exclamationmark.icloud")
                    .foregroundStyle(.orange)
            }
            .font(.footnote)
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("sync.location.local")
        }
    }
}

/// The "Saves on This iPhone" section of the dashboard.
struct SaveSyncSection: View {
    @Environment(AppModel.self) private var app
    let deviceId: String
    let isConnected: Bool

    var body: some View {
        let sync = app.saveSync(for: deviceId)
        Section {
            if let progress = sync.progress {
                VStack(alignment: .leading, spacing: 6) {
                    HStack {
                        Text("Syncing saves…")
                        Spacer()
                        Button("Cancel") { sync.cancel() }
                            .buttonStyle(.borderless)
                            .accessibilityIdentifier("sync.cancel")
                    }
                    ProgressView(value: progress.fraction)
                    if progress.total > 0 {
                        Text("Saves checked: \(progress.checked) of \(progress.total)")
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                }
                .accessibilityElement(children: .contain)
                .accessibilityIdentifier("sync.progress")
            } else {
                Button {
                    app.startSaveSync(deviceId: deviceId)
                } label: {
                    Label("Sync Saves", systemImage: "arrow.triangle.2.circlepath")
                }
                .disabled(!isConnected)
                .accessibilityIdentifier("console.sync")
            }
            if case .failed(let error) = sync.phase {
                ErrorView(error: error, retry: { app.startSaveSync(deviceId: deviceId) }, deviceId: deviceId)
            }
            if let report = sync.lastReport, !sync.isRunning {
                SyncReportView(report: report)
            }
            NavigationLink(value: Route.syncedSaves(deviceId)) {
                Label("Saves on This iPhone", systemImage: "externaldrive")
            }
            .accessibilityIdentifier("console.syncedSaves")
        } header: {
            Text("Save Sync")
        } footer: {
            if let location = app.vaultLocation {
                VaultLocationNotice(location: location)
            }
        }
    }
}

/// The outcome of the last sync.
struct SyncReportView: View {
    let report: SaveSyncReport
    @State private var showFailures = false

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            if report.stored.isEmpty {
                Text("Up to date · saves checked: \(report.total)")
            } else {
                Text("New versions: \(report.stored.count) · saves checked: \(report.total)")
            }
            Text("Last sync \(report.finishedAt.formatted(.relative(presentation: .named)))")
                .font(.footnote)
                .foregroundStyle(.secondary)
            if report.truncated {
                Text("The console listed only its first 10,000 saves.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("sync.report")
        if !report.failures.isEmpty {
            DisclosureGroup(isExpanded: $showFailures) {
                ForEach(report.failures) { failure in
                    VStack(alignment: .leading, spacing: 2) {
                        Text(failure.gameName).font(.subheadline)
                        Text(AppError.sync(failure.error.error).message)
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                    .accessibilityElement(children: .combine)
                }
            } label: {
                Label("\(report.failures.count) saves could not be copied", systemImage: "exclamationmark.triangle")
                    .foregroundStyle(.orange)
            }
            .accessibilityIdentifier("sync.failures")
        }
    }
}

/// The consoles with synced saves (from the app settings).
struct SyncedConsolesView: View {
    @Environment(AppModel.self) private var app
    @State private var consoles: [VaultConsole]?
    @State private var error: AppError?

    var body: some View {
        List {
            if let error {
                Section { ErrorView(error: error, retry: load) }
            }
            Section {
                ForEach(consoles ?? []) { console in
                    NavigationLink(value: Route.syncedSaves(console.id)) {
                        Label(app.store.device(id: console.id)?.name ?? console.info.name, systemImage: "gamecontroller")
                    }
                }
            } footer: {
                if let location = app.vaultLocation { VaultLocationNotice(location: location) }
            }
        }
        .overlay {
            if consoles?.isEmpty == true {
                ContentUnavailableView("No synced saves", systemImage: "externaldrive",
                                       description: Text("Sync the saves of a console from its dashboard."))
            } else if consoles == nil && error == nil {
                ProgressView()
            }
        }
        .navigationTitle("Saves on This iPhone")
        .task { await load() }
        .refreshable { await load() }
    }

    private func load() async {
        do {
            consoles = try await app.currentVault().consoles()
            error = nil
        } catch {
            self.error = AppError.sync(error)
        }
    }
}

/// The synced saves of a console, by system.
struct SyncedSavesView: View {
    @Environment(AppModel.self) private var app
    let deviceId: String
    @State private var games: [VaultGame]?
    @State private var error: AppError?

    var body: some View {
        let sync = app.saveSync(for: deviceId)
        let systems = Dictionary(grouping: games ?? [], by: \.info.systemName)
        List {
            if let error {
                Section { ErrorView(error: error, retry: load) }
            }
            ForEach(systems.keys.sorted { $0.localizedStandardCompare($1) == .orderedAscending }, id: \.self) { system in
                Section(system) {
                    ForEach(systems[system] ?? []) { game in
                        NavigationLink(value: Route.syncedGame(SyncedGameRoute(
                            deviceId: deviceId, gameId: game.id, name: game.info.name, systemName: system
                        ))) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(game.info.name)
                                if let latest = game.latest {
                                    Text("Versions: \(game.versionCount) · latest \(latest.info.syncedAt.formatted(date: .abbreviated, time: .shortened))")
                                        .font(.footnote)
                                        .foregroundStyle(.secondary)
                                }
                            }
                            .accessibilityElement(children: .combine)
                        }
                        .accessibilityIdentifier("synced.game.\(game.info.name)")
                    }
                }
            }
            if let location = app.vaultLocation {
                Section { VaultLocationNotice(location: location) }
            }
        }
        .overlay {
            if games?.isEmpty == true {
                ContentUnavailableView("No synced saves", systemImage: "externaldrive",
                                       description: Text("Sync the saves of this console from its dashboard."))
            } else if games == nil && error == nil {
                ProgressView()
            }
        }
        .navigationTitle(app.store.device(id: deviceId)?.name ?? String(localized: "Saves on This iPhone"))
        .task { await load() }
        .refreshable { await load() }
        // A sync that ends while the screen is open shows its versions.
        .onChange(of: sync.isRunning) { _, running in
            if !running { Task { await load() } }
        }
    }

    private func load() async {
        do {
            games = try await app.currentVault().games(deviceId: deviceId)
            error = nil
        } catch {
            self.error = AppError.sync(error)
        }
    }
}

/// The versions of one synced save: restore one to the console, or share it.
struct SyncedGameView: View {
    @Environment(AppModel.self) private var app
    let route: SyncedGameRoute
    @State private var versions: [SaveVersion]?
    @State private var error: AppError?
    @State private var notice: String?
    @State private var selected: SaveVersion?
    @State private var toRestore: SaveVersion?
    @State private var exported: ExportedFile?
    @State private var isWorking = false

    var body: some View {
        List {
            if let error {
                Section { ErrorView(error: error, retry: load, deviceId: route.deviceId) }
            }
            if let notice {
                Section { NoticeView(text: notice) }
            }
            Section {
                ForEach(Array((versions ?? []).enumerated()), id: \.element.id) { index, version in
                    Button {
                        selected = version
                    } label: {
                        VStack(alignment: .leading, spacing: 2) {
                            HStack {
                                // Seconds: two syncs a minute apart stay distinct.
                                Text(version.info.syncedAt.formatted(date: .abbreviated, time: .standard))
                                if index == 0 {
                                    Text("Latest")
                                        .font(.caption)
                                        .padding(.horizontal, 6)
                                        .background(Capsule().fill(.tint.opacity(0.15)))
                                }
                            }
                            Text(verbatim: "\(version.info.save.name) · \(version.info.save.sizeBytes.byteString)")
                                .font(.footnote)
                                .foregroundStyle(.secondary)
                            if version.info.rtc != nil {
                                Text("With its clock file")
                                    .font(.footnote)
                                    .foregroundStyle(.secondary)
                            }
                        }
                        .accessibilityElement(children: .combine)
                    }
                    .tint(.primary)
                    .accessibilityIdentifier("synced.version.\(index)")
                }
            } header: {
                Text(route.systemName)
            } footer: {
                Text("Up to 10 versions are kept; the oldest is removed when a new one is synced.")
            }
        }
        .overlay {
            if versions == nil && error == nil { ProgressView() }
        }
        .disabled(isWorking)
        .overlay(alignment: .bottom) {
            if isWorking {
                ProgressView().padding().background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12)).padding()
            }
        }
        .navigationTitle(route.name)
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
        .refreshable { await load() }
        .exportSheet($exported)
        .confirmationDialog(
            selected?.info.syncedAt.formatted(date: .abbreviated, time: .standard) ?? "",
            isPresented: Binding(get: { selected != nil }, set: { if !$0 { selected = nil } }),
            titleVisibility: .visible, presenting: selected
        ) { version in
            Button("Restore to the Console…") { toRestore = version }
                .accessibilityIdentifier("synced.restore")
            Button("Share") { Task { await share(version) } }
                .accessibilityIdentifier("synced.share")
        }
        .alert(
            "Restore this version?",
            isPresented: Binding(get: { toRestore != nil }, set: { if !$0 { toRestore = nil } }),
            presenting: toRestore
        ) { version in
            Button("Restore") { Task { await restore(version) } }
                .accessibilityIdentifier("synced.restore.confirm")
            Button("Cancel", role: .cancel) {}
        } message: { version in
            Text("The save of \(route.name) on the console will be replaced by the version of \(version.info.syncedAt.formatted(date: .abbreviated, time: .standard)). The console first keeps a copy of its current save (Before replace). The clock file on the console is kept as it is.")
        }
    }

    private func load() async {
        do {
            versions = try await app.currentVault().versions(deviceId: route.deviceId, gameId: route.gameId)
            error = nil
        } catch {
            self.error = AppError.sync(error)
        }
    }

    private func export(_ version: SaveVersion) async throws -> ExportedVersion {
        try await app.currentVault().export(
            deviceId: route.deviceId, gameId: route.gameId, versionId: version.id, to: app.exportDirectory
        )
    }

    private func share(_ version: SaveVersion) async {
        isWorking = true
        defer { isWorking = false }
        do {
            let files = try await export(version)
            exported = ExportedFile(file: DownloadedFile(url: files.save), others: files.rtc.map { [$0] } ?? [])
            error = nil
        } catch {
            self.error = AppError.sync(error)
        }
    }

    /// `PUT /games/{gameId}/save` (or the transfer queue above 64 MiB):
    /// the pak copies the current save first (`before-replace`).
    private func restore(_ version: SaveVersion) async {
        isWorking = true
        defer { isWorking = false }
        notice = nil
        let files: ExportedVersion
        do {
            files = try await export(version)
        } catch {
            self.error = AppError.sync(error)
            return
        }
        // Only the save goes back; the clock file of the console stays.
        if let rtc = files.rtc { try? FileManager.default.removeItem(at: rtc) }
        let picked = PickedFile(url: files.save, name: version.info.save.name, sizeBytes: version.info.save.sizeBytes)
        do {
            switch try await app.session(for: route.deviceId).replaceSave(gameId: route.gameId, gameName: route.name, with: picked) {
            case .replaced:
                notice = String(localized: "Version restored. The previous save is in the backup copies of the game on the console.")
            case .queued:
                notice = String(localized: "The version is being sent to the console. Follow it in Transfers.")
            }
            error = nil
        } catch {
            let error = AppError.gameData(error)
            if !error.isCancellation { self.error = error }
        }
    }
}

/// The app settings (docs/specs/06-app-ios.md §4.9).
struct AppSettingsView: View {
    @Environment(AppModel.self) private var app

    var body: some View {
        @Bindable var app = app
        List {
            Section {
                Toggle("Sync Saves When Connecting", isOn: $app.syncOnConnect)
                    .accessibilityIdentifier("settings.syncOnConnect")
                NavigationLink(value: Route.syncedConsoles) {
                    Text("Saves on This iPhone")
                }
                .accessibilityIdentifier("settings.syncedSaves")
            } header: {
                Text("Save Sync")
            } footer: {
                VStack(alignment: .leading, spacing: 8) {
                    Text("The saves of a console (with their clock files) are copied to your own iCloud Drive, never to the publisher of the app. Up to 10 versions are kept per save.")
                    if let location = app.vaultLocation { VaultLocationNotice(location: location) }
                }
            }
            Section("About") {
                LabeledContent("Version", value: AppModel.appVersion)
            }
        }
        .navigationTitle("Settings")
        // Shows where the saves go before the first sync.
        .task { _ = await app.currentVault() }
    }
}
