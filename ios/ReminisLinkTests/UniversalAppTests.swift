import Foundation
import Testing
@testable import ReminisLink

/// Milestone M15: one app on iPhone, iPad and Mac. The device families and
/// orientations of the bundle, the readable width of large windows, and the
/// Library tab seen as two columns.
@MainActor
@Suite("Universal app (M15)")
struct UniversalAppTests {
    static let game = Route.game(GameRoute(
        deviceId: "d", gameId: "g", systemLabel: "Handheld Advance", name: "Thistledown Isles", hasArt: false, fileName: "Thistledown Isles.hab"
    ))
    static let other = Route.game(GameRoute(
        deviceId: "d", gameId: "h", systemLabel: "Handheld Advance", name: "Brambleway Dash", hasArt: false, fileName: "Brambleway Dash.hab"
    ))

    /// iPhone and iPad, every iPad orientation (Split View, Slide Over and
    /// Stage Manager need them), never full screen only.
    @Test func theBundleRunsOnIPhoneAndIPad() throws {
        // The file itself: Bundle resolves device suffixes (~ipad) for the
        // device it runs on.
        let url = try #require(Bundle.main.url(forResource: "Info", withExtension: "plist"))
        let info = try #require(NSDictionary(contentsOf: url) as? [String: Any])
        let families = try #require(info["UIDeviceFamily"] as? [Int])
        #expect(Set(families) == [1, 2])
        let iPad = try #require(info["UISupportedInterfaceOrientations~ipad"] as? [String])
        #expect(Set(iPad) == [
            "UIInterfaceOrientationPortrait", "UIInterfaceOrientationPortraitUpsideDown",
            "UIInterfaceOrientationLandscapeLeft", "UIInterfaceOrientationLandscapeRight",
        ])
        #expect(info["UIRequiresFullScreen"] as? Bool != true)
        #expect(info["UIRequiresFullScreen~ipad"] as? Bool != true)
    }

    /// Narrow windows are left as they are; wide ones get centred content.
    @Test func onlyWideWindowsGetAReadableWidth() {
        #expect(ReadableWidth.inset(forWidth: 320) == 0)
        #expect(ReadableWidth.inset(forWidth: 440) == 0)
        #expect(ReadableWidth.inset(forWidth: ReadableWidth.maxWidth) == 0)
        #expect(ReadableWidth.inset(forWidth: 1032) == 166)
        #expect(ReadableWidth.inset(forWidth: 1376) == 338)
    }

    /// The same path drives the stack of a narrow window and the columns
    /// of a wide one: nothing is lost when the width changes.
    @Test func theLibraryPathIsTheSelectionAndTheDetail() {
        let save = Route.save(GameRoute(
            deviceId: "d", gameId: "g", systemLabel: "Handheld Advance", name: "Thistledown Isles", hasArt: false, fileName: "Thistledown Isles.hab"
        ))
        let path = [Self.game, save]
        #expect(LibraryColumns.selection(of: path) == Self.game)
        #expect(LibraryColumns.detail(of: path) == [save])
        #expect(LibraryColumns.selection(of: []) == nil)
        #expect(LibraryColumns.detail(of: []) == [])
        // Selecting the game shown keeps the screens above it.
        #expect(LibraryColumns.path(path, selecting: Self.game) == path)
        // Another game replaces everything.
        #expect(LibraryColumns.path(path, selecting: Self.other) == [Self.other])
        #expect(LibraryColumns.path(path, selecting: nil) == [])
        // Back in the detail column; a push.
        #expect(LibraryColumns.path(path, detail: []) == [Self.game])
        #expect(LibraryColumns.path([Self.game], detail: [save]) == path)
        #expect(LibraryColumns.path([], detail: [save]) == [])
    }
}
