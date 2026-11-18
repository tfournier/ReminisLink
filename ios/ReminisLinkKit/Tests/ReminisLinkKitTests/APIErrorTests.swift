import Foundation
import ReminisLinkDemo
import Testing
@testable import ReminisLinkKit

@Suite("Error decoding")
struct APIErrorTests {
    @Test func everyContractCodeRoundTrips() {
        let raw = ["invalid_request", "invalid_id", "invalid_name", "unknown_client", "bad_signature", "clock_skew",
                   "replayed_request", "pairing_failed", "not_found", "conflict", "checksum_mismatch", "state_locked",
                   "too_large", "unsupported_format", "patch_mismatch", "too_many_attempts", "internal",
                   "shutting_down", "insufficient_storage"]
        #expect(ErrorCode.allKnown.map(\.rawValue) == raw)
        for code in raw {
            #expect(ErrorCode(rawValue: code).rawValue == code)
            #expect(ErrorCode(rawValue: code) != .other(code))
        }
        #expect(ErrorCode(rawValue: "brand_new") == .other("brand_new"))
    }

    func failure(_ response: FakeResponse) async -> APIError? {
        let client = stubClient { _, _ in response }
        do {
            _ = try await client.device()
            return nil
        } catch {
            return error as? APIError
        }
    }

    @Test(arguments: [
        (400, "invalid_request", ErrorCode.invalidRequest),
        (400, "invalid_id", .invalidId),
        (401, "bad_signature", .badSignature),
        (401, "replayed_request", .replayedRequest),
        (404, "not_found", .notFound),
        (413, "too_large", .tooLarge),
        (500, "internal", .internal),
        (503, "shutting_down", .shuttingDown),
        (418, "teapot", .other("teapot")),
    ])
    func serverErrors(status: Int, raw: String, code: ErrorCode) async {
        let error = await failure(.error(status, code: raw, message: "log only"))
        #expect(error == .server(ServerError(status: status, code: code, message: "log only")))
    }

    @Test func tooManyAttemptsCarriesRetryAfter() async throws {
        let error = await failure(.error(429, code: "too_many_attempts", headers: ["Retry-After": "30"]))
        guard case .server(let server) = error else { Issue.record("\(String(describing: error))"); return }
        #expect(server.code == .tooManyAttempts)
        #expect(server.retryAfter == 30)
    }

    @Test func insufficientStorageMissingBytes() async throws {
        let error = await failure(.error(507, code: "insufficient_storage", details: ["required": 35_232_153, "available": 1_048_576]))
        guard case .server(let server) = error else { Issue.record("\(String(describing: error))"); return }
        #expect(server.missingBytes == 34_183_577, "\(server.details)")
        #expect(ServerError(status: 507, code: .insufficientStorage, message: "").missingBytes == nil)
    }

    @Test func errorWithoutDetails() async {
        let body = Data(#"{"error":{"code":"conflict","message":"m"}}"#.utf8)
        let error = await failure(FakeResponse(status: 409, body: body))
        #expect(error == .server(ServerError(status: 409, code: .conflict, message: "m")))
    }

    @Test func nonJSONErrorBody() async {
        let error = await failure(FakeResponse(status: 502, body: Data("Bad Gateway".utf8)))
        #expect(error == .unexpectedStatus(502))
    }

    @Test func invalidSuccessBody() async {
        let error = await failure(.json(200, ["unexpected": true]))
        guard case .invalidResponse = error else { Issue.record("\(String(describing: error))"); return }
    }

    @Test func networkFailure() async {
        let error = await failure(.networkFailure(.timedOut))
        #expect(error == .network(.timedOut))
    }
}
