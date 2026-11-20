// Package transfer moves files onto the SD card: resumable uploads from the
// app (docs/specs/03-protocole.md §7), the queue of long jobs (§8), and the installation of a received file at
// its destination with the conflict rules (docs/specs/04-api-v1.md,
// Transferts).
package transfer

import (
	"fmt"
	"net/http"
)

// Error codes used by this package (docs/specs/03-protocole.md §6).
const (
	CodeInvalidRequest      = "invalid_request"
	CodeInvalidID           = "invalid_id"
	CodeInvalidName         = "invalid_name"
	CodeNotFound            = "not_found"
	CodeConflict            = "conflict"
	CodeChecksumMismatch    = "checksum_mismatch"
	CodeStateLocked         = "state_locked"
	CodeTooLarge            = "too_large"
	CodeInternal            = "internal"
	CodeShuttingDown        = "shutting_down"
	CodeInsufficientStorage = "insufficient_storage"
	CodeTooManyAttempts     = "too_many_attempts"
	CodeDeviceBusy          = "device_busy"
	CodeUnsupportedFormat   = "unsupported_format"
)

// Error is an error the API reports as it is: HTTP status, code, English
// message and details. It never holds an absolute path of the console.
type Error struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *Error) Error() string { return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message) }

// Body returns the "error" object of the API.
func (e *Error) Body() ErrorBody {
	d := e.Details
	if d == nil {
		d = map[string]any{}
	}
	return ErrorBody{Code: e.Code, Message: e.Message, Details: d}
}

// ErrorBody is the "error" object of an error response, also used as the
// error of a failed job.
type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

func newError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func badRequest(message string) *Error {
	return newError(http.StatusBadRequest, CodeInvalidRequest, message)
}

func invalidName(message string) *Error {
	return newError(http.StatusBadRequest, CodeInvalidName, message)
}

func notFound(message string) *Error { return newError(http.StatusNotFound, CodeNotFound, message) }

func internal(message string) *Error {
	return newError(http.StatusInternalServerError, CodeInternal, message)
}

func insufficientStorage(required, available int64) *Error {
	return &Error{
		Status: http.StatusInsufficientStorage, Code: CodeInsufficientStorage, Message: "Not enough free space",
		Details: map[string]any{"required": required, "available": max(available, 0)},
	}
}
