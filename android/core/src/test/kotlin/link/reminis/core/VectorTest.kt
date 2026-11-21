package link.reminis.core

import java.io.ByteArrayInputStream
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.util.Base64
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

/** Every shared test vector of protocol/test-vectors, as the Go and Swift suites check them. */
class VectorTest {

    // signing.json

    @Test
    fun signingCases() {
        val file = Vectors.load("signing")
        val cases = file.objects("cases")
        assertTrue(cases.isNotEmpty())
        for (c in cases) {
            val name = c.str("name")
            val key = SeedEd25519Key(c.str("seed").b64u())
            assertEquals(name, c.str("publicKey"), Base64Url.encode(key.publicKey))
            val body = c.str("body").toByteArray(Charsets.UTF_8)
            val unsigned = c.str("contentSha256") == Signer.UNSIGNED_PAYLOAD
            if (!unsigned) assertEquals(name, c.str("contentSha256"), Signer.contentSha256(body))
            assertEquals(name, c.str("canonicalQuery"), Signer.canonicalQuery(c.str("query")))
            val canonical = Signer.canonicalRequest(
                c.str("method"), c.str("path"), c.str("query"), c.str("time"), c.str("nonce"), c.str("contentSha256"),
            )
            assertEquals(name, c.str("canonicalRequest"), canonical)
            // Ed25519 (RFC 8032) is deterministic: Tink gives the same bytes.
            assertEquals(name, c.str("signature"), Signer.signature(key, canonical))
            assertTrue(name, Signer.isValidSignature(c.str("signature"), key.publicKey, canonical))
            assertFalse(name, Signer.isValidSignature(c.str("signature"), key.publicKey, canonical + "x"))

            val headers = Signer(c.str("clientId"), key).headers(
                method = c.str("method"),
                path = c.str("path"),
                rawQuery = c.str("query"),
                body = body,
                unsignedBody = unsigned,
                time = c.str("time").toLong(),
                nonce = c.str("nonce"),
            )
            val expected = c.getValue("headers").jsonObject.mapValues { it.value.jsonPrimitive.content }
            assertEquals(name, expected, headers.asMap())
        }
    }

    @Test
    fun signingInvalidQueries() {
        val queries = Vectors.load("signing").objects("invalidQueries")
        assertTrue(queries.isNotEmpty())
        for (q in queries) {
            try {
                Signer.canonicalQuery(q.str("query"))
                fail("${q.str("name")} accepted")
            } catch (e: SignerException) {
                assertEquals(SignerException.Reason.QUERY, e.reason)
            }
        }
    }

    // pairing.json

    @Test
    fun pairingProofs() {
        val cases = Vectors.load("pairing").objects("cases")
        assertTrue(cases.isNotEmpty())
        for (c in cases) {
            val name = c.str("name")
            val secret = c.str("secret").b64u()
            assertArrayEquals(name, c.str("secretHex").hexToBytes(), secret)
            val key = SeedEd25519Key(c.str("clientSeed").b64u())
            assertEquals(name, c.str("clientPublicKey"), Base64Url.encode(key.publicKey))

            val clientMessage = Pairing.clientProofMessage(
                c.str("deviceId"), c.str("clientId"), c.str("clientPublicKey"), c.str("clientNonce"),
            )
            assertEquals(name, c.str("clientProofMessage"), clientMessage)
            assertEquals(name, c.str("clientProof"), Pairing.proof(secret, clientMessage))
            val deviceMessage = Pairing.deviceProofMessage(
                c.str("deviceId"), c.str("clientId"), c.str("clientPublicKey"), c.str("clientNonce"), c.str("deviceNonce"),
            )
            assertEquals(name, c.str("deviceProofMessage"), deviceMessage)
            assertEquals(name, c.str("deviceProof"), Pairing.proof(secret, deviceMessage))
            assertTrue(Pairing.isValidProof(c.str("deviceProof"), secret, deviceMessage))

            // The whole exchange, as the app runs it.
            val attempt = PairingAttempt(
                secret = secret,
                deviceId = c.str("deviceId"),
                clientId = c.str("clientId"),
                key = key,
                clientName = "Pixel",
                clientNonce = c.str("clientNonce").b64u(),
            )
            val request = attempt.request
            assertEquals(name, c.str("clientProof"), request.proof)
            assertEquals("android", request.platform)
            attempt.finish(PairResponse(c.str("deviceId"), "Brick", c.str("deviceNonce"), c.str("deviceProof")))
            try {
                attempt.finish(PairResponse(c.str("deviceId"), "Brick", c.str("deviceNonce"), c.str("clientProof")))
                fail("$name: wrong device proof accepted")
            } catch (e: PairingException) {
                assertEquals(PairingException.Reason.INVALID_DEVICE_PROOF, e.reason)
            }
        }
    }

    @Test
    fun pairingLinks() {
        val links = Vectors.load("pairing").objects("links")
        assertTrue(links.isNotEmpty())
        for (l in links) {
            val name = l.str("name")
            val parsed = PairingLink.parse(l.str("link"))
            assertEquals(name, l.str("deviceId"), parsed.deviceId)
            assertEquals(name, l.str("deviceName"), parsed.deviceName)
            assertArrayEquals(name, l.str("secret").b64u(), parsed.secret)
            assertEquals(name, Endpoint(l.str("host"), l.int("port")), parsed.endpoint)
            assertEquals(name, l.str("pin"), parsed.pin)
            // Built from its fields, the link is the pak's exactly.
            val built = PairingLink(l.str("deviceId"), l.str("deviceName"), l.str("secret").b64u(), Endpoint(l.str("host"), l.int("port")), l.str("pin"))
            assertEquals(name, l.str("link"), built.toUri())
        }
    }

    // tls.json

    @Test
    fun tlsPin() {
        val v = Vectors.load("tls")
        val spki = v.str("spkiDerHex").hexToBytes()
        assertEquals(v.str("pin"), TlsPin.pin(spki))
        assertEquals(v.str("pinStdBase64"), Base64.getEncoder().encodeToString(v.str("pin").b64u()))
        // The SPKI is the fixed P-256 header followed by the X9.63 point.
        val point = v.str("publicKeyX963Hex").hexToBytes()
        assertArrayEquals(point, spki.copyOfRange(spki.size - point.size, spki.size))

        val certificate = CertificateFactory.getInstance("X.509")
            .generateCertificate(ByteArrayInputStream(v.str("certificatePem").toByteArray())) as X509Certificate
        assertArrayEquals(spki, certificate.publicKey.encoded)
        assertEquals(v.str("pin"), TlsPin.pin(certificate))

        val trust = PinnedTrustManager(v.str("pin"))
        trust.checkServerTrusted(arrayOf(certificate), "ECDHE_ECDSA")
        val other = Base64Url.encode(ByteArray(32))
        try {
            PinnedTrustManager(other).checkServerTrusted(arrayOf(certificate), "ECDHE_ECDSA")
            fail("wrong pin accepted")
        } catch (e: PinMismatchException) {
            assertEquals(v.str("pin"), e.actualPin)
        }
        try {
            PinnedTrustManager(null).checkServerTrusted(arrayOf(certificate), "ECDHE_ECDSA")
            fail("no pin accepted a server")
        } catch (_: PinMismatchException) {
        }
    }

    // ids.json

    @Test
    fun ids() {
        val v = Vectors.load("ids")
        for (id in v.objects("valid")) {
            val bytes = id.str("pathUtf8Hex").hexToBytes()
            assertArrayEquals(id.str("name"), bytes, id.str("path").toByteArray(Charsets.UTF_8))
            assertEquals(id.str("name"), id.str("id"), Base64Url.encode(bytes))
            assertArrayEquals(id.str("name"), bytes, Base64Url.decode(id.str("id")))
        }
        // The app never decodes ids, but its base64url decoder must refuse
        // what the pak refuses as invalid base64url.
        val invalid = v.objects("invalid").filter { it.str("reason") == "invalid base64url" }
        assertTrue(invalid.isNotEmpty())
        for (id in invalid) assertNull(id.str("name"), Base64Url.decode(id.str("id")))
    }
}
