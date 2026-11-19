import SwiftUI

@main
struct ReminisLinkApp: App {
    @State private var app = AppModel.make()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(app)
        }
    }
}

struct RootView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.scenePhase) private var scenePhase

    var body: some View {
        @Bindable var app = app
        NavigationStack(path: $app.path) {
            ConsolesView()
                .navigationDestination(for: Route.self) { route in
                    destination(route)
                        // M10: another device has write access.
                        .readOnlyBanner(deviceId: route.deviceId)
                }
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
            Text("Its key on this iPhone will be replaced. The console must be found on the network under the same identity.")
        }
        .onOpenURL { app.handle(url: $0) }
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

    @ViewBuilder
    private func destination(_ route: Route) -> some View {
        switch route {
        case .console(let id): ConsoleView(deviceId: id)
        case .library(let id): LibraryView(deviceId: id)
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
        }
    }
}
