package link.reminis.core

import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test

/** The client against a local TLS server with a self-signed P-256 certificate, like the pak's. */
class ApiClientTest {
    private val deviceId = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"
    private val clientId = "3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
    private val key = SeedEd25519Key(ByteArray(32) { it.toByte() })
    private val now = 1_795_539_600_000L

    private lateinit var certificate: HeldCertificate
    private lateinit var server: MockWebServer
    private lateinit var pin: String

    @Before
    fun setUp() {
        certificate = HeldCertificate.Builder().ecdsa256().commonName(deviceId).build()
        pin = TlsPin.pin(certificate.certificate)
        server = MockWebServer()
        server.useHttps(HandshakeCertificates.Builder().heldCertificate(certificate).build().sslSocketFactory(), false)
        server.start()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private val endpoint get() = Endpoint(server.hostName, server.port)

    private fun client(pin: String? = this.pin, credentials: Boolean = true) = ApiClient(
        endpoint = endpoint,
        expectedDeviceId = deviceId,
        credentials = if (credentials) DeviceCredentials(deviceId, clientId, key, this.pin) else null,
        pin = pin,
        nowMillis = { now },
    )

    private fun helloJson(serverTime: String = "2026-11-24T17:00:00Z", apiVersion: Int = 2) = """
        {"deviceId":"$deviceId","deviceName":"Brick","model":"brick","platform":"tg5040","os":"nextui",
         "osVersion":null,"pakVersion":"1.0.1","apiVersion":$apiVersion,"minClientVersion":"1.0.0",
         "serverTime":"$serverTime","pairingOpen":true,"futureField":{"x":1}}
    """.trimIndent()

    private val deviceJson = """
        {"deviceId":"$deviceId","deviceName":"Brick","model":"brick","platform":"tg5040","os":"nextui",
         "osVersion":"NextUI-20261013","pakVersion":"1.0.1","network":{"ipv4":"192.168.1.42","ssid":null},
         "battery":{"percent":80,"charging":false},
         "storage":[{"id":"sd","label":"SD card","totalBytes":1000,"freeBytes":250}],
         "uptimeSeconds":12,"connectedClients":[]}
    """.trimIndent()

    private fun json(body: String, code: Int = 200) =
        MockResponse().setResponseCode(code).setHeader("Content-Type", "application/json").setBody(body)

    @Test
    fun rightPinIsAccepted() = runBlocking {
        server.enqueue(json(helloJson()))
        val hello = client().hello()
        assertEquals("Brick", hello.deviceName)
        val request = server.takeRequest()
        assertEquals("/api/v1/hello", request.path)
        assertEquals("android", request.getHeader("X-RL-Platform"))
        // /hello is not signed.
        assertNull(request.getHeader("X-RL-Signature"))
    }

    @Test
    fun wrongPinIsRefused() = runBlocking {
        server.enqueue(json(helloJson()))
        try {
            client(pin = Base64Url.encode(ByteArray(32) { 7 })).hello()
            fail("wrong pin accepted")
        } catch (_: ApiException.IdentityChanged) {
        }
        // The request never reached the server.
        assertEquals(0, server.requestCount)
    }

    @Test
    fun noPinRefusesEveryServer() = runBlocking {
        server.enqueue(json(helloJson()))
        try {
            ApiClient(endpoint = endpoint).hello()
            fail("accepted without a pin")
        } catch (_: ApiException.IdentityChanged) {
        }
    }

    @Test
    fun systemTrustIsNeverUsed() = runBlocking {
        // A certificate signed by a CA is refused too when its key is not the pinned one.
        val root = HeldCertificate.Builder().certificateAuthority(0).ecdsa256().build()
        val leaf = HeldCertificate.Builder().ecdsa256().signedBy(root).addSubjectAlternativeName(server.hostName).build()
        server.shutdown()
        server = MockWebServer()
        server.useHttps(HandshakeCertificates.Builder().heldCertificate(leaf, root.certificate).build().sslSocketFactory(), false)
        server.start()
        try {
            client().hello()
            fail("accepted another key")
        } catch (_: ApiException.IdentityChanged) {
        }
    }

    @Test
    fun helloChecksDeviceAndVersion() = runBlocking {
        server.enqueue(json(helloJson().replace(deviceId, "00000000-0000-4000-8000-000000000000")))
        try {
            client().hello(); fail()
        } catch (e: ApiException.DeviceMismatch) {
            assertEquals(deviceId, e.expected)
        }
        server.enqueue(json(helloJson(apiVersion = 1)))
        try {
            client().hello(); fail()
        } catch (e: ApiException.Incompatible) {
            assertEquals(1, e.apiVersion)
        }
    }

    @Test
    fun signedRequestCarriesValidHeaders() = runBlocking {
        server.enqueue(json(deviceJson))
        val status = client().device()
        assertEquals(80, status.battery?.percent)
        assertEquals(750L, status.storage.single().usedBytes)

        val request = server.takeRequest()
        assertEquals(clientId, request.getHeader("X-RL-Client"))
        assertEquals((now / 1000).toString(), request.getHeader("X-RL-Time"))
        assertEquals(Signer.contentSha256(ByteArray(0)), request.getHeader("X-RL-Content-SHA256"))
        val nonce = request.getHeader("X-RL-Nonce")!!
        assertEquals(16, Base64Url.decode(nonce)!!.size)
        val canonical = Signer.canonicalRequest(
            "GET", request.requestUrl!!.encodedPath, request.requestUrl!!.encodedQuery ?: "",
            request.getHeader("X-RL-Time")!!, nonce, request.getHeader("X-RL-Content-SHA256")!!,
        )
        assertEquals("RL2-ED25519\nGET\n/api/v1/device\n\n${now / 1000}\n$nonce\n${Signer.contentSha256(ByteArray(0))}", canonical)
        assertTrue(Signer.isValidSignature(request.getHeader("X-RL-Signature")!!, key.publicKey, canonical))
    }

    @Test
    fun everyAttemptHasANewNonce() = runBlocking {
        server.enqueue(json(deviceJson))
        server.enqueue(json(deviceJson))
        val c = client()
        c.device(); c.device()
        val a = server.takeRequest().getHeader("X-RL-Nonce")
        val b = server.takeRequest().getHeader("X-RL-Nonce")
        assertTrue(a != b)
    }

    @Test
    fun clockSkewIsCorrectedOnce() = runBlocking {
        server.enqueue(json("""{"error":{"code":"clock_skew","message":"Clock skew"}}""", 401))
        // The console is 1000 s ahead.
        server.enqueue(json(helloJson(serverTime = "2026-11-24T17:16:40Z")))
        server.enqueue(json(deviceJson))
        val c = client()
        c.device()
        assertEquals(1000L, c.clockOffsetSeconds)
        server.takeRequest()
        assertEquals("/api/v1/hello", server.takeRequest().path)
        val retry = server.takeRequest()
        assertEquals((now / 1000 + 1000).toString(), retry.getHeader("X-RL-Time"))
    }

    @Test
    fun serverErrorsAreDecoded() = runBlocking {
        server.enqueue(json("""{"error":{"code":"unknown_client","message":"Unknown client","details":{}}}""", 401))
        try {
            client().device(); fail()
        } catch (e: ApiException.Server) {
            assertEquals(401, e.status)
            assertEquals(ErrorCodes.UNKNOWN_CLIENT, e.code)
        }
    }

    @Test
    fun signedRouteNeedsCredentials() = runBlocking {
        try {
            client(credentials = false).device(); fail()
        } catch (_: ApiException.NotPaired) {
        }
    }

    @Test
    fun pairingChecksTheDeviceProof() = runBlocking {
        val secret = ByteArray(32) { 3 }
        val attempt = PairingAttempt(secret, deviceId, clientId, key, "Pixel")
        val request = attempt.request
        val deviceNonce = Base64Url.encode(ByteArray(32) { 9 })
        val proof = Pairing.proof(
            secret,
            Pairing.deviceProofMessage(deviceId, clientId, request.clientPublicKey, request.clientNonce, deviceNonce),
        )
        server.enqueue(json("""{"deviceId":"$deviceId","deviceName":"Brick","deviceNonce":"$deviceNonce","proof":"$proof"}"""))
        val response = client(credentials = false).pair(attempt)
        assertEquals("Brick", response.deviceName)
        val sent = ProtocolJson.decodeFromString(PairRequest.serializer(), server.takeRequest().body.readUtf8())
        assertEquals(request.proof, sent.proof)
        assertEquals("android", sent.platform)

        server.enqueue(json("""{"deviceId":"$deviceId","deviceName":"Brick","deviceNonce":"$deviceNonce","proof":"${request.proof}"}"""))
        try {
            client(credentials = false).pair(attempt); fail()
        } catch (e: ApiException.Pairing) {
            assertEquals(PairingException.Reason.INVALID_DEVICE_PROOF, e.reason)
        }
    }

    @Test
    fun libraryRoutes() = runBlocking {
        server.enqueue(
            json(
                """{"games":[{"id":"R2FtZQ","name":"Café","fileName":"Café.gba","kind":"file","sizeBytes":10,
                "modifiedAt":"2026-11-18T16:57:40Z","hasSave":true,"stateCount":2,"lockedStateCount":0,"cheatCount":1,
                "hasArt":false,"systemId":"R0JB","folderId":null,"folderPath":null}],"truncated":false,
                "generatedAt":"2026-11-18T16:57:40Z"}""",
            ),
        )
        server.enqueue(
            json(
                """{"id":"R2FtZQ","systemId":"R0JB","name":"Café","fileName":"Café.gba","kind":"file","totalBytes":30,
                "files":[{"fileId":"a","role":"rom","path":"Roms/GBA/Café.gba","sizeBytes":10,"modifiedAt":"2026-11-18T16:57:40Z"},
                {"fileId":"b","role":"save","path":"Saves/GBA/Café.gba.sav","sizeBytes":10,"modifiedAt":"2026-11-18T16:57:40Z"},
                {"fileId":"c","role":"state","slot":0,"path":".userdata/shared/GBA-mgba/Café.gba.st0","sizeBytes":10,"modifiedAt":"2026-11-18T16:57:40Z"}]}""",
            ),
        )
        val c = client()
        val games = c.allGames().games
        assertEquals(2, games.single().stateCount)
        val detail = c.game("R2FtZQ")
        assertTrue(detail.hasSave)
        assertEquals(1, detail.stateCount)
        assertEquals("Café.gba.sav", detail.files[1].name)
        assertEquals("/api/v1/games", server.takeRequest().path)
        assertEquals("/api/v1/games/R2FtZQ", server.takeRequest().path)
    }

    @Test
    fun queryIsSentCanonical() {
        val url = client().url(listOf("systems", "R0JB", "games"), listOf("q" to "Café Rêverie", "a" to "x/y;"))
        assertEquals("a=x%2Fy%3B&q=Caf%C3%A9%20R%C3%AAverie", url.encodedQuery)
        assertEquals(url.encodedQuery, Signer.canonicalQuery(url.encodedQuery!!))
    }
}
