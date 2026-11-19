#!/usr/bin/env python3
"""Checks the static site in site/ (standard library only).

    python3 site/tools/check_site.py                        # files on disk
    python3 site/tools/check_site.py --base-url http://127.0.0.1:8000
                                                            # also fetch every page and link
    python3 site/tools/check_site.py --set-repo-url https://github.com/owner/repo
                                                            # move every link to a new repository

Checks: every internal link and resource exists (fragments included), no
external resource is loaded (scripts, styles, images, fonts), every page has
lang, a title, one h1 and alt texts, tags are balanced, and every GitHub link
points to REPO_URL.
"""

from __future__ import annotations

import argparse
import re
import sys
import urllib.error
import urllib.request
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import urldefrag, urljoin, urlparse

# The repository of the project: the only place to change it (or use
# --set-repo-url, which rewrites the pages and this line).
REPO_URL = "https://github.com/tfournier/ReminisLink"

SITE = Path(__file__).resolve().parent.parent
SKIP_DIRS = {"tools"}
VOID = {"area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "source", "track", "wbr"}


class Page(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.links: list[tuple[str, str, dict]] = []  # (tag, url, attrs)
        self.ids: set[str] = set()
        self.stack: list[tuple[str, int]] = []
        self.errors: list[str] = []
        self.lang = None
        self.h1 = 0
        self.title = ""
        self._in_title = False

    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if tag == "html":
            self.lang = a.get("lang")
        if tag == "h1":
            self.h1 += 1
        if tag == "title":
            self._in_title = True
        if "id" in a:
            if a["id"] in self.ids:
                self.errors.append(f"duplicate id {a['id']!r}")
            self.ids.add(a["id"])
        if tag == "img" and "alt" not in a:
            self.errors.append(f"img without alt: {a.get('src')}")
        for attr in ("href", "src"):
            if attr in a and a[attr] is not None:
                self.links.append((tag, a[attr], a))
        if tag not in VOID:
            self.stack.append((tag, self.getpos()[0]))

    def handle_startendtag(self, tag, attrs):
        if tag not in VOID:
            self.errors.append(f"self-closing <{tag}/> at line {self.getpos()[0]}")
        self.handle_starttag(tag, attrs)
        if tag not in VOID and self.stack and self.stack[-1][0] == tag:
            self.stack.pop()

    def handle_endtag(self, tag):
        if tag == "title":
            self._in_title = False
        if tag in VOID:
            self.errors.append(f"end tag for void element </{tag}> at line {self.getpos()[0]}")
            return
        if not self.stack or self.stack[-1][0] != tag:
            open_tag = self.stack[-1] if self.stack else None
            self.errors.append(f"unexpected </{tag}> at line {self.getpos()[0]} (open: {open_tag})")
            # Recover: pop up to the matching tag if any.
            for i in range(len(self.stack) - 1, -1, -1):
                if self.stack[i][0] == tag:
                    del self.stack[i:]
                    break
            return
        self.stack.pop()

    def handle_data(self, data):
        if self._in_title:
            self.title += data


def pages() -> list[Path]:
    return sorted(p for p in SITE.rglob("*.html") if not (set(p.relative_to(SITE).parts) & SKIP_DIRS))


def url_path(page: Path) -> str:
    rel = page.relative_to(SITE).as_posix()
    if rel.endswith("index.html"):
        rel = rel[: -len("index.html")]
    return "/" + rel


def resolve(path: str) -> Path | None:
    target = SITE / path.lstrip("/")
    if path.endswith("/"):
        target = target / "index.html"
    elif target.is_dir():
        return None  # a directory without a trailing slash: GitHub Pages redirects, but be strict
    return target


def is_external(url: str) -> bool:
    return bool(urlparse(url).scheme) or url.startswith("//")


def check_files() -> tuple[list[str], dict[str, Page], set[str]]:
    errors: list[str] = []
    parsed: dict[str, Page] = {}
    for page in pages():
        p = Page()
        p.feed(page.read_text(encoding="utf-8"))
        p.close()
        parsed[url_path(page)] = p
    internal: set[str] = set()
    for base, p in parsed.items():
        where = f"{base}:"
        errors += [f"{where} {e}" for e in p.errors]
        errors += [f"{where} unclosed <{t}> from line {n}" for t, n in p.stack]
        if not p.lang:
            errors.append(f"{where} <html> has no lang")
        if not p.title.strip():
            errors.append(f"{where} no <title>")
        if p.h1 != 1:
            errors.append(f"{where} {p.h1} h1 elements (expected 1)")
        for tag, url, attrs in p.links:
            rel = (attrs.get("rel") or "").split()
            if is_external(url):
                if url.startswith("mailto:"):
                    continue
                # Only plain links may leave the site; canonical/alternate name our own URLs.
                if tag != "a" and not (tag == "link" and set(rel) & {"canonical", "alternate"}):
                    errors.append(f"{where} external resource {url}")
                if tag == "link" and set(rel) & {"canonical", "alternate"} and not url.startswith("https://reminis.link/"):
                    errors.append(f"{where} canonical/alternate outside reminis.link: {url}")
                if "github.com" in url and not url.startswith(REPO_URL):
                    errors.append(f"{where} GitHub link not under REPO_URL: {url}")
                continue
            full, frag = urldefrag(urljoin(base, url))
            path = urlparse(full).path
            if not path.startswith("/"):
                errors.append(f"{where} cannot resolve {url}")
                continue
            target = resolve(path)
            if target is None or not target.is_file():
                errors.append(f"{where} broken link {url}")
                continue
            internal.add(path)
            if frag:
                other = parsed.get(path)
                if other is None or frag not in other.ids:
                    errors.append(f"{where} missing anchor {url}")
    for name in ("CNAME", "404.html", "favicon.svg"):
        if not (SITE / name).is_file():
            errors.append(f"missing {name}")
    for f in SITE.rglob("*.css"):
        for m in re.finditer(r"url\(([^)]*)\)|@import", f.read_text(encoding="utf-8")):
            errors.append(f"{f.relative_to(SITE)}: external or imported resource in CSS: {m.group(0)}")
    return errors, parsed, internal


def check_http(base_url: str, paths: set[str]) -> list[str]:
    errors = []
    for path in sorted(paths):
        url = base_url.rstrip("/") + path
        try:
            with urllib.request.urlopen(url, timeout=10) as r:
                if r.status != 200:
                    errors.append(f"HTTP {r.status} for {url}")
        except urllib.error.HTTPError as e:
            errors.append(f"HTTP {e.code} for {url}")
        except OSError as e:
            errors.append(f"cannot fetch {url}: {e}")
    try:
        urllib.request.urlopen(base_url.rstrip("/") + "/does-not-exist/", timeout=10)
        errors.append("a missing page did not return 404")
    except urllib.error.HTTPError as e:
        if e.code != 404:
            errors.append(f"a missing page returned HTTP {e.code}")
    return errors


def set_repo_url(new: str) -> None:
    new = new.rstrip("/")
    if not re.fullmatch(r"https://github\.com/[\w.-]+/[\w.-]+", new):
        sys.exit(f"not a GitHub repository URL: {new}")
    for page in pages():
        text = page.read_text(encoding="utf-8")
        if REPO_URL in text:
            page.write_text(text.replace(REPO_URL, new), encoding="utf-8")
            print(f"updated {page.relative_to(SITE)}")
    # Visible texts show the URL without the scheme.
    old_short, new_short = REPO_URL.removeprefix("https://"), new.removeprefix("https://")
    for page in pages():
        text = page.read_text(encoding="utf-8")
        if old_short in text:
            page.write_text(text.replace(old_short, new_short), encoding="utf-8")
    me = Path(__file__)
    me.write_text(me.read_text(encoding="utf-8").replace(f'REPO_URL = "{REPO_URL}"', f'REPO_URL = "{new}"', 1), encoding="utf-8")
    print(f"REPO_URL is now {new}")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--base-url", help="also fetch every page and internal link from this server")
    ap.add_argument("--set-repo-url", metavar="URL", help="replace REPO_URL in every page and in this script")
    args = ap.parse_args()
    if args.set_repo_url:
        set_repo_url(args.set_repo_url)
        return 0
    errors, parsed, internal = check_files()
    if args.base_url:
        errors += check_http(args.base_url, internal | set(parsed))
    for e in errors:
        print(f"error: {e}")
    print(f"{len(parsed)} pages, {len(internal)} internal targets, {len(errors)} errors")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
