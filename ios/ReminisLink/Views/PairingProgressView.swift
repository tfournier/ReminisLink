import ReminisLinkKit
import SwiftUI

/// Visible steps: searching for the console → pairing → done, then a field
/// to rename the console on the app side.
struct PairingProgressView: View {
    @Bindable var flow: PairingFlow

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    row(.searching, "Searching for the console")
                    row(.pairing, "Pairing")
                    row(.done, "Done")
                }

                if let error = flow.error {
                    Section {
                        ErrorView(error: error, retry: { flow.start() })
                    }
                }

                if flow.step == .done {
                    Section {
                        TextField("Console name", text: $flow.name)
                            .textInputAutocapitalization(.words)
                            .accessibilityIdentifier("pairing.name")
                    } header: {
                        Text("Name on this iPhone")
                    }
                    Section {
                        Button("Open the Console") { flow.finish() }
                            .disabled(DeviceStore.clean(flow.name) == nil)
                            .accessibilityIdentifier("pairing.done")
                    }
                }
            }
            .navigationTitle("Pairing")
            .navigationBarTitleDisplayMode(.inline)
            .interactiveDismissDisabled(flow.isRunning)
            .onDisappear { flow.stop() }
            .toolbar {
                if flow.step != .done {
                    ToolbarItem(placement: .cancellationAction) {
                        Button("Cancel") { flow.cancel() }
                    }
                }
            }
        }
    }

    private func row(_ step: PairingFlow.Step, _ title: LocalizedStringKey) -> some View {
        HStack(spacing: 12) {
            Group {
                if flow.step > step || flow.step == .done {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                } else if flow.step == step && flow.error != nil {
                    Image(systemName: "xmark.circle.fill").foregroundStyle(.red)
                } else if flow.step == step {
                    ProgressView()
                } else {
                    Image(systemName: "circle").foregroundStyle(.secondary)
                }
            }
            .frame(width: 24)
            .accessibilityHidden(true)
            Text(title)
        }
        .accessibilityElement(children: .combine)
        .accessibilityValue(stateText(step))
    }

    private func stateText(_ step: PairingFlow.Step) -> Text {
        if flow.step > step || flow.step == .done { return Text("Completed") }
        if flow.step == step { return flow.error == nil ? Text("In progress") : Text("Failed") }
        return Text("Pending")
    }
}
