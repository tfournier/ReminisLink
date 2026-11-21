package link.reminis.core

import java.security.MessageDigest
import java.security.SecureRandom

/** An input that would break the canonical string (refused, like the pak does). */
class SignerException(val reason: Reason) : IllegalArgumentException("malformed ${reason.name.lowercase()}") {
    enum class Reason {
        METHOD, PATH, QUERY, TIME, NONCE, CONTENT_SHA256,

        /** The same query key twice: the pak refuses it (`400 invalid_request`, 03 §4.2). */
        DUPLICATE_KEY,

        /** `UNSIGNED` is only for `PUT /api/v1/uploads/{uploadId}`. */
        UNSIGNED_BODY,
    }
}

/** The five signature headers of a request (docs/specs/03-protocole.md §4.1). */
data class SignedHeaders(
    val clientId: String,
    val time: String,
    val nonce: String,
    val contentSha256: String,
    val signature: String,
) {
    fun asMap(): Map<String, String> = mapOf(
        CLIENT to clientId,
        TIME to time,
        NONCE to nonce,
        CONTENT_SHA256 to contentSha256,
        SIGNATURE to signature,
    )

    companion object {
        const val CLIENT = "X-RL-Client"
        const val TIME = "X-RL-Time"
        const val NONCE = "X-RL-Nonce"
        const val CONTENT_SHA256 = "X-RL-Content-SHA256"
        const val SIGNATURE = "X-RL-Signature"
    }
}

/**
 * Signs requests with the Ed25519 key of the client identity
 * (docs/specs/03-protocole.md §4, RL2-ED25519). Mirrors `Signer.swift` of
 * ReminisLinkKit and `internal/auth` of the pak.
 *
 * Canonical string (lines joined with "\n", no trailing newline):
 *
 *     RL2-ED25519
 *     <METHOD>
 *     <path as sent, percent-encoded, without the query>
 *     <canonical query>
 *     <X-RL-Time>
 *     <X-RL-Nonce>
 *     <X-RL-Content-SHA256>
 */
class Signer(val clientId: String, private val key: Ed25519Key) {

    /**
     * The signature headers of a request.
     *
     * @param path the path as sent, percent-encoded, with `/api/v1`.
     * @param rawQuery the query as sent (without `?`); build it with [encodeQuery].
     * @param unsignedBody only for upload chunks (`PUT /api/v1/uploads/{id}`);
     *   refused for any other request.
     * @param bodySha256 the lowercase hex SHA-256 of the body when already
     *   computed (a body streamed from a file); replaces [body]. Never
     *   `UNSIGNED`: that is only [unsignedBody].
     */
    fun headers(
        method: String,
        path: String,
        rawQuery: String = "",
        body: ByteArray = ByteArray(0),
        unsignedBody: Boolean = false,
        time: Long,
        nonce: String = makeNonce(),
        bodySha256: String? = null,
    ): SignedHeaders {
        if (unsignedBody && !isUploadChunk(method, path)) throw SignerException(SignerException.Reason.UNSIGNED_BODY)
        if (bodySha256 != null && (bodySha256 == UNSIGNED_PAYLOAD || !isValidContentSha256(bodySha256))) {
            throw SignerException(SignerException.Reason.CONTENT_SHA256)
        }
        // The pak refuses a repeated key: never sign one ([canonicalRequest]
        // itself stays a pure canonicalization, for the vectors).
        if (hasDuplicateKeys(rawQuery)) throw SignerException(SignerException.Reason.DUPLICATE_KEY)
        val content = when {
            unsignedBody -> UNSIGNED_PAYLOAD
            bodySha256 != null -> bodySha256
            else -> contentSha256(body)
        }
        val timeString = time.toString()
        val canonical = canonicalRequest(method, path, rawQuery, timeString, nonce, content)
        return SignedHeaders(clientId, timeString, nonce, content, signature(key, canonical))
    }

    companion object {
        const val ALGORITHM = "RL2-ED25519"
        const val UNSIGNED_PAYLOAD = "UNSIGNED"
        const val NONCE_SIZE = 16

        private val random = SecureRandom()

        /** 16 random bytes in base64url. */
        fun makeNonce(): String = Base64Url.encode(ByteArray(NONCE_SIZE).also(random::nextBytes))

        /** Lowercase hex SHA-256 of the body (the empty string's hash for no body). */
        fun contentSha256(body: ByteArray): String = MessageDigest.getInstance("SHA-256").digest(body).toHex()

        /** b64u(Ed25519(key, canonical)). */
        fun signature(key: Ed25519Key, canonical: String): String =
            Base64Url.encode(key.sign(canonical.toByteArray(Charsets.UTF_8)))

        fun isValidSignature(signature: String, publicKey: ByteArray, canonical: String): Boolean {
            val bytes = Base64Url.decode(signature) ?: return false
            return Ed25519.verify(publicKey, bytes, canonical.toByteArray(Charsets.UTF_8))
        }

        /** Builds the canonical string; rejects inputs that could break its line structure. */
        fun canonicalRequest(
            method: String,
            path: String,
            rawQuery: String,
            time: String,
            nonce: String,
            contentSha256: String,
        ): String {
            if (method.isEmpty() || !method.all { it in 'A'..'Z' || it in 'a'..'z' }) {
                throw SignerException(SignerException.Reason.METHOD)
            }
            val pathBytes = path.toByteArray(Charsets.UTF_8)
            if (pathBytes.firstOrNull() != '/'.code.toByte() ||
                pathBytes.any { isControlOrSpace(it) || it == '?'.code.toByte() || it == '#'.code.toByte() }
            ) {
                throw SignerException(SignerException.Reason.PATH)
            }
            if (rawQuery.toByteArray(Charsets.UTF_8).any { isControlOrSpace(it) || it == '#'.code.toByte() }) {
                throw SignerException(SignerException.Reason.QUERY)
            }
            if (!isValidTime(time)) throw SignerException(SignerException.Reason.TIME)
            if (Base64Url.decode(nonce)?.size != NONCE_SIZE) throw SignerException(SignerException.Reason.NONCE)
            if (!isValidContentSha256(contentSha256)) throw SignerException(SignerException.Reason.CONTENT_SHA256)
            val query = canonicalQuery(rawQuery)
            return listOf(ALGORITHM, method.uppercase(), path, query, time, nonce, contentSha256).joinToString("\n")
        }

        /**
         * Canonicalizes a raw query: pairs split on "&" then on the first "=",
         * decoded ("+" is a space, %XX a byte), re-encoded per RFC 3986,
         * sorted by encoded key then encoded value (byte order), joined with "&".
         */
        fun canonicalQuery(rawQuery: String): String {
            val pairs = mutableListOf<Pair<String, String>>()
            for (part in splitBytes(rawQuery.toByteArray(Charsets.UTF_8), '&'.code.toByte())) {
                if (part.isEmpty()) continue
                val eq = part.indexOf('='.code.toByte())
                val key = if (eq >= 0) part.copyOfRange(0, eq) else part
                val value = if (eq >= 0) part.copyOfRange(eq + 1, part.size) else ByteArray(0)
                pairs += encodeRfc3986(unescape(key)) to encodeRfc3986(unescape(value))
            }
            return sortPairs(pairs).joinToString("&") { "${it.first}=${it.second}" }
        }

        /**
         * Encodes query items for sending, in canonical order: its own
         * canonical form. A repeated key is refused, as the pak does
         * ([canonicalQuery] stays a pure canonicalization, for the vectors).
         */
        fun encodeQuery(items: List<Pair<String, String>>): String {
            val encoded = items.map { encodeRfc3986(it.first) to encodeRfc3986(it.second) }
            if (encoded.map { it.first }.toSet().size != encoded.size) {
                throw SignerException(SignerException.Reason.DUPLICATE_KEY)
            }
            return sortPairs(encoded).joinToString("&") { "${it.first}=${it.second}" }
        }

        /** Whether two pairs of a raw query have the same decoded key. */
        fun hasDuplicateKeys(rawQuery: String): Boolean {
            val keys = HashSet<String>()
            for (part in splitBytes(rawQuery.toByteArray(Charsets.UTF_8), '&'.code.toByte())) {
                if (part.isEmpty()) continue
                val eq = part.indexOf('='.code.toByte())
                val key = encodeRfc3986(unescape(if (eq >= 0) part.copyOfRange(0, eq) else part))
                if (!keys.add(key)) return true
            }
            return false
        }

        private val uploadChunkPath = Regex("^/api/v1/uploads/[^/]+$")

        /** `PUT /api/v1/uploads/{uploadId}`: the only request whose body may be `UNSIGNED`. */
        fun isUploadChunk(method: String, path: String): Boolean =
            method.equals("PUT", ignoreCase = true) && uploadChunkPath.matches(path)

        /** Percent-encodes every UTF-8 byte except A-Z a-z 0-9 - . _ ~. */
        fun encodeRfc3986(text: String): String = encodeRfc3986(text.toByteArray(Charsets.UTF_8))

        private const val HEX = "0123456789ABCDEF"

        private fun encodeRfc3986(bytes: ByteArray): String {
            val sb = StringBuilder(bytes.size)
            for (b in bytes) {
                val c = b.toInt() and 0xff
                if (isUnreserved(c)) sb.append(c.toChar()) else sb.append('%').append(HEX[c shr 4]).append(HEX[c and 15])
            }
            return sb.toString()
        }

        // Encoded strings are ASCII: comparing chars is comparing bytes.
        private fun sortPairs(pairs: List<Pair<String, String>>) =
            pairs.sortedWith(compareBy<Pair<String, String>> { it.first }.thenBy { it.second })

        private fun splitBytes(bytes: ByteArray, separator: Byte): List<ByteArray> {
            val parts = mutableListOf<ByteArray>()
            var start = 0
            for (i in bytes.indices) {
                if (bytes[i] == separator) {
                    parts += bytes.copyOfRange(start, i); start = i + 1
                }
            }
            parts += bytes.copyOfRange(start, bytes.size)
            return parts
        }

        private fun unescape(bytes: ByteArray): ByteArray {
            val out = java.io.ByteArrayOutputStream(bytes.size)
            var i = 0
            while (i < bytes.size) {
                val c = bytes[i]
                when (c) {
                    '+'.code.toByte() -> out.write(' '.code)
                    '%'.code.toByte() -> {
                        val hi = if (i + 2 < bytes.size) hexValue(bytes[i + 1]) else -1
                        val lo = if (i + 2 < bytes.size) hexValue(bytes[i + 2]) else -1
                        if (hi < 0 || lo < 0) throw SignerException(SignerException.Reason.QUERY)
                        out.write(hi shl 4 or lo)
                        i += 2
                    }
                    else -> out.write(c.toInt())
                }
                i++
            }
            return out.toByteArray()
        }

        private fun hexValue(b: Byte): Int = when (val c = b.toInt().toChar()) {
            in '0'..'9' -> c - '0'
            in 'a'..'f' -> c - 'a' + 10
            in 'A'..'F' -> c - 'A' + 10
            else -> -1
        }

        private fun isUnreserved(c: Int): Boolean =
            c in 'A'.code..'Z'.code || c in 'a'.code..'z'.code || c in '0'.code..'9'.code ||
                c == '-'.code || c == '.'.code || c == '_'.code || c == '~'.code

        private fun isControlOrSpace(b: Byte): Boolean {
            val c = b.toInt() and 0xff
            return c <= 0x20 || c == 0x7f
        }

        /** Canonical decimal Unix time: digits only, no sign, no leading zero. */
        private fun isValidTime(s: String): Boolean {
            if (s.isEmpty() || s.length > 19 || (s.length > 1 && s[0] == '0') || !s.all { it in '0'..'9' }) return false
            return s.toLongOrNull() != null
        }

        private fun isValidContentSha256(s: String): Boolean =
            s == UNSIGNED_PAYLOAD || (s.length == 64 && s.all { it in '0'..'9' || it in 'a'..'f' })
    }
}
