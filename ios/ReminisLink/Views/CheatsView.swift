import ReminisLinkKit
import SwiftUI

/// The cheats of a game (docs/specs/06-app-ios.md §4.5): switches, add,
/// edit, delete, reorder, import. Changes stay on this iPhone until saved;
/// the whole list is then sent with `If-Match`. If the file changed on the
/// console meanwhile, nothing is overwritten: the user can only reload.
struct CheatsView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    let route: GameRoute

    @State private var editor: CheatEditor?
    @State private var error: AppError?
    @State private var notice: String?
    @State private var isWorking = false
    @State private var editing: Cheat?
    @State private var isAdding = false
    @State private var showImport = false
    @State private var showConflict = false
    @State private var confirmDiscard = false
    @State private var confirmEmpty = false
    /// The shown error comes from a save: "Try Again" saves again (local
    /// changes are never dropped without "Discard Changes").
    @State private var saveFailed = false
    /// Only the last read of the list is used (M14: a reload, a change on
    /// the console and a save may overlap).
    @State private var reads = LoadGeneration()

    private var session: ConsoleSession { app.session(for: route.deviceId) }
    private var hasChanges: Bool { editor?.hasChanges ?? false }

    var body: some View {
        List {
            if let error {
                Section { ErrorView(error: error, retry: retryAfterError) }
            }
            if let notice {
                Section { NoticeView(text: notice) }
            }
            if let editor {
                cheatsSection(editor)
            }
        }
        .overlay {
            if editor == nil && error == nil { ProgressView() }
        }
        .disabled(isWorking)
        .overlay(alignment: .bottom) {
            if isWorking {
                ProgressView().padding().background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12)).padding()
            }
        }
        .navigationTitle("Cheats")
        .navigationBarTitleDisplayMode(.inline)
        // Unsaved changes are never lost by going back: save or discard.
        .navigationBarBackButtonHidden(hasChanges)
        .toolbar { toolbar }
        .task { if editor == nil { await load() } }
        .refreshable { if !hasChanges { await load() } }
        // M14: the console changed something (any device): the list is
        // read again; local edits are kept, and the user is told if they
        // can no longer be saved.
        .onChange(of: session.libraryRevision) { Task { await consoleMayHaveChanged() } }
        // M14: a newer list of the console held back while a sheet edits
        // a cheat is shown once the sheet closes.
        .sheet(item: $editing, onDismiss: applyHeldList) { cheat in
            CheatEditView(cheat: cheat) { description, code, enabled in
                editor?.update(id: cheat.id, description: description, code: code, enabled: enabled) ?? false
            }
        }
        .sheet(isPresented: $isAdding, onDismiss: applyHeldList) {
            CheatEditView(cheat: nil) { description, code, enabled in
                editor?.add(description: description, code: code, enabled: enabled) ?? false
            }
        }
        .sheet(isPresented: $showImport) {
            CheatImportView(route: route) { result in
                notice = String(localized: "Imported: \(result.imported). Cheats in the list: \(result.total).")
                Task { await load() }
            }
        }
        .alert("The list changed on the console. Reload?", isPresented: $showConflict) {
            Button("Reload", role: .destructive) { Task { await load() } }
                .accessibilityIdentifier("cheats.reload")
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Your changes were not saved. Reloading shows the list of the console and discards your changes on this iPhone.")
        }
        .confirmationDialog("Save an empty list?", isPresented: $confirmEmpty, titleVisibility: .visible) {
            Button("Save the Empty List", role: .destructive) { Task { await saveChanges() } }
                .accessibilityIdentifier("cheats.saveEmptyConfirm")
        } message: {
            Text("The cheat file of the game is deleted on the console; its previous content is kept there (.cht.bak).")
        }
        .confirmationDialog("Discard your changes?", isPresented: $confirmDiscard, titleVisibility: .visible) {
            Button("Discard Changes", role: .destructive) {
                // A newer list of the console, if one was reported, is shown.
                editor?.discardChanges()
                notice = nil
            }
                .accessibilityIdentifier("cheats.discardConfirm")
        } message: {
            Text("The list of the console stays as it is.")
        }
    }

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        if hasChanges {
            ToolbarItem(placement: .cancellationAction) {
                Button("Discard") { confirmDiscard = true }
                    .accessibilityIdentifier("cheats.discard")
            }
            ToolbarItem(placement: .confirmationAction) {
                Button(String(localized: "action.save", defaultValue: "Save", comment: "Verb: saves the changes of the cheat list")) {
                    if let editor, editor.cheats.isEmpty, !editor.original.isEmpty {
                        confirmEmpty = true
                    } else {
                        Task { await saveChanges() }
                    }
                }
                    // Another device has write access (M10).
                    .disabled(session.isReadOnly)
                    .accessibilityIdentifier("cheats.save")
            }
        } else {
            ToolbarItem(placement: .primaryAction) {
                Menu {
                    Button {
                        isAdding = true
                    } label: {
                        Label("Add a Cheat…", systemImage: "plus")
                    }
                    .accessibilityIdentifier("cheats.addCheat")
                    Button {
                        showImport = true
                    } label: {
                        Label("Import…", systemImage: "square.and.arrow.down")
                    }
                    .accessibilityIdentifier("cheats.import")
                } label: {
                    Label("Add", systemImage: "plus")
                }
                .accessibilityIdentifier("cheats.add")
                .disabled(editor == nil || session.isReadOnly)
            }
        }
        if let editor, !editor.cheats.isEmpty, !session.isReadOnly {
            ToolbarItem(placement: .bottomBar) {
                EditButton()
            }
        }
        if hasChanges {
            ToolbarItem(placement: .bottomBar) {
                Button {
                    isAdding = true
                } label: {
                    Label("Add a Cheat…", systemImage: "plus")
                }
                .disabled(session.isReadOnly)
            }
        }
    }

    private func cheatsSection(_ editor: CheatEditor) -> some View {
        Section {
            if editor.cheats.isEmpty {
                Text("No cheat for this game. Add one, or import a RetroArch cheat file (.cht).")
                    .foregroundStyle(.secondary)
            }
            ForEach(editor.cheats) { cheat in
                CheatRow(cheat: cheat, isOn: Binding(
                    get: { self.editor?.cheats.first { $0.id == cheat.id }?.enabled ?? false },
                    set: { self.editor?.setEnabled(id: cheat.id, $0) }
                )) {
                    editing = cheat
                }
                .disabled(session.isReadOnly)
            }
            .onDelete { self.editor?.remove(atOffsets: $0) }
            .onMove { self.editor?.move(fromOffsets: $0, toOffset: $1) }
            .deleteDisabled(session.isReadOnly)
            .moveDisabled(session.isReadOnly)
        } header: {
            if !editor.cheats.isEmpty {
                Text("\(editor.cheats.count) cheats")
            }
        } footer: {
            if editor.hasChanges {
                Text("Changes are kept on this iPhone until you save them.")
                    .accessibilityIdentifier("cheats.unsaved")
            } else if !editor.cheats.isEmpty {
                Text("Turn a cheat on or off, tap it to edit it, and use Edit to reorder or delete. The previous file is kept on the console (.cht.bak).")
            }
        }
    }

    // MARK: Actions

    private func load() async {
        let gameId = route.gameId
        saveFailed = false
        let generation = reads.begin()
        do {
            let list = try await session.perform { try await $0.cheats(gameId: gameId) }
            guard reads.isCurrent(generation) else { return }
            if editor == nil {
                editor = CheatEditor(list)
            } else {
                editor?.load(list)
            }
            error = nil
        } catch {
            guard reads.isCurrent(generation) else { return }
            show(error)
        }
    }

    private func consoleMayHaveChanged() async {
        guard editor != nil, !isWorking else { return }
        let gameId = route.gameId
        let generation = reads.begin()
        guard let list = try? await session.perform({ try await $0.cheats(gameId: gameId) }),
              reads.isCurrent(generation)
        else { return }
        let canApply = editing == nil && !isAdding
        if editor?.consoleChanged(list, canApply: canApply) == .conflict {
            notice = String(localized: "This list changed on the console. Your changes can no longer be saved over it: discard them to see the new list.")
        }
    }

    private func applyHeldList() {
        editor?.applyConsoleList()
    }

    private func show(_ error: Error) {
        let error = AppError(error)
        if !error.isCancellation { self.error = error }
    }

    private func saveChanges() async {
        guard let editor else { return }
        let gameId = route.gameId
        let update = editor.update()
        let etag = editor.etag
        // A read sent before the save would show the list before it.
        _ = reads.begin()
        isWorking = true
        defer { isWorking = false }
        error = nil
        notice = nil
        saveFailed = false
        do {
            let saved = try await session.perform { try await $0.replaceCheats(gameId: gameId, update, ifMatch: etag) }
            self.editor?.load(saved)
            notice = String(localized: "Cheats saved.")
            session.contentChanged()
        } catch let error as AppError where error.isPreconditionFailed {
            showConflict = true
        } catch {
            show(error)
            saveFailed = !AppError(error).isCancellation
        }
    }

    /// "Try Again" repeats what failed: the save, or the first load. It
    /// never reloads over unsaved changes.
    private func retryAfterError() async {
        if saveFailed, hasChanges {
            await saveChanges()
        } else if !hasChanges {
            await load()
        }
    }
}

/// One cheat: a switch, its description and its code. Tapping the text
/// edits it.
struct CheatRow: View {
    let cheat: Cheat
    @Binding var isOn: Bool
    let edit: () -> Void

    var body: some View {
        HStack {
            Button(action: edit) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(cheat.description)
                        .foregroundStyle(.primary)
                    Text(cheat.code)
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
            }
            .buttonStyle(.borderless)
            .tint(.primary)
            .accessibilityLabel(Text(cheat.description))
            .accessibilityHint(Text("Edits this cheat"))
            .accessibilityIdentifier("cheat.edit")
            Toggle(isOn: $isOn) {
                Text(cheat.description)
            }
            .labelsHidden()
            .accessibilityIdentifier("cheat.toggle")
        }
    }
}

/// Adds or edits a cheat: description, code, on or off.
struct CheatEditView: View {
    @Environment(\.dismiss) private var dismiss
    let cheat: Cheat?
    /// Returns false when the values are not valid.
    let save: (String, String, Bool) -> Bool

    @State private var description = ""
    @State private var code = ""
    @State private var enabled = false
    @State private var didLoad = false

    private var isValid: Bool { CheatEditor.validated(description: description, code: code) != nil }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("Description", text: $description)
                        .accessibilityIdentifier("cheat.description")
                    TextField("Code", text: $code)
                        .font(.body.monospaced())
                        .textInputAutocapitalization(.characters)
                        .autocorrectionDisabled()
                        .accessibilityIdentifier("cheat.code")
                    Toggle("Enabled", isOn: $enabled)
                        .accessibilityIdentifier("cheat.enabled")
                } footer: {
                    Text("The code is written as in a RetroArch cheat file (for example several codes joined with +).")
                }
                if let cheat, !cheat.extra.isEmpty {
                    Section {
                        Text("This cheat has other settings from its file. They are kept as they are.")
                            .foregroundStyle(.secondary)
                    }
                }
            }
            .navigationTitle(cheat == nil ? "New Cheat" : "Edit Cheat")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(cheat == nil ? "Add" : "Done") {
                        if save(description, code, enabled) { dismiss() }
                    }
                    .disabled(!isValid)
                    .accessibilityIdentifier("cheat.done")
                }
            }
            .onAppear {
                guard !didLoad else { return }
                didLoad = true
                description = cheat?.description ?? ""
                code = cheat?.code ?? ""
                enabled = cheat?.enabled ?? true
            }
        }
    }
}
