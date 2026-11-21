package link.reminis.core

/** Why a pairing link was refused. */
class PairingLinkException(val reason: Reason) : Exception("pairing link: ${reason.name.lowercase()}") {
    enum class Reason {
        /** Not a valid ReminisLink pairing link. */
        INVALID,

        /** A link of a pak older than M13 (`v=1`): the pak must be updated. */
        OUTDATED_PAK,

        /** A link of a newer pak than this app knows: the app must be updated. */
        NEWER_VERSION,
    }
}

/**
 * The pairing link of the QR code, version 2 (docs/specs/03-protocole.md §3.1):
 *
 *     reminislink://pair?v=2&id=<deviceId>&n=<name>&s=<secret b64u>&h=<IPv4>&p=<port>&k=<pin>
 *
 * `k` is the pin of the console's certificate: it authenticates the console,
 * so the app may connect to `h`:`p` when mDNS does not find the `id`.
 */
data class PairingLink(
    val deviceId: String,
    val deviceName: String?,
    val secret: ByteArray,
    val endpoint: Endpoint?,
    val pin: String,
) {
    /** The link, as the pak encodes it. */
    fun toUri(): String {
        val items = mutableListOf("v" to VERSION.toString(), "id" to deviceId)
        deviceName?.let { items += "n" to it }
        items += "s" to Base64Url.encode(secret)
        endpoint?.let {
            items += "h" to it.host
            items += "p" to it.port.toString()
        }
        items += "k" to pin
        return "$SCHEME://$HOST?" + items.joinToString("&") { "${it.first}=${Signer.encodeRfc3986(it.second)}" }
    }

    /** The same link with another address (tests: an emulator reaches the host at 10.0.2.2). */
    fun withEndpoint(endpoint: Endpoint?): PairingLink = copy(endpoint = endpoint)

    override fun equals(other: Any?): Boolean = other is PairingLink && deviceId == other.deviceId &&
        deviceName == other.deviceName && secret.contentEquals(other.secret) && endpoint == other.endpoint && pin == other.pin

    override fun hashCode(): Int = deviceId.hashCode() * 31 + pin.hashCode()

    override fun toString(): String = "PairingLink(deviceId=$deviceId, deviceName=$deviceName, endpoint=$endpoint)"

    companion object {
        const val SCHEME = "reminislink"
        const val HOST = "pair"
        const val VERSION = 2

        private val prefix = Regex("^([A-Za-z][A-Za-z0-9+.-]*)://([^/?#]*)(/?)\\?(.*)$", RegexOption.DOT_MATCHES_ALL)
        private val uuid = Regex("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")

        /** Parses a link; a repeated parameter is refused. */
        fun parse(text: String): PairingLink {
            val s = text.trim()
            // Only printable ASCII: an unencoded space or non-ASCII character
            // is not a URL.
            if (s.any { it.code !in 0x21..0x7e }) throw invalid()
            val m = prefix.matchEntire(s) ?: throw invalid()
            val (scheme, authority, _, rawQuery) = m.destructured
            if (!scheme.equals(SCHEME, ignoreCase = true) || !authority.equals(HOST, ignoreCase = true)) throw invalid()
            if ('#' in rawQuery) throw invalid()
            val params = parseQuery(rawQuery) ?: throw invalid()
            val v = params["v"]?.takeIf(::isDecimal)?.toIntOrNull() ?: throw invalid()
            if (v < VERSION) throw PairingLinkException(PairingLinkException.Reason.OUTDATED_PAK)
            if (v > VERSION) throw PairingLinkException(PairingLinkException.Reason.NEWER_VERSION)
            val id = params["id"]?.takeIf { uuid.matches(it) } ?: throw invalid()
            val secret = params["s"]?.let(Base64Url::decode)?.takeIf { it.size == Pairing.SECRET_SIZE } ?: throw invalid()
            val pinBytes = params["k"]?.let(Base64Url::decode)?.takeIf { it.size == TlsPin.SIZE } ?: throw invalid()
            val h = params["h"]
            val p = params["p"]
            val endpoint = when {
                h == null && p == null -> null
                h != null && p != null -> {
                    val port = p.takeIf(::isDecimal)?.toIntOrNull()
                    if (!isIPv4(h) || port == null || port !in 1..65535) throw invalid()
                    Endpoint(h, port)
                }
                else -> throw invalid()
            }
            val name = params["n"]?.trim()?.ifEmpty { null }
            return PairingLink(id, name, secret, endpoint, Base64Url.encode(pinBytes))
        }

        fun parseOrNull(text: String): PairingLink? = try {
            parse(text)
        } catch (_: PairingLinkException) {
            null
        }

        private fun invalid() = PairingLinkException(PairingLinkException.Reason.INVALID)

        /** Form-urlencoded query ("+" is a space); null on a malformed escape or a repeated key. */
        internal fun parseQuery(raw: String): Map<String, String>? {
            val result = LinkedHashMap<String, String>()
            for (part in raw.split('&')) {
                if (part.isEmpty()) continue
                val eq = part.indexOf('=')
                val key = decode(if (eq >= 0) part.substring(0, eq) else part) ?: return null
                val value = decode(if (eq >= 0) part.substring(eq + 1) else "") ?: return null
                if (key in result) return null
                result[key] = value
            }
            return result
        }

        /** Strict form decoding: "%" must start two hex digits and the result must be valid UTF-8. */
        private fun decode(s: String): String? {
            val out = java.io.ByteArrayOutputStream(s.length)
            var i = 0
            while (i < s.length) {
                when (val c = s[i]) {
                    '+' -> out.write(' '.code)
                    '%' -> {
                        if (i + 2 >= s.length) return null
                        val hi = Character.digit(s[i + 1], 16)
                        val lo = Character.digit(s[i + 2], 16)
                        if (hi < 0 || lo < 0) return null
                        out.write(hi shl 4 or lo)
                        i += 2
                    }
                    else -> out.write(c.code)
                }
                i++
            }
            val decoder = Charsets.UTF_8.newDecoder()
                .onMalformedInput(java.nio.charset.CodingErrorAction.REPORT)
                .onUnmappableCharacter(java.nio.charset.CodingErrorAction.REPORT)
            return try {
                decoder.decode(java.nio.ByteBuffer.wrap(out.toByteArray())).toString()
            } catch (_: java.nio.charset.CharacterCodingException) {
                null
            }
        }

        private fun isDecimal(s: String) = s.isNotEmpty() && s.length <= 5 && s.all { it in '0'..'9' }

        /** Dotted-quad IPv4, each part 0-255 without a leading zero. */
        fun isIPv4(s: String): Boolean {
            val parts = s.split('.')
            return parts.size == 4 && parts.all { part ->
                part.isNotEmpty() && part.length <= 3 && part.all { it in '0'..'9' } &&
                    !(part.length > 1 && part[0] == '0') && part.toInt() <= 255
            }
        }
    }
}
