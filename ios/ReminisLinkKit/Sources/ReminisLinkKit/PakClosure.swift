import Foundation

/// When the app takes a failed request for "the console closed
/// ReminisLink" (milestone M11) and goes back to the list of consoles.
///
/// A Wi-Fi glitch alone never does: it takes `503 shutting_down` (the pak
/// refuses requests while it quits), or a connection failure while the
/// Bonjour service of the console is gone (the pak withdraws its
/// announcement as it quits; docs/specs/05-console-pak.md §1).
public enum PakClosure {
    /// How long the browser must have been running before the absence of
    /// a service means anything: right after a start (back from the
    /// background), the results are not in yet.
    public static let minimumBrowsing: Duration = .seconds(3)
    /// How long the `/hello` probe may take (sent once, never retried).
    public static let probeTimeout: TimeInterval = 3

    /// Whether `error`, answered to a request of a live connection, means
    /// the pak closed.
    ///
    /// - `serviceVisible`: the console is among the services Bonjour sees.
    /// - `browsingFor`: how long the browser has been running (nil when it
    ///   is stopped, or the local network is denied: nothing is known).
    public static func isClosed(
        _ error: APIError, serviceVisible: Bool, browsingFor: Duration?,
        minimumBrowsing: Duration = PakClosure.minimumBrowsing
    ) -> Bool {
        if error.code == .shuttingDown { return true }
        guard case .network(let code) = error, code != .cancelled else { return false }
        guard let browsingFor, browsingFor >= minimumBrowsing else { return false }
        return !serviceVisible
    }

    /// Whether the failure of the `/hello` probe, sent when the console's
    /// Bonjour service disappeared (the pak sends a goodbye as it quits),
    /// means the pak closed: `503 shutting_down`, or no connection (refused,
    /// lost, timed out). Any answer, even an error, means it is still there.
    public static func probeMeansClosed(_ error: Error) -> Bool {
        guard let error = error as? APIError else { return false }
        if error.code == .shuttingDown { return true }
        guard case .network(let code) = error else { return false }
        return code != .cancelled
    }
}
