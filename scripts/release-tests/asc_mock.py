"""A small mock of the App Store Connect API for scripts/test-release-scripts.sh
(milestone M17): enough of apps, App Store versions, localizations, builds,
Xcode Cloud (ciProduct, workflows, build runs) and review submissions to run
ios/ci_scripts/AppStoreConnect.swift end to end, offline.

Usage: python3 asc_mock.py <scenario> <port file> <state file>
Writes the chosen port to <port file>, and its state (what was attached and
submitted, the requests) to <state file> after every request.

Scenarios (version 1.1.0, tag ios-v1.1.0):
  release        a VALID TestFlight internal-only beta build 41 of 1.1.0 is
                 already there, and a failed Release run 41 of the beta tag;
                 the Release run 42 of the tag is running, then succeeds; its
                 build 42 is processing, then VALID and App Store eligible.
  number         as release, but the build is chosen by number (42).
  internal       the Release run's build 42 is internal-only.
  foreign-draft  a draft submission already holds another item.
  own-draft      a draft submission holds this version only.
  submitted      the version is already waiting for review.
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import parse_qs, urlparse

scenario, port_file, state_file = sys.argv[1], sys.argv[2], sys.argv[3]
VERSION, TAG = "1.1.0", "ios-v1.1.0"

state = {
    "versions": [],
    "locs": [{"id": "loc-en", "attributes": {"locale": "en-US", "whatsNew": ""}}],
    "polls": 0,
    "attached": None,
    "subs": [],
    "items": [],
    "submitted": False,
    "requests": [],
}
if scenario == "submitted":
    state["versions"].append({"type": "appStoreVersions", "id": "ver-1", "attributes": {
        "versionString": VERSION, "appVersionState": "WAITING_FOR_REVIEW", "releaseType": "AFTER_APPROVAL"}})
if scenario in ("foreign-draft", "own-draft"):
    state["subs"].append({"type": "reviewSubmissions", "id": "sub-1", "attributes": {"state": "READY_FOR_REVIEW"}})
    linked = {"type": "appStoreVersions", "id": "ver-1"} if scenario == "own-draft" else None
    state["items"].append({"type": "reviewSubmissionItems", "id": "item-0",
                           "relationships": {"appStoreVersion": {"data": linked}}})
    if scenario == "own-draft":
        state["versions"].append({"type": "appStoreVersions", "id": "ver-1", "attributes": {
            "versionString": VERSION, "appVersionState": "READY_FOR_REVIEW", "releaseType": "AFTER_APPROVAL"}})


def beta_build():
    return {"type": "builds", "id": "build-41", "attributes": {
        "version": "41", "processingState": "VALID", "buildAudienceType": "INTERNAL_ONLY",
        "expired": False, "usesNonExemptEncryption": False}}


def release_build():
    # Polls: 1 run running, 2 build not uploaded, 3 processing, 4+ valid.
    if state["polls"] < 3:
        return None
    audience = "INTERNAL_ONLY" if scenario == "internal" else "APP_STORE_ELIGIBLE"
    processing = "PROCESSING" if state["polls"] == 3 else "VALID"
    return {"type": "builds", "id": "build-42", "attributes": {
        "version": "42", "processingState": processing, "buildAudienceType": audience,
        "expired": False, "usesNonExemptEncryption": False}}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def send(self, code, obj=None):
        body = b"" if obj is None else json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
        with open(state_file, "w") as f:
            json.dump(state, f)

    def body(self):
        n = int(self.headers.get("Content-Length") or 0)
        return json.loads(self.rfile.read(n)) if n else None

    def handle_any(self, method):
        auth = self.headers.get("Authorization", "")
        if not (auth.startswith("Bearer ") and auth.count(".") == 2):
            return self.send(401, {"errors": [{"code": "NOT_AUTHORIZED", "title": "no JWT"}]})
        url = urlparse(self.path)
        q = {k: v[0] for k, v in parse_qs(url.query).items()}
        p = url.path
        b = self.body() if method in ("POST", "PATCH") else None
        state["requests"].append(f"{method} {p}")

        if method == "GET" and p == "/v1/apps":
            return self.send(200, {"data": [{"type": "apps", "id": "app-1", "attributes": {"bundleId": q.get("filter[bundleId]")}}]})
        if method == "GET" and p == "/v1/apps/app-1/appStoreVersions":
            return self.send(200, {"data": [v for v in state["versions"] if v["attributes"]["versionString"] == q["filter[versionString]"]]})
        if method == "POST" and p == "/v1/appStoreVersions":
            v = {"type": "appStoreVersions", "id": "ver-1",
                 "attributes": dict(b["data"]["attributes"], appVersionState="PREPARE_FOR_SUBMISSION")}
            state["versions"].append(v)
            return self.send(201, {"data": v})
        if method == "GET" and p == "/v1/appStoreVersions/ver-1/appStoreVersionLocalizations":
            return self.send(200, {"data": state["locs"]})
        if method == "PATCH" and p.startswith("/v1/appStoreVersionLocalizations/"):
            for loc in state["locs"]:
                if loc["id"] == p.rsplit("/", 1)[1]:
                    loc["attributes"].update(b["data"]["attributes"])
            return self.send(200, {"data": {}})
        if method == "POST" and p == "/v1/appStoreVersionLocalizations":
            state["locs"].append({"id": "loc-fr", "attributes": b["data"]["attributes"]})
            return self.send(201, {"data": {}})
        if method == "PATCH" and p == "/v1/appStoreVersions/ver-1":
            return self.send(200, {"data": {}})

        # Xcode Cloud
        if method == "GET" and p == "/v1/apps/app-1/ciProduct":
            return self.send(200, {"data": {"type": "ciProducts", "id": "prod-1"}})
        if method == "GET" and p == "/v1/ciProducts/prod-1/workflows":
            return self.send(200, {"data": [
                {"type": "ciWorkflows", "id": "wf-beta", "attributes": {"name": "Beta"}},
                {"type": "ciWorkflows", "id": "wf-release", "attributes": {"name": "Release"}}]})
        if method == "GET" and p == "/v1/ciWorkflows/wf-release/buildRuns":
            state["polls"] += 1
            runs = [{"type": "ciBuildRuns", "id": "run-41", "attributes": {
                "number": 41, "executionProgress": "COMPLETE", "completionStatus": "FAILED"},
                "relationships": {"sourceBranchOrTag": {"data": {"type": "scmGitReferences", "id": "ref-beta"}}}}]
            running = state["polls"] == 1
            runs.append({"type": "ciBuildRuns", "id": "run-42", "attributes": {
                "number": 42, "executionProgress": "RUNNING" if running else "COMPLETE",
                "completionStatus": None if running else "SUCCEEDED"},
                "relationships": {"sourceBranchOrTag": {"data": {"type": "scmGitReferences", "id": "ref-release"}}}})
            included = [
                {"type": "scmGitReferences", "id": "ref-beta", "attributes": {"kind": "TAG", "name": TAG + "-beta.1", "canonicalName": "refs/tags/" + TAG + "-beta.1"}},
                {"type": "scmGitReferences", "id": "ref-release", "attributes": {"kind": "TAG", "name": TAG, "canonicalName": "refs/tags/" + TAG}}]
            return self.send(200, {"data": runs, "included": included})
        if method == "GET" and p == "/v1/ciBuildRuns/run-42/builds":
            build = release_build()
            return self.send(200, {"data": [build] if build else []})

        if method == "GET" and p == "/v1/builds":
            if scenario == "number":
                state["polls"] += 1
            builds = [beta_build()]
            if release_build():
                builds.append(release_build())
            wanted = q.get("filter[version]")
            if q.get("filter[preReleaseVersion.version]") != VERSION or wanted is None:
                return self.send(400, {"errors": [{"code": "PARAMETER_ERROR", "title": "unexpected filter"}]})
            return self.send(200, {"data": [x for x in builds if x["attributes"]["version"] == wanted]})
        if method == "PATCH" and p == "/v1/appStoreVersions/ver-1/relationships/build":
            state["attached"] = b["data"]["id"]
            return self.send(204)

        if method == "GET" and p == "/v1/reviewSubmissions":
            return self.send(200, {"data": state["subs"]})
        if method == "POST" and p == "/v1/reviewSubmissions":
            sub = {"type": "reviewSubmissions", "id": "sub-1", "attributes": {"state": "READY_FOR_REVIEW"}}
            state["subs"].append(sub)
            return self.send(201, {"data": sub})
        if method == "GET" and p == "/v1/reviewSubmissions/sub-1/items":
            return self.send(200, {"data": state["items"]})
        if method == "POST" and p == "/v1/reviewSubmissionItems":
            state["items"].append({"type": "reviewSubmissionItems", "id": "item-1", "relationships": b["data"]["relationships"]})
            return self.send(201, {"data": {}})
        if method == "PATCH" and p == "/v1/reviewSubmissions/sub-1":
            state["submitted"] = b["data"]["attributes"]["submitted"]
            return self.send(200, {"data": {}})
        return self.send(404, {"errors": [{"code": "NOT_FOUND", "title": "not mocked", "detail": "SECRET-DETAIL " + p}]})

    def do_GET(self):
        self.handle_any("GET")

    def do_POST(self):
        self.handle_any("POST")

    def do_PATCH(self):
        self.handle_any("PATCH")


server = HTTPServer(("127.0.0.1", 0), Handler)
with open(port_file, "w") as f:
    f.write(str(server.server_address[1]))
server.serve_forever()
