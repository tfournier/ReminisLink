// Debug builds only: the fake pak never ships in a release build.
#if DEBUG
import Foundation

/// A canned HTTP response.
public struct FakeResponse: Sendable {
    public var status: Int
    public var headers: [String: String]
    public var body: Data
    /// When set, the request fails at the transport level instead.
    public var transportError: URLError.Code?
    /// Seconds before the answer is delivered, without blocking the other
    /// requests (the handler has already run).
    public var delay: TimeInterval = 0
    /// When set, the answer is delivered once the gate opens (the handler
    /// has already run).
    public var gate: FakeGate?
    /// When set, the body is streamed: the headers go at once, then what
    /// the stream sends, until it finishes (M14, `GET /events`).
    public var stream: FakeStream?

    public init(status: Int, headers: [String: String] = [:], body: Data = Data(), transportError: URLError.Code? = nil) {
        self.status = status
        self.headers = headers
        self.body = body
        self.transportError = transportError
    }

    public static func json(_ status: Int, _ object: Any, headers: [String: String] = [:]) -> FakeResponse {
        let body = (try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])) ?? Data()
        return FakeResponse(status: status, headers: headers.merging(["Content-Type": "application/json"]) { a, _ in a }, body: body)
    }

    public static func error(_ status: Int, code: String, message: String = "error", details: [String: Any] = [:], headers: [String: String] = [:]) -> FakeResponse {
        json(status, ["error": ["code": code, "message": message, "details": details]], headers: headers)
    }

    public static func networkFailure(_ code: URLError.Code = .cannotConnectToHost) -> FakeResponse {
        FakeResponse(status: 0, transportError: code)
    }
}

/// Answers the requests sent to one fake host.
public protocol FakeHTTPHandler: Sendable {
    func respond(to request: URLRequest, body: Data) -> FakeResponse
}

/// A point a test holds a request (or its answer) at, then releases: the
/// test knows for sure the request is pending (``reached``), without
/// relying on timing.
public final class FakeGate: @unchecked Sendable {
    private let lock = NSLock()
    private var _reached = false
    private var _isOpen = false
    private var actions: [() -> Void] = []

    public init() {}

    /// The request got to the gate and waits there.
    public var reached: Bool { lock.withLock { _reached } }

    /// Lets the request (or its answer) go on.
    public func open() {
        let pending = lock.withLock { () -> [() -> Void] in
            _isOpen = true
            defer { actions = [] }
            return actions
        }
        for action in pending { DispatchQueue.global().async(execute: action) }
    }

    func hold(_ action: @escaping () -> Void) {
        let runNow = lock.withLock { () -> Bool in
            _reached = true
            if !_isOpen { actions.append(action) }
            return _isOpen
        }
        if runNow { DispatchQueue.global().async(execute: action) }
    }
}

/// A response body sent piece by piece, as long as the request stays open
/// (Server-Sent Events). What is sent before the protocol attaches is
/// kept and delivered first.
public final class FakeStream: @unchecked Sendable {
    private let lock = NSLock()
    private var sink: ((Data) -> Void)?
    private var finisher: (() -> Void)?
    private var pending: [Data] = []
    private var finished = false
    private var _onClose: (() -> Void)?

    public init() {}

    /// The stream ended, from either side.
    public var isFinished: Bool { lock.withLock { finished } }

    /// Called once when the client goes away or the stream finishes.
    public var onClose: (() -> Void)? {
        get { lock.withLock { _onClose } }
        set { lock.withLock { _onClose = newValue } }
    }

    public func send(_ data: Data) {
        let sink = lock.withLock { () -> ((Data) -> Void)? in
            guard !finished else { return nil }
            if self.sink == nil { pending.append(data) }
            return self.sink
        }
        sink?(data)
    }

    /// Ends the body (the connection closes normally).
    public func finish() {
        let (finisher, onClose) = lock.withLock { () -> ((() -> Void)?, (() -> Void)?) in
            guard !finished else { return (nil, nil) }
            finished = true
            defer {
                sink = nil
                self.finisher = nil
                _onClose = nil
            }
            return (self.finisher, _onClose)
        }
        finisher?()
        onClose?()
    }

    func attach(send: @escaping (Data) -> Void, finish: @escaping () -> Void) {
        let (backlog, done) = lock.withLock { () -> ([Data], Bool) in
            sink = send
            finisher = finish
            defer { pending = [] }
            return (pending, finished)
        }
        for data in backlog { send(data) }
        if done { finish() }
    }

    /// The client closed the connection.
    func cancel() {
        let onClose = lock.withLock { () -> (() -> Void)? in
            guard !finished else { return nil }
            finished = true
            defer {
                sink = nil
                finisher = nil
                _onClose = nil
            }
            return _onClose
        }
        onClose?()
    }
}

/// A handler that can hold a request before it reaches it (as if it stayed
/// on the network): requests sent later may be handled first.
public protocol FakeLateArrivals: FakeHTTPHandler {
    /// The gate `request` waits at before the handler runs, if any.
    func arrivalGate(for request: URLRequest) -> FakeGate?
}

/// A handler made of a closure.
public struct ClosureHandler: FakeHTTPHandler {
    let handler: @Sendable (URLRequest, Data) -> FakeResponse

    public init(_ handler: @escaping @Sendable (URLRequest, Data) -> FakeResponse) {
        self.handler = handler
    }

    public func respond(to request: URLRequest, body: Data) -> FakeResponse {
        handler(request, body)
    }
}

/// `URLProtocol` that routes requests to the handler registered for their
/// host. Each test registers its own host, so tests can run in parallel.
public final class FakeURLProtocol: URLProtocol, @unchecked Sendable {
    private static let registry = Registry()

    public static func register(_ handler: FakeHTTPHandler, host: String) {
        registry.set(handler, for: host.lowercased())
    }

    public static func unregister(host: String) {
        registry.set(nil, for: host.lowercased())
    }

    override public class func canInit(with request: URLRequest) -> Bool {
        guard let host = request.url?.host() else { return false }
        return registry.handler(for: host.lowercased()) != nil
    }

    override public class func canonicalRequest(for request: URLRequest) -> URLRequest {
        request
    }

    override public func startLoading() {
        guard let url = request.url, let host = url.host(), let handler = Self.registry.handler(for: host.lowercased()) else {
            client?.urlProtocol(self, didFailWithError: URLError(.cannotFindHost))
            return
        }
        let body = Self.body(of: request)
        if let late = handler as? FakeLateArrivals, let gate = late.arrivalGate(for: request) {
            let request = request
            gate.hold { [self] in
                schedule(handler.respond(to: request, body: body), for: url)
            }
            return
        }
        schedule(handler.respond(to: request, body: body), for: url)
    }

    private func schedule(_ response: FakeResponse, for url: URL) {
        if let gate = response.gate {
            gate.hold { [self] in deliver(response, for: url) }
        } else if response.delay > 0 {
            DispatchQueue.global().asyncAfter(deadline: .now() + response.delay) { [self] in
                deliver(response, for: url)
            }
        } else {
            deliver(response, for: url)
        }
    }

    private func deliver(_ response: FakeResponse, for url: URL) {
        if let code = response.transportError {
            client?.urlProtocol(self, didFailWithError: URLError(code))
            return
        }
        let http = HTTPURLResponse(url: url, statusCode: response.status, httpVersion: "HTTP/1.1", headerFields: response.headers)!
        // A redirection is offered to the session, as a real HTTP answer
        // would be: it either follows it (a new request) or takes this
        // answer as the final one.
        if (300..<400).contains(response.status), let location = response.headers["Location"],
           let target = URL(string: location, relativeTo: url) {
            var redirected = request
            redirected.url = target.absoluteURL
            client?.urlProtocol(self, wasRedirectedTo: redirected, redirectResponse: http)
        }
        client?.urlProtocol(self, didReceive: http, cacheStoragePolicy: .notAllowed)
        if let stream = response.stream {
            streamLock.withLock { openStream = stream }
            stream.attach(
                send: { [weak self] data in
                    guard let self else { return }
                    self.client?.urlProtocol(self, didLoad: data)
                },
                finish: { [weak self] in
                    guard let self else { return }
                    self.client?.urlProtocolDidFinishLoading(self)
                }
            )
            return
        }
        client?.urlProtocol(self, didLoad: response.body)
        client?.urlProtocolDidFinishLoading(self)
    }

    private let streamLock = NSLock()
    private var openStream: FakeStream?

    override public func stopLoading() {
        let stream = streamLock.withLock { () -> FakeStream? in
            defer { openStream = nil }
            return openStream
        }
        stream?.cancel()
    }

    /// `URLProtocol` gets the body as a stream.
    static func body(of request: URLRequest) -> Data {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return Data() }
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 16 * 1024)
        while stream.hasBytesAvailable {
            let n = stream.read(&buffer, maxLength: buffer.count)
            if n <= 0 { break }
            data.append(buffer, count: n)
        }
        return data
    }

    private final class Registry: @unchecked Sendable {
        private let lock = NSLock()
        private var handlers: [String: FakeHTTPHandler] = [:]

        func set(_ handler: FakeHTTPHandler?, for host: String) {
            lock.withLock { handlers[host] = handler }
        }

        func handler(for host: String) -> FakeHTTPHandler? {
            lock.withLock { handlers[host] }
        }
    }
}
#endif
