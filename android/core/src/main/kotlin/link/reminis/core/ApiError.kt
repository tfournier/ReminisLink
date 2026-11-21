package link.reminis.core

import kotlinx.serialization.json.JsonElement

/** Error codes of the contract (docs/specs/03-protocole.md §6). */
object ErrorCodes {
    const val INVALID_REQUEST = "invalid_request"
    const val INVALID_ID = "invalid_id"
    const val UNKNOWN_CLIENT = "unknown_client"
    const val BAD_SIGNATURE = "bad_signature"
    const val CLOCK_SKEW = "clock_skew"
    const val REPLAYED_REQUEST = "replayed_request"
    const val PAIRING_FAILED = "pairing_failed"
    const val INVALID_NAME = "invalid_name"
    const val NOT_FOUND = "not_found"
    const val CONFLICT = "conflict"
    const val CHECKSUM_MISMATCH = "checksum_mismatch"
    const val STATE_LOCKED = "state_locked"
    const val TOO_LARGE = "too_large"
    const val UNSUPPORTED_FORMAT = "unsupported_format"
    const val INSUFFICIENT_STORAGE = "insufficient_storage"
    const val TOO_MANY_ATTEMPTS = "too_many_attempts"
    const val DEVICE_BUSY = "device_busy"
    const val INTERNAL = "internal"
    const val SHUTTING_DOWN = "shutting_down"
    const val DISCONNECTED = "disconnected"
}

/** Every failure of [ApiClient]. The UI picks a translated text from the case and the code. */
sealed class ApiException(message: String, cause: Throwable? = null) : Exception(message, cause) {
    /** An error answer of the pak. */
    class Server(
        val status: Int,
        val code: String,
        val serverMessage: String,
        val details: Map<String, JsonElement> = emptyMap(),
        val retryAfter: Int? = null,
    ) : ApiException("HTTP $status $code: $serverMessage") {
        /**
         * A write that waited 10 s for the one in progress (`429 device_busy`):
         * nothing was changed. A pak from M13 answered `429 too_many_attempts`
         * with the message `Device busy` instead, still recognized.
         */
        val isDeviceBusy: Boolean
            get() = status == 429 &&
                (code == ErrorCodes.DEVICE_BUSY || (code == ErrorCodes.TOO_MANY_ATTEMPTS && serverMessage == LEGACY_DEVICE_BUSY))

        companion object {
            const val LEGACY_DEVICE_BUSY = "Device busy"
        }
    }

    class UnexpectedStatus(val status: Int) : ApiException("unexpected HTTP status $status")

    class InvalidResponse(detail: String, cause: Throwable? = null) : ApiException("invalid response: $detail", cause)

    /** The console could not be reached (no route, timeout, connection refused...). */
    class Network(cause: Throwable) : ApiException("network: ${cause.message}", cause)

    /** `/hello` answered for another console. */
    class DeviceMismatch(val expected: String, val actual: String) : ApiException("device mismatch")

    /** The pak speaks another API version, or needs a newer app. */
    class Incompatible(val apiVersion: Int, val minClientVersion: String) : ApiException("incompatible pak")

    /** A signed route without credentials. */
    class NotPaired : ApiException("not paired")

    class Pairing(val reason: PairingException.Reason) : ApiException("pairing: $reason")

    /** The console presented another certificate than the pinned one (card reformatted, or a fake pak). */
    class IdentityChanged : ApiException("certificate pin mismatch")
}
