import ReminisLinkKit
import SwiftUI

/// Paired consoles (docs/specs/06-app-ios.md §4.1).
struct ConsolesView: View {
    @Environment(AppModel.self) private var app

    var body: some View {
        Group {
            if app.store.devices.isEmpty {
                WelcomeView()
            } else {
                List {
                    if app.discovery.status == .localNetworkDenied {
                        Section { ErrorView(error: .localNetworkDenied) }
                    }
                    Section {
                        ForEach(app.store.devices) { device in
                            NavigationLink(value: Route.console(device.id)) {
                                ConsoleRow(device: device, isOnline: DiscoveryLogic.find(id: device.id, in: app.discovery.devices) != nil)
                            }
                            .accessibilityIdentifier("console.\(device.id)")
                        }
                    }
                }
            }
        }
        .navigationTitle("Consoles")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button {
                    app.sheet = .addConsole()
                } label: {
                    Label("Add a Console", systemImage: "plus")
                }
                .accessibilityIdentifier("consoles.add")
            }
        }
    }
}

struct ConsoleRow: View {
    let device: PairedDevice
    let isOnline: Bool

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: device.model.symbolName)
                .font(.title2)
                .foregroundStyle(.tint)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(device.name).font(.headline)
                Text(device.model.localizedName)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            HStack(spacing: 4) {
                Circle()
                    .fill(isOnline ? Color.green : Color.secondary)
                    .frame(width: 8, height: 8)
                    .accessibilityHidden(true)
                Text(isOnline ? "Online" : "Offline")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

/// Shown while no console is paired: three steps to get started.
struct WelcomeView: View {
    @Environment(AppModel.self) private var app

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                Image(systemName: "gamecontroller")
                    .font(.system(size: 56))
                    .foregroundStyle(.tint)
                    .frame(maxWidth: .infinity)
                    .accessibilityHidden(true)
                Text("Manage your handheld from your iPhone")
                    .font(.title2.bold())
                    .frame(maxWidth: .infinity)
                    .multilineTextAlignment(.center)
                VStack(alignment: .leading, spacing: 16) {
                    step(1, "Install ReminisLink on the console from the Pak Store.")
                    step(2, "Open ReminisLink from the Tools menu of the console. If another device is already paired, open Settings > Pair a New Device.")
                    step(3, "Scan the QR code shown on the console.")
                }
                if app.discovery.status == .localNetworkDenied {
                    ErrorView(error: .localNetworkDenied)
                }
                Button {
                    app.sheet = .addConsole()
                } label: {
                    Text("Add a Console").frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .accessibilityIdentifier("welcome.add")
            }
            .padding()
        }
        .accessibilityIdentifier("welcome")
    }

    private func step(_ number: Int, _ text: LocalizedStringKey) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text(number, format: .number)
                .font(.headline)
                .frame(minWidth: 28, minHeight: 28)
                .background(Circle().fill(.tint.opacity(0.15)))
            Text(text)
                .fixedSize(horizontal: false, vertical: true)
        }
        .accessibilityElement(children: .combine)
    }
}
