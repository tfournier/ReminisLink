import CryptoKit
import Foundation
import ReminisLinkKit

// The paired clients of the fake pak (M10, M13): the disconnection of a
// client from the console (`401 disconnected` until
// `POST /clients/me/connect`), Forget, and the writes run one at a time
// (M13: a write that waited too long for the one in progress gets
// `429 device_busy` with `Retry-After`;
// simulated here with ``busyNextWrites(_:retryAfter:)``). As the contract
// says, the disconnections are kept in memory only.

extension FakePak {
    /// Settings > Devices > Disconnect on the console: the signed requests
    /// of the client get `401 disconnected` until it calls
    /// `POST /clients/me/connect`.
    public func disconnect(clientId: String) {
        lock.withLock { _ = disconnectedClients.insert(clientId) }
        // M14: its event streams end, without an event.
        endEventStreams(clientId: clientId)
    }

    /// Settings > Devices > Forget on the console: the pairing is removed
    /// (`401 unknown_client` afterwards).
    public func forget(clientId: String) {
        lock.withLock {
            clients[clientId] = nil
            clientNames[clientId] = nil
            clientPlatforms[clientId] = nil
            lastSeen[clientId] = nil
            disconnectedClients.remove(clientId)
        }
        endEventStreams(clientId: clientId)
    }

    public func isDisconnected(clientId: String) -> Bool {
        lock.withLock { disconnectedClients.contains(clientId) }
    }

    /// The `platform` given at pairing and the last `X-RL-Platform` seen
    /// for a client (informative).
    public func platforms(clientId: String) -> (paired: String?, lastSeen: String?) {
        lock.withLock { (clientPlatforms[clientId], lastRequestPlatform[clientId]) }
    }

    #if DEBUG
    /// The next `count` writes (requests that change the SD card) find
    /// another write running for too long: `429 device_busy` with
    /// `Retry-After: retryAfter`, nothing changed.
    public func busyNextWrites(_ count: Int, retryAfter: Int = 1) {
        lock.withLock {
            busyWrites += count
            busyRetryAfter = retryAfter
        }
    }
    #endif

    /// The writes refused as busy so far.
    public var busyRefusals: Int { lock.withLock { _busyRefusals } }

    /// The checks of the contract after a valid signature: a disconnected
    /// client gets `401 disconnected` (but for `POST /clients/me/connect`).
    func checkDisconnected(clientId: String, method: String, segments: [String]) -> FakeResponse? {
        lock.withLock { () -> FakeResponse? in
            let isConnect = method == "POST" && segments == ["clients", "me", "connect"]
            guard disconnectedClients.contains(clientId), !isConnect else { return nil }
            return .error(401, code: "disconnected", message: "Disconnected by the console")
        }
    }

    /// A write waiting for its turn (after the body was verified): busy
    /// while ``busyNextWrites(_:retryAfter:)`` says so.
    func checkBusy(method: String, segments: [String]) -> FakeResponse? {
        guard Self.waitsForWriteTurn(method: method, segments: segments) else { return nil }
        return lock.withLock { () -> FakeResponse? in
            guard busyWrites > 0 else { return nil }
            busyWrites -= 1
            _busyRefusals += 1
            return .error(429, code: "device_busy", message: "Device busy",
                          headers: ["Retry-After": String(busyRetryAfter)])
        }
    }

    /// The signed routes that wait for their turn to write (M13, "Writes
    /// one at a time"): all but `GET`, `/clients/...`, creating, sending
    /// and cancelling an upload, the jobs (created or cancelled).
    static func waitsForWriteTurn(method: String, segments: [String]) -> Bool {
        if method == "GET" || method == "HEAD" { return false }
        if segments.first == "clients" { return false }
        if segments.first == "uploads" { return segments.count == 3 && segments[2] == "complete" }
        if segments.first == "jobs" { return false }
        // A job (patch) waits only for the step that installs its file, and
        // never fails for it.
        // M12: a pak installation is a job too.
        if segments == ["paks", "install"] { return false }
        if segments.count == 3 && segments[0] == "games" && segments[2] == "patch" { return false }
        return true
    }

    /// `POST /clients/me/connect`.
    func connectClient(clientId: String) -> FakeResponse {
        lock.withLock { _ = disconnectedClients.remove(clientId) }
        return FakeResponse(status: 204)
    }
}
