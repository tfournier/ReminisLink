package link.reminis.core

import com.google.crypto.tink.subtle.Ed25519Sign
import com.google.crypto.tink.subtle.Ed25519Verify
import java.security.SecureRandom

/**
 * An Ed25519 signing key of the client identity (docs/specs/03-protocole.md
 * §3.2). The app keeps it in the Android Keystore when the device supports
 * Ed25519 there (API 33+), else as a seed encrypted with an Android Keystore
 * AES key (see docs/specs/10-app-android.md); :core only needs this interface.
 */
interface Ed25519Key {
    /** The 32 raw bytes of the public key (`clientPublicKey`, once encoded). */
    val publicKey: ByteArray

    /** A 64-byte Ed25519 signature of [message]. */
    fun sign(message: ByteArray): ByteArray
}

/** An Ed25519 key from its 32-byte seed (RFC 8032: deterministic signatures). */
class SeedEd25519Key(seed: ByteArray) : Ed25519Key {
    init {
        require(seed.size == SEED_SIZE) { "an Ed25519 seed is 32 bytes" }
    }

    private val signer = Ed25519Sign(seed)
    override val publicKey: ByteArray = Ed25519Sign.KeyPair.newKeyPairFromSeed(seed).publicKey

    override fun sign(message: ByteArray): ByteArray = signer.sign(message)

    companion object {
        const val SEED_SIZE = 32

        fun randomSeed(random: SecureRandom = SecureRandom()): ByteArray = ByteArray(SEED_SIZE).also(random::nextBytes)
    }
}

object Ed25519 {
    const val PUBLIC_KEY_SIZE = 32
    const val SIGNATURE_SIZE = 64

    /** Whether [signature] is a valid signature of [message] by [publicKey]. */
    fun verify(publicKey: ByteArray, signature: ByteArray, message: ByteArray): Boolean {
        if (publicKey.size != PUBLIC_KEY_SIZE || signature.size != SIGNATURE_SIZE) return false
        return try {
            Ed25519Verify(publicKey).verify(signature, message)
            true
        } catch (_: java.security.GeneralSecurityException) {
            false
        }
    }
}
