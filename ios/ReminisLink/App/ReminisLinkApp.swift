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
                    switch route {
                    case .console(let id): ConsoleView(deviceId: id)
                    case .library(let id): LibraryView(deviceId: id)
                    case .folder(let folder): FolderView(route: folder)
                    case .game(let game): GameDetailView(route: game)
                    case .trash(let id): TrashView(deviceId: id)
                    }
                }
        }
        .sheet(item: $app.sheet) { sheet in
            switch sheet {
            case .addConsole: AddConsoleView()
            case .pairing(let flow): PairingProgressView(flow: flow)
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
            if phase == .active { app.discovery.start() } else if phase == .background { app.discovery.stop() }
        }
    }
}
