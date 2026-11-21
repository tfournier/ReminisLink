// App Store Connect API steps of an app release (milestone M17). Run by
// asc.sh with the Swift of Xcode (`xcrun swift AppStoreConnect.swift ...`):
// Foundation and CryptoKit only, no dependency.
//
//   prepare   --bundle-id ID --version X.Y.Z --whats-new en-US=FILE [--whats-new fr-FR=FILE]
//             Creates the App Store version X.Y.Z if needed (released
//             automatically after approval) and sets its "What's New".
//   submit    (same options) [--wait-minutes N]
//             prepare, then waits for the newest build of X.Y.Z to be
//             processed, attaches it to the version and submits it to App
//             Review. Does nothing when the version is already submitted.
//   selftest  offline checks: key parsing and ES256 JWT.
//
// The key comes from the environment: ASC_KEY_ID, ASC_ISSUER_ID and
// ASC_PRIVATE_KEY (content of the .p8 file, with or without its line breaks)
// or ASC_PRIVATE_KEY_PATH. Neither the key nor a token is ever printed.

import CryptoKit
import Foundation

struct Failure: Error, CustomStringConvertible {
    let description: String
    init(_ description: String) { self.description = description }
}

func log(_ message: String) {
    print("ReminisLink: \(message)")
}

// MARK: - Key and token

enum PrivateKeyText {
    /// The PEM of a .p8 key, rebuilt from its base64 body whatever line
    /// breaks the secret variable kept.
    static func pem(_ raw: String) throws -> String {
        let body = raw
            .replacingOccurrences(of: "-----BEGIN PRIVATE KEY-----", with: "")
            .replacingOccurrences(of: "-----END PRIVATE KEY-----", with: "")
            .replacingOccurrences(of: "\\n", with: "")
            .filter { !$0.isWhitespace }
        guard !body.isEmpty, Data(base64Encoded: body) != nil else {
            throw Failure("ASC_PRIVATE_KEY is not the content of an App Store Connect .p8 key")
        }
        var lines: [String] = []
        var rest = Substring(body)
        while !rest.isEmpty {
            lines.append(String(rest.prefix(64)))
            rest = rest.dropFirst(64)
        }
        return "-----BEGIN PRIVATE KEY-----\n" + lines.joined(separator: "\n") + "\n-----END PRIVATE KEY-----\n"
    }

    static func key(_ raw: String) throws -> P256.Signing.PrivateKey {
        do {
            return try P256.Signing.PrivateKey(pemRepresentation: pem(raw))
        } catch let failure as Failure {
            throw failure
        } catch {
            throw Failure("ASC_PRIVATE_KEY could not be read as a P-256 private key")
        }
    }
}

func base64URL(_ data: Data) -> String {
    data.base64EncodedString()
        .replacingOccurrences(of: "+", with: "-")
        .replacingOccurrences(of: "/", with: "_")
        .replacingOccurrences(of: "=", with: "")
}

/// ES256 JSON Web Token of the App Store Connect API (20 minutes at most).
struct TokenMaker {
    let keyId: String
    let issuerId: String
    let key: P256.Signing.PrivateKey

    func token(now: Date = Date(), lifetime: TimeInterval = 15 * 60) throws -> String {
        let header = try JSONSerialization.data(withJSONObject: ["alg": "ES256", "kid": keyId, "typ": "JWT"], options: [.sortedKeys])
        let issuedAt = Int(now.timeIntervalSince1970)
        let payload = try JSONSerialization.data(withJSONObject: [
            "iss": issuerId, "iat": issuedAt, "exp": issuedAt + Int(lifetime), "aud": "appstoreconnect-v1",
        ] as [String: Any], options: [.sortedKeys])
        let input = base64URL(header) + "." + base64URL(payload)
        let signature = try key.signature(for: Data(input.utf8))
        // JWS ES256: r || s, 64 bytes (rawRepresentation), not DER.
        return input + "." + base64URL(signature.rawRepresentation)
    }
}

// MARK: - API

typealias JSON = [String: Any]

final class AppStoreConnect {
    let tokens: TokenMaker
    /// ASC_API_BASE: tests only (a local mock of the API).
    let base = URL(string: ProcessInfo.processInfo.environment["ASC_API_BASE"] ?? "https://api.appstoreconnect.apple.com")!
    private var cachedToken: (value: String, made: Date)?

    init(tokens: TokenMaker) { self.tokens = tokens }

    private func token() throws -> String {
        if let cached = cachedToken, Date().timeIntervalSince(cached.made) < 10 * 60 { return cached.value }
        let value = try tokens.token()
        cachedToken = (value, Date())
        return value
    }

    /// One request; the JSON answer (empty for 204). Errors carry the API's
    /// titles and details, never the token.
    @discardableResult
    func call(_ method: String, _ path: String, query: [String: String] = [:], body: JSON? = nil) async throws -> JSON {
        var components = URLComponents(url: base.appending(path: path), resolvingAgainstBaseURL: false)!
        if !query.isEmpty {
            components.queryItems = query.sorted { $0.key < $1.key }.map { URLQueryItem(name: $0.key, value: $0.value) }
        }
        var request = URLRequest(url: components.url!)
        request.httpMethod = method
        request.timeoutInterval = 60
        request.setValue("Bearer \(try token())", forHTTPHeaderField: "Authorization")
        if let body {
            request.httpBody = try JSONSerialization.data(withJSONObject: body)
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        var attempt = 0
        while true {
            attempt += 1
            let (data, response) = try await URLSession.shared.data(for: request)
            let status = (response as? HTTPURLResponse)?.statusCode ?? 0
            // Rate limit or a passing server error: try again a few times.
            if (status == 429 || status >= 500) && attempt < 4 {
                try await Task.sleep(nanoseconds: UInt64(attempt) * 10_000_000_000)
                continue
            }
            let json = (data.isEmpty ? [:] : (try? JSONSerialization.jsonObject(with: data)) as? JSON) ?? [:]
            guard (200..<300).contains(status) else {
                let errors = (json["errors"] as? [JSON] ?? []).map { error in
                    [error["title"] as? String, error["detail"] as? String].compactMap { $0 }.joined(separator: ": ")
                }
                throw Failure("\(method) \(path): HTTP \(status)\(errors.isEmpty ? "" : " - " + errors.joined(separator: "; "))")
            }
            return json
        }
    }
}

func data(_ json: JSON) -> [JSON] {
    if let list = json["data"] as? [JSON] { return list }
    if let one = json["data"] as? JSON { return [one] }
    return []
}

func attributes(_ item: JSON) -> JSON { item["attributes"] as? JSON ?? [:] }
func id(_ item: JSON) -> String { item["id"] as? String ?? "" }

// MARK: - Release steps

struct Release {
    let api: AppStoreConnect
    let bundleId: String
    let version: String
    let whatsNew: [(locale: String, text: String)]

    /// States of a version that still accepts changes and a submission.
    static let editableStates: Set<String> = [
        "PREPARE_FOR_SUBMISSION", "READY_FOR_REVIEW", "DEVELOPER_REJECTED", "REJECTED", "METADATA_REJECTED", "INVALID_BINARY",
    ]

    func appId() async throws -> String {
        let apps = try await api.call("GET", "/v1/apps", query: ["filter[bundleId]": bundleId, "fields[apps]": "bundleId"])
        guard let app = data(apps).first(where: { attributes($0)["bundleId"] as? String == bundleId }) else {
            throw Failure("no app with the bundle id \(bundleId) in App Store Connect (or the API key cannot see it)")
        }
        return id(app)
    }

    func state(_ version: JSON) -> String {
        let a = attributes(version)
        return a["appVersionState"] as? String ?? a["appStoreState"] as? String ?? "UNKNOWN"
    }

    /// The App Store version X.Y.Z, created when missing.
    func appStoreVersion(app: String) async throws -> JSON {
        let found = try await api.call("GET", "/v1/apps/\(app)/appStoreVersions", query: [
            "filter[platform]": "IOS", "filter[versionString]": version, "limit": "1",
        ])
        if let existing = data(found).first { return existing }
        log("creating App Store version \(version)")
        let created = try await api.call("POST", "/v1/appStoreVersions", body: ["data": [
            "type": "appStoreVersions",
            "attributes": ["platform": "IOS", "versionString": version, "releaseType": "AFTER_APPROVAL"],
            "relationships": ["app": ["data": ["type": "apps", "id": app]]],
        ] as JSON])
        guard let item = data(created).first else { throw Failure("App Store Connect did not return the new version") }
        return item
    }

    /// Creates or updates the version and its "What's New"; nil when the
    /// version can no longer change (already submitted or released).
    func prepare() async throws -> (app: String, version: String)? {
        let app = try await appId()
        let item = try await appStoreVersion(app: app)
        let versionId = id(item)
        let current = state(item)
        guard Self.editableStates.contains(current) else {
            log("version \(version) is \(current): nothing to change")
            return nil
        }
        if attributes(item)["releaseType"] as? String != "AFTER_APPROVAL" {
            try await api.call("PATCH", "/v1/appStoreVersions/\(versionId)", body: ["data": [
                "type": "appStoreVersions", "id": versionId, "attributes": ["releaseType": "AFTER_APPROVAL"],
            ] as JSON])
        }
        let localizations = try await api.call("GET", "/v1/appStoreVersions/\(versionId)/appStoreVersionLocalizations", query: ["limit": "50"])
        for (locale, text) in whatsNew {
            if let localization = data(localizations).first(where: { attributes($0)["locale"] as? String == locale }) {
                try await api.call("PATCH", "/v1/appStoreVersionLocalizations/\(id(localization))", body: ["data": [
                    "type": "appStoreVersionLocalizations", "id": id(localization), "attributes": ["whatsNew": text],
                ] as JSON])
            } else {
                try await api.call("POST", "/v1/appStoreVersionLocalizations", body: ["data": [
                    "type": "appStoreVersionLocalizations",
                    "attributes": ["locale": locale, "whatsNew": text],
                    "relationships": ["appStoreVersion": ["data": ["type": "appStoreVersions", "id": versionId]]],
                ] as JSON])
            }
            log("\"What's New\" set for \(locale)")
        }
        log("version \(version): released automatically after approval")
        return (app, versionId)
    }

    /// The newest build of X.Y.Z once processed, waiting for it.
    func processedBuild(app: String, waitMinutes: Int) async throws -> JSON {
        let deadline = Date().addingTimeInterval(TimeInterval(waitMinutes * 60))
        while true {
            let builds = try await api.call("GET", "/v1/builds", query: [
                "filter[app]": app, "filter[preReleaseVersion.version]": version, "filter[expired]": "false",
                "sort": "-uploadedDate", "limit": "1", "fields[builds]": "version,processingState,uploadedDate",
            ])
            if let build = data(builds).first {
                let number = attributes(build)["version"] as? String ?? "?"
                switch attributes(build)["processingState"] as? String {
                case "VALID":
                    log("build \(number) of \(version) is processed")
                    return build
                case "FAILED", "INVALID":
                    throw Failure("build \(number) of \(version) was refused by App Store Connect processing")
                default:
                    log("build \(number) of \(version) is still being processed")
                }
            } else {
                log("no build of \(version) in App Store Connect yet")
            }
            guard Date() < deadline else {
                throw Failure("no processed build of \(version) after \(waitMinutes) minutes: run the Submit workflow again later, or submit by hand")
            }
            // ASC_POLL_SECONDS: tests only.
            let poll = ProcessInfo.processInfo.environment["ASC_POLL_SECONDS"].flatMap(UInt64.init) ?? 60
            try await Task.sleep(nanoseconds: poll * 1_000_000_000)
        }
    }

    func submit(waitMinutes: Int) async throws {
        guard let prepared = try await prepare() else { return }
        let (app, versionId) = prepared
        let build = try await processedBuild(app: app, waitMinutes: waitMinutes)
        try await api.call("PATCH", "/v1/appStoreVersions/\(versionId)/relationships/build", body: [
            "data": ["type": "builds", "id": id(build)],
        ])
        log("build \(attributes(build)["version"] as? String ?? "?") attached to version \(version)")

        // A draft submission of the app is reused (one per platform at most).
        let drafts = try await api.call("GET", "/v1/reviewSubmissions", query: [
            "filter[app]": app, "filter[platform]": "IOS", "filter[state]": "READY_FOR_REVIEW", "limit": "1",
        ])
        let submissionId: String
        if let draft = data(drafts).first {
            submissionId = id(draft)
        } else {
            let created = try await api.call("POST", "/v1/reviewSubmissions", body: ["data": [
                "type": "reviewSubmissions",
                "attributes": ["platform": "IOS"],
                "relationships": ["app": ["data": ["type": "apps", "id": app]]],
            ] as JSON])
            submissionId = data(created).first.map(id) ?? ""
        }
        guard !submissionId.isEmpty else { throw Failure("App Store Connect did not return a review submission") }
        let items = try await api.call("GET", "/v1/reviewSubmissions/\(submissionId)/items", query: ["include": "appStoreVersion", "limit": "50"])
        let hasVersion = data(items).contains { item in
            let related = (item["relationships"] as? JSON)?["appStoreVersion"] as? JSON
            return (related?["data"] as? JSON).map(id) == versionId
        }
        if !hasVersion {
            try await api.call("POST", "/v1/reviewSubmissionItems", body: ["data": [
                "type": "reviewSubmissionItems",
                "relationships": [
                    "reviewSubmission": ["data": ["type": "reviewSubmissions", "id": submissionId]],
                    "appStoreVersion": ["data": ["type": "appStoreVersions", "id": versionId]],
                ],
            ] as JSON])
        }
        try await api.call("PATCH", "/v1/reviewSubmissions/\(submissionId)", body: ["data": [
            "type": "reviewSubmissions", "id": submissionId, "attributes": ["submitted": true],
        ] as JSON])
        log("version \(version) submitted to App Review")
    }
}

// MARK: - Command line

struct Options {
    var command = ""
    var bundleId = ""
    var version = ""
    var waitMinutes = 90
    var whatsNew: [(locale: String, text: String)] = []

    init(_ arguments: [String]) throws {
        var rest = arguments[...]
        command = rest.popFirst() ?? ""
        while let flag = rest.popFirst() {
            guard let value = rest.popFirst() else { throw Failure("\(flag) needs a value") }
            switch flag {
            case "--bundle-id": bundleId = value
            case "--version": version = value
            case "--wait-minutes":
                guard let minutes = Int(value), minutes >= 0 else { throw Failure("--wait-minutes takes a number of minutes") }
                waitMinutes = minutes
            case "--whats-new":
                let parts = value.split(separator: "=", maxSplits: 1).map(String.init)
                guard parts.count == 2 else { throw Failure("--whats-new takes LOCALE=FILE") }
                let text = try String(contentsOfFile: parts[1], encoding: .utf8).trimmingCharacters(in: .whitespacesAndNewlines)
                guard !text.isEmpty, text.count <= 4000 else { throw Failure("\"What's New\" of \(parts[0]): empty or over 4000 characters") }
                whatsNew.append((parts[0], text))
            default:
                throw Failure("unknown option \(flag)")
            }
        }
    }
}

func tokenMaker() throws -> TokenMaker {
    let environment = ProcessInfo.processInfo.environment
    guard let keyId = environment["ASC_KEY_ID"], !keyId.isEmpty,
          let issuerId = environment["ASC_ISSUER_ID"], !issuerId.isEmpty
    else { throw Failure("ASC_KEY_ID and ASC_ISSUER_ID are needed") }
    var raw = environment["ASC_PRIVATE_KEY"] ?? ""
    if raw.isEmpty, let path = environment["ASC_PRIVATE_KEY_PATH"], !path.isEmpty {
        raw = try String(contentsOfFile: (path as NSString).expandingTildeInPath, encoding: .utf8)
    }
    guard !raw.isEmpty else { throw Failure("ASC_PRIVATE_KEY (or ASC_PRIVATE_KEY_PATH) is needed") }
    return TokenMaker(keyId: keyId, issuerId: issuerId, key: try PrivateKeyText.key(raw))
}

/// Offline checks (asc.sh selftest, scripts/test-release-scripts.sh).
func selftest() throws {
    let key = P256.Signing.PrivateKey()
    let pem = key.pemRepresentation
    // The secret variable may lose the line breaks, or keep them as "\n".
    let variants = [pem, pem.replacingOccurrences(of: "\n", with: ""), pem.replacingOccurrences(of: "\n", with: "\\n"), pem.replacingOccurrences(of: "\n", with: " ")]
    for variant in variants {
        let parsed = try PrivateKeyText.key(variant)
        guard parsed.publicKey.rawRepresentation == key.publicKey.rawRepresentation else { throw Failure("key parsing changed the key") }
    }
    do {
        _ = try PrivateKeyText.key("not a key")
        throw Failure("a malformed key was accepted")
    } catch let failure as Failure where failure.description.hasPrefix("ASC_PRIVATE_KEY") {}

    let now = Date(timeIntervalSince1970: 1_800_000_000)
    let token = try TokenMaker(keyId: "TESTKEY123", issuerId: "00000000-0000-0000-0000-000000000000", key: key).token(now: now)
    let parts = token.split(separator: ".").map(String.init)
    guard parts.count == 3 else { throw Failure("a JWT has three parts") }
    func decode(_ part: String) throws -> Data {
        var s = part.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        while s.count % 4 != 0 { s += "=" }
        guard let data = Data(base64Encoded: s) else { throw Failure("JWT part is not base64url") }
        return data
    }
    let header = try JSONSerialization.jsonObject(with: decode(parts[0])) as? JSON ?? [:]
    let payload = try JSONSerialization.jsonObject(with: decode(parts[1])) as? JSON ?? [:]
    guard header["alg"] as? String == "ES256", header["kid"] as? String == "TESTKEY123", header["typ"] as? String == "JWT" else {
        throw Failure("JWT header")
    }
    guard payload["aud"] as? String == "appstoreconnect-v1", payload["iat"] as? Int == 1_800_000_000,
          let exp = payload["exp"] as? Int, exp - 1_800_000_000 <= 20 * 60
    else { throw Failure("JWT payload") }
    let signature = try P256.Signing.ECDSASignature(rawRepresentation: decode(parts[2]))
    guard key.publicKey.isValidSignature(signature, for: Data((parts[0] + "." + parts[1]).utf8)) else {
        throw Failure("JWT signature does not verify")
    }
    log("selftest passed (key parsing, ES256 JWT)")
}

do {
    let options = try Options(Array(CommandLine.arguments.dropFirst()))
    switch options.command {
    case "selftest":
        try selftest()
    case "prepare", "submit":
        guard !options.bundleId.isEmpty, options.version.range(of: #"^\d+\.\d+\.\d+$"#, options: .regularExpression) != nil else {
            throw Failure("--bundle-id and --version X.Y.Z are needed")
        }
        let release = Release(api: AppStoreConnect(tokens: try tokenMaker()), bundleId: options.bundleId, version: options.version, whatsNew: options.whatsNew)
        if options.command == "prepare" {
            _ = try await release.prepare()
        } else {
            try await release.submit(waitMinutes: options.waitMinutes)
        }
    default:
        throw Failure("usage: AppStoreConnect.swift prepare|submit --bundle-id ID --version X.Y.Z --whats-new LOCALE=FILE... [--wait-minutes N] | selftest")
    }
} catch {
    FileHandle.standardError.write(Data("ReminisLink: error: \(error)\n".utf8))
    exit(1)
}
