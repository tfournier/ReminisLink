package link.reminis.core

/**
 * Base64url without padding (RFC 4648 §5), as used by every binary value of
 * the protocol. The decoder is strict, like the Swift and Go ones: only the
 * URL-safe alphabet, no padding, no whitespace, and the unused bits of the
 * last character must be zero (one encoding per value).
 */
object Base64Url {
    private const val ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
    private val decodeTable = IntArray(128) { -1 }.also { table ->
        ALPHABET.forEachIndexed { i, c -> table[c.code] = i }
    }

    fun encode(bytes: ByteArray): String {
        val out = StringBuilder((bytes.size * 4 + 2) / 3)
        var i = 0
        while (i + 3 <= bytes.size) {
            val n = (bytes[i].toInt() and 0xff shl 16) or (bytes[i + 1].toInt() and 0xff shl 8) or (bytes[i + 2].toInt() and 0xff)
            out.append(ALPHABET[n shr 18 and 63]).append(ALPHABET[n shr 12 and 63])
                .append(ALPHABET[n shr 6 and 63]).append(ALPHABET[n and 63])
            i += 3
        }
        when (bytes.size - i) {
            1 -> {
                val n = bytes[i].toInt() and 0xff shl 16
                out.append(ALPHABET[n shr 18 and 63]).append(ALPHABET[n shr 12 and 63])
            }
            2 -> {
                val n = (bytes[i].toInt() and 0xff shl 16) or (bytes[i + 1].toInt() and 0xff shl 8)
                out.append(ALPHABET[n shr 18 and 63]).append(ALPHABET[n shr 12 and 63]).append(ALPHABET[n shr 6 and 63])
            }
        }
        return out.toString()
    }

    /** The decoded bytes, or null for anything but canonical unpadded base64url. */
    fun decode(text: String): ByteArray? {
        if (text.length % 4 == 1) return null
        val values = IntArray(text.length)
        for ((i, c) in text.withIndex()) {
            val v = if (c.code < 128) decodeTable[c.code] else -1
            if (v < 0) return null
            values[i] = v
        }
        val out = ByteArray(text.length * 3 / 4)
        var o = 0
        var i = 0
        while (i + 4 <= values.size) {
            val n = (values[i] shl 18) or (values[i + 1] shl 12) or (values[i + 2] shl 6) or values[i + 3]
            out[o++] = (n shr 16).toByte(); out[o++] = (n shr 8).toByte(); out[o++] = n.toByte()
            i += 4
        }
        when (values.size - i) {
            2 -> {
                if (values[i + 1] and 0x0f != 0) return null
                out[o] = ((values[i] shl 2) or (values[i + 1] shr 4)).toByte()
            }
            3 -> {
                if (values[i + 2] and 0x03 != 0) return null
                val n = (values[i] shl 12) or (values[i + 1] shl 6) or values[i + 2]
                out[o++] = (n shr 10).toByte(); out[o] = (n shr 2).toByte()
            }
        }
        return out
    }
}

internal fun ByteArray.toHex(): String {
    val digits = "0123456789abcdef"
    val sb = StringBuilder(size * 2)
    for (b in this) sb.append(digits[b.toInt() shr 4 and 15]).append(digits[b.toInt() and 15])
    return sb.toString()
}

/** Constant-time comparison of two byte arrays (lengths are not secret). */
internal fun constantTimeEquals(a: ByteArray, b: ByteArray): Boolean {
    if (a.size != b.size) return false
    var diff = 0
    for (i in a.indices) diff = diff or (a[i].toInt() xor b[i].toInt())
    return diff == 0
}
