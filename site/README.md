# reminis.link

The website of ReminisLink: plain HTML and CSS, no framework, no build step,
no JavaScript, no cookies, no trackers and nothing loaded from another site
(system fonts only). It is published at <https://reminis.link> by
[`.github/workflows/site.yml`](../.github/workflows/site.yml).

| URL | File |
|---|---|
| `/`, `/fr/` | `index.html`, `fr/index.html` (home) |
| `/install/`, `/fr/install/` | installation guide: pak, app, pairing, save sync, update, uninstall |
| `/privacy/`, `/fr/privacy/` | privacy policy (same text as [`docs/privacy.md`](../docs/privacy.md)) |
| `/support/`, `/fr/support/` | support: FAQ, how to report a problem |
| any other path | `404.html` |

`assets/` holds the stylesheet, the icon (from
`ios/ReminisLink/Resources/Assets.xcassets/AppIcon.appiconset/AppIcon.png`,
resized with `sips -z 256 256`) and screenshots of the pak, copied from
`docs/screenshots/` (`pairing.png`, `settings.png` and `language.png`, as
`pak-pairing.png`, `pak-settings.png` and `pak-language.png`). Copy them
again after `make screenshots`. `favicon.svg` is `ios/Design/AppIcon/icon.svg`.
`tools/` and this README are not published.

## URLs for App Store Connect

| Field | URL |
|---|---|
| Privacy Policy URL | `https://reminis.link/privacy/` |
| Support URL | `https://reminis.link/support/` |
| Marketing URL (optional) | `https://reminis.link/` |

The French App Store page can use `https://reminis.link/fr/privacy/` and
`https://reminis.link/fr/support/` (each page also links to the other
language).

## Preview and check

```sh
python3 -m http.server 8000 --bind 127.0.0.1 --directory site
# open http://127.0.0.1:8000/
python3 site/tools/check_site.py                                   # files on disk
python3 site/tools/check_site.py --base-url http://127.0.0.1:8000  # and over HTTP
```

`check_site.py` fails on a broken internal link or anchor, an external
resource (script, stylesheet, image, font), a page without `lang`, title, a
single `h1` or alt texts, unbalanced tags, and a GitHub link that is not under
the repository URL. The workflow runs it before publishing. Python's server
does not serve `404.html` for missing pages, GitHub Pages does.

## To do before the first publication

### 1. Contact e-mail

Apple asks for a way to contact the developer on the support page. The
GitHub issues link is there; to add an e-mail address, search for
`CONTACT EMAIL` in `support/index.html` and `fr/support/index.html`: replace
`contact@example.invalid` with the address and remove the comment markers
around the paragraph.

### 2. Repository URL

The pages link to `https://github.com/tfournier/ReminisLink`. If the
repository has another address, run:

```sh
python3 site/tools/check_site.py --set-repo-url https://github.com/<owner>/<repo>
```

It rewrites every page and the `REPO_URL` constant of the script, the single
place where the address is defined. (`docs/privacy.md` and the iOS READMEs
quote it too.)

### 3. GitHub Pages

1. Push the branch and merge it into `main`.
2. In the repository: *Settings > Pages > Build and deployment > Source*:
   **GitHub Actions**.
3. Run the workflow once (*Actions > Site > Run workflow*) or push a change
   under `site/`.
4. *Settings > Pages > Custom domain*: `reminis.link`, then *Save*. GitHub
   checks the DNS (step 4). With a deployment by GitHub Actions the custom
   domain is this setting; `site/CNAME` is kept as a record of it.
5. Optional but recommended: verify the domain for your account
   (*GitHub Settings > Pages > Add a domain*, a TXT record), so nobody else
   can use it for their Pages.

### 4. DNS of reminis.link

At the registrar of the domain, for the apex `reminis.link`:

| Type | Name | Value |
|---|---|---|
| A | `@` | `185.199.108.153` |
| A | `@` | `185.199.109.153` |
| A | `@` | `185.199.110.153` |
| A | `@` | `185.199.111.153` |
| AAAA | `@` | `2606:50c0:8000::153` |
| AAAA | `@` | `2606:50c0:8001::153` |
| AAAA | `@` | `2606:50c0:8002::153` |
| AAAA | `@` | `2606:50c0:8003::153` |
| CNAME | `www` | `<github user>.github.io.` |

Remove any other A, AAAA or ALIAS record of the apex (the parking page of the
registrar, for example). These are the addresses documented by GitHub
(*Managing a custom domain for your GitHub Pages site*): check them there
before you enter them. With `www` pointing to GitHub, GitHub redirects
`www.reminis.link` to `reminis.link`.

Check:

```sh
dig +short reminis.link A
dig +short reminis.link AAAA
dig +short www.reminis.link CNAME
```

### 5. HTTPS

Once the DNS check passes in *Settings > Pages* (it can take up to 24 hours),
GitHub requests a Let's Encrypt certificate; then tick **Enforce HTTPS**.
Give the URLs to App Store Connect only when `https://reminis.link/privacy/`
opens without warning.

## Rules for the content

- No game names, no source of games, no third-party logo. TrimUI and NextUI
  are named only as what the app works with, with the non-affiliation notice.
- The privacy page says the same as `docs/privacy.md`: change both, and the
  date, together.
- English and French pages have the same content; each links to the other.
