package link.reminis.core

import java.security.SecureRandom
import java.util.UUID
import javax.crypto.Mac
import javax.crypto.spec.SecretKeySpec
import kotlinx.serialization.Serializable

/** `platform` of `POST /pair`. */
object ClientPlatform {
    const val ANDROID = "android"
}

/** Body of `POST /api/v1/pair` (pairing v2). */
@Serializable
data class PairRequest(
    val clientId: String,
    val clientName: String,
    val platform: String,
    val clientPublicKey: String,
    val clientNonce: String,
    val proof: String,
)

/** Response of `POST /api/v1/pair`. */
@Serializable
data class PairResponse(
    val deviceId: String,
    val deviceName: String,
    val deviceNonce: String,
    val proof: String,
)

class PairingException(val reason: Reason) : Exception("pairing: ${reason.name.lowercase()}") {
    enum class Reason { INVALID_SECRET, INVALID_NONCE, DEVICE_MISMATCH, INVALID_DEVICE_NONCE, INVALID_DEVICE_PROOF }
}

/**
 * Pairing primitives (docs/specs/03-protocole.md §3.2, pairing v2). No shared
 * key is derived: the QR secret only proves, both ways, that each side read
 * the console's screen.
 */
object Pairing {
    const val SECRET_SIZE = 32
    const val NONCE_SIZE = 32
    /** Longest `clientName`, in Unicode code points (the pak counts runes). */
    const val MAX_CLIENT_NAME_CODE_POINTS = 64

    private val random = SecureRandom()

    fun clientProofMessage(deviceId: String, clientId: String, clientPublicKey: String, clientNonce: String) =
        listOf("RL2-PAIR-CLIENT", deviceId, clientId, clientPublicKey, clientNonce).joinToString("|")

    fun deviceProofMessage(
        deviceId: String,
        clientId: String,
        clientPublicKey: String,
        clientNonce: String,
        deviceNonce: String,
    ) = listOf("RL2-PAIR-DEVICE", deviceId, clientId, clientPublicKey, clientNonce, deviceNonce).joinToString("|")

    /** b64u(HMAC-SHA256(secret, message)). */
    fun proof(secret: ByteArray, message: String): String = Base64Url.encode(hmac(secret, message))

    /** Checks a proof in constant time. */
    fun isValidProof(proof: String, secret: ByteArray, message: String): Boolean {
        val mac = Base64Url.decode(proof) ?: return false
        return constantTimeEquals(mac, hmac(secret, message))
    }

    /** A new client id: a lowercase UUID (the pak refuses uppercase). */
    fun makeClientId(): String = UUID.randomUUID().toString().lowercase()

    fun makeNonce(): ByteArray = ByteArray(NONCE_SIZE).also(random::nextBytes)

    /**
     * A `clientName` for `POST /pair`: trimmed, at most 64 code points without
     * cutting a code point in two, [fallback] when empty.
     */
    fun clientName(name: String, fallback: String = "Android"): String {
        val trimmed = name.trim()
        val count = trimmed.codePointCount(0, trimmed.length)
        val cut = if (count > MAX_CLIENT_NAME_CODE_POINTS) {
            trimmed.substring(0, trimmed.offsetByCodePoints(0, MAX_CLIENT_NAME_CODE_POINTS)).trim()
        } else {
            trimmed
        }
        return cut.ifEmpty { fallback }
    }

    private fun hmac(secret: ByteArray, message: String): ByteArray {
        val mac = Mac.getInstance("HmacSHA256")
        mac.init(SecretKeySpec(secret, "HmacSHA256"))
        return mac.doFinal(message.toByteArray(Charsets.UTF_8))
    }
}

/** One pairing exchange, from the request to the verified answer. */
class PairingAttempt(
    val secret: ByteArray,
    val deviceId: String,
    val clientId: String,
    val key: Ed25519Key,
    val clientName: String,
    val platform: String = ClientPlatform.ANDROID,
    val clientNonce: ByteArray = Pairing.makeNonce(),
) {
    init {
        if (secret.size != Pairing.SECRET_SIZE) throw PairingException(PairingException.Reason.INVALID_SECRET)
        if (clientNonce.size != Pairing.NONCE_SIZE) throw PairingException(PairingException.Reason.INVALID_NONCE)
    }

    private val publicKey = Base64Url.encode(key.publicKey)
    private val nonce = Base64Url.encode(clientNonce)

    /** The body to send to `POST /api/v1/pair`. */
    val request: PairRequest
        get() = PairRequest(
            clientId = clientId,
            clientName = clientName,
            platform = platform,
            clientPublicKey = publicKey,
            clientNonce = nonce,
            proof = Pairing.proof(secret, Pairing.clientProofMessage(deviceId, clientId, publicKey, nonce)),
        )

    /** Checks the device proof (mutual authentication). Store the pairing only on success. */
    fun finish(response: PairResponse) {
        if (response.deviceId != deviceId) throw PairingException(PairingException.Reason.DEVICE_MISMATCH)
        if (Base64Url.decode(response.deviceNonce)?.size != Pairing.NONCE_SIZE) {
            throw PairingException(PairingException.Reason.INVALID_DEVICE_NONCE)
        }
        val message = Pairing.deviceProofMessage(deviceId, clientId, publicKey, nonce, response.deviceNonce)
        if (!Pairing.isValidProof(response.proof, secret, message)) {
            throw PairingException(PairingException.Reason.INVALID_DEVICE_PROOF)
        }
    }
}
