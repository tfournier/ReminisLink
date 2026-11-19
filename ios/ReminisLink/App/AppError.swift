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
}

/// Every error the screens show, with its translated message.
enum AppError: Error, Equatable {
    case api(APIError)
    /// No Bonjour result with the right id after 8 s.
    case consoleNotFound
    /// `NWBrowser` reports `kDNSServiceErr_PolicyDenied`.
    case localNetworkDenied
    case invalidPairingLink
    /// `pairingOpen: false` (or `401 pairing_failed` with pairing closed):
    /// the pairing screen is not shown on the console (M8, on demand).
    case pairingClosed
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
    /// The console could not download the patch of a link.
    case patchDownloadFailed
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
    /// The task was cancelled (the screen went away): never shown.
    case cancelled
    case unexpected

    init(_ error: Error) {
        switch error {
        case let error as AppError: self = error
        case is CancellationError: self = .cancelled
        case let error as APIError: self = .api(error)
        case is ResolveError: self = .consoleNotFound
        case DeviceLookupError.localNetworkDenied: self = .localNetworkDenied
        case DeviceLookupError.notFound: self = .consoleNotFound
        case is KeychainError, is DeviceStoreError: self = .storage
        case let error as PairingError: self = .api(.pairing(error))
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
        case .pairingClosed:
            String(localized: "On the console, open Settings > Pair a New Device, then scan the QR code shown.")
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
        case .patchDownloadFailed:
            String(localized: "The console could not download the patch. Check the link and the Wi-Fi connection of the console.")
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
            // "Take Over" is offered by the read-only banner.
            case .server(let server) where server.code == .readOnly: nil
            // The keys of the app and of the pak no longer match.
            case .server(let server) where server.code == .badSignature: .repair
            case .server(let server) where server.code == .pairingFailed: nil
            case .notPaired: .repair
            case .incompatible, .pairing: nil
            default: .retry
            }
        case .upload(.outcomeUnknown), .upload(.paused), .upload(.fileChanged), .upload(.cancelNotConfirmed), .file: nil
        case .restoreRefused, .unsupportedGameFormat, .patchMismatch, .unsupportedPatch, .jobLost: nil
        case .patchDownloadFailed, .alreadyPatched, .romChangedDuringPatch, .patchTooLarge: nil
        case .upload(.tooManyRestarts): .retry
        case .invalidPairingLink, .storage, .cancelled, .unreadablePicture: nil
        case .syncChecksumMismatch, .vault, .versionChanged, .gameFileChanged, .gameRemoved, .consoleClosed: nil
        case .pairingClosed, .unexpected: .retry
        }
    }

    var isCancellation: Bool { self == .cancelled }

    /// `401 disconnected` (M10): the console disconnected this iPhone.
    var isDisconnected: Bool {
        if case .api(let error) = self { return error.isDisconnected }
        return false
    }

    /// `423 read_only` (M10): another device has write access.
    var isReadOnly: Bool {
        if case .api(let error) = self { return error.isReadOnly }
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

    /// The `error` of a failed job of the Patch screen: the patch job as
    /// above; the download of the patch to the staging area says the
    /// console could not download it, unless the SD card is full.
    static func patchJob(_ server: ServerError, kind: JobKind) -> AppError {
        if kind == .patch {
            switch server.code {
            case .conflict: return .romChangedDuringPatch
            case .tooLarge: return .patchTooLarge
            default: return patch(APIError.server(server))
            }
        }
        if server.code == .insufficientStorage { return .api(.server(server)) }
        return .patchDownloadFailed
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
        case .incompatible:
            return String(localized: "This version of the app is not compatible with the ReminisLink version on the console. Update both.")
        case .notPaired:
            return String(localized: "This console no longer recognizes your iPhone.")
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
            case .readOnly:
                if let writer = server.writerName {
                    return String(localized: "Read-only — \(writer) has write access. Nothing was changed. Take over to make changes from this iPhone.")
                }
                return String(localized: "Read-only — another device has write access. Nothing was changed. Take over to make changes from this iPhone.")
            case .clockSkew:
                return String(localized: "The clocks of the console and of this iPhone are too far apart.")
            case .pairingFailed:
                return String(localized: "Pairing failed. The code may have expired: show a new QR code on the console and try again.")
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
