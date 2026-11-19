import Foundation
import Network

/// A minimal HTTP/1.1 server on the loopback interface, for what the
/// `URLProtocol` fake cannot show: the body bytes a real connection sends
/// (`didSendBodyData`). One request per connection; the body is read and
/// dropped, only its length is kept.
final class LocalHTTPServer: @unchecked Sendable {
    struct Request: Sendable {
        let method: String
        let path: String
        let headers: [String: String]
        let bodyLength: Int
    }

    struct Response: Sendable {
        var status: Int
        var headers: [String: String] = [:]
        var body = Data()
    }

    private let listener: NWListener
    private let queue = DispatchQueue(label: "LocalHTTPServer")
    private let handler: @Sendable (Request) -> Response

    init(handler: @escaping @Sendable (Request) -> Response) throws {
        let parameters = NWParameters.tcp
        parameters.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: .any)
        listener = try NWListener(using: parameters)
        self.handler = handler
    }

    /// Starts listening and returns the port.
    func start() async throws -> UInt16 {
        listener.newConnectionHandler = { [weak self] connection in self?.serve(connection) }
        return try await withCheckedThrowingContinuation { continuation in
            let resumed = Resumed()
            listener.stateUpdateHandler = { [listener] state in
                switch state {
                case .ready:
                    if resumed.once() { continuation.resume(returning: listener.port?.rawValue ?? 0) }
                case .failed(let error):
                    if resumed.once() { continuation.resume(throwing: error) }
                default:
                    break
                }
            }
            listener.start(queue: queue)
        }
    }

    func stop() {
        listener.cancel()
    }

    private func serve(_ connection: NWConnection) {
        connection.start(queue: queue)
        receive(connection, buffer: Data())
    }

    private func receive(_ connection: NWConnection, buffer: Data, head: (Request, Int)? = nil) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 1 << 20) { [weak self] data, _, isComplete, error in
            guard let self else { return }
            var buffer = buffer
            if let data { buffer.append(data) }
            var head = head
            if head == nil, let end = buffer.range(of: Data("\r\n\r\n".utf8)) {
                let lines = String(decoding: buffer[..<end.lowerBound], as: UTF8.self).components(separatedBy: "\r\n")
                let first = lines.first?.split(separator: " ") ?? []
                var headers: [String: String] = [:]
                for line in lines.dropFirst() {
                    guard let colon = line.firstIndex(of: ":") else { continue }
                    headers[line[..<colon].lowercased()] = line[line.index(after: colon)...].trimmingCharacters(in: .whitespaces)
                }
                let length = Int(headers["content-length"] ?? "") ?? 0
                let request = Request(
                    method: first.count > 0 ? String(first[0]) : "", path: first.count > 1 ? String(first[1]) : "",
                    headers: headers, bodyLength: length
                )
                buffer.removeSubrange(..<end.upperBound)
                head = (request, length)
            }
            if let (request, length) = head, buffer.count >= length {
                let response = self.handler(request)
                var text = "HTTP/1.1 \(response.status) X\r\nContent-Length: \(response.body.count)\r\nConnection: close\r\n"
                for (name, value) in response.headers { text += "\(name): \(value)\r\n" }
                var bytes = Data((text + "\r\n").utf8)
                bytes.append(response.body)
                connection.send(content: bytes, completion: .contentProcessed { _ in connection.cancel() })
                return
            }
            if isComplete || error != nil {
                connection.cancel()
                return
            }
            // Only the length of the body is needed: drop what was read.
            if let current = head {
                head = (current.0, current.1 - buffer.count)
                buffer.removeAll()
            }
            self.receive(connection, buffer: buffer, head: head)
        }
    }

    private final class Resumed: @unchecked Sendable {
        private let lock = NSLock()
        private var done = false
        func once() -> Bool { lock.withLock { defer { done = true }; return !done } }
    }
}
