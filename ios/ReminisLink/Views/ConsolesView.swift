import ReminisLinkKit
import SwiftUI

/// Paired consoles (docs/specs/06-app-ios.md §4.1).
struct ConsolesView: View {
    @Environment(AppModel.self) private var app
    /// A console paired before M13 to forget, waiting for confirmation.
    @State private var toForget: PairedDevice?
    @State private var forgetError: AppError?

    var body: some View {
        Group {
            if app.store.devices.isEmpty {
                WelcomeView()
            } else {
                List {
                    if let notice = app.closedNotice {
                        Section { ClosedConsoleNoticeView(notice: notice) }
                    }
                    if app.discovery.status == .localNetworkDenied {
                        Section { ErrorView(error: .localNetworkDenied) }
                    }
                    Section {
                        ForEach(app.store.devices) { device in
                            // Opens the console with its tabs (M11); a
                            // console paired before M13 is paired again
                            // first (scan its QR code).
                            Button {
                                if device.needsRepair {
                                    app.showRepair(deviceId: device.id)
                                } else {
                                    app.openConsole(device.id)
                                }
                            } label: {
                                HStack {
                                    ConsoleRow(device: device, isOnline: DiscoveryLogic.find(id: device.id, in: app.discovery.devices) != nil)
                                    Image(systemName: "chevron.forward")
                                        .font(.footnote.weight(.semibold))
                                        .foregroundStyle(.tertiary)
                                        .accessibilityHidden(true)
                                }
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(.plain)
                            .accessibilityIdentifier("console.\(device.id)")
                            // Never a full swipe: the entry is what is left to
                            // pair it again; forgetting it is confirmed first.
                            .swipeActions(allowsFullSwipe: false) {
                                if device.needsRepair {
                                    Button("Forget") { toForget = device }
                                        .tint(.red)
                                        .accessibilityIdentifier("console.forget")
                                }
                            }
                        }
                    } footer: {
                        if app.store.devices.contains(where: \.needsRepair) {
                            Text("This version of ReminisLink pairs differently: update ReminisLink on the console, then scan its QR code again.")
                        }
                    }
                }
            }
        }
        .confirmationDialog(
            "Forget this console?",
            isPresented: Binding(get: { toForget != nil }, set: { if !$0 { toForget = nil } }),
            titleVisibility: .visible, presenting: toForget
        ) { device in
            Button("Forget", role: .destructive) {
                do {
                    try app.forget(deviceId: device.id)
                } catch {
                    forgetError = AppError(error)
                }
            }
            .accessibilityIdentifier("console.forget.confirm")
        } message: { device in
            Text("\(device.name) will be removed from this list. To use it again, scan its QR code.")
        }
        .alert(
            "Could Not Forget the Console",
            isPresented: Binding(get: { forgetError != nil }, set: { if !$0 { forgetError = nil } }),
            presenting: forgetError
        ) { _ in
            Button("OK", role: .cancel) {}
        } message: { error in
            Text(error.message)
        }
        .navigationTitle("Consoles")
        .toolbar {
            ToolbarItem(placement: .topBarLeading) {
                NavigationLink(value: Route.settings) {
                    Label("Settings", systemImage: "gearshape")
                }
                .accessibilityIdentifier("consoles.settings")
            }
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

/// "The console closed ReminisLink" (milestone M11): the app came back
/// to the list when the pak quit; its uploads wait, paused.
struct ClosedConsoleNoticeView: View {
    @Environment(AppModel.self) private var app
    let notice: ClosedConsoleNotice

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: "power.circle.fill")
                .font(.title2)
                .foregroundStyle(.orange)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 4) {
                Text("The console closed ReminisLink")
                    .font(.headline)
                if let name = app.store.device(id: notice.deviceId)?.name {
                    Text(name).font(.subheadline).foregroundStyle(.secondary)
                }
                Text("Uploads in progress are paused. Open ReminisLink on the console, then the console here, to go on.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("consoles.closedNotice")
            Spacer(minLength: 0)
            Button {
                app.closedNotice = nil
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .foregroundStyle(.secondary)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(Text("Dismiss"))
            .accessibilityIdentifier("closedNotice.dismiss")
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
            if device.needsRepair {
                // M13: paired before the new pairing; scan its QR code.
                Label("Pair Again", systemImage: "qrcode.viewfinder")
                    .font(.footnote)
                    .foregroundStyle(.orange)
                    .accessibilityIdentifier("console.needsRepair")
            } else {
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
