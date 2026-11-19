import Foundation

/// Chooses where the saves are kept: the `Documents` folder of the app's
/// iCloud container (shown as "ReminisLink" in iCloud Drive) when iCloud
/// Drive is available, else the app's own `Documents` folder on the iPhone
/// (shown in Files under On My iPhone).
public enum SaveVaultLocator {
    /// `FileManager.url(forUbiquityContainerIdentifier:)`: nil without an
    /// iCloud account, with iCloud Drive off for the app, or without the
    /// entitlement (simulator builds without a team). It may block: never
    /// call it on the main thread.
    public typealias UbiquityLookup = @Sendable (String?) -> URL?

    public static let systemLookup: UbiquityLookup = { identifier in
        FileManager.default.url(forUbiquityContainerIdentifier: identifier)
    }

    /// The vault to use now.
    @concurrent
    public static func resolve(
        containerIdentifier: String?,
        localRoot: URL,
        stagingDirectory: URL,
        lookup: UbiquityLookup = systemLookup
    ) async -> FileSaveVault {
        if let container = lookup(containerIdentifier) {
            let documents = container.appending(path: "Documents", directoryHint: .isDirectory)
            if (try? FileManager.default.createDirectory(at: documents, withIntermediateDirectories: true)) != nil {
                return FileSaveVault(location: .iCloud, root: documents, stagingDirectory: stagingDirectory)
            }
        }
        return FileSaveVault(location: .local, root: localRoot, stagingDirectory: stagingDirectory)
    }
}
