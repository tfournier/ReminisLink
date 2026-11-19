import ReminisLinkKit
import SwiftUI

/// Milestone M10 (single writer): shown above the screens of a console
/// while another device has write access, with "Take Over" (after a
/// confirmation). Write actions are disabled meanwhile; reads, downloads to
/// this iPhone and the save sync stay available.
struct ReadOnlyBanner: View {
    @Environment(AppModel.self) private var app
    let deviceId: String
    @State private var confirm = false
    @State private var isTaking = false
    @State private var error: AppError?

    var body: some View {
        let session = app.session(for: deviceId)
        if case .readOnly(let holder, let tookOver) = session.lease.access, session.state == .connected {
            VStack(alignment: .leading, spacing: 8) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Image(systemName: "lock.fill")
                        .accessibilityHidden(true)
                    Text(Self.title(holder: holder, tookOver: tookOver))
                        .font(.subheadline.weight(.semibold))
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("readOnly.title")
                }
                Text("Viewing and downloading still work. Changes are disabled on this iPhone.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                if let error {
                    Text(error.message)
                        .font(.footnote)
                        .foregroundStyle(.red)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Button {
                    confirm = true
                } label: {
                    HStack {
                        Text("Take Over")
                        if isTaking { ProgressView() }
                    }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
                .disabled(isTaking)
                .accessibilityIdentifier("readOnly.takeOver")
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(12)
            .background(.yellow.opacity(0.18), in: RoundedRectangle(cornerRadius: 12))
            .padding(.horizontal)
            .padding(.vertical, 4)
            .background(.bar)
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("readOnly")
            .confirmationDialog("Take write access?", isPresented: $confirm, titleVisibility: .visible) {
                Button("Take Over") { Task { await takeOver(session) } }
                    .accessibilityIdentifier("readOnly.confirm")
                Button("Cancel", role: .cancel) {}
            } message: {
                if holder.isEmpty {
                    Text("The other device becomes read-only. Its transfers in progress pause.")
                } else {
                    Text("\(holder) becomes read-only. Its transfers in progress pause.")
                }
            }
        }
    }

    static func title(holder: String, tookOver: Bool) -> String {
        switch (holder.isEmpty, tookOver) {
        case (true, true): String(localized: "Read-only — another device took over")
        case (true, false): String(localized: "Read-only — another device has write access")
        case (false, true): String(localized: "Read-only — \(holder) took over")
        case (false, false): String(localized: "Read-only — \(holder) has write access")
        }
    }

    private func takeOver(_ session: ConsoleSession) async {
        isTaking = true
        defer { isTaking = false }
        do {
            try await session.takeOver()
            error = nil
        } catch {
            let error = AppError(error)
            if !error.isCancellation { self.error = error }
        }
    }
}

/// The banner above a screen of a console, and ``EnvironmentValues/consoleIsReadOnly``
/// for the rows that do not hold the session.
private struct ReadOnlyBannerModifier: ViewModifier {
    @Environment(AppModel.self) private var app
    let deviceId: String

    func body(content: Content) -> some View {
        content
            .environment(\.consoleIsReadOnly, app.session(for: deviceId).isReadOnly)
            .safeAreaInset(edge: .top, spacing: 0) { ReadOnlyBanner(deviceId: deviceId) }
    }
}

extension View {
    /// The read-only banner of a console above this screen (M10).
    @ViewBuilder
    func readOnlyBanner(deviceId: String?) -> some View {
        if let deviceId {
            modifier(ReadOnlyBannerModifier(deviceId: deviceId))
        } else {
            self
        }
    }
}

extension EnvironmentValues {
    /// Another device has write access to the console of this screen (M10):
    /// write actions are disabled.
    @Entry var consoleIsReadOnly = false
}

extension ConsoleSession {
    /// Write actions are possible: no other device has write access (M10).
    var canWrite: Bool { !isReadOnly }
}
