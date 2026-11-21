package link.reminis.app.ui

import android.content.Context
import android.text.format.Formatter
import link.reminis.app.R
import link.reminis.core.ApiException
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
        ErrorCodes.TOO_MANY_ATTEMPTS, ErrorCodes.DEVICE_BUSY -> context.getString(R.string.error_too_many_attempts)
        ErrorCodes.UNKNOWN_CLIENT -> context.getString(R.string.error_unknown_client)
        ErrorCodes.DISCONNECTED -> context.getString(R.string.error_disconnected)
        ErrorCodes.SHUTTING_DOWN -> context.getString(R.string.error_shutting_down)
        else -> context.getString(R.string.error_generic, error.code)
    }
    is java.security.GeneralSecurityException, is java.io.IOException -> context.getString(R.string.error_storage)
    else -> context.getString(R.string.error_generic, error.javaClass.simpleName)
}

fun formatBytes(context: Context, bytes: Long): String = Formatter.formatFileSize(context, bytes)
