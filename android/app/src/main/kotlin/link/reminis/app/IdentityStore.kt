package link.reminis.app

import android.content.Context
import android.os.Build
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Log
import androidx.annotation.RequiresApi
import java.io.IOException
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.PrivateKey
import java.security.Signature
import java.security.spec.ECGenParameterSpec
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import link.reminis.core.Base64Url
import link.reminis.core.Ed25519
import link.reminis.core.Ed25519Key
import link.reminis.core.Pairing
import link.reminis.core.SeedEd25519Key

/** Where the private key of the identity lives. */
enum class KeyStorage {
    /** An Ed25519 key generated inside the Android Keystore (API 33+), never exportable. */
    KEYSTORE,

    /** A Tink Ed25519 seed encrypted (AES-256-GCM) with a non-exportable Android Keystore key. */
    SEALED_SEED,
}

/** The identity of this phone towards every console: a `clientId` and an Ed25519 key. */
class ClientIdentity(val clientId: String, val key: Ed25519Key, val storage: KeyStorage)

/**
 * Creates and keeps the client identity (docs/specs/10-app-android.md, "Clé
 * de signature"). Unlike iOS (identity synchronized by the iCloud Keychain),
 * each Android device has its own identity and pairs once per console.
 *
 * 1. API 33+: an Ed25519 key pair generated in the Android Keystore, checked
 *    once by a test signature (some Keystore implementations do not support
 *    Ed25519 even on API 33+).
 * 2. Otherwise: a random 32-byte seed signed with Tink's pure Java Ed25519,
 *    stored encrypted with an AES-256-GCM key of the Android Keystore.
 */
class IdentityStore(context: Context) {
    private val prefs = context.getSharedPreferences("identity", Context.MODE_PRIVATE)
    private val keyStore: KeyStore by lazy { KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) } }

    @Volatile
    private var cached: ClientIdentity? = null

    /**
     * The identity, created on first use and returned only once saved
     * (`commit()` checked). Throws if the Keystore or the storage is unusable.
     */
    @Synchronized
    fun identity(): ClientIdentity {
        cached?.let { return it }
        val loaded = load() ?: create()
        cached = loaded
        return loaded
    }

    private fun load(): ClientIdentity? {
        val clientId = prefs.getString(KEY_CLIENT_ID, null) ?: return null
        return when (prefs.getString(KEY_STORAGE, null)) {
            KeyStorage.KEYSTORE.name -> {
                val publicKey = prefs.getString(KEY_PUBLIC, null)?.let(Base64Url::decode) ?: return null
                val privateKey = keyStore.getKey(ALIAS_ED25519, null) as? PrivateKey ?: return null
                ClientIdentity(clientId, KeystoreEd25519Key(privateKey, publicKey), KeyStorage.KEYSTORE)
            }
            KeyStorage.SEALED_SEED.name -> {
                val sealed = prefs.getString(KEY_SEALED_SEED, null)?.let(Base64Url::decode) ?: return null
                val iv = prefs.getString(KEY_IV, null)?.let(Base64Url::decode) ?: return null
                val aes = keyStore.getKey(ALIAS_AES, null) as? SecretKey ?: return null
                val cipher = Cipher.getInstance(AES_GCM)
                cipher.init(Cipher.DECRYPT_MODE, aes, GCMParameterSpec(128, iv))
                val seed = cipher.doFinal(sealed)
                ClientIdentity(clientId, SeedEd25519Key(seed).also { seed.fill(0) }, KeyStorage.SEALED_SEED)
            }
            else -> null
        }
    }

    private fun create(): ClientIdentity {
        val clientId = Pairing.makeClientId()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            try {
                val key = createKeystoreKey()
                val saved = prefs.edit()
                    .clear()
                    .putString(KEY_CLIENT_ID, clientId)
                    .putString(KEY_STORAGE, KeyStorage.KEYSTORE.name)
                    .putString(KEY_PUBLIC, Base64Url.encode(key.publicKey))
                    .commit()
                if (!saved) {
                    // Never pair with an identity that would be lost on restart.
                    runCatching { keyStore.deleteEntry(ALIAS_ED25519) }
                    throw IOException("the identity could not be saved")
                }
                return ClientIdentity(clientId, key, KeyStorage.KEYSTORE)
            } catch (e: IOException) {
                throw e
            } catch (e: Exception) {
                Log.i(TAG, "Ed25519 is not available in the Keystore, using a sealed seed: ${e.javaClass.simpleName}")
                runCatching { keyStore.deleteEntry(ALIAS_ED25519) }
            }
        }
        val seed = SeedEd25519Key.randomSeed()
        val aes = createAesKey()
        val cipher = Cipher.getInstance(AES_GCM)
        cipher.init(Cipher.ENCRYPT_MODE, aes)
        val sealed = cipher.doFinal(seed)
        val key = SeedEd25519Key(seed)
        seed.fill(0)
        val saved = prefs.edit()
            .clear()
            .putString(KEY_CLIENT_ID, clientId)
            .putString(KEY_STORAGE, KeyStorage.SEALED_SEED.name)
            .putString(KEY_SEALED_SEED, Base64Url.encode(sealed))
            .putString(KEY_IV, Base64Url.encode(cipher.iv))
            .commit()
        if (!saved) {
            // Never pair with an identity that would be lost on restart.
            runCatching { keyStore.deleteEntry(ALIAS_AES) }
            throw IOException("the identity could not be saved")
        }
        return ClientIdentity(clientId, key, KeyStorage.SEALED_SEED)
    }

    @RequiresApi(Build.VERSION_CODES.TIRAMISU)
    private fun createKeystoreKey(): KeystoreEd25519Key {
        val generator = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, ANDROID_KEYSTORE)
        generator.initialize(
            KeyGenParameterSpec.Builder(ALIAS_ED25519, KeyProperties.PURPOSE_SIGN)
                .setAlgorithmParameterSpec(ECGenParameterSpec("ed25519"))
                .setDigests(KeyProperties.DIGEST_NONE)
                .build(),
        )
        val pair = generator.generateKeyPair()
        val raw = rawEd25519PublicKey(pair.public.encoded) ?: error("unexpected Ed25519 public key encoding")
        val key = KeystoreEd25519Key(pair.private, raw)
        // The Keystore must produce signatures the pak accepts.
        val probe = "RL2-ED25519 probe".toByteArray()
        check(Ed25519.verify(raw, key.sign(probe), probe)) { "Keystore Ed25519 signature does not verify" }
        return key
    }

    private fun createAesKey(): SecretKey {
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(ALIAS_AES, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return generator.generateKey()
    }

    companion object {
        private const val TAG = "ReminisLink"
        private const val ANDROID_KEYSTORE = "AndroidKeyStore"
        private const val ALIAS_ED25519 = "link.reminis.identity.ed25519"
        private const val ALIAS_AES = "link.reminis.identity.seal"
        private const val AES_GCM = "AES/GCM/NoPadding"
        private const val KEY_CLIENT_ID = "clientId"
        private const val KEY_STORAGE = "storage"
        private const val KEY_PUBLIC = "publicKey"
        private const val KEY_SEALED_SEED = "sealedSeed"
        private const val KEY_IV = "iv"

        /** DER prefix of an Ed25519 SubjectPublicKeyInfo (RFC 8410), before the 32 raw bytes. */
        private val ED25519_SPKI_PREFIX = byteArrayOf(
            0x30, 0x2a, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x03, 0x21, 0x00,
        )

        fun rawEd25519PublicKey(spki: ByteArray): ByteArray? {
            if (spki.size != ED25519_SPKI_PREFIX.size + Ed25519.PUBLIC_KEY_SIZE) return null
            if (!spki.copyOfRange(0, ED25519_SPKI_PREFIX.size).contentEquals(ED25519_SPKI_PREFIX)) return null
            return spki.copyOfRange(ED25519_SPKI_PREFIX.size, spki.size)
        }
    }
}

/** An Ed25519 key of the Android Keystore: the private key never leaves secure hardware or the keystore daemon. */
private class KeystoreEd25519Key(private val privateKey: PrivateKey, override val publicKey: ByteArray) : Ed25519Key {
    override fun sign(message: ByteArray): ByteArray {
        val signature = Signature.getInstance("Ed25519")
        signature.initSign(privateKey)
        signature.update(message)
        return signature.sign()
    }
}
