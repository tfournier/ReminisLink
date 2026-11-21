package link.reminis.app.ui

import android.content.Context
import android.text.format.Formatter
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.longOrNull
import link.reminis.app.LocalFileException
import link.reminis.app.R
import link.reminis.app.UnreadablePictureException
import link.reminis.core.ApiException
import link.reminis.core.UploadFailure
import link.reminis.core.ConsoleNotFoundException
import link.reminis.core.ErrorCodes
import link.reminis.core.PairingException
import link.reminis.core.PairingLinkException

/** Pairing is closed on the console (`pairingOpen: false` in `/hello`). */
class PairingClosedException : Exception("pairing closed")

/** A translated message for any failure; the English server message only goes to the logs. */
fun errorMessage(context: Context, error: Throwable): String = when (error) {
    is ConsoleNotFoundException -> context.getString(R.string.error_not_found)
    is PairingClosedException -> context.getString(R.string.error_pairing_closed)
    is PairingLinkGoneException -> context.getString(R.string.error_link_gone)
    is PairingLinkException -> when (error.reason) {
        PairingLinkException.Reason.INVALID -> context.getString(R.string.error_invalid_link)
        PairingLinkException.Reason.OUTDATED_PAK -> context.getString(R.string.error_outdated_pak)
        PairingLinkException.Reason.NEWER_VERSION -> context.getString(R.string.error_newer_link)
    }
    is ApiException.IdentityChanged -> context.getString(R.string.error_identity_changed)
    is ApiException.Network -> context.getString(R.string.error_network)
    is ApiException.Incompatible -> context.getString(R.string.error_incompatible)
    is ApiException.DeviceMismatch -> context.getString(R.string.error_device_mismatch)
    is ApiException.NotPaired -> context.getString(R.string.error_unknown_client)
    is ApiException.Pairing -> context.getString(R.string.error_pairing_proof)
    is PairingException -> context.getString(R.string.error_pairing_proof)
    is ApiException.Server -> when (error.code) {
        ErrorCodes.PAIRING_FAILED -> context.getString(R.string.error_pairing_failed)
        ErrorCodes.DEVICE_BUSY -> context.getString(R.string.error_device_busy, error.retryAfter ?: 10)
        ErrorCodes.TOO_MANY_ATTEMPTS ->
            // A pak from M13 sent too_many_attempts with the message "Device busy".
            if (error.serverMessage == "Device busy") {
                context.getString(R.string.error_device_busy, error.retryAfter ?: 10)
            } else {
                context.getString(R.string.error_too_many_attempts)
            }
        ErrorCodes.UNKNOWN_CLIENT -> context.getString(R.string.error_unknown_client)
        ErrorCodes.DISCONNECTED -> context.getString(R.string.error_disconnected)
        ErrorCodes.SHUTTING_DOWN -> context.getString(R.string.error_shutting_down)
        ErrorCodes.CONFLICT -> context.getString(R.string.error_conflict)
        ErrorCodes.CHECKSUM_MISMATCH -> context.getString(R.string.error_checksum)
        ErrorCodes.STATE_LOCKED -> context.getString(R.string.error_state_locked)
        ErrorCodes.TOO_LARGE -> context.getString(R.string.error_too_large)
        ErrorCodes.INVALID_NAME -> context.getString(R.string.error_invalid_name)
        ErrorCodes.UNSUPPORTED_FORMAT -> context.getString(R.string.error_unsupported_format)
        ErrorCodes.NOT_FOUND -> context.getString(R.string.error_item_not_found)
        ErrorCodes.INSUFFICIENT_STORAGE -> {
            val required = (error.details["required"] as? JsonPrimitive)?.longOrNull
            val available = (error.details["available"] as? JsonPrimitive)?.longOrNull
            if (required != null && available != null && required > available) {
                context.getString(R.string.error_insufficient_storage_missing, formatBytes(context, required - available))
            } else {
                context.getString(R.string.error_insufficient_storage)
            }
        }
        else -> context.getString(R.string.error_generic, error.code)
    }
    is UploadFailure -> when (error.reason) {
        UploadFailure.Reason.OUTCOME_UNKNOWN -> context.getString(R.string.error_outcome_unknown)
        UploadFailure.Reason.CANCEL_NOT_CONFIRMED -> context.getString(R.string.error_cancel_not_confirmed)
        UploadFailure.Reason.TOO_MANY_RESTARTS -> context.getString(R.string.error_too_many_restarts)
        UploadFailure.Reason.FILE_CHANGED -> context.getString(R.string.error_file)
        UploadFailure.Reason.PAUSED -> context.getString(R.string.transfer_paused)
    }
    is LocalFileException, is UnreadablePictureException -> context.getString(R.string.error_file)
    is link.reminis.core.PartialDocumentException -> context.getString(R.string.error_partial_document)
    is link.reminis.core.ConsoleForgottenException -> context.getString(R.string.error_console_forgotten)
    is java.security.GeneralSecurityException, is java.io.IOException -> context.getString(R.string.error_storage)
    else -> context.getString(R.string.error_generic, error.javaClass.simpleName)
}

fun formatBytes(context: Context, bytes: Long): String = Formatter.formatFileSize(context, bytes)
