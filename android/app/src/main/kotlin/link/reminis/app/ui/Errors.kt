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
import link.reminis.core.JobLostException
import link.reminis.core.PakJobFailure
import link.reminis.core.PakRefusal
import link.reminis.core.PatchJobFailure
import link.reminis.core.isPakSelfConflict
import link.reminis.core.pakRefusal
import link.reminis.core.patchMismatchReason
import link.reminis.core.trashReplaceRefusal
import link.reminis.core.isMissingRoute
import link.reminis.core.renameConflicts
import link.reminis.core.renameSharesDiscs
import link.reminis.core.sdChanged
import link.reminis.core.sdProtection

/** Pairing is closed on the console (`pairingOpen: false` in `/hello`). */
class PairingClosedException : Exception("pairing closed")

/** A failure whose text depends on where it happened (a patch, a pak, a restore): already chosen. */
class UserMessage(@param:androidx.annotation.StringRes val id: Int, val args: List<Any> = emptyList(), cause: Throwable? = null) :
    Exception("user message", cause)

/** The error of a patch request (M6): the console's refusals get their own texts. */
fun patchError(error: Throwable): Throwable {
    val server = when (error) {
        is PatchJobFailure -> {
            when (error.server.code) {
                ErrorCodes.CONFLICT -> return UserMessage(R.string.patch_error_rom_changed, cause = error)
                ErrorCodes.TOO_LARGE -> return UserMessage(R.string.patch_error_too_large, cause = error)
            }
            error.server
        }
        is ApiException.Server -> error
        else -> return error
    }
    return when (server.code) {
        ErrorCodes.PATCH_MISMATCH -> UserMessage(
            if (server.patchMismatchReason == "already-patched") R.string.patch_error_already else R.string.patch_error_mismatch,
            cause = error,
        )
        ErrorCodes.UNSUPPORTED_FORMAT -> UserMessage(R.string.patch_error_unsupported, cause = error)
        else -> server
    }
}

/**
 * The error of a pak action (M12): a refused archive gets its reason,
 * ReminisLink itself its own text; a `404` on the list ([routeMissing])
 * means an older pak.
 */
fun pakError(error: Throwable, routeMissing: Boolean = false): Throwable {
    val server = (error as? PakJobFailure)?.server ?: error as? ApiException.Server
    server?.pakRefusal?.let { return PakRefusalException(it) }
    if (server?.isPakSelfConflict == true) return UserMessage(R.string.pak_error_self, cause = error)
    if (routeMissing) {
        val status = (error as? ApiException.UnexpectedStatus)?.status ?: server?.status
        if (status == 404 || status == 405) return UserMessage(R.string.pak_error_needs_newer, cause = error)
    }
    return server ?: error
}

/** Carries a pak refusal to [errorMessage]. */
class PakRefusalException(val refusal: PakRefusal) : Exception("pak refused")

/** The error of a restore from the trash (M18): a `404` (or an older pak's `405`) is a gone entry. */
fun trashRestoreError(error: Throwable): Throwable {
    val server = error as? ApiException.Server
    if (server?.trashReplaceRefusal != null) return UserMessage(R.string.trash_replace_refused, cause = error)
    server?.pakRefusal?.let { return PakRefusalException(it) }
    val status = (error as? ApiException.UnexpectedStatus)?.status ?: server?.status
    if (status == 404 || status == 405) return UserMessage(R.string.trash_entry_gone, cause = error)
    return error
}

/**
 * The error of an archive action (M19): a refused archive, a ROM name
 * already taken, or ([routeMissing], the export) a pak older than M19, a
 * game gone or with nothing to export.
 */
fun archiveError(error: Throwable, routeMissing: Boolean = false): Throwable {
    if (error is link.reminis.core.ArchivesNeedNewerPakException) return UserMessage(R.string.archive_needs_newer, cause = error)
    val server = (error as? link.reminis.core.ArchiveJobFailure)?.server ?: error as? ApiException.Server
    if (routeMissing) {
        // A pak older than M19 has no such route (`404 No such route`, or `405`); a `404` of the
        // route itself is the game, or a game with nothing to export.
        val status = (error as? ApiException.UnexpectedStatus)?.status ?: server?.status
        if (status == 405 || (status == 404 && server?.serverMessage?.contains("route", ignoreCase = true) != false)) {
            return UserMessage(R.string.archive_needs_newer, cause = error)
        }
        if (status == 404) return UserMessage(if (server?.serverMessage == "Nothing to export") R.string.archive_empty else R.string.error_item_not_found, cause = error)
    }
    return when {
        server?.code == ErrorCodes.UNSUPPORTED_FORMAT -> UserMessage(R.string.archive_invalid, cause = error)
        server?.code == ErrorCodes.CONFLICT && server.status == 409 -> UserMessage(R.string.archive_game_exists, cause = error)
        server != null && error is link.reminis.core.ArchiveJobFailure -> server
        else -> error
    }
}

/** The text of a protected place of the SD card (M23, `details.protected`). */
fun sdProtectionMessage(protection: String, cause: Throwable? = null): UserMessage = UserMessage(
    when (protection) {
        link.reminis.core.SdProtection.SYSTEM -> R.string.sd_protected_system
        link.reminis.core.SdProtection.DATA -> R.string.sd_protected_data
        link.reminis.core.SdProtection.ROOT -> R.string.sd_protected_root
        link.reminis.core.SdProtection.LINK -> R.string.sd_protected_link
        else -> R.string.sd_protected_other
    },
    cause = cause,
)

/**
 * The error of a write of the SD browser (M23): a protected place, a taken
 * name, an item changed or gone, or an older pak (`405`, or the `404` "No
 * route" of a route it does not have).
 */
fun sdWriteError(error: Throwable): Throwable {
    if (error is UserMessage) return error
    val server = error as? ApiException.Server
    server?.sdProtection?.let { return sdProtectionMessage(it, error) }
    if (server != null && server.sdChanged.isNotEmpty()) return UserMessage(R.string.sd_entry_changed, cause = error)
    if (server?.code == ErrorCodes.CONFLICT) return UserMessage(R.string.sd_name_taken, cause = error)
    val status = (error as? ApiException.UnexpectedStatus)?.status ?: server?.status
    if (status == 405 || server?.isMissingRoute == true) return UserMessage(R.string.files_need_newer, cause = error)
    if (status == 404) return UserMessage(R.string.sd_entry_gone, cause = error)
    return error
}

/** The error of the rename of a game (M23). */
fun renameError(error: Throwable): Throwable {
    if (error is UserMessage) return error
    val server = error as? ApiException.Server
    if (server != null && server.renameSharesDiscs) return UserMessage(R.string.rename_shared, cause = error)
    if (server?.code == ErrorCodes.CONFLICT) {
        val places = server.renameConflicts
        return if (places.isEmpty()) {
            UserMessage(R.string.rename_taken, cause = error)
        } else {
            UserMessage(R.string.rename_taken_places, listOf(places.joinToString(", ")), cause = error)
        }
    }
    if (server?.code == ErrorCodes.INVALID_NAME) return UserMessage(R.string.rename_invalid, cause = error)
    if (server?.code == ErrorCodes.UNSUPPORTED_FORMAT) return server
    if (server?.isMissingRoute == true) return UserMessage(R.string.files_need_newer, cause = error)
    if (server?.code == ErrorCodes.NOT_FOUND) return UserMessage(R.string.error_item_not_found, cause = error)
    val status = (error as? ApiException.UnexpectedStatus)?.status ?: server?.status
    if (status == 404 || status == 405) return UserMessage(R.string.files_need_newer, cause = error)
    return error
}

/** What is wrong with a new game name, said before sending (M23, `GameNameRule`). */
fun nameProblemMessage(problem: link.reminis.core.GameNameRule.Problem): UserMessage = when (problem) {
    link.reminis.core.GameNameRule.Problem.Empty -> UserMessage(R.string.name_empty)
    is link.reminis.core.GameNameRule.Problem.ForbiddenCharacter -> UserMessage(R.string.name_forbidden, listOf(problem.character.toString()))
    link.reminis.core.GameNameRule.Problem.InvisibleCharacter -> UserMessage(R.string.name_invisible)
    link.reminis.core.GameNameRule.Problem.StartsWithDot -> UserMessage(R.string.name_dot)
    link.reminis.core.GameNameRule.Problem.TrailingSpaceOrDot -> UserMessage(R.string.name_trailing)
    link.reminis.core.GameNameRule.Problem.Reserved -> UserMessage(R.string.name_reserved)
    link.reminis.core.GameNameRule.Problem.TooLong -> UserMessage(R.string.name_too_long)
}

/** A screenshot too large to show here (more than 64 MiB, or no room left on the phone): share it instead. */
class ScreenshotTooLargeException(cause: Throwable? = null) : Exception("screenshot too large", cause)

/**
 * The error of a screenshot route (M22): a `404` on the list (or an older
 * pak's `405`) means a pak older than M22, on one screenshot that it left
 * the console meanwhile.
 */
fun screenshotsError(error: Throwable, list: Boolean = false): Throwable {
    if (error is link.reminis.core.ScreenshotsPartlyMovedException) return error
    val server = error as? ApiException.Server
    val status = (error as? ApiException.UnexpectedStatus)?.status ?: server?.status
    if (list && (status == 404 || status == 405)) return UserMessage(R.string.screenshots_needs_newer, cause = error)
    if (!list && status == 404) return UserMessage(R.string.screenshot_gone, cause = error)
    return error
}

private fun pakRefusalMessage(context: Context, refusal: PakRefusal): String = when (refusal) {
    PakRefusal.NotAnArchive -> context.getString(R.string.pak_refused_not_archive)
    PakRefusal.NoPak -> context.getString(R.string.pak_refused_no_pak)
    PakRefusal.NoLaunchScript -> context.getString(R.string.pak_refused_no_launch)
    PakRefusal.SeveralPaks -> context.getString(R.string.pak_refused_several)
    PakRefusal.UnsafeEntry -> context.getString(R.string.pak_refused_unsafe)
    PakRefusal.TooLarge -> context.getString(R.string.pak_refused_too_large)
    PakRefusal.UnsupportedCompression -> context.getString(R.string.pak_refused_compression)
    PakRefusal.NoName -> context.getString(R.string.pak_refused_no_name)
    is PakRefusal.Other -> context.getString(R.string.pak_refused_other, refusal.reason)
}

/** A translated message for any failure; the English server message only goes to the logs. */
fun errorMessage(context: Context, error: Throwable): String = when (error) {
    is UserMessage -> context.getString(error.id, *error.args.toTypedArray())
    is link.reminis.core.vault.SyncChecksumException, is link.reminis.core.vault.VaultUnavailableException,
    is link.reminis.core.vault.SaveVaultException -> syncErrorMessage(context, error)!!
    is link.reminis.core.ScreenshotsPartlyMovedException ->
        context.resources.getQuantityString(R.plurals.screenshots_partly_moved, error.moved, error.moved)
    is ScreenshotTooLargeException -> context.getString(R.string.screenshot_too_large)
    is link.reminis.core.ScreenshotChangedException -> context.getString(R.string.screenshot_changed)
    is link.reminis.core.ArchivesNeedNewerPakException -> context.getString(R.string.archive_needs_newer)
    is link.reminis.core.ArchiveJobFailure -> errorMessage(context, archiveError(error))
    is PakRefusalException -> pakRefusalMessage(context, error.refusal)
    is PatchJobFailure -> errorMessage(context, patchError(error))
    is PakJobFailure -> errorMessage(context, pakError(error))
    is JobLostException -> context.getString(R.string.error_job_lost)
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
        ErrorCodes.CONFLICT -> when {
            error.status == 412 -> context.getString(R.string.cheats_conflict_short)
            error.sdProtection != null -> errorMessage(context, sdProtectionMessage(error.sdProtection!!))
            else -> context.getString(R.string.error_conflict)
        }
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
