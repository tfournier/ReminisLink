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

/// A handler that can make a request reach it late (as if it stayed on the
/// network): the handler runs after the delay, so later requests may be
/// handled first.
public protocol FakeLateArrivals: FakeHTTPHandler {
    /// Seconds before `request` reaches the handler (0: at once).
    func arrivalDelay(for request: URLRequest) -> TimeInterval
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
        if let late = handler as? FakeLateArrivals, case let arrival = late.arrivalDelay(for: request), arrival > 0 {
            let request = request
            DispatchQueue.global().asyncAfter(deadline: .now() + arrival) { [self] in
                let response = handler.respond(to: request, body: body)
                DispatchQueue.global().asyncAfter(deadline: .now() + response.delay) { [self] in
                    deliver(response, for: url)
                }
            }
            return
        }
        let response = handler.respond(to: request, body: body)
        if response.delay > 0 {
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
        client?.urlProtocol(self, didLoad: response.body)
        client?.urlProtocolDidFinishLoading(self)
    }

    override public func stopLoading() {}

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
