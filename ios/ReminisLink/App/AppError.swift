import Foundation
import ReminisLinkKit

/// What the user can do about an error (docs/specs/06-app-ios.md §5).
enum RecoveryAction: Equatable {
    case openSettings
    case retry
    case repair
    /// `401 disconnected` (M10): "Reconnect", which lifts the
    /// disconnection set from the console.
    case reconnect
    /// M17: ReminisLink on the console is too old for this app: a link to
    /// the install page.
    case updatePak
    /// M17: ReminisLink on the console needs a newer app: a link to the App
    /// Store.
    case updateApp
    /// M15: the identity the pointer names never arrived: give this device
    /// a new identity (Settings), after a confirmation.
    case resetIdentity
}

/// Where to update each side (M17).
enum UpdateLinks {
    /// The install page of the website, in the language of the app.
    static var installPage: URL {
        Bundle.main.preferredLocalizations.first == "fr"
            ? URL(string: "https://reminis.link/fr/install/")!
            : URL(string: "https://reminis.link/install/")!
    }

    /// The app on the App Store.
    static let appStore = URL(string: "https://apps.apple.com/app/id6837216406")!
}

/// Every error the screens show, with its translated message.
enum AppError: Error, Equatable {
    case api(APIError)
    /// No Bonjour result with the right id after 8 s.
    case consoleNotFound
    /// `NWBrowser` reports `kDNSServiceErr_PolicyDenied`.
    case localNetworkDenied
    case invalidPairingLink
    /// M13: the pairing link of a pak older than M13 (`v=1`): update the
    /// pak on the console.
    case outdatedPak
    /// A pairing link of a newer pak than this app knows: update the app.
    case pairingLinkTooNew
    /// `pairingOpen: false` (or `401 pairing_failed` with pairing closed):
    /// the pairing screen is not shown on the console (M8, on demand).
    case pairingClosed
    /// M15: the console is paired (by another device of the Apple account),
    /// but the identity it knows has not reached this device through the
    /// iCloud Keychain yet. Not a reason to pair again.
    case pairingNotSynced
    /// M15: the pointer names the identity of the Apple account, but its
    /// key has not reached this device yet: no pairing with another one.
    case identityNotArrived
    /// M15: still not after 10 minutes: its key may exist nowhere.
    case identityMissing
    /// M15: no ReminisLink QR code in an imported picture.
    case noPairingCodeInPicture
    /// M15: an imported picture too large to be read.
    case pictureTooLarge
    case storage
    /// An upload stopped on the app side.
    case upload(UploadFailure)
    /// A file chosen or shared could not be read or copied.
    case file
    /// A backup copy, locked copy or resume version could not be put back
    /// (`409 conflict` without `details.existing`): damaged, or saved by
    /// another emulator. Nothing was changed.
    case restoreRefused
    /// `422 unsupported_format` on a save or state: the game uses a NextUI
    /// setting the pak does not handle (save or state format...).
    case unsupportedGameFormat
    /// `422 patch_mismatch`: the patch was made for another version of the
    /// ROM (milestone M6).
    case patchMismatch
    /// `422 unsupported_format` on a patch: not an IPS, BPS or UPS patch
    /// the console can apply.
    case unsupportedPatch
    /// `422 patch_mismatch` with `details.reason = already-patched`.
    case alreadyPatched
    /// `409 conflict` of a patch job: the ROM changed while it ran.
    case romChangedDuringPatch
    /// `413` of a patch job.
    case patchTooLarge
    /// The device no longer knows a job (the pak was restarted).
    case jobLost
    /// The picture chosen for a game art cannot be read or made small
    /// enough (milestone M8).
    case unreadablePicture
    /// A save downloaded for the sync is not the one the console listed
    /// (milestone M9): it changed during the sync or arrived damaged.
    case syncChecksumMismatch
    /// The synced saves could not be read or written on this iPhone.
    case vault
    /// The copy of a synced version read back is not the version chosen:
    /// nothing was sent.
    case versionChanged
    /// `404` on the download of a file of a game (milestone M11): it is
    /// no longer in the game's list (renamed, deleted, replaced).
    case gameFileChanged
    /// The game is no longer on the console (`404` on `GET /games/{id}`).
    case gameRemoved
    /// The console closed ReminisLink (M11): nothing connects until the
    /// user opens the console again.
    case consoleClosed
    /// M12: the console refused a pak (`422 unsupported_format`), and why.
    case pakRefused(PakRefusal)
    /// M12: ReminisLink cannot delete or replace itself while it runs
    /// (`409 conflict` with `details.self`).
    case pakSelf
    /// M12: what was chosen on the iPhone is neither a zip archive nor a
    /// `.pak` folder.
    case notAPak
    /// M12: the pak is older than M12 (`404` on `/paks` or `/sd/browse`).
    case needsNewerPak
    /// M19: the file is not a game archive the console accepts.
    case invalidArchive
    /// M19: a new game cannot be made, its ROM name is taken.
    case archiveGameExists
    /// M19: the game has nothing to export.
    case archiveEmpty
    /// M19: a pak older than M19.
    case archivesNeedNewerPak
    /// M18: `404` on a restore from the trash: the entry left the trash
    /// (restored, deleted or purged meanwhile), or the pak is older than
    /// M18.
    case trashEntryGone
    /// M18: `422` of `replace`: only the save of a game still on the
    /// console can replace its current save.
    case trashReplaceRefused
    /// The task was cancelled (the screen went away): never shown.
    case cancelled
    case unexpected

    /// The error of a refused pairing link.
    init(pairingLink error: PairingLinkError) {
        switch error {
        case .invalid: self = .invalidPairingLink
        case .outdatedPak: self = .outdatedPak
        case .newerVersion: self = .pairingLinkTooNew
        }
    }

    init(_ error: Error) {
        switch error {
        case let error as AppError: self = error
        case is CancellationError: self = .cancelled
        case let error as APIError: self = .api(error)
        case is ResolveError: self = .consoleNotFound
        case DeviceLookupError.localNetworkDenied: self = .localNetworkDenied
        case DeviceLookupError.notFound: self = .consoleNotFound
        case DeviceStoreError.identityNotArrived: self = .identityNotArrived
        case DeviceStoreError.identityMissing: self = .identityMissing
        case is KeychainError, is DeviceStoreError: self = .storage
        case let error as PairingError: self = .api(.pairing(error))
        case let error as PairingLinkError: self = AppError(pairingLink: error)
        case let error as UploadFailure: self = .upload(error)
        case is ArtImage.Failure: self = .unreadablePicture
        case is CocoaError: self = .file
        default: self = .unexpected
        }
    }

    var message: String {
        switch self {
        case .localNetworkDenied:
            String(localized: "ReminisLink does not have access to the local network.")
        case .consoleNotFound:
            String(localized: "Console not found. Check that ReminisLink is open on the console, that the iPhone and the console are on the same Wi-Fi (not a guest network), and turn off your VPN.")
        case .invalidPairingLink:
            String(localized: "This link is not a valid ReminisLink pairing link.")
        case .outdatedPak:
            String(localized: "This QR code comes from an older version of ReminisLink. Update ReminisLink on the console, then scan the new QR code.")
        case .pairingLinkTooNew:
            String(localized: "This QR code comes from a newer version of ReminisLink. Update the app.")
        case .pairingClosed:
            String(localized: "On the console, open Settings > Pair a New Device, then scan the QR code shown.")
        case .pairingNotSynced:
            String(localized: "The pairing of this console has not fully reached this device through iCloud Keychain yet. Keep it connected to the internet: ReminisLink connects as soon as it arrives.")
        case .identityNotArrived:
            String(localized: "The identity of your Apple devices has not fully reached this device through iCloud Keychain yet. Keep it connected to the internet, then try again.")
        case .identityMissing:
            String(localized: "The identity of your Apple devices has still not reached this device. If none of your devices can pair a console either, reset this device's identity.")
        case .pictureTooLarge:
            String(localized: "This picture is too large. Use a smaller picture of the QR code.")
        case .noPairingCodeInPicture:
            String(localized: "No ReminisLink QR code was found in this picture. Use a sharp picture where the whole QR code shown by the console is visible.")
        case .storage:
            String(localized: "The pairing information could not be read or saved on this iPhone.")
        case .upload(.outcomeUnknown):
            String(localized: "The connection was lost while the console was finishing the file. Check the library before sending it again.")
        case .upload(.paused):
            String(localized: "Paused. The transfer will resume.")
        case .upload(.fileChanged):
            String(localized: "The file changed on this iPhone during the transfer. Add it again.")
        case .upload(.tooManyRestarts):
            String(localized: "The console keeps interrupting the transfer. Try again later.")
        case .upload(.cancelNotConfirmed):
            String(localized: "The cancellation did not reach the console. The transfer is kept: cancel it again, or resume it.")
        case .file:
            String(localized: "The file could not be read on this iPhone.")
        case .restoreRefused:
            String(localized: "This copy cannot be restored: it is damaged or was made by another emulator. Nothing was changed.")
        case .unsupportedGameFormat:
            String(localized: "This game uses a NextUI setting that ReminisLink does not handle yet (save or state format). Nothing was changed.")
        case .patchMismatch:
            String(localized: "This patch does not match this ROM. Check the ROM version the patch asks for.")
        case .alreadyPatched:
            String(localized: "This game seems to be already patched with this patch.")
        case .romChangedDuringPatch:
            String(localized: "The ROM changed while the patch was applied. Nothing was changed, try again.")
        case .patchTooLarge:
            String(localized: "This patch or the patched ROM is too large for the console.")
        case .unsupportedPatch:
            String(localized: "This file is not a patch the console can apply. Choose an IPS, BPS or UPS patch.")
        case .jobLost:
            String(localized: "The console was restarted and stopped this task. Check the library before trying again.")
        case .unreadablePicture:
            String(localized: "This picture cannot be used as the image of the game. Choose another one.")
        case .syncChecksumMismatch:
            String(localized: "The save changed on the console during the sync, or arrived damaged. It was not kept; the next sync will try again.")
        case .vault:
            String(localized: "The synced saves could not be read or written on this iPhone.")
        case .versionChanged:
            String(localized: "This version changed on this iPhone since it was shown. Nothing was sent to the console; open the list again.")
        case .gameFileChanged:
            String(localized: "This file changed on the console. The list of files was refreshed: try again.")
        case .gameRemoved:
            String(localized: "This game is no longer on the console.")
        case .consoleClosed:
            String(localized: "The console closed ReminisLink.")
        case .pakRefused(let refusal):
            Self.message(for: refusal)
        case .pakSelf:
            String(localized: "ReminisLink cannot delete or replace itself while it runs on the console.")
        case .notAPak:
            String(localized: "Choose a zip archive or a folder whose name ends with .pak.")
        case .needsNewerPak:
            String(localized: "Update ReminisLink on the console to manage its apps and browse its SD card.")
        case .invalidArchive:
            String(localized: "This file is not a ReminisLink game archive, or it is damaged, or it was made by a newer version of ReminisLink.")
        case .archiveGameExists:
            String(localized: "This game is already on the console. Import the archive into that game instead.")
        case .archiveEmpty:
            String(localized: "This game has no data to export.")
        case .archivesNeedNewerPak:
            String(localized: "Update ReminisLink on the console to export and import game archives.")
        case .trashReplaceRefused:
            String(localized: "Only the save of a game still on the console can replace its current save.")
        case .trashEntryGone:
            String(localized: "This entry is no longer in the trash of the console, or ReminisLink on the console is too old to restore it (update it).")
        case .cancelled:
            String(localized: "Cancelled.")
        case .unexpected:
            String(localized: "Something went wrong.")
        case .api(let error):
            Self.message(for: error)
        }
    }

    var action: RecoveryAction? {
        switch self {
        case .localNetworkDenied: .openSettings
        case .consoleNotFound: .retry
        case .api(let error):
            switch error {
            case .server(let server) where server.code == .unknownClient: .repair
            case .server(let server) where server.code == .disconnected: .reconnect
            // The keys of the app and of the pak no longer match.
            case .server(let server) where server.code == .badSignature: .repair
            case .server(let server) where server.code == .pairingFailed: nil
            case .notPaired: .repair
            // M13: the console's certificate changed (SD card reformatted,
            // or another device): pair again, scanning its QR code.
            case .identityChanged: .repair
            case .incompatible(.updatePak): .updatePak
            case .incompatible(.updateApp): .updateApp
            case .pairing: nil
            default: .retry
            }
        case .upload(.outcomeUnknown), .upload(.paused), .upload(.fileChanged), .upload(.cancelNotConfirmed), .file: nil
        case .restoreRefused, .unsupportedGameFormat, .patchMismatch, .unsupportedPatch, .jobLost: nil
        case .alreadyPatched, .romChangedDuringPatch, .patchTooLarge: nil
        case .upload(.tooManyRestarts): .retry
        case .invalidPairingLink, .outdatedPak, .pairingLinkTooNew, .storage, .cancelled, .unreadablePicture: nil
        case .syncChecksumMismatch, .vault, .versionChanged, .gameFileChanged, .gameRemoved, .consoleClosed: nil
        case .invalidArchive, .archiveGameExists, .archiveEmpty, .archivesNeedNewerPak: nil
        case .pakRefused, .pakSelf, .notAPak, .needsNewerPak, .trashEntryGone, .trashReplaceRefused: nil
        case .pairingClosed, .pairingNotSynced, .identityNotArrived, .unexpected: .retry
        case .noPairingCodeInPicture, .pictureTooLarge: nil
        case .identityMissing: .resetIdentity
        }
    }

    var isCancellation: Bool { self == .cancelled }

    /// The pairing cannot be used from this device: not found, identity not
    /// arrived, or refused by the console (M15: opening the console again
    /// reads the shared Keychain again before showing it).
    var isPairingProblem: Bool {
        switch self {
        case .pairingNotSynced, .api(.notPaired): true
        case .api(.server(let server)): server.code == .unknownClient || server.code == .badSignature
        default: false
        }
    }

    /// `401 disconnected` (M10): the console disconnected this iPhone.
    var isDisconnected: Bool {
        if case .api(let error) = self { return error.isDisconnected }
        return false
    }

    /// `429 device_busy` (or an M13 pak's `429 too_many_attempts`
    /// `Device busy`): another write was running on the console; nothing
    /// was changed.
    var isDeviceBusy: Bool {
        if case .api(let error) = self { return error.isDeviceBusy }
        return false
    }

    /// The error of an action on a save or a state: a `409 conflict`
    /// without `details.existing` means the copy could not be restored, a
    /// `422 unsupported_format` a NextUI format the pak does not handle.
    static func gameData(_ error: Error) -> AppError {
        let error = AppError(error)
        guard case .api(.server(let server)) = error else { return error }
        switch server.code {
        case .conflict where server.status == 409 && server.details["existing"] == nil:
            return .restoreRefused
        case .unsupportedFormat:
            return .unsupportedGameFormat
        default:
            return error
        }
    }

    /// The error of the download of a file of a game (M11): a `404` means
    /// the file is no longer in the game's list.
    static func gameFile(_ error: Error) -> AppError {
        let error = AppError(error)
        if case .api(.server(let server)) = error, server.code == .notFound { return .gameFileChanged }
        return error
    }

    /// The error of a pak action (M12): a refused archive gets its reason,
    /// ReminisLink itself its own text; a `404` on the paks list or the SD
    /// browser means an older pak (``needsNewerPak``) only where the
    /// caller says so (`routeMissing`).
    static func pak(_ error: Error, routeMissing: Bool = false) -> AppError {
        let error = AppError(error)
        guard case .api(let api) = error else { return error }
        let server: ServerError? = if case .server(let s) = api { s } else { nil }
        if let server, let refusal = server.pakRefusal { return .pakRefused(refusal) }
        if let server, server.isPakSelfConflict { return .pakSelf }
        if routeMissing {
            let status: Int? = if case .unexpectedStatus(let s) = api { s } else { server?.status }
            if status == 404 || status == 405 { return .needsNewerPak }
        }
        return error
    }

    /// The error of a restore from the trash (M18): a `404` (or an older
    /// pak's `405`) is ``trashEntryGone``, a pak folder refused by the
    /// installation checks gets its reason.
    static func trashRestore(_ error: Error) -> AppError {
        let error = AppError(error)
        guard case .api(let api) = error else { return error }
        let server: ServerError? = if case .server(let s) = api { s } else { nil }
        if server?.trashReplaceRefusal != nil { return .trashReplaceRefused }
        if let server, let refusal = server.pakRefusal { return .pakRefused(refusal) }
        let status: Int? = if case .unexpectedStatus(let s) = api { s } else { server?.status }
        if status == 404 || status == 405 { return .trashEntryGone }
        return error
    }

    private static func message(for refusal: PakRefusal) -> String {
        switch refusal {
        case .notAnArchive:
            String(localized: "This file is not a zip archive the console can read.")
        case .noPak:
            String(localized: "No pak in this archive: it must hold one folder named Name.pak, or a launch.sh file at its top.")
        case .noLaunchScript:
            String(localized: "This pak has no launch.sh file: NextUI could not start it.")
        case .severalPaks:
            String(localized: "This archive holds more than one pak. Install them one at a time.")
        case .unsafeEntry:
            String(localized: "The console refused this archive to protect the SD card (a link, or a file outside the pak folder).")
        case .tooLarge:
            String(localized: "This archive is too large for the console once unpacked.")
        case .unsupportedCompression:
            String(localized: "This archive is encrypted or compressed in a way the console cannot read.")
        case .noName:
            String(localized: "The console could not name this pak. Rename the archive Name.pak.zip and try again.")
        case .other(let reason):
            String(localized: "The console refused this pak: \(reason)")
        }
    }

    /// The error of a save sync, or of one game of it: a checksum mismatch
    /// and the files of the vault get their own texts.
    static func sync(_ error: Error) -> AppError {
        switch error {
        case is SaveSyncError: .syncChecksumMismatch
        case SaveVaultError.versionMismatch: .versionChanged
        case is SaveVaultError, is CocoaError: .vault
        case let error as NSError where error.domain == NSCocoaErrorDomain: .vault
        default: AppError(error)
        }
    }

    /// The error of restoring a synced version: the vault's own errors,
    /// else those of a save replacement.
    static func restore(_ error: Error) -> AppError {
        switch error {
        case is SaveVaultError, is CocoaError: sync(error)
        default: gameData(error)
        }
    }

    /// The error of a patch (`POST /games/{gameId}/patch` or the `error`
    /// of its job): `422 patch_mismatch` and `422 unsupported_format` get
    /// their own texts.
    static func patch(_ error: Error) -> AppError {
        let error = AppError(error)
        guard case .api(.server(let server)) = error else { return error }
        switch server.code {
        case .patchMismatch: return server.patchMismatchReason == "already-patched" ? .alreadyPatched : .patchMismatch
        case .unsupportedFormat: return .unsupportedPatch
        default: return error
        }
    }

    /// The `error` of a failed patch job of the Patch screen.
    static func patchJob(_ server: ServerError) -> AppError {
        switch server.code {
        case .conflict: return .romChangedDuringPatch
        case .tooLarge: return .patchTooLarge
        default: return patch(APIError.server(server))
        }
    }

    /// A save refused because the resource changed on the console
    /// (`412`, e.g. the cheat list).
    var isPreconditionFailed: Bool {
        if case .api(let error) = self { return CheatEditor.isConflict(error) }
        return false
    }

    private static func message(for error: APIError) -> String {
        switch error {
        case .network:
            return String(localized: "The console is not responding.")
        case .deviceMismatch:
            return String(localized: "Another console answered at this address.")
        case .incompatible(.updatePak):
            return String(localized: "ReminisLink on the console is too old for this app. Update the ReminisLink pak on the console, then open it again.")
        case .incompatible(.updateApp):
            return String(localized: "ReminisLink on the console needs a newer version of this app. Update the ReminisLink app.")
        case .notPaired:
            return String(localized: "This console no longer recognizes your iPhone.")
        case .identityChanged:
            return String(localized: "This console's identity changed: it is not the console paired with this app, or it was reset. Nothing was sent. Pair it again by scanning its QR code.")
        case .pairing:
            return String(localized: "The console could not prove its identity. Pairing was cancelled.")
        case .signing, .invalidResponse, .unexpectedStatus:
            return String(localized: "The console sent an unexpected answer.")
        case .server(let server):
            switch server.code {
            case .unknownClient:
                return String(localized: "This console no longer recognizes your iPhone.")
            case .disconnected:
                return String(localized: "Disconnected by the console.")
            case .clockSkew:
                return String(localized: "The clocks of the console and of this iPhone are too far apart.")
            case .pairingFailed:
                return String(localized: "Pairing failed. The code may have expired: show a new QR code on the console and try again.")
            case _ where server.isDeviceBusy: // device_busy, or an M13 pak's too_many_attempts "Device busy"
                if let seconds = server.retryAfter {
                    return String(localized: "The console is busy with another change. Nothing was changed. Try again in \(seconds) seconds.")
                }
                return String(localized: "The console is busy with another change. Nothing was changed. Try again in a moment.")
            case .tooManyAttempts:
                if let seconds = server.retryAfter {
                    return String(localized: "Too many attempts. Try again in \(seconds) seconds.")
                }
                return String(localized: "Too many attempts. Try again in a moment.")
            case .insufficientStorage:
                if let missing = server.missingBytes {
                    let size = missing.formatted(.byteCount(style: .file))
                    return String(localized: "Not enough space on the SD card: \(size) missing.")
                }
                return String(localized: "Not enough space on the SD card.")
            case .notFound:
                return String(localized: "This item no longer exists on the console.")
            case .shuttingDown:
                return String(localized: "ReminisLink is closing on the console.")
            case .tooLarge:
                return String(localized: "The request is too large for the console.")
            case .conflict where server.status == 412:
                return String(localized: "The list changed on the console. Reload it.")
            case .conflict:
                if case .string(let existing)? = server.details["existing"] {
                    return String(localized: "A file with this name already exists on the console: \(existing)")
                }
                return String(localized: "A file with this name already exists on the console.")
            case .checksumMismatch:
                return String(localized: "The file arrived damaged on the console and was discarded. Send it again.")
            case .invalidName:
                return String(localized: "The console refused this file name.")
            case .invalidRequest:
                return String(localized: "The console refused the request.")
            case .stateLocked:
                return String(localized: "This state is locked on the console. Unlock it first.")
            case .unsupportedFormat:
                return String(localized: "The console found nothing it can use in this file.")
            case .internal:
                return String(localized: "The console ran into an error. Details are in its log.")
            default:
                return String(localized: "The console reported an error (\(server.code.rawValue)).")
            }
        }
    }
}
