import Foundation

/// One event of a Server-Sent Events stream, once dispatched.
public struct SSEEvent: Sendable, Equatable {
    /// The last `id` seen in the stream (kept from one event to the next,
    /// as the SSE specification says), nil before any.
    public var id: String?
    /// `event`, or `message` when the event named none.
    public var type: String
    /// The `data` lines joined with `\n`.
    public var data: String

    public init(id: String?, type: String, data: String) {
        self.id = id
        self.type = type
        self.data = data
    }
}

/// What a line or a blank line of the stream produced.
public enum SSEItem: Sendable, Equatable {
    case event(SSEEvent)
    /// A comment line (`: keep-alive`), without its colon and first space.
    case comment(String)
}

/// Incremental parser of `text/event-stream` (the WHATWG "event stream
/// interpretation"), fed byte by byte or in chunks.
///
/// - Lines end with CRLF, LF or CR (a CRLF split between two chunks counts
///   once); a leading UTF-8 BOM is ignored.
/// - `data` lines accumulate (joined with `\n`), `event` names the type,
///   `id` sets the last event id (ignored if it holds NUL), `retry` and
///   unknown fields are ignored; a line without a colon is a field with an
///   empty value; one space after the colon is removed.
/// - A blank line dispatches the event, unless no `data` line came (then
///   the buffers are reset and nothing is dispatched).
/// - An event cut by the end of the stream is dropped.
/// - A line longer than ``maxLineBytes`` is dropped whole, and an event
///   whose `data` lines add up to more than ``maxEventBytes`` is dropped
///   whole (up to the next blank line): memory stays bounded.
public struct SSEParser: Sendable {
    public static let maxLineBytes = 1 << 20
    public static let maxEventBytes = 1 << 20

    private var line: [UInt8] = []
    private var lineTooLong = false
    private var lastWasCR = false
    private var isFirstLine = true
    private var data = ""
    private var hasData = false
    /// The event being read went over ``maxEventBytes``.
    private var eventTooLarge = false
    private var eventType = ""
    private var lastEventId: String?

    public init() {}

    /// Feeds one byte; returns what a completed line produced, if anything.
    public mutating func feed(_ byte: UInt8) -> SSEItem? {
        switch byte {
        case 0x0A:
            if lastWasCR {
                lastWasCR = false
                return nil
            }
            return endLine()
        case 0x0D:
            lastWasCR = true
            return endLine()
        default:
            lastWasCR = false
            if line.count < Self.maxLineBytes {
                line.append(byte)
            } else {
                lineTooLong = true
            }
            return nil
        }
    }

    /// Feeds bytes; returns what they produced, in order.
    public mutating func feed<S: Sequence>(_ bytes: S) -> [SSEItem] where S.Element == UInt8 {
        var items: [SSEItem] = []
        for byte in bytes {
            if let item = feed(byte) { items.append(item) }
        }
        return items
    }

    private mutating func endLine() -> SSEItem? {
        var bytes = line
        line.removeAll(keepingCapacity: true)
        if isFirstLine {
            isFirstLine = false
            if bytes.starts(with: [0xEF, 0xBB, 0xBF]) { bytes.removeFirst(3) }
        }
        if lineTooLong {
            lineTooLong = false
            return nil
        }
        return process(bytes)
    }

    private mutating func process(_ bytes: [UInt8]) -> SSEItem? {
        if bytes.isEmpty { return dispatch() }
        let text = String(decoding: bytes, as: UTF8.self)
        if text.hasPrefix(":") {
            var comment = text.dropFirst()
            if comment.first == " " { comment = comment.dropFirst() }
            return .comment(String(comment))
        }
        let field: Substring
        var value: Substring
        if let colon = text.firstIndex(of: ":") {
            field = text[..<colon]
            value = text[text.index(after: colon)...]
            if value.first == " " { value = value.dropFirst() }
        } else {
            field = Substring(text)
            value = ""
        }
        switch field {
        case "event":
            eventType = String(value)
        case "data":
            guard !eventTooLarge else { break }
            if data.utf8.count + value.utf8.count + 1 > Self.maxEventBytes {
                eventTooLarge = true
                data = ""
                break
            }
            data += value
            data += "\n"
            hasData = true
        case "id":
            if !value.contains("\0") { lastEventId = String(value) }
        default:
            // `retry` and unknown fields: ignored.
            break
        }
        return nil
    }

    private mutating func dispatch() -> SSEItem? {
        defer {
            data = ""
            hasData = false
            eventTooLarge = false
            eventType = ""
        }
        guard hasData, !eventTooLarge else { return nil }
        var payload = data
        if payload.hasSuffix("\n") { payload.removeLast() }
        return .event(SSEEvent(id: lastEventId, type: eventType.isEmpty ? "message" : eventType, data: payload))
    }
}
