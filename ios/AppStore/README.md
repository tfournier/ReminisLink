# App Store texts

The texts of the App Store page, one folder per language (`en-US`, `fr-FR`),
one file per field (the layout of `fastlane deliver`, so they can be pasted
by hand or uploaded by a tool later). Nothing here is sent automatically.

| File | App Store Connect field | Limit |
|---|---|---|
| `name.txt` | Name | 30 characters |
| `subtitle.txt` | Subtitle | 30 characters |
| `promotional_text.txt` | Promotional Text | 170 characters |
| `description.txt` | Description | 4000 characters |
| `keywords.txt` | Keywords (comma-separated, no space after the commas) | 100 bytes |
| `release_notes.txt` | What's New in This Version | 4000 characters |
| `review_notes.txt` | App Review Information > Notes (English only) | 4000 characters |

Rules (docs/specs/01-vision-perimetre.md):

- The description says that users bring their own files. No text names,
  suggests or links to a source of games.
- No brand of a third party in the name, subtitle or keywords (App Review
  guideline 2.3.7). The description names TrimUI and NextUI only to say what
  the app works with, with a notice that they are not affiliated.

Other fields to fill in App Store Connect:

- Privacy Policy URL: <https://reminis.link/privacy/> (French page:
  <https://reminis.link/fr/privacy/>), the published `docs/privacy.md`
  (see `site/README.md`).
- App Privacy: "Data Not Collected".
- Suggested: category Utilities; age rating 4+ (the app shows no content
  from others and has no web browser). To confirm when filling the form.
- Support URL: <https://reminis.link/support/> (French page:
  <https://reminis.link/fr/support/>).
- Marketing URL (optional): <https://reminis.link/>.

Check the lengths after an edit:

```sh
for f in ios/AppStore/*/*.txt; do printf '%-40s %5s chars %5s bytes\n' "$f" \
  "$(tr -d '\n' < "$f" | LC_ALL=en_US.UTF-8 wc -m)" "$(tr -d '\n' < "$f" | wc -c)"; done
```
