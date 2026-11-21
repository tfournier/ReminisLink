package link.reminis.core

import java.io.IOException
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resumeWithException
import kotlinx.coroutines.suspendCancellableCoroutine
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
import okio.Buffer

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

    /** `POST /pair`, then checks the device proof. Store the pairing only when this returns. */
    suspend fun pair(attempt: PairingAttempt): PairResponse {
        val body = ProtocolJson.encodeToString(attempt.request).toRequestBody(JSON)
        val response: PairResponse = decode(execute(request("POST", listOf("pair"), body = body)), accept = 200)
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

    // MARK: plumbing

    private suspend inline fun <reified T> signedJson(
        method: String,
        segments: List<String>,
        query: List<Pair<String, String>> = emptyList(),
    ): T {
        if (signer == null) throw ApiException.NotPaired()
        var retried = false
        while (true) {
            val response = execute(request(method, segments, query, signed = true))
            if (response.code == 401 && !retried) {
                val error = serverError(response)
                if (error is ApiException.Server && error.code == ErrorCodes.CLOCK_SKEW) {
                    retried = true
                    hello()
                    continue
                }
                throw error
            }
            return decode(response, accept = 200)
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
        private val JSON = "application/json".toMediaType()

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

/** Tag of the requests [SigningInterceptor] signs. */
object SignRequest

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
        val body = request.body?.let { b -> Buffer().also { b.writeTo(it) }.readByteArray() } ?: ByteArray(0)
        val headers = signer.headers(
            method = request.method,
            path = request.url.encodedPath,
            rawQuery = request.url.encodedQuery ?: "",
            body = body,
            time = unixTime(),
        )
        val builder = request.newBuilder()
        headers.asMap().forEach { (name, value) -> builder.header(name, value) }
        return chain.proceed(builder.build())
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
