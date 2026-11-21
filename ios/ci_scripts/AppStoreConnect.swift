// App Store Connect API steps of an app release (milestone M17). Run by
// asc.sh with the Swift of Xcode (`xcrun swift AppStoreConnect.swift ...`):
// Foundation and CryptoKit only, no dependency.
//
//   prepare   --bundle-id ID --version X.Y.Z --whats-new en-US=FILE [--whats-new fr-FR=FILE]
//             Creates the App Store version X.Y.Z if needed (released
//             automatically after approval) and sets its "What's New".
//   submit    (same options) --tag ios-vX.Y.Z --release-workflow NAME
//             [--build-number N] [--wait-minutes N]
//             prepare, then waits for the build of that exact release: the
//             build of the Xcode Cloud run of the Release workflow on the
//             tag, found through the Xcode Cloud API (the newest run, or the
//             run numbered N), never just the newest upload of X.Y.Z (a
//             TestFlight beta or a manual upload has the same version).
//             Checks that it is processed and App Store eligible, attaches
//             it and submits the version to App Review, alone: a draft
//             submission holding anything else, before or right before the
//             submission, stops it. Does nothing when the version is
//             already submitted.
//   selftest  offline checks: key parsing and ES256 JWT.
//
// The key comes from the environment: ASC_KEY_ID, ASC_ISSUER_ID and
// ASC_PRIVATE_KEY (content of the .p8 file, with or without its line breaks)
// or ASC_PRIVATE_KEY_PATH. Neither the key nor a token is ever printed, nor
// the body of an answer (errors: status, code and title only).
//
// Tests only: ASC_TEST_MODE=1 with ASC_API_BASE on a local address and a key
// id starting with TEST (a mock of the API); the token is then signed with an
// ephemeral key made in the process, ASC_PRIVATE_KEY and ASC_PRIVATE_KEY_PATH
// are never read. ASC_POLL_SECONDS.

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
    let base: URL
    private var cachedToken: (value: String, made: Date)?

    init(tokens: TokenMaker) throws {
        self.tokens = tokens
        base = try Self.baseURL(ProcessInfo.processInfo.environment)
    }

    /// Apple's API, or (tests only) a local mock: ASC_API_BASE is refused
    /// unless ASC_TEST_MODE=1, the address is local and the key id is a
    /// test one, so a real token never goes anywhere else.
    static func baseURL(_ environment: [String: String]) throws -> URL {
        guard let override = environment["ASC_API_BASE"], !override.isEmpty else {
            return URL(string: "https://api.appstoreconnect.apple.com")!
        }
        guard environment["ASC_TEST_MODE"] == "1",
              let url = URL(string: override), ["127.0.0.1", "localhost"].contains(url.host ?? ""),
              (environment["ASC_KEY_ID"] ?? "").hasPrefix("TEST")
        else {
            throw Failure("ASC_API_BASE is for tests against a local mock only (ASC_TEST_MODE=1, local address, TEST key id): unset it")
        }
        return url
    }

    static var pollSeconds: UInt64 {
        let environment = ProcessInfo.processInfo.environment
        guard environment["ASC_TEST_MODE"] == "1" else { return 60 }
        return environment["ASC_POLL_SECONDS"].flatMap(UInt64.init) ?? 60
    }

    private func token() throws -> String {
        if let cached = cachedToken, Date().timeIntervalSince(cached.made) < 10 * 60 { return cached.value }
        let value = try tokens.token()
        cachedToken = (value, Date())
        return value
    }

    /// One request; the JSON answer (empty for 204). Errors carry the
    /// status and the API's error codes and titles only: never a detail
    /// (it may quote the request), a body or the token.
    @discardableResult
    func call(_ method: String, _ path: String, query: [String: String] = [:], body: JSON? = nil) async throws -> JSON {
        var components = URLComponents(url: base.appending(path: path), resolvingAgainstBaseURL: false)!
        if !query.isEmpty {
            components.queryItems = query.sorted { $0.key < $1.key }.map { URLQueryItem(name: $0.key, value: $0.value) }
        }
        return try await send(method, components.url!, label: path, body: body)
    }

    /// Every page of a list (`links.next`), `maxPages` at most: the data
    /// and the included resources of all pages. A next link to another
    /// host than the API is refused (the token never leaves it).
    func all(_ path: String, query: [String: String], maxPages: Int = 20) async throws -> (data: [JSON], included: [JSON]) {
        var page = try await call("GET", path, query: query)
        var items = data(page)
        var included = page["included"] as? [JSON] ?? []
        var pages = 1
        while let next = (page["links"] as? JSON)?["next"] as? String, !next.isEmpty {
            guard pages < maxPages else { throw Failure("GET \(path): more than \(maxPages) pages") }
            guard let url = URL(string: next), url.scheme == base.scheme, url.host == base.host, url.port == base.port else {
                throw Failure("GET \(path): the next page is not on the App Store Connect API")
            }
            page = try await send("GET", url, label: path, body: nil)
            items += data(page)
            included += page["included"] as? [JSON] ?? []
            pages += 1
        }
        return (items, included)
    }

    private func send(_ method: String, _ url: URL, label path: String, body: JSON?) async throws -> JSON {
        var request = URLRequest(url: url)
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
                let errors = (json["errors"] as? [JSON] ?? []).prefix(5).map { error in
                    [error["code"] as? String, error["title"] as? String].compactMap { $0 }.joined(separator: ": ")
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

    /// The Xcode Cloud runs of `workflow` on `tag` (more than one when the
    /// run was started again), newest first; every page is read.
    func releaseRuns(app: String, tag: String, workflow: String) async throws -> [JSON] {
        let product: JSON
        do {
            product = try await api.call("GET", "/v1/apps/\(app)/ciProduct")
        } catch {
            throw Failure("Xcode Cloud cannot be read with this key (\(error)): give the key the App Manager or Admin access, or submit by hand (asc.sh instructions)")
        }
        guard let productId = data(product).first.map(id), !productId.isEmpty else { throw Failure("no Xcode Cloud product for this app") }
        let workflows = try await api.all("/v1/ciProducts/\(productId)/workflows", query: ["limit": "50"])
        guard let workflowId = workflows.data.first(where: { attributes($0)["name"] as? String == workflow }).map(id) else {
            throw Failure("no Xcode Cloud workflow named \(workflow)")
        }
        let runs = try await api.all("/v1/ciWorkflows/\(workflowId)/buildRuns", query: [
            "limit": "50", "include": "sourceBranchOrTag",
        ])
        let tagReferences = Set(runs.included.filter { reference in
            let a = attributes(reference)
            return reference["type"] as? String == "scmGitReferences" && a["kind"] as? String == "TAG"
                && (a["name"] as? String == tag || a["canonicalName"] as? String == "refs/tags/\(tag)")
        }.map(id))
        return runs.data
            .filter { run in
                let reference = ((run["relationships"] as? JSON)?["sourceBranchOrTag"] as? JSON)?["data"] as? JSON
                return reference.map(id).map(tagReferences.contains) ?? false
            }
            .sorted { (attributes($0)["number"] as? Int ?? 0) > (attributes($1)["number"] as? Int ?? 0) }
    }

    /// The build of X.Y.Z with this build number, if uploaded.
    func build(app: String, number: String) async throws -> JSON? {
        let builds = try await api.call("GET", "/v1/builds", query: [
            "filter[app]": app, "filter[preReleaseVersion.version]": version, "filter[version]": number, "limit": "2",
            "fields[builds]": "version,processingState,buildAudienceType,expired,usesNonExemptEncryption",
        ])
        let matches = data(builds).filter { attributes($0)["version"] as? String == number }
        guard matches.count <= 1 else { throw Failure("several builds \(number) of \(version)") }
        return matches.first
    }

    /// The build of this release once processed, waiting for it: always a
    /// build uploaded by a run of the Release workflow on the tag, found
    /// through the Xcode Cloud API (the newest run, or the run whose
    /// number is the build number given). A build of any other origin (a
    /// beta, a manual upload) is never returned.
    func processedBuild(app: String, selector: BuildSelector, waitMinutes: Int) async throws -> JSON {
        let (tag, workflow, given) = (selector.tag, selector.workflow, selector.buildNumber)
        let deadline = Date().addingTimeInterval(TimeInterval(waitMinutes * 60))
        while true {
            let runs = try await releaseRuns(app: app, tag: tag, workflow: workflow)
            let run: JSON?
            if let given {
                // The number only chooses among the runs of the tag.
                guard let numbered = runs.first(where: { String(attributes($0)["number"] as? Int ?? 0) == given }) else {
                    let known = runs.map { String(attributes($0)["number"] as? Int ?? 0) }
                    throw Failure("build \(given) is not the build of a \(workflow) run of \(tag) (runs of the tag: \(known.isEmpty ? "none" : known.joined(separator: ", "))): a build of another origin is never submitted. Nothing was submitted.")
                }
                run = numbered
            } else {
                run = runs.first
            }
            if let run {
                let a = attributes(run)
                let number = String(a["number"] as? Int ?? 0)
                switch (a["executionProgress"] as? String, a["completionStatus"] as? String) {
                case ("COMPLETE", "SUCCEEDED"):
                    if let build = try await runBuild(app: app, run: run, number: number) { return build }
                case ("COMPLETE", let status):
                    throw Failure("the \(workflow) run \(number) of \(tag) ended \(status ?? "without a status"): nothing to submit")
                default:
                    log("the \(workflow) run \(number) of \(tag) is still running")
                }
            } else {
                log("no \(workflow) run of \(tag) in Xcode Cloud yet")
            }
            guard Date() < deadline else {
                throw Failure("no processed build of this release of \(version) after \(waitMinutes) minutes: run the Submit workflow again later, or submit by hand")
            }
            try await Task.sleep(nanoseconds: AppStoreConnect.pollSeconds * 1_000_000_000)
        }
    }

    /// The build `number` of a succeeded run, once that run lists it and
    /// App Store Connect has processed it; nil while waiting.
    func runBuild(app: String, run: JSON, number: String) async throws -> JSON? {
        let runBuilds = try await api.all("/v1/ciBuildRuns/\(id(run))/builds", query: ["limit": "10"])
        guard let build = try await build(app: app, number: number) else {
            log("build \(number) of \(version) is not in App Store Connect yet")
            return nil
        }
        // The build must be the one the run uploaded.
        guard runBuilds.data.map(id).contains(id(build)) else {
            log("build \(number) of \(version) is not yet listed by its Xcode Cloud run")
            return nil
        }
        switch attributes(build)["processingState"] as? String {
        case "VALID":
            try Self.checkEligible(build, number: number, version: version)
            log("build \(number) of \(version) is processed and App Store eligible")
            return build
        case "FAILED", "INVALID":
            throw Failure("build \(number) of \(version) was refused by App Store Connect processing")
        default:
            log("build \(number) of \(version) is still being processed")
            return nil
        }
    }

    /// A build App Review can take: not expired, not a TestFlight
    /// internal-only build, export compliance answered.
    static func checkEligible(_ build: JSON, number: String, version: String) throws {
        let a = attributes(build)
        if a["expired"] as? Bool == true { throw Failure("build \(number) of \(version) has expired") }
        guard a["buildAudienceType"] as? String == "APP_STORE_ELIGIBLE" else {
            throw Failure("build \(number) of \(version) is \(a["buildAudienceType"] as? String ?? "not marked App Store eligible") (a TestFlight internal-only build cannot be submitted)")
        }
        guard a["usesNonExemptEncryption"] is Bool else {
            throw Failure("build \(number) of \(version) has no export compliance answer")
        }
    }

    /// The app store versions linked by the items of a review submission
    /// (nil for an item that is not an App Store version), every page.
    func linkedVersions(submission: String) async throws -> [String?] {
        let items = try await api.all("/v1/reviewSubmissions/\(submission)/items", query: ["include": "appStoreVersion", "limit": "50"])
        return items.data.map { item in
            (((item["relationships"] as? JSON)?["appStoreVersion"] as? JSON)?["data"] as? JSON).map(id)
        }
    }

    func submit(selector: BuildSelector, waitMinutes: Int) async throws {
        guard let prepared = try await prepare() else { return }
        let (app, versionId) = prepared
        let build = try await processedBuild(app: app, selector: selector, waitMinutes: waitMinutes)
        let number = attributes(build)["version"] as? String ?? "?"

        // One draft submission per platform at most. It is used only when
        // it holds nothing but this version: never submit anything else.
        let drafts = try await api.call("GET", "/v1/reviewSubmissions", query: [
            "filter[app]": app, "filter[platform]": "IOS", "filter[state]": "READY_FOR_REVIEW", "limit": "1",
        ])
        var submissionId = ""
        var hasVersion = false
        if let draft = data(drafts).first {
            submissionId = id(draft)
            let linked = try await linkedVersions(submission: submissionId)
            let foreign = linked.filter { $0 != versionId }.count
            guard foreign == 0 else {
                throw Failure("a draft submission in App Store Connect already holds \(foreign) other item(s): submit or remove them by hand (App Store Connect > the app > App Review), then run Submit again. Nothing was submitted.")
            }
            hasVersion = !linked.isEmpty
        }

        try await api.call("PATCH", "/v1/appStoreVersions/\(versionId)/relationships/build", body: [
            "data": ["type": "builds", "id": id(build)],
        ])
        log("build \(number) attached to version \(version)")

        if submissionId.isEmpty {
            let created = try await api.call("POST", "/v1/reviewSubmissions", body: ["data": [
                "type": "reviewSubmissions",
                "attributes": ["platform": "IOS"],
                "relationships": ["app": ["data": ["type": "apps", "id": app]]],
            ] as JSON])
            submissionId = data(created).first.map(id) ?? ""
        }
        guard !submissionId.isEmpty else { throw Failure("App Store Connect did not return a review submission") }
        if !hasVersion {
            try await api.call("POST", "/v1/reviewSubmissionItems", body: ["data": [
                "type": "reviewSubmissionItems",
                "relationships": [
                    "reviewSubmission": ["data": ["type": "reviewSubmissions", "id": submissionId]],
                    "appStoreVersion": ["data": ["type": "appStoreVersions", "id": versionId]],
                ],
            ] as JSON])
        }
        // Read the whole draft again right before submitting it: an item
        // added meanwhile (by hand in App Store Connect) stops everything.
        let final = try await linkedVersions(submission: submissionId)
        guard !final.isEmpty, final.allSatisfy({ $0 == versionId }) else {
            throw Failure("the draft submission now holds \(final.filter { $0 != versionId }.count) other item(s) (changed in App Store Connect while Submit ran): remove them by hand, then run Submit again. Nothing was submitted.")
        }
        try await api.call("PATCH", "/v1/reviewSubmissions/\(submissionId)", body: ["data": [
            "type": "reviewSubmissions", "id": submissionId, "attributes": ["submitted": true],
        ] as JSON])
        log("version \(version) (build \(number)) submitted to App Review")
    }
}

/// Which build a submission takes: always the build uploaded by a run of
/// the Release workflow on the release tag; a build number (read in
/// TestFlight) only chooses which run of that tag.
struct BuildSelector {
    let tag: String
    let workflow: String
    let buildNumber: String?
}

// MARK: - Command line

struct Options {
    var command = ""
    var bundleId = ""
    var version = ""
    var waitMinutes = 90
    var whatsNew: [(locale: String, text: String)] = []
    var tag = ""
    var releaseWorkflow = ""
    var buildNumber = ""

    init(_ arguments: [String]) throws {
        var rest = arguments[...]
        command = rest.popFirst() ?? ""
        while let flag = rest.popFirst() {
            guard let value = rest.popFirst() else { throw Failure("\(flag) needs a value") }
            switch flag {
            case "--bundle-id": bundleId = value
            case "--version": version = value
            case "--tag": tag = value
            case "--release-workflow": releaseWorkflow = value
            case "--build-number":
                guard value.range(of: #"^[1-9][0-9]*$"#, options: .regularExpression) != nil else {
                    throw Failure("--build-number takes a build number")
                }
                buildNumber = value
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

func tokenMaker(_ environment: [String: String] = ProcessInfo.processInfo.environment) throws -> TokenMaker {
    guard let keyId = environment["ASC_KEY_ID"], !keyId.isEmpty,
          let issuerId = environment["ASC_ISSUER_ID"], !issuerId.isEmpty
    else { throw Failure("ASC_KEY_ID and ASC_ISSUER_ID are needed") }
    // A test address (refused unless it is a valid test setup) gets a
    // throwaway key: the real key is never read, so it cannot sign a
    // token for a mock.
    _ = try AppStoreConnect.baseURL(environment)
    if let override = environment["ASC_API_BASE"], !override.isEmpty {
        return TokenMaker(keyId: keyId, issuerId: issuerId, key: P256.Signing.PrivateKey())
    }
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
    // ASC_API_BASE: refused outside the test mode, for a remote host, or
    // with a key id that is not a test one.
    let apple = try AppStoreConnect.baseURL([:])
    guard apple.absoluteString == "https://api.appstoreconnect.apple.com" else { throw Failure("default base URL") }
    for environment in [
        ["ASC_API_BASE": "http://127.0.0.1:1", "ASC_KEY_ID": "TESTKEY"],
        ["ASC_API_BASE": "https://example.com", "ASC_TEST_MODE": "1", "ASC_KEY_ID": "TESTKEY"],
        ["ASC_API_BASE": "http://127.0.0.1:1", "ASC_TEST_MODE": "1", "ASC_KEY_ID": "ABC123"],
    ] {
        if (try? AppStoreConnect.baseURL(environment)) != nil { throw Failure("ASC_API_BASE accepted: \(environment)") }
    }
    guard (try? AppStoreConnect.baseURL(["ASC_API_BASE": "http://127.0.0.1:1", "ASC_TEST_MODE": "1", "ASC_KEY_ID": "TESTKEY"])) != nil else {
        throw Failure("test base URL refused")
    }
    // Test mode: an ephemeral key, the real key variables never read.
    let testEnvironment = [
        "ASC_API_BASE": "http://127.0.0.1:1", "ASC_TEST_MODE": "1", "ASC_KEY_ID": "TESTKEY", "ASC_ISSUER_ID": "x",
        "ASC_PRIVATE_KEY": pem, "ASC_PRIVATE_KEY_PATH": "/nonexistent/AuthKey.p8",
    ]
    let testKey = try tokenMaker(testEnvironment).key
    guard testKey.publicKey.rawRepresentation != key.publicKey.rawRepresentation else {
        throw Failure("test mode signed with ASC_PRIVATE_KEY")
    }
    guard (try? tokenMaker(testEnvironment.merging(["ASC_PRIVATE_KEY": "not a key"]) { $1 })) != nil else {
        throw Failure("test mode read ASC_PRIVATE_KEY")
    }
    if (try? tokenMaker(testEnvironment.merging(["ASC_API_BASE": "https://example.com"]) { $1 })) != nil {
        throw Failure("test mode accepted a remote ASC_API_BASE")
    }
    // Only App Store eligible, processed builds with export compliance.
    try Release.checkEligible(["attributes": ["buildAudienceType": "APP_STORE_ELIGIBLE", "usesNonExemptEncryption": false, "expired": false]], number: "1", version: "1.0.0")
    for attributes: JSON in [
        ["buildAudienceType": "INTERNAL_ONLY", "usesNonExemptEncryption": false],
        ["usesNonExemptEncryption": false],
        ["buildAudienceType": "APP_STORE_ELIGIBLE"],
        ["buildAudienceType": "APP_STORE_ELIGIBLE", "usesNonExemptEncryption": false, "expired": true],
    ] {
        if (try? Release.checkEligible(["attributes": attributes], number: "1", version: "1.0.0")) != nil {
            throw Failure("ineligible build accepted: \(attributes)")
        }
    }
    log("selftest passed (key parsing, ES256 JWT, API address guard, test-mode key, build eligibility)")
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
        let release = Release(api: try AppStoreConnect(tokens: try tokenMaker()), bundleId: options.bundleId, version: options.version, whatsNew: options.whatsNew)
        if options.command == "prepare" {
            _ = try await release.prepare()
        } else {
            guard options.tag == "ios-v\(options.version)", !options.releaseWorkflow.isEmpty else {
                throw Failure("submit needs --tag ios-v\(options.version) and --release-workflow (--build-number only chooses among the builds of that workflow's runs on the tag)")
            }
            let selector = BuildSelector(tag: options.tag, workflow: options.releaseWorkflow,
                                         buildNumber: options.buildNumber.isEmpty ? nil : options.buildNumber)
            try await release.submit(selector: selector, waitMinutes: options.waitMinutes)
        }
    default:
        throw Failure("usage: AppStoreConnect.swift prepare|submit --bundle-id ID --version X.Y.Z --whats-new LOCALE=FILE... [--wait-minutes N] | selftest")
    }
} catch {
    FileHandle.standardError.write(Data("ReminisLink: error: \(error)\n".utf8))
    exit(1)
}
