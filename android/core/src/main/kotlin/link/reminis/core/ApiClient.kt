package link.reminis.core

import java.io.IOException
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resumeWithException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerializationException
import kotlinx.serialization.encodeToString
import okhttp3.Call
import okhttp3.Callback
import okhttp3.HttpUrl
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okio.HashingSink
import okio.blackholeSink
import okio.buffer

/** What a signed client needs for one paired console: the identity that signs and the console's pin. */
class DeviceCredentials(val deviceId: String, val clientId: String, val key: Ed25519Key, val pin: String)

/**
 * Client of the pak API (protocol/openapi.yaml), mirror of `APIClient.swift`.
 *
 * Every request goes over HTTPS and the console must present the certificate
 * pinned at pairing ([PinnedTrustManager]): never system trust. Every route
 * but `/hello` and `/pair` is signed (RL2-ED25519) by [SigningInterceptor],
 * with a new nonce for every attempt. After `401 clock_skew`, the client asks
 * `/hello` for the console time, corrects its offset and retries once.
 */
class ApiClient(
    val endpoint: Endpoint,
    val expectedDeviceId: String? = null,
    private val credentials: DeviceCredentials? = null,
    pin: String? = credentials?.pin,
    /** Compared with `minClientVersion` of `/hello` (M17: a client protocol version, not the app version). */
    private val clientProtocolVersion: String = CLIENT_PROTOCOL_VERSION,
    baseClient: OkHttpClient = defaultBaseClient,
    private val nowMillis: () -> Long = System::currentTimeMillis,
) {
    /** Console time minus local time, in seconds, measured on the last `/hello`. */
    @Volatile
    var clockOffsetSeconds: Long = 0
        private set

    private val signer = credentials?.let { Signer(it.clientId, it.key) }
    private val trust = PinnedTrustManager(pin)

    internal val http: OkHttpClient = baseClient.newBuilder()
        .sslSocketFactory(trust.socketFactory(), trust)
        .hostnameVerifier(PinnedTrustManager.anyHostname)
        // Never leave the pinned connection: a redirect could go to plain
        // HTTP with the X-RL-* headers, or reuse a signature for another path.
        .followRedirects(false)
        .followSslRedirects(false)
        .addInterceptor(SigningInterceptor({ signer }, { nowMillis() / 1000 + clockOffsetSeconds }))
        .addNetworkInterceptor { chain ->
            chain.request().tag(BeforeSend::class.java)?.action?.invoke()
            chain.proceed(chain.request())
        }
        .build()

    // MARK: unsigned routes

    /** `GET /hello`: checks the device id and the compatibility, and measures the clock offset. */
    suspend fun hello(): Hello {
        val response = execute(request("GET", listOf("hello")))
        val hello: Hello = decode(response, accept = 200)
        clockOffsetSeconds = parseRfc3339Seconds(hello.serverTime)?.let { it - nowMillis() / 1000 } ?: 0
        if (expectedDeviceId != null && hello.deviceId != expectedDeviceId) {
            throw ApiException.DeviceMismatch(expectedDeviceId, hello.deviceId)
        }
        if (hello.apiVersion != API_VERSION || !AppVersion.isSupported(clientProtocolVersion, hello.minClientVersion)) {
            throw ApiException.Incompatible(hello.apiVersion, hello.minClientVersion)
        }
        return hello
    }

    /**
     * `POST /pair`, then checks the device proof. Store the pairing only when
     * this returns.
     *
     * [beforeSend] runs on the connected, pinned connection right before the
     * request is written: the exact point from which the console may pair.
     * Throwing an [IOException] there aborts without sending anything.
     */
    suspend fun pair(attempt: PairingAttempt, beforeSend: (() -> Unit)? = null): PairResponse {
        val body = ProtocolJson.encodeToString(attempt.request).toRequestBody(JSON)
        var request = request("POST", listOf("pair"), body = body)
        if (beforeSend != null) request = request.newBuilder().tag(BeforeSend::class.java, BeforeSend(beforeSend)).build()
        val response: PairResponse = decode(execute(request), accept = 200)
        try {
            attempt.finish(response)
        } catch (e: PairingException) {
            throw ApiException.Pairing(e.reason)
        }
        return response
    }

    // MARK: signed routes

    suspend fun device(): DeviceStatus = signedJson("GET", listOf("device"))

    suspend fun systems(): List<GameSystem> = signedJson<SystemList>("GET", listOf("systems")).systems

    /** `GET /games` (M11): every game, in one flat list. */
    suspend fun allGames(): AllGames = signedJson("GET", listOf("games"))

    suspend fun game(id: String): GameDetail = signedJson("GET", listOf("games", id))

    // MARK: box art (M8)

    /** `PUT /games/{gameId}/art`: a PNG of at most 4 MiB and 1024 x 1024 (signed body). */
    suspend fun setGameArt(gameId: String, png: ByteArray): ArtGame =
        signedJson("PUT", listOf("games", gameId, "art"), body = png.toRequestBody(PNG))

    /** `DELETE /games/{gameId}/art`: the art goes to the trash of the console. */
    suspend fun deleteGameArt(gameId: String): ArtGame = signedJson("DELETE", listOf("games", gameId, "art"))

    // MARK: uploads (docs/specs/03-protocole.md §7)

    suspend fun createUpload(request: CreateUploadRequest): UploadCreated = signedJson(
        "POST", listOf("uploads"), body = request.toJson().toRequestBody(JSON), accept = 201,
    )

    suspend fun uploadStatus(id: String): UploadStatus = signedJson("GET", listOf("uploads", id))

    /**
     * `PUT /uploads/{id}`: one chunk, read from [file] at [offset]. The only
     * request whose body is `UNSIGNED` (the whole file is checked against
     * the SHA-256 announced at creation). Returns the new offset.
     *
     * @param onSent bytes of this chunk handed to the network so far.
     */
    suspend fun sendChunk(
        uploadId: String,
        offset: Long,
        file: java.io.File,
        length: Long,
        onSent: ((Long) -> Unit)? = null,
    ): Long {
        val response = signedResponse {
            request("PUT", listOf("uploads", uploadId), body = FileRegionBody(file, offset, length, onSent), signed = true)
                .newBuilder()
                .header("Upload-Offset", offset.toString())
                .tag(UnsignedBody::class.java, UnsignedBody)
                .build()
        }
        return response.use {
            if (it.code != 204) throw serverError(it)
            it.header("Upload-Offset")?.toLongOrNull() ?: (offset + length)
        }
    }

    suspend fun cancelUpload(id: String) = signedEmpty("DELETE", listOf("uploads", id))

    suspend fun completeUpload(id: String): UploadResult = signedJson("POST", listOf("uploads", id, "complete"))

    // MARK: saves (M5)

    suspend fun saveBackups(gameId: String): List<Backup> =
        signedJson<BackupList>("GET", listOf("games", gameId, "save", "backups")).backups

    /** `POST /games/{gameId}/save/backups`: a manual backup copy, with an optional label. */
    suspend fun createSaveBackup(gameId: String, label: String?): Backup = signedJson(
        "POST", listOf("games", gameId, "save", "backups"),
        body = ProtocolJson.encodeToString(CreateBackupRequest(label)).toRequestBody(JSON), accept = 201,
    )

    suspend fun restoreSaveBackup(gameId: String, backupId: String): BackupResult =
        signedJson("POST", listOf("games", gameId, "save", "backups", backupId, "restore"))

    /** The backup copy goes to the trash of the console. */
    suspend fun deleteSaveBackup(gameId: String, backupId: String) =
        signedEmpty("DELETE", listOf("games", gameId, "save", "backups", backupId))

    /**
     * `PUT /games/{gameId}/save` (at most [MAX_SAVE_BODY_BYTES], larger
     * saves go through an upload). The body is streamed from [file] and
     * signed like any other.
     */
    suspend fun replaceSave(gameId: String, file: java.io.File): BackupResult =
        signedJson("PUT", listOf("games", gameId, "save"), body = FileRegionBody(file, 0, file.length(), null))

    // MARK: states (M5)

    suspend fun states(gameId: String): SlotList = signedJson("GET", listOf("games", gameId, "states"))

    suspend fun lockState(gameId: String, slot: Int): Slot =
        signedJson("PUT", listOf("games", gameId, "states", slot.toString(), "lock"))

    suspend fun unlockState(gameId: String, slot: Int): Slot =
        signedJson("DELETE", listOf("games", gameId, "states", slot.toString(), "lock"))

    suspend fun restoreLockedState(gameId: String, slot: Int): Slot =
        signedJson("POST", listOf("games", gameId, "states", slot.toString(), "restore"))

    /** The state goes to the trash of the console (`409 state_locked` when locked). */
    suspend fun deleteState(gameId: String, slot: Int): TrashMoveResult =
        signedJson("DELETE", listOf("games", gameId, "states", slot.toString()))

    /** The PNG thumbnail of a slot, null when larger than [maxBytes]. */
    suspend fun stateThumbnail(gameId: String, slot: Int, maxBytes: Long = 8L shl 20): ByteArray? {
        val response = signedResponse {
            request("GET", listOf("games", gameId, "states", slot.toString(), "thumbnail"), signed = true)
        }
        return response.use {
            if (it.code != 200) throw serverError(it)
            val body = it.body ?: return null
            if (body.contentLength() > maxBytes) return null
            withContext(Dispatchers.IO) {
                val source = body.source()
                // More than maxBytes: not read at all.
                if (source.request(maxBytes + 1)) null else source.readByteArray()
            }
        }
    }

    // MARK: downloads

    /**
     * Streams a file of the console into [out], never in memory: a file of a
     * game (M11), the save, a backup copy or a state ([DownloadSource]).
     * Cancelling the coroutine cancels the call. Returns the bytes written.
     *
     * @param onProgress bytes written so far, and the total when known.
     */
    suspend fun download(
        source: DownloadSource,
        out: java.io.OutputStream,
        onProgress: (Long, Long?) -> Unit = { _, _ -> },
    ): Long {
        val response = signedResponse { request("GET", source.segments, signed = true) }
        return response.use {
            if (it.code != 200) throw serverError(it)
            val body = it.body ?: throw ApiException.InvalidResponse("empty body")
            val total = body.contentLength().takeIf { n -> n >= 0 }
            val job = currentCoroutineContext()[Job]
            // A read blocked on a stalled connection ends when the coroutine is cancelled.
            val handle = job?.invokeOnCompletion { _ -> runCatching { it.close() } }
            try {
                withContext(Dispatchers.IO) {
                    val input = body.byteStream()
                    val buffer = ByteArray(64 * 1024)
                    var written = 0L
                    onProgress(0, total)
                    while (true) {
                        ensureActive()
                        val n = try {
                            input.read(buffer)
                        } catch (e: IOException) {
                            ensureActive()
                            throw ApiException.Network(e)
                        }
                        if (n < 0) break
                        out.write(buffer, 0, n)
                        written += n
                        onProgress(written, total)
                    }
                    if (total != null && written != total) throw ApiException.Network(IOException("truncated download"))
                    out.flush()
                    written
                }
            } finally {
                handle?.dispose()
            }
        }
    }

    // MARK: plumbing

    /**
     * A signed request with the clock-skew retry: after `401 clock_skew`,
     * `/hello` corrects the offset and the request is built and signed
     * again, once. Any other answer is returned as is (the caller closes it).
     */
    private suspend fun signedResponse(build: () -> Request): Response {
        if (signer == null) throw ApiException.NotPaired()
        var retried = false
        while (true) {
            val response = execute(build())
            if (response.code == 401 && !retried) {
                val error = serverError(response)
                if (error is ApiException.Server && error.code == ErrorCodes.CLOCK_SKEW) {
                    retried = true
                    hello()
                    continue
                }
                throw error
            }
            return response
        }
    }

    private suspend inline fun <reified T> signedJson(
        method: String,
        segments: List<String>,
        query: List<Pair<String, String>> = emptyList(),
        body: okhttp3.RequestBody? = null,
        accept: Int = 200,
    ): T = decode(signedResponse { request(method, segments, query, body, signed = true) }, accept)

    /** A signed request answered `204 No Content`. */
    private suspend fun signedEmpty(method: String, segments: List<String>) {
        signedResponse { request(method, segments, signed = true) }.use {
            if (it.code != 204) throw serverError(it)
        }
    }

    internal fun url(segments: List<String>, query: List<Pair<String, String>> = emptyList()): HttpUrl {
        val builder = HttpUrl.Builder().scheme("https").host(endpoint.host).port(endpoint.port)
            .addPathSegment("api").addPathSegment("v1")
        segments.forEach { builder.addPathSegment(it) }
        // Already canonical: the pak reads exactly what was signed.
        if (query.isNotEmpty()) builder.encodedQuery(Signer.encodeQuery(query))
        return builder.build()
    }

    private fun request(
        method: String,
        segments: List<String>,
        query: List<Pair<String, String>> = emptyList(),
        body: okhttp3.RequestBody? = null,
        signed: Boolean = false,
    ): Request {
        val builder = Request.Builder().url(url(segments, query)).header(PLATFORM_HEADER, PLATFORM)
        builder.method(method, body ?: if (method == "POST" || method == "PUT") ByteArray(0).toRequestBody(null) else null)
        if (signed) builder.tag(SignRequest::class.java, SignRequest)
        return builder.build()
    }

    private suspend fun execute(request: Request): Response = try {
        http.newCall(request).await()
    } catch (e: IOException) {
        throw if (e.causeChain().any { it is PinMismatchException }) ApiException.IdentityChanged() else ApiException.Network(e)
    }

    private inline fun <reified T> decode(response: Response, accept: Int): T = response.use {
        if (it.code != accept) throw serverError(it)
        val text = it.body?.string() ?: throw ApiException.InvalidResponse("empty body")
        try {
            ProtocolJson.decodeFromString<T>(text)
        } catch (e: SerializationException) {
            throw ApiException.InvalidResponse(e.message ?: "JSON", e)
        } catch (e: IllegalArgumentException) {
            throw ApiException.InvalidResponse(e.message ?: "JSON", e)
        }
    }

    private fun serverError(response: Response): ApiException = response.use {
        val text = it.body?.string().orEmpty()
        val envelope = runCatching { ProtocolJson.decodeFromString<ErrorEnvelope>(text) }.getOrNull()
            ?: return ApiException.UnexpectedStatus(it.code)
        ApiException.Server(
            status = it.code,
            code = envelope.error.code,
            serverMessage = envelope.error.message,
            details = envelope.error.details.orEmpty(),
            retryAfter = it.header("Retry-After")?.toIntOrNull(),
        )
    }

    companion object {
        const val API_VERSION = 2
        /**
         * The client protocol version of this app (M17), compared with
         * `minClientVersion`: the level of the API features it relies on,
         * shared with the iOS app, not the Android versionName.
         */
        const val CLIENT_PROTOCOL_VERSION = "1.0.0"
        const val PLATFORM_HEADER = "X-RL-Platform"
        const val PLATFORM = "android"

        /** Larger saves go through `POST /uploads` (target `save`). */
        const val MAX_SAVE_BODY_BYTES = 64L shl 20
        private val JSON = "application/json".toMediaType()
        private val PNG = "image/png".toMediaType()

        /** Shared connection pool and dispatcher; each console derives its own pinned client. */
        val defaultBaseClient: OkHttpClient by lazy {
            OkHttpClient.Builder()
                .connectTimeout(10, TimeUnit.SECONDS)
                .readTimeout(15, TimeUnit.SECONDS)
                .writeTimeout(15, TimeUnit.SECONDS)
                .retryOnConnectionFailure(false)
                .cache(null)
                .build()
        }

        /** Seconds since the epoch of an RFC 3339 timestamp, null if unreadable. */
        fun parseRfc3339Seconds(text: String): Long? = try {
            java.time.OffsetDateTime.parse(text).toEpochSecond()
        } catch (_: java.time.format.DateTimeParseException) {
            null
        }
    }
}

/** Runs on the connected socket right before a request is written (see [ApiClient.pair]). */
class BeforeSend(val action: () -> Unit)

/** Tag of the requests [SigningInterceptor] signs. */
object SignRequest

/**
 * Tag of an upload chunk: signed with `UNSIGNED` as the body hash. The
 * [Signer] refuses it for any other request than `PUT /api/v1/uploads/{id}`.
 */
object UnsignedBody

/**
 * Adds the five signature headers to the requests tagged [SignRequest]: the
 * path and query are taken percent-encoded from the URL actually sent, and
 * the body is hashed from its bytes.
 */
class SigningInterceptor(
    private val signer: () -> Signer?,
    private val unixTime: () -> Long,
) : Interceptor {
    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        if (request.tag(SignRequest::class.java) == null) return chain.proceed(request)
        // The pak refuses a repeated query key: never sign one.
        if (request.url.querySize != request.url.queryParameterNames.size) {
            throw IOException(SignerException(SignerException.Reason.DUPLICATE_KEY))
        }
        val signer = signer() ?: throw IOException("signed request without credentials")
        val unsigned = request.tag(UnsignedBody::class.java) != null
        // The body is hashed as it is streamed (a file body is never held in memory).
        val sha = if (unsigned) {
            null
        } else {
            request.body?.let { body ->
                val hashing = HashingSink.sha256(blackholeSink())
                hashing.buffer().use { body.writeTo(it) }
                hashing.hash.hex()
            } ?: Signer.contentSha256(ByteArray(0))
        }
        val headers = try {
            signer.headers(
                method = request.method,
                path = request.url.encodedPath,
                rawQuery = request.url.encodedQuery ?: "",
                unsignedBody = unsigned,
                time = unixTime(),
                bodySha256 = sha,
            )
        } catch (e: SignerException) {
            // Never a non-IOException out of an interceptor (it would kill OkHttp's thread).
            throw IOException(e)
        }
        val builder = request.newBuilder()
        headers.asMap().forEach { (name, value) -> builder.header(name, value) }
        return chain.proceed(builder.build())
    }
}

/**
 * [length] bytes of [file] from [offset], read as they are sent (never held
 * in memory) and readable again for a new attempt or for hashing.
 */
class FileRegionBody(
    private val file: java.io.File,
    private val offset: Long,
    private val length: Long,
    private val onSent: ((Long) -> Unit)?,
) : okhttp3.RequestBody() {
    override fun contentType() = OCTET_STREAM

    override fun contentLength() = length

    override fun writeTo(sink: okio.BufferedSink) {
        java.io.RandomAccessFile(file, "r").use { raf ->
            raf.seek(offset)
            val buffer = ByteArray(64 * 1024)
            var left = length
            var sent = 0L
            while (left > 0) {
                val n = raf.read(buffer, 0, minOf(buffer.size.toLong(), left).toInt())
                if (n < 0) throw IOException("file shorter than expected")
                sink.write(buffer, 0, n)
                left -= n
                sent += n
                onSent?.invoke(sent)
            }
        }
    }

    private companion object {
        val OCTET_STREAM = "application/octet-stream".toMediaType()
    }
}

private fun Throwable.causeChain(): Sequence<Throwable> = generateSequence(this) { it.cause }

/** Runs a call on OkHttp's dispatcher; cancelling the coroutine cancels the call. */
internal suspend fun Call.await(): Response = suspendCancellableCoroutine { continuation ->
    enqueue(object : Callback {
        override fun onResponse(call: Call, response: Response) {
            continuation.resume(response) { _, value, _ -> value.close() }
        }

        override fun onFailure(call: Call, e: IOException) {
            continuation.resumeWithException(e)
        }
    })
    continuation.invokeOnCancellation { runCatching { cancel() } }
}
