import Foundation
import ReminisLinkDemo
import Testing
@testable import ReminisLinkKit

@Suite("Screenshot models (M22)")
struct ScreenshotModelTests {
    @Test func decodesTheContractExampleAndIgnoresUnknownFields() throws {
        let list = try ProtocolJSON.decoder().decode(ScreenshotList.self, from: Data(#"""
        {"screenshots":[
          {"screenshotId":"sc_U2t5IEdhcmRlbiBRdWVzdC4yMDI2LTExLTIxLTE4LTMwLTAwLnBuZw","name":"Sky Garden Quest.2026-11-21-18-30-00.png",
           "takenAt":"2026-11-21T18:30:00Z","sizeBytes":183204,"format":"png","gameName":"Sky Garden Quest","hasThumbnail":true,"newField":[1]},
          {"screenshotId":"sc_x","name":"capture.bmp","takenAt":"2026-11-20T08:00:00Z","sizeBytes":10,"format":"bmp","gameName":null,"hasThumbnail":false},
          {"screenshotId":"sc_y","name":"future.webp","takenAt":"2026-11-19T08:00:00Z","format":"webp"}
        ],"totalBytes":183214,"truncated":false,"extra":{}}
        """#.utf8))
        #expect(list.screenshots.count == 3 && list.totalBytes == 183_214 && !list.truncated)
        let first = list.screenshots[0]
        #expect(first.gameName == "Sky Garden Quest" && first.title == "Sky Garden Quest" && first.hasThumbnail && first.format == .png)
        #expect(first.takenAt == ProtocolJSON.parseDate("2026-11-21T18:30:00Z"))
        #expect(list.screenshots[1].title == "capture" && !list.screenshots[1].hasThumbnail && list.screenshots[1].format == .bmp)
        // Lenient: missing fields get defaults, an unknown format is kept.
        let future = list.screenshots[2]
        #expect(future.format.rawValue == "webp" && future.sizeBytes == 0 && !future.hasThumbnail && future.gameName == nil)
        // Without totalBytes and truncated (an older or newer pak).
        let bare = try ProtocolJSON.decoder().decode(ScreenshotList.self, from: Data(#"{"screenshots":[]}"#.utf8))
        #expect(bare.screenshots.isEmpty && bare.totalBytes == 0 && !bare.truncated)
    }

    @Test func thumbnailKeyChangesWithTheFile() {
        let date = Date(timeIntervalSince1970: 1_000)
        let a = Screenshot(screenshotId: "sc_a", name: "a.png", takenAt: date, sizeBytes: 10)
        let resized = Screenshot(screenshotId: "sc_a", name: "a.png", takenAt: date, sizeBytes: 11)
        let retaken = Screenshot(screenshotId: "sc_a", name: "a.png", takenAt: date.addingTimeInterval(1), sizeBytes: 10)
        #expect(Set([a.thumbnailKey, resized.thumbnailKey, retaken.thumbnailKey]).count == 3)
    }

    @Test func groupsByDayNewestFirst() throws {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = try #require(TimeZone(identifier: "Europe/Paris"))
        func shot(_ name: String, _ date: String) -> Screenshot {
            Screenshot(screenshotId: name, name: name, takenAt: ProtocolJSON.parseDate(date)!, sizeBytes: 1)
        }
        // 23:30 UTC on the 20th is the 21st in Paris.
        let groups = ScreenshotList.byDay([
            shot("b", "2026-11-20T10:00:00Z"), shot("a", "2026-11-21T09:00:00Z"),
            shot("c", "2026-11-20T23:30:00Z"), shot("d", "2026-11-20T10:00:00Z"),
        ], calendar: calendar)
        #expect(groups.map { $0.screenshots.map(\.name) } == [["a", "c"], ["b", "d"]])
        #expect(groups.map { calendar.component(.day, from: $0.day) } == [21, 20])
    }

    @Test func splitsLargeTrashRequests() {
        let ids = (0..<1_203).map { "sc_\($0)" } + ["sc_0", "sc_1"]
        let batches = ScreenshotTrashRequest.batches(ids)
        #expect(batches.map(\.screenshotIds.count) == [500, 500, 203])
        #expect(batches.flatMap(\.screenshotIds) == (0..<1_203).map { "sc_\($0)" })
        #expect(ScreenshotTrashRequest.batches([]).isEmpty)
    }

    /// Universal layout: the number of columns follows the width of the
    /// window, never the device.
    @Test func columnsFollowTheWidth() {
        #expect(ScreenshotGridLayout.columns(forWidth: 0) == 2)
        #expect(ScreenshotGridLayout.columns(forWidth: .nan) == 2)
        #expect(ScreenshotGridLayout.columns(forWidth: 200) == 2) // a narrow split
        #expect(ScreenshotGridLayout.columns(forWidth: 320) == 3) // iPhone SE
        #expect(ScreenshotGridLayout.columns(forWidth: 393) == 3) // iPhone
        #expect(ScreenshotGridLayout.columns(forWidth: 820) == 7) // iPad portrait
        #expect(ScreenshotGridLayout.columns(forWidth: 1_400) == 8) // a wide Mac window
    }

    @Test func selection() {
        let date = Date(timeIntervalSince1970: 0)
        let all = ["a", "b", "c"].map { Screenshot(screenshotId: $0, name: $0, takenAt: date, sizeBytes: 1) }
        var selection = ScreenshotSelection()
        selection.toggle("b")
        selection.toggle("a")
        selection.toggle("b")
        #expect(selection.selected(in: all).map(\.screenshotId) == ["a"])
        selection.toggleAll(all)
        #expect(selection.count == 3)
        selection.toggleAll(all)
        #expect(selection.isEmpty)
        selection.toggleAll(all)
        selection.keep(Array(all.prefix(1)))
        #expect(selection.ids == ["a"])
        selection.clear()
        #expect(selection.isEmpty)
    }

    @Test func readsTheEvent() {
        #expect(PakEvent(SSEEvent(id: nil, type: "screenshots", data: "{}")) == .screenshots(EventScreenshots()))
        #expect(PakEvent(SSEEvent(id: nil, type: "screenshots", data: "nope")) == nil)
    }
}

@Suite("Screenshots against the fake pak (M22)")
struct ScreenshotAPITests {
    @Test func listsDownloadsAndPreviews() async throws {
        let pak = makePak()
        pak.drawsArtwork = true
        let client = try await pairedClient(pak)
        let list = try await client.screenshots()
        #expect(list.screenshots.map(\.name) == pak.screenshotNames)
        #expect(list.totalBytes == list.screenshots.reduce(0) { $0 + $1.sizeBytes })
        let first = try #require(list.screenshots.first)
        let thumbnail = try #require(try await client.screenshotThumbnail(id: first.screenshotId))
        #expect(thumbnail.starts(with: [0xFF, 0xD8, 0xFF])) // a JPEG
        let directory = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let file = try await client.downloadScreenshot(id: first.screenshotId, into: directory)
        #expect(file.url.lastPathComponent == first.name)
        #expect(try Data(contentsOf: file.url).starts(with: [0x89, 0x50, 0x4E, 0x47]))
        // A BMP has no preview; a gone screenshot neither.
        let bmp = try #require(list.screenshots.first { $0.format == .bmp })
        #expect(!bmp.hasThumbnail)
        #expect(try await client.screenshotThumbnail(id: bmp.screenshotId) == nil)
        #expect(try await client.screenshotThumbnail(id: FakePak.screenshotId("gone.png")) == nil)
    }

    @Test func trashesInOneEntryAndRestores() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let list = try await client.screenshots().screenshots
        let picked = Array(list.prefix(2))
        let moved = try await client.trashScreenshots(ScreenshotTrashRequest(screenshotIds: picked.map(\.screenshotId)))
        #expect(moved.moved == picked.map { "Screenshots/" + $0.name })
        #expect(try await client.screenshots().screenshots.count == list.count - 2)
        let entry = try #require(try await client.trash().entries.first)
        #expect(entry.reason == "delete-screenshot" && entry.label == "2 screenshots")

        // A new screenshot of the same name: a conflict, then a free name.
        pak.addScreenshot(name: picked[0].name)
        await #expect { _ = try await client.restoreTrashEntry(id: entry.id, onConflict: .fail) } throws: { error in
            guard case .server(let e) = error as? APIError else { return false }
            return e.status == 409
        }
        _ = try await client.restoreTrashEntry(id: entry.id, onConflict: .rename)
        // Every file of the entry under the same free name, as on the pak.
        let names = try await client.screenshots().screenshots.map(\.name)
        let free = picked.map { ($0.name as NSString).deletingPathExtension + " (2).png" }
        #expect(names.contains(picked[0].name) && free.allSatisfy(names.contains))
    }

    @Test func missingScreenshotsMoveNothing() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let first = try #require(try await client.screenshots().screenshots.first)
        await #expect {
            _ = try await client.trashScreenshots(ScreenshotTrashRequest(screenshotIds: [first.screenshotId, FakePak.screenshotId("gone.png")]))
        } throws: { error in
            guard case .server(let e) = error as? APIError else { return false }
            return e.status == 404 && e.code == .notFound
        }
        #expect(try await client.screenshots().screenshots.contains(first))
    }

    @Test func anOlderPakHasNoScreenshots() async throws {
        let pak = makePak()
        pak.screenshotsSupported = false
        let client = try await pairedClient(pak)
        await #expect { _ = try await client.screenshots() } throws: { error in
            guard case .server(let e) = error as? APIError else { return false }
            return e.status == 404
        }
    }
}
