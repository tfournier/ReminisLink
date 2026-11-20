import Foundation
import Testing
@testable import ReminisLinkKit

/// Milestone M14: the Server-Sent Events parser and the typed events.
@Suite("Event stream parser")
struct EventParserTests {
    func parse(_ text: String) -> [SSEItem] {
        var parser = SSEParser()
        return parser.feed(Array(text.utf8))
    }

    func events(_ text: String) -> [SSEEvent] {
        parse(text).compactMap { if case .event(let e) = $0 { e } else { nil } }
    }

    @Test func theContractExampleGivesFourItems() {
        let text = """
        id: 1
        event: hello
        data: {"serverTime":"2026-11-20T08:00:00Z","keepAliveSeconds":15}

        : keep-alive

        id: 2
        event: library
        data: {"systemIds":["R2FtZSBCb3kgQWR2YW5jZSAoR0JBKQ"]}

        id: 3
        event: shutdown
        data: {"reason":"quit"}


        """
        let items = parse(text)
        #expect(items == [
            .event(SSEEvent(id: "1", type: "hello", data: #"{"serverTime":"2026-11-20T08:00:00Z","keepAliveSeconds":15}"#)),
            .comment("keep-alive"),
            .event(SSEEvent(id: "2", type: "library", data: #"{"systemIds":["R2FtZSBCb3kgQWR2YW5jZSAoR0JBKQ"]}"#)),
            .event(SSEEvent(id: "3", type: "shutdown", data: #"{"reason":"quit"}"#)),
        ])
    }

    @Test func dataLinesAreJoinedWithNewlines() {
        #expect(events("data: one\ndata:two\ndata\ndata:  four\n\n") == [
            SSEEvent(id: nil, type: "message", data: "one\ntwo\n\n four"),
        ])
    }

    @Test func crlfCrAndLfAllEndLines() {
        let expected = [SSEEvent(id: "7", type: "job", data: "x"), SSEEvent(id: "8", type: "job", data: "y")]
        #expect(events("id: 7\r\nevent: job\r\ndata: x\r\n\r\nid: 8\revent: job\rdata: y\r\r") == expected)
        #expect(events("id: 7\nevent: job\ndata: x\n\nid: 8\nevent: job\ndata: y\n\n") == expected)
    }

    @Test func aCrlfSplitBetweenChunksCountsOnce() {
        var parser = SSEParser()
        var items = parser.feed(Array("data: a\r".utf8))
        items += parser.feed(Array("\n\r".utf8))
        items += parser.feed(Array("\n".utf8))
        #expect(items == [.event(SSEEvent(id: nil, type: "message", data: "a"))])
    }

    @Test func commentsAreReportedAndChangeNothing() {
        #expect(parse(": keep-alive\n:\n:no space\n") == [.comment("keep-alive"), .comment(""), .comment("no space")])
        // A comment between the fields of an event does not cut it.
        #expect(events("event: device\n: keep-alive\ndata: {}\n\n") == [SSEEvent(id: nil, type: "device", data: "{}")])
    }

    @Test func unknownFieldsAndRetryAreIgnored() {
        #expect(events("retry: 3000\nfoo: bar\nevent: hello\ndata: {}\n\n") == [SSEEvent(id: nil, type: "hello", data: "{}")])
    }

    @Test func anEventWithoutDataIsNotDispatched() {
        // Its type does not leak into the next event either.
        #expect(events("event: hello\n\ndata: x\n\n") == [SSEEvent(id: nil, type: "message", data: "x")])
    }

    @Test func theLastIdIsKeptAcrossEvents() {
        #expect(events("id: 4\ndata: a\n\ndata: b\n\nid\ndata: c\n\n").map(\.id) == ["4", "4", ""])
    }

    @Test func anIdWithNulIsIgnored() {
        #expect(events("id: 1\ndata: a\n\nid: 2\u{0}x\ndata: b\n\n").map(\.id) == ["1", "1"])
    }

    @Test func aLeadingBomIsIgnored() {
        var parser = SSEParser()
        let items = parser.feed([0xEF, 0xBB, 0xBF] + Array("data: a\n\n".utf8))
        #expect(items == [.event(SSEEvent(id: nil, type: "message", data: "a"))])
    }

    @Test func anEventCutByTheEndIsDropped() {
        #expect(events("data: a\n\ndata: b\n").map(\.data) == ["a"])
    }

    @Test func aTooLongLineIsDropped() {
        let long = String(repeating: "x", count: SSEParser.maxLineBytes + 10)
        #expect(events("data: \(long)\ndata: kept\n\n") == [SSEEvent(id: nil, type: "message", data: "kept")])
    }

    @Test func anEventTooLargeInAllIsDroppedUpToTheNextBlankLine() {
        // Each line is under the line limit; together they are over the
        // event limit.
        let line = "data: " + String(repeating: "x", count: SSEParser.maxEventBytes / 4) + "\n"
        let text = "event: library\n" + String(repeating: line, count: 5) + "data: tail\n\nevent: hello\ndata: next\n\n"
        #expect(events(text) == [SSEEvent(id: nil, type: "hello", data: "next")])
        // Just under the limit, it passes.
        let fits = "data: " + String(repeating: "y", count: SSEParser.maxEventBytes - 7) + "\n\n"
        #expect(events(fits).first?.data.utf8.count == SSEParser.maxEventBytes - 7)
    }

    // MARK: - Typed events

    @Test func knownEventsAreDecoded() throws {
        let hello = try #require(PakEvent(SSEEvent(id: "1", type: "hello", data: #"{"serverTime":"2026-11-20T08:00:00Z","keepAliveSeconds":15}"#)))
        #expect(hello == .hello(EventHello(serverTime: Date(timeIntervalSince1970: 1_795_161_600), keepAliveSeconds: 15)))
        #expect(PakEvent(SSEEvent(id: nil, type: "shutdown", data: #"{"reason":"stopped"}"#)) == .shutdown(EventShutdown(reason: "stopped")))
        #expect(PakEvent(SSEEvent(id: nil, type: "library", data: #"{"systemIds":[]}"#)) == .library(EventLibrary(systemIds: [])))
        #expect(EventLibrary(systemIds: []).affectedSystems == nil)
        #expect(EventLibrary(systemIds: ["a", "b"]).affectedSystems == ["a", "b"])
        let device = try #require(PakEvent(SSEEvent(id: nil, type: "device", data: """
        {"battery":null,"connectedClients":[{"clientName":"iPad","lastSeenAt":"2026-11-20T08:00:00Z"}],\
        "storage":[{"id":"sd","label":"SD","totalBytes":10,"freeBytes":4}]}
        """)))
        guard case .device(let d) = device else { Issue.record("not a device event"); return }
        #expect(d.battery == nil)
        #expect(d.connectedClients.map(\.clientName) == ["iPad"])
        #expect(d.storage.first?.freeBytes == 4)
        let job = try #require(PakEvent(SSEEvent(id: nil, type: "job", data: """
        {"id":"job_1","kind":"patch","state":"running","progress":{"done":1,"total":3},\
        "createdAt":"2026-11-20T08:00:00Z","updatedAt":"2026-11-20T08:00:01Z","result":null,"error":null}
        """)))
        guard case .job(let j) = job else { Issue.record("not a job event"); return }
        #expect(j.id == "job_1")
        #expect(j.state == .running)
    }

    @Test func unknownTypesAreKeptAsUnknownAndBadDataIgnored() {
        #expect(PakEvent(SSEEvent(id: nil, type: "future", data: "anything")) == .unknown("future"))
        #expect(PakEvent(SSEEvent(id: nil, type: "message", data: "x")) == .unknown("message"))
        #expect(PakEvent(SSEEvent(id: nil, type: "hello", data: "not json")) == nil)
        #expect(PakEvent(SSEEvent(id: nil, type: "library", data: "{}")) == nil)
    }

    @Test func aDeviceEventUpdatesTheDashboardFields() {
        let status = DeviceStatus(
            deviceId: "d", deviceName: "Brick", model: .brick, platform: "tg5040", os: "nextui", osVersion: nil,
            pakVersion: "1.0.0", network: .init(ipv4: "1.2.3.4", ssid: nil), battery: .init(percent: 80, charging: false),
            storage: [.init(id: "sd", label: "SD", totalBytes: 10, freeBytes: 5)], uptimeSeconds: 3
        )
        let updated = status.updated(with: EventDevice(
            battery: .init(percent: 42, charging: true),
            connectedClients: [ConnectedClient(clientName: "iPad", lastSeenAt: Date())],
            storage: [.init(id: "sd", label: "SD", totalBytes: 10, freeBytes: 2)]
        ))
        #expect(updated.battery == .init(percent: 42, charging: true))
        #expect(updated.storage.first?.freeBytes == 2)
        #expect(updated.connectedClients.map(\.clientName) == ["iPad"])
        #expect(updated.deviceName == "Brick")
        #expect(updated.network == status.network)
    }
}
