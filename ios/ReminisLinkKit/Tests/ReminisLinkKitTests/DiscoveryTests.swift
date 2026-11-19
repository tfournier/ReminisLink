import Foundation
import Network
import Testing
@testable import ReminisLinkKit

@Suite("Discovery")
struct DiscoveryTests {
    let id = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"
    let txt = ["v": "1", "id": "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10", "api": "1", "model": "brick",
               "platform": "tg5040", "os": "nextui", "osv": "NextUI-20261013-039a06de", "cv": "1.0.0"]

    func device(_ id: String, _ name: String) -> DiscoveredDevice {
        DiscoveredDevice(id: id, name: name, model: .brick, service: BonjourService(name: name))
    }

    @Test func readsTheTXTRecord() throws {
        let d = try #require(DiscoveryLogic.device(service: BonjourService(name: "Brick de Thomas"), txt: txt))
        #expect(d.id == id)
        #expect(d.name == "Brick de Thomas")
        #expect(d.model == .brick)
        #expect(d.platform == "tg5040")
        #expect(d.osVersion == "NextUI-20261013-039a06de")
        #expect(d.pakVersion == "1.0.0")
        #expect(d.apiVersion == 1)
        #expect(d.isCompatible)
    }

    @Test func toleratesMissingAndUnknownKeys() throws {
        let d = try #require(DiscoveryLogic.device(service: BonjourService(name: "x"), txt: ["id": id, "future": "1", "api": "2"]))
        #expect(d.model.rawValue == "")
        #expect(d.osVersion == nil)
        #expect(!d.isCompatible)
    }

    @Test func refusesServicesWithoutAUsableId() {
        for bad in [[:], ["id": ""], ["id": id.uppercased()], ["id": "brick"]] {
            #expect(DiscoveryLogic.device(service: BonjourService(name: "x"), txt: bad) == nil)
        }
    }

    @Test func mergesByIdAndSortsByName() {
        let other = "00000000-0000-4000-8000-000000000001"
        let merged = DiscoveryLogic.merge([device(id, "Brick"), device(other, "Alpha"), device(id, "Brick (2)")])
        #expect(merged.map(\.id) == [other, id])
        #expect(merged.last?.name == "Brick")
    }

    @Test func findsByIdNeverByName() {
        let devices = [device(id, "Brick")]
        #expect(DiscoveryLogic.find(id: id, in: devices)?.name == "Brick")
        #expect(DiscoveryLogic.find(id: "Brick", in: devices) == nil)
        #expect(DiscoveryLogic.unpaired(devices, pairedIds: [id]).isEmpty)
        #expect(DiscoveryLogic.unpaired(devices, pairedIds: []).count == 1)
    }

    @Test func policyDeniedIsNotNothingFound() {
        #expect(DiscoveryLogic.status(for: .waiting(.dns(DNSServiceErrorType(-65570)))) == .localNetworkDenied)
        #expect(DiscoveryLogic.status(for: .failed(.dns(DNSServiceErrorType(-65570)))) == .localNetworkDenied)
        #expect(DiscoveryLogic.status(for: .waiting(.dns(DNSServiceErrorType(-65537)))) == .searching)
        #expect(DiscoveryLogic.status(for: .ready) == .searching)
        if case .failed = DiscoveryLogic.status(for: .failed(.posix(.ENETDOWN))) {} else { Issue.record("expected failed") }
    }

    @MainActor
    @Test func waitForDeviceWithFixedDevices() async {
        let discovery = Discovery(fixedDevices: [device(id, "Brick")])
        #expect(await discovery.waitForDevice(id: id, timeout: .seconds(1))?.name == "Brick")
        let start = ContinuousClock.now
        #expect(await discovery.waitForDevice(id: "00000000-0000-4000-8000-000000000001", timeout: .milliseconds(300)) == nil)
        #expect(ContinuousClock.now - start >= .milliseconds(300))
    }

    @Test func resolvedAddresses() {
        #expect(EndpointResolver.endpoint(from: .hostPort(host: .ipv4(IPv4Address("192.168.1.42")!), port: 47821))
            == Endpoint(host: "192.168.1.42", port: 47821))
        #expect(EndpointResolver.endpoint(from: .hostPort(host: .name("brick.local", nil), port: 47821))
            == Endpoint(host: "brick.local", port: 47821))
    }

    @Test func fixedEndpointSkipsResolution() async throws {
        let d = DiscoveredDevice(id: id, name: "x", model: .brick, service: BonjourService(name: "x"),
                                 fixedEndpoint: Endpoint(host: "demo.invalid", port: 1))
        #expect(try await EndpointResolver.resolve(d) == Endpoint(host: "demo.invalid", port: 1))
    }
}
