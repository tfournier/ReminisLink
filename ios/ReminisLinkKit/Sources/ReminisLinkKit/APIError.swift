import Foundation

/// The `code` of an error response (docs/specs/03-protocole.md §6).
public enum ErrorCode: Hashable, Sendable {
    case invalidRequest
    case invalidId
    case invalidName
    case unknownClient
    case badSignature
    case clockSkew
    case replayedRequest
    case pairingFailed
    case notFound
    case conflict
    case checksumMismatch
    case stateLocked
    case tooLarge
    case unsupportedFormat
    case patchMismatch
    case tooManyAttempts
    case `internal`
    case shuttingDown
    case insufficientStorage
    /// M10: the console disconnected this client (`401`), until
    /// `POST /clients/me/connect`.
    case disconnected
    /// A code this version of the app does not know.
    case other(String)

    private static let table: [(ErrorCode, String)] = [
        (.invalidRequest, "invalid_request"),
        (.invalidId, "invalid_id"),
        (.invalidName, "invalid_name"),
        (.unknownClient, "unknown_client"),
        (.badSignature, "bad_signature"),
        (.clockSkew, "clock_skew"),
        (.replayedRequest, "replayed_request"),
        (.pairingFailed, "pairing_failed"),
        (.notFound, "not_found"),
        (.conflict, "conflict"),
        (.checksumMismatch, "checksum_mismatch"),
        (.stateLocked, "state_locked"),
        (.tooLarge, "too_large"),
        (.unsupportedFormat, "unsupported_format"),
        (.patchMismatch, "patch_mismatch"),
        (.tooManyAttempts, "too_many_attempts"),
        (.internal, "internal"),
        (.shuttingDown, "shutting_down"),
        (.insufficientStorage, "insufficient_storage"),
        (.disconnected, "disconnected"),
    ]

    public init(rawValue: String) {
        self = Self.table.first { $0.1 == rawValue }?.0 ?? .other(rawValue)
    }

    public var rawValue: String {
        if case .other(let raw) = self { return raw }
        return Self.table.first { $0.0 == self }!.1
    }

    /// Every code of the contract.
    public static var allKnown: [ErrorCode] { table.map(\.0) }
}

/// An error response of the pak.
public struct ServerError: Error, Sendable, Equatable {
    public let status: Int
    public let code: ErrorCode
    /// English, for logs only: never shown to the user.
    public let message: String
    public let details: [String: JSONValue]
    /// `Retry-After`, in seconds, when present (`429`).
    public let retryAfter: Int?

    public init(status: Int, code: ErrorCode, message: String, details: [String: JSONValue] = [:], retryAfter: Int? = nil) {
        self.status = status
        self.code = code
        self.message = message
        self.details = details
        self.retryAfter = retryAfter
    }

    /// For `507 insufficient_storage`: `details.required - details.available`.
    public var missingBytes: Int64? {
        guard let required = details["required"]?.int64Value, let available = details["available"]?.int64Value else {
            return nil
        }
        return max(0, required - available)
    }

    /// M13: a write that waited 10 s for the write in progress on the
    /// console (`429 too_many_attempts`, message `Device busy`, with
    /// `Retry-After`). Nothing was changed: the same request can be sent
    /// again. The contract tells it from the other `429`s by its message.
    public var isDeviceBusy: Bool {
        status == 429 && code == .tooManyAttempts && message == "Device busy"
    }
}

/// Everything ``APIClient`` can throw.
public enum APIError: Error, Sendable, Equatable {
    /// The pak answered with an error body.
    case server(ServerError)
    /// An error status without a readable error body, or an unexpected status.
    case unexpectedStatus(Int)
    /// The response body does not match the contract.
    case invalidResponse(String)
    /// The request did not reach the pak, or the answer did not come back.
    case network(URLError.Code)
    /// `/hello` answered for another device than the expected one.
    case deviceMismatch(expected: String, actual: String)
    /// The pak speaks another major API version, or needs a newer app.
    case incompatible(apiVersion: Int, minClientVersion: String)
    /// A signed route was called without credentials.
    case notPaired
    /// The request could not be signed.
    case signing(SignerError)
    /// The pairing exchange failed on the app side (bad device proof...).
    case pairing(PairingError)
    /// M13: the console presented another certificate than the one pinned
    /// at pairing (or the pin is unknown). Nothing was sent: pair again.
    case identityChanged

    /// The server error code, if this is a server error.
    public var code: ErrorCode? {
        if case .server(let e) = self { return e.code }
        return nil
    }

    /// `429 Device busy` (M13): another write was running on the console.
    public var isDeviceBusy: Bool {
        if case .server(let e) = self { return e.isDeviceBusy }
        return false
    }

    /// `401 disconnected` (M10): the console disconnected this client.
    public var isDisconnected: Bool { code == .disconnected }
}
