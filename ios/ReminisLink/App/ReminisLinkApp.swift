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
    @Environment(\.horizontalSizeClass) private var systemSizeClass
    /// UI tests (Debug only): a window width forced by a link, to change
    /// the size class without resizing the window (M15).
    @State private var testSizeClass: UserInterfaceSizeClass?

    var body: some View {
        @Bindable var app = app
        VStack(spacing: 0) {
            // M16: always visible during the demo, above every tab.
            if app.isDemo && !DemoBanner.isHiddenForScreenshots { DemoBanner() }
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
                            .readableWidth()
                            .navigationDestination(for: Route.self) { RouteDestination(route: $0) }
                    }
                    .transition(.move(edge: .leading))
                }
            }
            .animation(.default, value: app.openConsoleId)
            // Always applied (the same value outside UI tests), so the
            // tree keeps its identity.
            .environment(\.horizontalSizeClass, testSizeClass ?? systemSizeClass)
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
            case .archiveImport(let context): ArchiveImportView(context: context)
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
            #if DEBUG
            if let width = UITestWidth(url: url) {
                testSizeClass = width.sizeClass
                return
            }
            #endif
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
        // M15: the iCloud Keychain sends no change notification, and a
        // Mac window stays active while another app is in front: the
        // shared pairings are read again every minute while active (a few
        // Keychain reads), besides the key-value store marker.
        .task(id: scenePhase) {
            guard scenePhase == .active else { return }
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(60))
                guard !Task.isCancelled else { return }
                app.reloadPairings()
            }
        }
    }
}

/// The screen of a navigation destination, at a readable width in a large
/// window (M15).
struct RouteDestination: View {
    let route: Route

    var body: some View {
        if case .screenshots = route {
            // M22: the grid uses the whole width of the window.
            screen
        } else {
            screen.readableWidth()
        }
    }

    @ViewBuilder
    private var screen: some View {
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
        case .screenshots(let id): ScreenshotsView(deviceId: id)
        case .sdFolder(let folder): SdFolderView(route: folder)
        }
    }
}

/// The tabs of an open console (milestone M11, M23): Console (dashboard,
/// with Transfers and Trash), Library, Apps and Files (the SD card), each
/// with its own navigation. A button of
/// each tab's first screen goes back to the list of consoles.
///
/// Large windows (M15): the tabs can become a sidebar (iPad, Mac), and the
/// Library tab shows the list of games and the game side by side. What is
/// shown stays in ``AppModel/tabPaths`` either way, so a window that changes
/// size (Split View, Stage Manager, the screens of an iPhone Duo) keeps its
/// navigation.
struct ConsoleTabsView: View {
    @Environment(AppModel.self) private var app
    let deviceId: String
    @State private var isVisible = false

    var body: some View {
        @Bindable var app = app
        let session = app.session(for: deviceId)
        TabView(selection: $app.consoleTab) {
            Tab("Console", systemImage: "gamecontroller", value: ConsoleTab.console) {
                tab(.console) { ConsoleView(deviceId: deviceId) }
                    .accessibilityIdentifier("tab.console")
            }
            Tab("Library", systemImage: "square.grid.2x2", value: ConsoleTab.library) {
                // Identified by its presentation (library.columns or
                // library.stack).
                LibraryTab(deviceId: deviceId)
            }
            // M23: the apps and the files of the SD card get tabs; the
            // transfers and the trash open from the Console tab.
            Tab("Apps", systemImage: "square.stack.3d.down.right", value: ConsoleTab.apps) {
                tab(.apps) { PaksView(deviceId: deviceId) }
                    .accessibilityIdentifier("tab.apps")
            }
            Tab("Files", systemImage: "folder", value: ConsoleTab.files) {
                tab(.files) { SdFolderView(route: .root(deviceId)) }
                    .accessibilityIdentifier("tab.files")
            }
        }
        .tabViewStyle(.sidebarAdaptable)
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

    private func tab<Content: View>(_ tab: ConsoleTab, @ViewBuilder root: () -> Content) -> some View {
        NavigationStack(path: Binding(
            get: { app.tabPaths[tab] ?? [] },
            set: { app.tabPaths[tab] = $0 }
        )) {
            root()
                .readableWidth()
                .consolesButton()
                .navigationDestination(for: Route.self) { RouteDestination(route: $0) }
        }
    }
}

/// The Library tab (M15): one `NavigationSplitView`, whatever the width. In
/// a wide window the list of games is a column and the selected screen is
/// beside it; in a narrow one (iPhone, Slide Over, the outer screen of an
/// iPhone Duo) the split view collapses into one stack: the list, then the
/// selection and the screens pushed above it, as before M15. The first route
/// of the tab's path is the selection (a game, or the systems of Browse by
/// Console) and the rest is pushed in the detail column. Being one view
/// tree, a change of width keeps the screens shown and their state (an open
/// sheet, a draft, a download in progress).
struct LibraryTab: View {
    @Environment(AppModel.self) private var app
    @Environment(\.horizontalSizeClass) private var sizeClass
    let deviceId: String

    private var path: [Route] { app.tabPaths[.library] ?? [] }

    var body: some View {
        NavigationSplitView(preferredCompactColumn: compactColumn) {
            LibraryView(deviceId: deviceId)
                .readableWidth()
                .consolesButton()
                .environment(\.librarySelection, selection)
                .navigationSplitViewColumnWidth(min: 340, ideal: 420, max: 480)
        } detail: {
            NavigationStack(path: Binding(
                get: { LibraryColumns.detail(of: path) },
                set: { app.tabPaths[.library] = LibraryColumns.path(path, detail: $0) }
            )) {
                Group {
                    if let first = path.first {
                        // A new selection is a new screen, with its own state.
                        RouteDestination(route: first).id(first)
                    } else {
                        ContentUnavailableView("Select a game", systemImage: "square.grid.2x2")
                            .accessibilityIdentifier("library.noSelection")
                    }
                }
                .navigationDestination(for: Route.self) { RouteDestination(route: $0) }
            }
        }
        // Which presentation is shown, for the UI tests.
        .accessibilityIdentifier(sizeClass == .regular ? "library.columns" : "library.stack")
    }

    /// Collapsed (narrow window): the list while nothing is selected, else
    /// the selection and what is pushed above it. Back to the list clears
    /// the selection.
    private var compactColumn: Binding<NavigationSplitViewColumn> {
        Binding(
            get: { path.isEmpty ? .sidebar : .detail },
            set: { column in
                // Written by SwiftUI in a wide window too, and in a narrow
                // one when a screen above the selection is popped (before
                // the detail stack itself): only "back" from the selection
                // itself means "back to the list".
                if sizeClass == .compact, column == .sidebar, path.count == 1 { app.tabPaths[.library] = [] }
            }
        )
    }

    /// The first route of the path: what the list shows as selected.
    private var selection: Binding<Route?> {
        Binding(
            get: { LibraryColumns.selection(of: path) },
            set: { app.tabPaths[.library] = LibraryColumns.path(path, selecting: $0) }
        )
    }
}

/// The path of the Library tab seen as two columns (M15): the first route
/// is selected in the list, the others are pushed beside it.
enum LibraryColumns {
    static func selection(of path: [Route]) -> Route? {
        path.first
    }

    /// The screens pushed in the detail column.
    static func detail(of path: [Route]) -> [Route] {
        Array(path.dropFirst())
    }

    /// A row selected in the list: its screen alone, or the same path when
    /// it is already the selection (screens pushed above it stay).
    static func path(_ path: [Route], selecting route: Route?) -> [Route] {
        guard route != path.first else { return path }
        return route.map { [$0] } ?? []
    }

    /// The detail column pushed or went back.
    static func path(_ path: [Route], detail: [Route]) -> [Route] {
        guard let first = path.first else { return path }
        return [first] + detail
    }
}

extension View {
    /// "Consoles", at the top left of the first screen of each tab (M11):
    /// back to the list of consoles.
    func consolesButton() -> some View {
        modifier(ConsolesButton())
    }
}

private struct ConsolesButton: ViewModifier {
    @Environment(AppModel.self) private var app

    func body(content: Content) -> some View {
        content.toolbar {
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
    }
}

#if DEBUG
/// UI tests (M15): `reminislink://uitest-width?class=compact|regular|system`
/// changes the width class of the console screens as a narrower or wider
/// window would, without resizing the window (XCUITest cannot). Only when
/// the test launched the app with `RL_UITEST_WIDTH_LINKS=1`: elsewhere the
/// link is an ordinary (invalid) link.
struct UITestWidth {
    let sizeClass: UserInterfaceSizeClass?

    init?(url: URL) {
        guard ProcessInfo.processInfo.environment["RL_UITEST_WIDTH_LINKS"] == "1",
              url.scheme?.lowercased() == "reminislink", url.host()?.lowercased() == "uitest-width",
              let value = URLComponents(url: url, resolvingAgainstBaseURL: false)?
                  .queryItems?.first(where: { $0.name == "class" })?.value
        else { return nil }
        switch value {
        case "compact": sizeClass = .compact
        case "regular": sizeClass = .regular
        case "system": sizeClass = nil
        default: return nil
        }
    }
}
#endif
