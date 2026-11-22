import Foundation

/// `hello`: the first event of a stream (milestone M14).
public struct EventHello: Codable, Sendable, Equatable {
    public let serverTime: Date
    /// The longest gap between two keep-alives (15).
    public let keepAliveSeconds: Int

    public init(serverTime: Date, keepAliveSeconds: Int) {
        self.serverTime = serverTime
        self.keepAliveSeconds = keepAliveSeconds
    }
}

/// `shutdown`: the pak starts closing; the stream ends right after.
public struct EventShutdown: Codable, Sendable, Equatable {
    /// `quit` (left from the console) or `stopped` (power off, signal);
    /// another value from a newer pak is kept as it is.
    public let reason: String

    public init(reason: String) { self.reason = reason }
}

/// `device`: the battery, the connected clients or the free space changed.
public struct EventDevice: Codable, Sendable, Equatable {
    public let battery: DeviceStatus.Battery?
    public let connectedClients: [ConnectedClient]
    public let storage: [DeviceStatus.Storage]

    public init(battery: DeviceStatus.Battery?, connectedClients: [ConnectedClient], storage: [DeviceStatus.Storage]) {
        self.battery = battery
        self.connectedClients = connectedClients
        self.storage = storage
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        battery = try c.decodeIfPresent(DeviceStatus.Battery.self, forKey: .battery)
        connectedClients = try c.decode([ConnectedClient].self, forKey: .connectedClients)
        storage = try c.decode([DeviceStatus.Storage].self, forKey: .storage)
    }

    private enum CodingKeys: String, CodingKey {
        case battery, connectedClients, storage
    }
}

/// `library`: the SD card changed through the API (any client).
public struct EventLibrary: Codable, Sendable, Equatable {
    /// The systems whose games changed; empty when unknown (everything).
    public let systemIds: [String]

    public init(systemIds: [String]) { self.systemIds = systemIds }

    /// The systems to refresh, nil for all of them.
    public var affectedSystems: Set<String>? {
        systemIds.isEmpty ? nil : Set(systemIds)
    }
}

/// A typed event of `GET /events` (protocol/openapi.yaml).
public enum PakEvent: Sendable, Equatable {
    case hello(EventHello)
    case shutdown(EventShutdown)
    case device(EventDevice)
    case library(EventLibrary)
    case job(Job)
    /// M12: the installed paks changed (installed or deleted by any
    /// client); reload `GET /paks`.
    case paks(EventPaks)
    /// M22: the screenshots changed (moved to the trash or restored by any
    /// client); reload `GET /screenshots`.
    case screenshots(EventScreenshots)
    /// A type this app does not know (a newer pak): ignored.
    case unknown(String)

    /// The event, or nil when a known type carries unreadable data (the
    /// event is then ignored, as an unknown one).
    public init?(_ event: SSEEvent) {
        let data = Data(event.data.utf8)
        let decoder = ProtocolJSON.decoder()
        do {
            switch event.type {
            case "hello": self = .hello(try decoder.decode(EventHello.self, from: data))
            case "shutdown": self = .shutdown(try decoder.decode(EventShutdown.self, from: data))
            case "device": self = .device(try decoder.decode(EventDevice.self, from: data))
            case "library": self = .library(try decoder.decode(EventLibrary.self, from: data))
            case "job": self = .job(try decoder.decode(Job.self, from: data))
            case "paks": self = .paks(try decoder.decode(EventPaks.self, from: data))
            case "screenshots": self = .screenshots(try decoder.decode(EventScreenshots.self, from: data))
            default: self = .unknown(event.type)
            }
        } catch {
            return nil
        }
    }
}

extension DeviceStatus {
    /// This status with the fields of a `device` event.
    public func updated(with event: EventDevice) -> DeviceStatus {
        DeviceStatus(
            deviceId: deviceId, deviceName: deviceName, model: model, platform: platform, os: os,
            osVersion: osVersion, pakVersion: pakVersion, network: network, battery: event.battery,
            storage: event.storage, uptimeSeconds: uptimeSeconds, connectedClients: event.connectedClients
        )
    }
}
