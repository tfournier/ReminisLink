package transfer

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

// FetchTimeout bounds a synchronous small download (the cheat import from
// a URL), headers and body included.
const FetchTimeout = 60 * time.Second

// CodeUnsupportedFormat is answered when a synchronous download fails
// (docs/specs/03-protocole.md §6 has no gateway error; see FetchSmall).
const CodeUnsupportedFormat = "unsupported_format"

// FetchSmall downloads a small file synchronously (POST
// /games/{gameId}/cheats/import with a URL source): http and https only,
// the redirects and certificates of the downloads, at most limit bytes
// (413 too_large beyond). A missing remote file is 404 not_found; any
// other failure (network, HTTP error) is 422 unsupported_format, the
// content being unusable. The log only shows the scheme and the host of
// the URL, never its path or query.
func (s *Service) FetchSmall(ctx context.Context, raw string, limit int64) ([]byte, *Error) {
	u, apiErr := checkURL(raw)
	if apiErr != nil {
		return nil, apiErr
	}
	client, err := s.httpClient()
	if err != nil {
		s.log.Error("fetch: no HTTP client", "error", err)
		return nil, internal("No certificate store")
	}
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, badRequest("Invalid url")
	}
	req.Header.Set("User-Agent", "ReminisLink")
	fail := func(msg string) *Error {
		s.log.Warn("fetch failed", "url", safeURL(u), "error", msg)
		return newError(http.StatusUnprocessableEntity, CodeUnsupportedFormat, "Download failed: "+msg)
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, errTooManyRedirects) || errors.Is(err, errBadRedirect) {
			return nil, badRequest("Download failed: " + downloadErrorText(err))
		}
		return nil, fail(downloadErrorText(err))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		s.log.Warn("fetch failed", "url", safeURL(u), "status", resp.StatusCode)
		return nil, notFound("Download failed: HTTP " + strconv.Itoa(resp.StatusCode))
	case resp.StatusCode != http.StatusOK:
		return nil, fail("HTTP " + strconv.Itoa(resp.StatusCode))
	}
	if resp.ContentLength > limit {
		return nil, newError(http.StatusRequestEntityTooLarge, CodeTooLarge, "File too large")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fail(downloadErrorText(err))
	}
	if int64(len(data)) > limit {
		return nil, newError(http.StatusRequestEntityTooLarge, CodeTooLarge, "File too large")
	}
	s.log.Info("fetched", "url", safeURL(u), "bytes", len(data))
	return data, nil
}
