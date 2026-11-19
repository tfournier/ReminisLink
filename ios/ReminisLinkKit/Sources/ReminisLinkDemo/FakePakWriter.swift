// Debug builds only: the fake pak never ships in a release build.
#if DEBUG
import CryptoKit
import Foundation
import ReminisLinkKit

// Milestone M10 of the fake pak: the write lease (one writer at a time),
// `GET`/`PUT`/`DELETE /writer`, `423 read_only` on the routes that change
// the SD card, and the disconnection of a client from the console
// (`401 disconnected` until `POST /clients/me/connect`). As the contract
// says, all of it is kept in memory only.

extension FakePak {
    /// How long the lease lasts after the last request of its holder.
    public static let leaseDuration: TimeInterval = 60

    /// The clock of the lease (tests move it to reach the expiry).
    public var leaseClock: @Sendable () -> Date {
        get { lock.withLock { _leaseClock } }
        set { lock.withLock { _leaseClock = newValue } }
    }

    /// The client holding the lease now, if any.
    public var writerClient: String? {
        lock.withLock { currentWriterLocked() }
    }

    /// As if another paired client (`name`) held the lease: it is
    /// registered, takes the lease and keeps it while it is not taken over
    /// (it renews it, like an app in the foreground). For UI tests and
    /// previews driving a second client without a second app.
    @discardableResult
    public func simulateOtherWriter(name: String, clientId: String = "00000000-0000-4000-8000-0000000000aa") -> String {
        lock.withLock {
            clients[clientId] = clients[clientId] ?? .init(size: .bits256)
            clientNames[clientId] = name
            writerClientId = clientId
            writerExpiry = .distantFuture
        }
        return clientId
    }

    /// Another client takes the lease (`PUT /writer` with `force`).
    public func takeOver(by clientId: String) {
        lock.withLock {
            writerClientId = clientId
            writerExpiry = _leaseClock().addingTimeInterval(Self.leaseDuration)
        }
    }

    /// Settings > Devices > Disconnect on the console: the lease of the
    /// client is dropped and its signed requests get `401 disconnected`
    /// until it calls `POST /clients/me/connect`.
    public func disconnect(clientId: String) {
        lock.withLock {
            disconnectedClients.insert(clientId)
            if writerClientId == clientId { writerClientId = nil }
        }
    }

    /// Settings > Devices > Forget on the console: the pairing is removed
    /// (`401 unknown_client` afterwards) and the lease dropped.
    public func forget(clientId: String) {
        lock.withLock {
            clients[clientId] = nil
            clientNames[clientId] = nil
            lastSeen[clientId] = nil
            disconnectedClients.remove(clientId)
            if writerClientId == clientId { writerClientId = nil }
        }
    }

    public func isDisconnected(clientId: String) -> Bool {
        lock.withLock { disconnectedClients.contains(clientId) }
    }

    /// `writerHeld` of `/hello`.
    var writerHeld: Bool { lock.withLock { currentWriterLocked() != nil } }

    /// The holder, once an expired lease is dropped. Call with the lock held.
    func currentWriterLocked() -> String? {
        guard let id = writerClientId else { return nil }
        if _leaseClock() >= writerExpiry || clients[id] == nil {
            writerClientId = nil
            return nil
        }
        return id
    }

    /// `writer` of `GET /device` and `/writer`, for `clientId`.
    func writerJSON(for clientId: String) -> Any {
        lock.withLock { writerJSONLocked(for: clientId) }
    }

    private func writerJSONLocked(for clientId: String) -> Any {
        guard let id = currentWriterLocked() else { return NSNull() }
        return [
            "clientName": clientNames[id] ?? "",
            "you": id == clientId,
            "expiresAt": ProtocolJSON.formatDate(writerExpiry == .distantFuture
                ? _leaseClock().addingTimeInterval(Self.leaseDuration) : writerExpiry),
        ] as [String: Any]
    }

    /// The checks of the contract after a valid signature: a disconnected
    /// client gets `401 disconnected` (but for `POST /clients/me/connect`);
    /// a request of the holder renews the lease; a request that changes
    /// the SD card takes the lease if it is free, else `423 read_only`.
    func checkLease(clientId: String, method: String, segments: [String]) -> FakeResponse? {
        lock.withLock { () -> FakeResponse? in
            let isConnect = method == "POST" && segments == ["clients", "me", "connect"]
            if disconnectedClients.contains(clientId) && !isConnect {
                return .error(401, code: "disconnected", message: "Disconnected by the console")
            }
            let now = _leaseClock()
            let holder = currentWriterLocked()
            if holder == clientId && writerExpiry != .distantFuture {
                writerExpiry = now.addingTimeInterval(Self.leaseDuration)
            }
            guard Self.needsLease(method: method, segments: segments) else { return nil }
            if let holder, holder != clientId {
                return .error(423, code: "read_only", message: "Another device has write access",
                              details: ["writer": clientNames[holder] ?? ""])
            }
            writerClientId = clientId
            writerExpiry = now.addingTimeInterval(Self.leaseDuration)
            return nil
        }
    }

    /// Every signed route that changes the SD card (each `423` of the
    /// contract): all but `GET`, `/writer`, `POST /clients/me/connect` and
    /// `DELETE /clients/me`.
    static func needsLease(method: String, segments: [String]) -> Bool {
        if method == "GET" || method == "HEAD" { return false }
        if segments == ["writer"] { return false }
        if segments.first == "clients" { return false }
        return true
    }

    /// `GET`, `PUT`, `DELETE /writer` and `POST /clients/me/connect`.
    func routeWriter(clientId: String, method: String, segments: [String], body: Data) -> FakeResponse {
        switch (method, segments) {
        case ("POST", ["clients", "me", "connect"]):
            lock.withLock { _ = disconnectedClients.remove(clientId) }
            return FakeResponse(status: 204)
        case ("GET", ["writer"]):
            return .json(200, ["writer": writerJSON(for: clientId)])
        case ("PUT", ["writer"]):
            var force = false
            if !body.isEmpty {
                guard let object = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any] else {
                    return .error(400, code: "invalid_request", message: "Invalid body")
                }
                if let value = object["force"] {
                    guard let flag = value as? Bool, CFGetTypeID(value as CFTypeRef) == CFBooleanGetTypeID() else {
                        return .error(400, code: "invalid_request", message: "force must be a boolean")
                    }
                    force = flag
                }
            }
            return lock.withLock { () -> FakeResponse in
                if let holder = currentWriterLocked(), holder != clientId, !force {
                    return .error(423, code: "read_only", message: "Another device has write access",
                                  details: ["writer": clientNames[holder] ?? ""])
                }
                writerClientId = clientId
                writerExpiry = _leaseClock().addingTimeInterval(Self.leaseDuration)
                return .json(200, ["writer": writerJSONLocked(for: clientId)])
            }
        case ("DELETE", ["writer"]):
            lock.withLock {
                if currentWriterLocked() == clientId { writerClientId = nil }
            }
            return FakeResponse(status: 204)
        default:
            return .error(404, code: "not_found", message: "No route")
        }
    }
}
#endif
