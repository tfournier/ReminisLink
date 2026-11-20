import SwiftUI

@main
struct ReminisLinkApp: App {
    @State private var root = AppRoot(live: AppModel.make())

    var body: some Scene {
        WindowGroup {
            let app = root.current
            // The demo mode (M16) gets its own tree: nothing of the real
            // app's screens is kept, and its Library sort goes to its own
            // defaults.
            RootView()
                .environment(app)
                .environment(root)
                .defaultAppStorage(root.demo?.defaults ?? .standard)
                .id(ObjectIdentifier(app))
        }
    }
}

struct RootView: View {
    @Environment(AppModel.self) private var app
    @Environment(AppRoot.self) private var root
    @Environment(\.scenePhase) private var scenePhase

    var body: some View {
        @Bindable var app = app
        VStack(spacing: 0) {
            // M16: always visible during the demo, above every tab.
            if app.isDemo { DemoBanner() }
            Group {
                // Milestone M11: an open console shows its tabs; the list
                // of consoles is the root otherwise.
                if let deviceId = app.openConsoleId {
                    ConsoleTabsView(deviceId: deviceId)
                        .id(deviceId)
                        .transition(.move(edge: .trailing))
                } else {
                    NavigationStack(path: $app.listPath) {
                        ConsolesView()
                            .navigationDestination(for: Route.self) { RouteDestination(route: $0) }
                    }
                    .transition(.move(edge: .leading))
                }
            }
            .animation(.default, value: app.openConsoleId)
        }
        .alert(
            "The demo could not open",
            isPresented: Binding(get: { root.demoError != nil }, set: { if !$0 { root.demoError = nil } }),
            presenting: root.demoError
        ) { _ in
            Button("OK", role: .cancel) {}
        } message: { error in
            Text(error.message)
        }
        .sheet(item: $app.sheet, onDismiss: { app.sheetDismissed() }) { sheet in
            switch sheet {
            case .addConsole(let repairing): AddConsoleView(repairing: repairing)
            case .pairing(let flow): PairingProgressView(flow: flow)
            case .add(let context): AddView(context: context)
            case .patch(let context): PatchView(context: context)
            }
        }
        .alert(
            "Invalid link",
            isPresented: Binding(get: { app.linkError != nil }, set: { if !$0 { app.linkError = nil } }),
            presenting: app.linkError
        ) { _ in
            Button("OK", role: .cancel) {}
        } message: { error in
            Text(error.message)
        }
        .alert(
            "This console is already paired. Pair it again?",
            isPresented: Binding(get: { app.pendingRepair != nil }, set: { if !$0 { app.pendingRepair = nil } })
        ) {
            Button("Pair Again") { app.confirmRepair() }
                .accessibilityIdentifier("repair.confirm")
            Button("Cancel", role: .cancel) { app.pendingRepair = nil }
        } message: {
            Text("Its pairing will be replaced on all your Apple devices. The console must present the same identity as its QR code.")
        }
        .onOpenURL { url in
            // A link opened during the demo is for the real app (M16).
            if app.isDemo { root.handle(url: url) } else { app.handle(url: url) }
        }
        .onChange(of: scenePhase, initial: true) { _, phase in
            if phase == .active {
                app.discovery.start()
                app.becomeActive()
            } else if phase == .background {
                app.discovery.stop()
                app.enterBackground()
            }
        }
    }
}

/// The screen of a navigation destination.
struct RouteDestination: View {
    let route: Route

    @ViewBuilder
    var body: some View {
        switch route {
        case .console(let id): ConsoleView(deviceId: id)
        case .library(let id): LibraryView(deviceId: id)
        case .systems(let id): SystemsView(deviceId: id)
        case .folder(let folder): FolderView(route: folder)
        case .game(let game): GameDetailView(route: game)
        case .save(let game): SaveView(route: game)
        case .states(let game): StatesView(route: game)
        case .cheats(let game): CheatsView(route: game)
        case .trash(let id): TrashView(deviceId: id)
        case .transfers(let id): TransfersView(deviceId: id)
        case .settings: AppSettingsView()
        case .syncedConsoles: SyncedConsolesView()
        case .syncedSaves(let id): SyncedSavesView(deviceId: id)
        case .syncedGame(let game): SyncedGameView(route: game)
        case .paks(let id): PaksView(deviceId: id)
        case .sdFolder(let folder): SdFolderView(route: folder)
        }
    }
}

/// The tabs of an open console (milestone M11): Console (dashboard),
/// Library, Transfers and Trash, each with its own navigation. A button of
/// each tab's first screen goes back to the list of consoles.
struct ConsoleTabsView: View {
    @Environment(AppModel.self) private var app
    let deviceId: String
    @State private var isVisible = false

    var body: some View {
        @Bindable var app = app
        let session = app.session(for: deviceId)
        TabView(selection: $app.consoleTab) {
            tab(.console) { ConsoleView(deviceId: deviceId) }
                .tabItem { Label("Console", systemImage: "gamecontroller") }
                .accessibilityIdentifier("tab.console")
            tab(.library) { LibraryView(deviceId: deviceId) }
                .tabItem { Label("Library", systemImage: "square.grid.2x2") }
                .accessibilityIdentifier("tab.library")
            tab(.transfers) { TransfersView(deviceId: deviceId) }
                .tabItem { Label("Transfers", systemImage: "arrow.up.arrow.down") }
                .badge(activeTransfers(session))
                .accessibilityIdentifier("tab.transfers")
            tab(.trash) { TrashView(deviceId: deviceId) }
                .tabItem { Label("Trash", systemImage: "trash") }
                .accessibilityIdentifier("tab.trash")
        }
        // Every tab needs the connection (the Trash tab only reads what it
        // loaded).
        .task { await session.connectIfNeeded() }
        .onAppear { isVisible = true }
        .onDisappear { isVisible = false }
        // A connection cancelled by a screen leaves .idle: never stay
        // without trying again while the console is open.
        .onChange(of: session.state) { _, state in
            if state == .idle && isVisible {
                Task { await session.connectIfNeeded() }
            }
        }
    }

    private func activeTransfers(_ session: ConsoleSession) -> Int {
        session.transfers.uploads.filter { !$0.phase.isFinished }.count
            + session.transfers.jobs.filter { !$0.isFinished }.count
    }

    private func tab<Content: View>(_ tab: ConsoleTab, @ViewBuilder root: () -> Content) -> some View {
        NavigationStack(path: Binding(
            get: { app.tabPaths[tab] ?? [] },
            set: { app.tabPaths[tab] = $0 }
        )) {
            root()
                .toolbar {
                    ToolbarItem(placement: .topBarLeading) {
                        Button {
                            app.closeConsole()
                        } label: {
                            HStack(spacing: 4) {
                                Image(systemName: "chevron.backward").fontWeight(.semibold)
                                Text("Consoles")
                            }
                        }
                        .accessibilityLabel(Text("Consoles"))
                        .accessibilityIdentifier("tab.consoles")
                    }
                }
                .navigationDestination(for: Route.self) { RouteDestination(route: $0) }
        }
        .tag(tab)
    }
}
