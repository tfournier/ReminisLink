package link.reminis.core.demo

import java.security.SecureRandom
import java.util.UUID
import link.reminis.core.ApiClient
import link.reminis.core.Base64Url
import link.reminis.core.DeviceCredentials
import link.reminis.core.Endpoint
import link.reminis.core.SeedEd25519Key
import okhttp3.OkHttpClient

/**
 * One session of the demo mode (M16): a [FakePak] and the signed client that
 * talks to it. Isolated from the real app:
 * - **No network**: the client's requests are answered by the fake pak's
 *   interceptor before OkHttp would resolve a name or open a socket; its
 *   host (a new `<uuid>.try.demo.reminislink.invalid` per session, a
 *   reserved TLD) could not be reached anyway.
 * - **No Keystore, no stored pairing**: its identity is a random key in
 *   memory, its pin a random value; nothing is written to the list of
 *   consoles.
 * - **No trace**: [close] stops the fake pak; everything it held is in memory.
 */
class DemoConsole(deviceName: String, cheatName: (Int) -> String) {
    val pak = FakePak(deviceName = deviceName, cheatName = cheatName)
    val deviceId: String get() = pak.deviceId
    val endpoint = Endpoint("${UUID.randomUUID().toString().lowercase()}.try.demo.reminislink.invalid", 443)

    /** The demo's own pairing identity (never the Keystore's): pin and session. */
    val identity: Any = "demo:" + UUID.randomUUID()

    private val random = SecureRandom()
    private val pin = Base64Url.encode(ByteArray(32).also(random::nextBytes))
    private val credentials = DeviceCredentials(
        deviceId = pak.deviceId,
        clientId = UUID.randomUUID().toString().lowercase(),
        key = SeedEd25519Key(ByteArray(32).also(random::nextBytes)),
        pin = pin,
    )

    val client = ApiClient(
        endpoint = endpoint,
        expectedDeviceId = pak.deviceId,
        credentials = credentials,
        pin = pin,
        baseClient = OkHttpClient.Builder().addInterceptor(pak.interceptor).build(),
    )

    fun close() = pak.close()
}
