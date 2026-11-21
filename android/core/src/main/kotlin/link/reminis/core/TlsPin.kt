package link.reminis.core

import java.security.MessageDigest
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import javax.net.ssl.HostnameVerifier
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSocketFactory
import javax.net.ssl.X509TrustManager

/**
 * The certificate pin of a console (docs/specs/03-protocole.md §1, M13):
 * `b64u(SHA-256(DER of the SubjectPublicKeyInfo))` of its self-signed ECDSA
 * P-256 certificate. The app trusts that key only: no system trust, no
 * certificate authority, no host name check.
 */
object TlsPin {
    const val SIZE = 32

    fun pin(spki: ByteArray): String = Base64Url.encode(MessageDigest.getInstance("SHA-256").digest(spki))

    /** The pin of a certificate (`publicKey.encoded` is the SPKI DER on the JVM and Android). */
    fun pin(certificate: X509Certificate): String = pin(certificate.publicKey.encoded)

    /** Compares two pins in constant time, on the decoded bytes. */
    fun matches(actual: String, expected: String): Boolean {
        val a = Base64Url.decode(actual) ?: return false
        val b = Base64Url.decode(expected) ?: return false
        return a.size == SIZE && b.size == SIZE && constantTimeEquals(a, b)
    }
}

/** The console presented another certificate key than the pinned one. */
class PinMismatchException(val actualPin: String?) :
    CertificateException("the console's certificate does not match its pairing pin")

/**
 * Accepts only a server whose leaf certificate carries the pinned key. The
 * chain is never evaluated against the system trust store, and a missing pin
 * refuses every server (OkHttp's `CertificatePinner` cannot be used: it runs
 * after the system trust check, which a self-signed certificate fails).
 */
class PinnedTrustManager(private val pin: String?) : X509TrustManager {
    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) {
        val leaf = chain?.firstOrNull() ?: throw PinMismatchException(null)
        val actual = TlsPin.pin(leaf)
        if (pin == null || !TlsPin.matches(actual, pin)) throw PinMismatchException(actual)
    }

    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) {
        throw CertificateException("no client certificates")
    }

    override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()

    fun socketFactory(): SSLSocketFactory {
        val context = SSLContext.getInstance("TLS")
        context.init(null, arrayOf(this), null)
        return context.socketFactory
    }

    companion object {
        /** The pin authenticates the server: the host name is irrelevant (the address may be an IP). */
        val anyHostname = HostnameVerifier { _, _ -> true }
    }
}
