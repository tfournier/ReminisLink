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
    case pairingClosed
    case storage
    /// An upload stopped on the app side.
    case upload(UploadFailure)
    /// A file chosen or shared could not be read or copied.
    case file
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
            String(localized: "Pairing is not open on the console. Open ReminisLink on the console and show the QR code.")
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
        case .upload(.tooManyRestarts): .retry
        case .invalidPairingLink, .storage, .cancelled: nil
        case .pairingClosed, .unexpected: .retry
        }
    }

    var isCancellation: Bool { self == .cancelled }

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
                return String(localized: "This state is locked on the console.")
            case .internal:
                return String(localized: "The console ran into an error. Details are in its log.")
            default:
                return String(localized: "The console reported an error (\(server.code.rawValue)).")
            }
        }
    }
}
