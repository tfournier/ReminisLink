import Foundation
import Testing
@testable import ReminisLinkKit

@MainActor
@Suite("Device locator")
struct LocatorTests {
    let id = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"
    var device: DiscoveredDevice { DiscoveredDevice(id: id, name: "Brick", model: .brick, service: BonjourService(name: "Brick")) }

    func locate(found: DiscoveredDevice?, resolveFails: Bool = false, denied: Bool = false,
                waited: Box<Duration?> = Box(nil)) async throws -> Endpoint {
        try await DeviceLocator.locate(
            timeout: .seconds(8),
            find: { waited.value = $0; return found },
            resolve: { _ in
                if resolveFails { throw ResolveError.timeout }
                return Endpoint(host: "10.0.0.7", port: 1)
            },
            isLocalNetworkDenied: { denied }
        )
    }

    @Test func bonjourWithTheFullTimeout() async throws {
        let waited = Box<Duration?>(nil)
        #expect(try await locate(found: device, waited: waited) == Endpoint(host: "10.0.0.7", port: 1))
        #expect(waited.value == .seconds(8))
    }

    @Test func notFoundWithoutBonjour() async throws {
        let waited = Box<Duration?>(nil)
        await #expect(throws: DeviceLookupError.notFound) { try await locate(found: nil, waited: waited) }
        #expect(waited.value == .seconds(8))
        await #expect(throws: DeviceLookupError.notFound) { try await locate(found: device, resolveFails: true) }
        await #expect(throws: DeviceLookupError.localNetworkDenied) { try await locate(found: nil, denied: true) }
    }

    @Test func cancelledLookupThrowsCancellation() async throws {
        let discovery = Discovery(fixedDevices: [])
        let task = Task { @MainActor in
            try await DeviceLocator.locate(
                timeout: .seconds(8),
                find: { await discovery.waitForDevice(id: "00000000-0000-4000-8000-000000000001", timeout: $0) },
                resolve: { _ in Endpoint(host: "x", port: 1) },
                isLocalNetworkDenied: { false }
            )
        }
        try await Task.sleep(for: .milliseconds(100))
        task.cancel()
        await #expect(throws: CancellationError.self) { try await task.value }
    }

    @Test func restartDelayGrows() {
        #expect(DiscoveryLogic.restartDelay(afterFailures: 0) == .seconds(1))
        #expect(DiscoveryLogic.restartDelay(afterFailures: 1) == .seconds(2))
        #expect(DiscoveryLogic.restartDelay(afterFailures: 4) == .seconds(16))
        #expect(DiscoveryLogic.restartDelay(afterFailures: 5) == .seconds(30))
        #expect(DiscoveryLogic.restartDelay(afterFailures: 50) == .seconds(30))
    }
}

final class Box<T>: @unchecked Sendable {
    var value: T
    init(_ value: T) { self.value = value }
}
