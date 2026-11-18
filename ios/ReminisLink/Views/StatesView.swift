import ReminisLinkKit
import SwiftUI

/// The state slots of a game (docs/specs/06-app-ios.md §4.5): one row per
/// slot (thumbnail, date, size, lock), lock, unlock, restore the locked
/// copy, download, delete, and the resume history.
struct StatesView: View {
    /// A confirmation waiting for the user.
    enum Confirmation: Identifiable {
        case unlock(StateSlot)
        case restore(StateSlot)
        case delete(StateSlot)
        case restoreResume(ResumeHistoryEntry)

        var id: String {
            switch self {
            case .unlock(let s): "unlock-\(s.slot)"
            case .restore(let s): "restore-\(s.slot)"
            case .delete(let s): "delete-\(s.slot)"
            case .restoreResume(let e): "resume-\(e.id)"
            }
        }
    }

    @Environment(AppModel.self) private var app
    let route: GameRoute

    @State private var slots: SlotList?
    @State private var history: ResumeHistory?
    /// Slots reported `restored` while this screen is open: the pak reports
    /// it only once (docs/specs/05 §5.2), the badge stays until leaving.
    @State private var restoredSlots: Set<Int> = []
    @State private var error: AppError?
    @State private var notice: String?
    @State private var isWorking = false
    @State private var exported: ExportedFile?
    @State private var selected: StateSlot?
    @State private var confirmation: Confirmation?

    private var session: ConsoleSession { app.session(for: route.deviceId) }

    var body: some View {
        List {
            if let error {
                Section { ErrorView(error: error, retry: load) }
            }
            if let notice {
                Section { NoticeView(text: notice) }
            }
            if let slots {
                slotsSection(slots)
                historySection(resumeSlot: slots.resumeSlot)
            }
        }
        .overlay {
            if slots == nil && error == nil { ProgressView() }
        }
        .disabled(isWorking)
        .overlay(alignment: .bottom) {
            if isWorking {
                ProgressView().padding().background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12)).padding()
            }
        }
        .navigationTitle("States")
        .navigationBarTitleDisplayMode(.inline)
        .task { if slots == nil { await load() } }
        .refreshable { await load() }
        .exportSheet($exported)
        .confirmationDialog(
            selected.map { String(localized: "Slot \($0.slot)") } ?? "",
            isPresented: Binding(get: { selected != nil }, set: { if !$0 { selected = nil } }),
            titleVisibility: .visible, presenting: selected
        ) { slot in
            actions(for: slot)
        } message: { slot in
            if slot.locked {
                Text("Locked states cannot be deleted: unlock it first.")
            } else {
                Text("\(slot.modifiedAt.dateTimeString) · \(slot.sizeBytes.byteString)")
            }
        }
        .alert(
            confirmation.map(title) ?? Text(verbatim: ""),
            isPresented: Binding(get: { confirmation != nil }, set: { if !$0 { confirmation = nil } }),
            presenting: confirmation
        ) { confirmation in
            buttons(confirmation)
        } message: { confirmation in
            message(confirmation)
        }
    }

    // MARK: Sections

    private func slotsSection(_ list: SlotList) -> some View {
        Section {
            if list.slots.isEmpty {
                Text("No state for this game on the console.")
                    .foregroundStyle(.secondary)
            }
            ForEach(list.slots) { slot in
                Button {
                    selected = slot
                } label: {
                    StateRow(
                        deviceId: route.deviceId, gameId: route.gameId, slot: slot,
                        isResume: slot.slot == list.resumeSlot,
                        restored: slot.wasRestored || restoredSlots.contains(slot.slot)
                    )
                }
                .foregroundStyle(.primary)
                .accessibilityIdentifier("state.\(slot.slot)")
            }
        } header: {
            Text("Slots")
        } footer: {
            VStack(alignment: .leading, spacing: 6) {
                Text("A lock keeps a copy of the state on the console. It cannot stop a game from overwriting the state: the state is restored the next time ReminisLink is opened on the console if it was overwritten in game.")
                Text("Deleted states go to the trash of the console for at least 30 days.")
            }
            .accessibilityIdentifier("states.lockHelp")
        }
    }

    @ViewBuilder
    private func historySection(resumeSlot: Int?) -> some View {
        if let history, resumeSlot != nil || !history.entries.isEmpty {
            Section {
                if history.entries.isEmpty {
                    Text("No version archived yet.")
                        .foregroundStyle(.secondary)
                }
                ForEach(history.entries) { entry in
                    Button {
                        confirmation = .restoreResume(entry)
                    } label: {
                        HStack {
                            Text(entry.capturedAt.dateTimeString)
                            Spacer()
                            Text(entry.sizeBytes.byteString).foregroundStyle(.secondary)
                        }
                        .accessibilityElement(children: .combine)
                        .accessibilityHint(Text("Restores this version of the resume state"))
                    }
                    .foregroundStyle(.primary)
                    .accessibilityIdentifier("resume.entry")
                }
            } header: {
                Text("Resume History")
            } footer: {
                VStack(alignment: .leading, spacing: 6) {
                    if let resumeSlot {
                        Text("The automatic resume state is in slot \(resumeSlot).")
                            .accessibilityIdentifier("resume.slot")
                    }
                    Text("Each time ReminisLink opens on the console, it archives the resume state if it changed. The last 5 versions are kept.")
                }
            }
        }
    }

    @ViewBuilder
    private func actions(for slot: StateSlot) -> some View {
        if slot.locked {
            Button("Restore the Locked Copy…") { confirmation = .restore(slot) }
                .accessibilityIdentifier("state.restore")
            Button("Unlock…") { confirmation = .unlock(slot) }
                .accessibilityIdentifier("state.unlock")
        } else {
            Button("Lock") { Task { await lock(slot) } }
                .accessibilityIdentifier("state.lock")
        }
        if slot.canDownload {
            Button("Download") { Task { await download(slot) } }
                .accessibilityIdentifier("state.download")
        }
        if slot.canDelete {
            Button("Delete…", role: .destructive) { confirmation = .delete(slot) }
                .accessibilityIdentifier("state.delete")
        }
    }

    private func title(_ confirmation: Confirmation) -> Text {
        switch confirmation {
        case .unlock(let slot): Text("Unlock slot \(slot.slot)?")
        case .restore(let slot): Text("Restore slot \(slot.slot)?")
        case .delete(let slot): Text("Delete slot \(slot.slot)?")
        case .restoreResume: Text("Restore this version?")
        }
    }

    @ViewBuilder
    private func buttons(_ confirmation: Confirmation) -> some View {
        switch confirmation {
        case .unlock(let slot):
            Button("Unlock", role: .destructive) { Task { await unlock(slot) } }
                .accessibilityIdentifier("confirm.ok")
        case .restore(let slot):
            Button("Restore") { Task { await restore(slot) } }
                .accessibilityIdentifier("confirm.ok")
        case .delete(let slot):
            Button("Delete", role: .destructive) { Task { await delete(slot) } }
                .accessibilityIdentifier("confirm.ok")
        case .restoreResume(let entry):
            Button("Restore") { Task { await restoreResume(entry) } }
                .accessibilityIdentifier("confirm.ok")
        }
        Button("Cancel", role: .cancel) {}
    }

    private func message(_ confirmation: Confirmation) -> Text {
        switch confirmation {
        case .unlock:
            Text("The locked copy is deleted. The state itself does not change, but it is no longer restored if a game overwrites it.")
        case .restore:
            Text("The current state goes to the trash of the console, then the locked copy replaces it.")
        case .delete:
            Text("The state goes to the trash of the console, where it stays for at least 30 days.")
        case .restoreResume(let entry):
            Text("The current resume state is archived first, then replaced by the version of \(entry.capturedAt.dateTimeString).")
        }
    }

    // MARK: Actions

    private func load() async {
        let gameId = route.gameId
        do {
            async let states = session.perform { try await $0.states(gameId: gameId) }
            async let resume = session.perform { try await $0.resumeHistory(gameId: gameId) }
            let (list, entries) = try await (states, resume)
            restoredSlots.formUnion(list.slots.filter(\.wasRestored).map(\.slot))
            slots = list
            history = entries
            error = nil
        } catch {
            show(error)
        }
    }

    private func show(_ error: Error) {
        let error = AppError(error)
        if !error.isCancellation { self.error = error }
    }

    private func run(_ action: () async throws -> Void) async {
        isWorking = true
        defer { isWorking = false }
        error = nil
        notice = nil
        do {
            try await action()
        } catch {
            show(error)
        }
    }

    private func updated(_ slot: StateSlot) {
        restoredSlots.remove(slot.slot)
        slots = slots?.replacing(slot)
        session.contentChanged()
    }

    private func lock(_ slot: StateSlot) async {
        let gameId = route.gameId
        let number = slot.slot
        await run {
            updated(try await session.perform { try await $0.lockState(gameId: gameId, slot: number) })
            notice = String(localized: "Slot \(number) locked.")
        }
    }

    private func unlock(_ slot: StateSlot) async {
        let gameId = route.gameId
        let number = slot.slot
        await run {
            let result = try await session.perform { try await $0.unlockState(gameId: gameId, slot: number) }
            notice = String(localized: "Slot \(number) unlocked.")
            if slot.isMissing {
                // Nothing left in that slot.
                session.contentChanged()
                await load()
            } else {
                updated(result)
            }
        }
    }

    private func restore(_ slot: StateSlot) async {
        let gameId = route.gameId
        let number = slot.slot
        await run {
            updated(try await session.perform { try await $0.restoreLockedState(gameId: gameId, slot: number) })
            notice = String(localized: "Slot \(number) restored from its locked copy.")
        }
    }

    private func delete(_ slot: StateSlot) async {
        let gameId = route.gameId
        let number = slot.slot
        await run {
            _ = try await session.perform { try await $0.deleteState(gameId: gameId, slot: number) }
            notice = String(localized: "Slot \(number) moved to the trash of the console.")
            session.contentChanged()
            await load()
        }
    }

    private func download(_ slot: StateSlot) async {
        let gameId = route.gameId
        let number = slot.slot
        await run {
            exported = try await session.export { try await $0.downloadState(gameId: gameId, slot: number, into: $1) }
        }
    }

    private func restoreResume(_ entry: ResumeHistoryEntry) async {
        let gameId = route.gameId
        let entryId = entry.id
        await run {
            history = try await session.perform { try await $0.restoreResumeHistory(gameId: gameId, entryId: entryId) }
            notice = String(localized: "Resume state restored.")
            session.contentChanged()
            await load()
        }
    }
}

/// One slot: thumbnail, number, date, size, lock and its status.
struct StateRow: View {
    @Environment(AppModel.self) private var app
    let deviceId: String
    let gameId: String
    let slot: StateSlot
    let isResume: Bool
    let restored: Bool

    @State private var thumbnail: UIImage?

    var body: some View {
        HStack(spacing: 12) {
            Group {
                if let thumbnail {
                    Image(uiImage: thumbnail).resizable().scaledToFill()
                } else {
                    Image(systemName: slot.isMissing ? "questionmark.square.dashed" : "photo")
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                        .background(.quaternary)
                }
            }
            .frame(width: 64, height: 48)
            .clipShape(RoundedRectangle(cornerRadius: 6))
            .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Text("Slot \(slot.slot)").font(.headline)
                    if isResume {
                        TagView(text: Text("Resume State"))
                    }
                    if restored {
                        TagView(text: Text("Restored"), tint: .green)
                            .accessibilityIdentifier("state.restored")
                    }
                }
                if slot.isMissing {
                    Text("Missing on the console. The locked copy is kept: restore it to bring the state back.")
                        .font(.subheadline)
                        .foregroundStyle(.orange)
                } else {
                    Text("\(slot.modifiedAt.dateTimeString) · \(slot.sizeBytes.byteString)")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
            }
            Spacer()
            if slot.locked {
                Image(systemName: "lock.fill")
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityValue(slot.locked ? Text("Locked") : Text(verbatim: ""))
        .accessibilityHint(Text("Shows the actions for this slot"))
        .task(id: slot) { await loadThumbnail() }
    }

    /// Best effort: a thumbnail error never touches the session.
    private func loadThumbnail() async {
        guard slot.hasThumbnail, let client = app.session(for: deviceId).client else {
            thumbnail = nil
            return
        }
        let number = slot.slot
        let gameId = gameId
        if let data = try? await client.stateThumbnail(gameId: gameId, slot: number) {
            thumbnail = UIImage(data: data)
        }
    }
}
