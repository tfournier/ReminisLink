package link.reminis.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull

// Jobs of the console (protocol/openapi.yaml, `/jobs`): a patch, a pak
// installation. The app polls `GET /jobs/{jobId}` every second while a
// screen that follows it is visible (docs/specs/03-protocole.md §8).

@Serializable
data class JobAccepted(val jobId: String)

/** Kinds of a job, kept as strings (`Job.kind`): an unknown kind is kept. */
object JobKinds {
    const val PATCH = "patch"
    const val PAK_INSTALL = "pak-install"

    /** M19: `POST /archives/{stagingId}/import`. */
    const val ARCHIVE_IMPORT = "archive-import"
}

/** States of a job (`Job.state`). */
object JobStates {
    const val QUEUED = "queued"
    const val RUNNING = "running"
    const val SUCCEEDED = "succeeded"
    const val FAILED = "failed"
    const val CANCELLED = "cancelled"
}

/** `GET /jobs/{jobId}`. */
@Serializable
data class Job(
    val id: String,
    val kind: String,
    val state: String,
    val progress: Progress = Progress(0, null),
    val createdAt: String = "",
    val updatedAt: String = "",
    val result: JsonObject? = null,
    val error: ErrorEnvelope.Body? = null,
    val cancelRequested: Boolean? = null,
) {
    @Serializable
    data class Progress(val done: Long, val total: Long? = null) {
        /** 0..1, null when the total is unknown. */
        val fraction: Float? get() = total?.takeIf { it > 0 }?.let { (done.toFloat() / it).coerceIn(0f, 1f) }
    }

    /** The job will not change any more. An unknown state is taken as finished: never polled forever. */
    val isFinished: Boolean get() = state != JobStates.QUEUED && state != JobStates.RUNNING

    val succeeded: Boolean get() = state == JobStates.SUCCEEDED

    /** The error of a failed job, as a server error (status from the code, as the pak would answer it). */
    val serverError: ApiException.Server?
        get() = error?.let { ApiException.Server(statusFor(it.code), it.code, it.message, it.details.orEmpty()) }

    val resultGameId: String? get() = (result?.get("gameId") as? JsonPrimitive)?.contentOrNull

    /** `result.verified` of a patch job: false for IPS (no checksum); null when absent. */
    val resultVerified: Boolean? get() = (result?.get("verified") as? JsonPrimitive)?.booleanOrNull

    /** `result.pakId` of a pak installation. */
    val resultPakId: String? get() = (result?.get("pakId") as? JsonPrimitive)?.contentOrNull
}

/** The status the pak answers with an error code, for the error of a job. */
private fun statusFor(code: String) = when (code) {
    ErrorCodes.CONFLICT -> 409
    ErrorCodes.UNSUPPORTED_FORMAT, ErrorCodes.PATCH_MISMATCH -> 422
    ErrorCodes.INSUFFICIENT_STORAGE -> 507
    ErrorCodes.TOO_LARGE -> 413
    ErrorCodes.NOT_FOUND -> 404
    else -> 500
}

@Serializable
data class JobList(val jobs: List<Job>)

/** The console no longer knows a job (`404`): it was restarted and stopped it. */
class JobLostException : Exception("job lost")

/**
 * One poll of a job: its new state, or why it could not be read. A `404`
 * means the console restarted and forgot it ([JobLostException]); any
 * other error leaves the job going on (shown, polled again).
 */
suspend fun pollJob(client: suspend () -> ApiClient, jobId: String): Result<Job> = try {
    Result.success(client().job(jobId))
} catch (e: kotlinx.coroutines.CancellationException) {
    throw e
} catch (e: ApiException.Server) {
    Result.failure(if (e.status == 404) JobLostException() else e)
} catch (e: Exception) {
    Result.failure(e)
}
