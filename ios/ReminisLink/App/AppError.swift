import Foundation
import ReminisLinkKit

/// What the user can do about an error (docs/specs/06-app-ios.md §5).
enum RecoveryAction: Equatable {
    case openSettings
    case retry
    case repair
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
            // The keys of the app and of the pak no longer match.
            case .server(let server) where server.code == .badSignature: .repair
            case .server(let server) where server.code == .pairingFailed: nil
            case .notPaired: .repair
            case .incompatible, .pairing: nil
            default: .retry
            }
        case .upload(.outcomeUnknown), .upload(.paused), .upload(.fileChanged), .file: nil
        case .restoreRefused, .unsupportedGameFormat, .patchMismatch, .unsupportedPatch, .jobLost: nil
        case .patchDownloadFailed, .alreadyPatched, .romChangedDuringPatch, .patchTooLarge: nil
        case .upload(.tooManyRestarts): .retry
        case .invalidPairingLink, .storage, .cancelled: nil
        case .pairingClosed, .unexpected: .retry
        }
    }

    var isCancellation: Bool { self == .cancelled }

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
