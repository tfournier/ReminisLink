package link.reminis.core

import java.io.File
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/** Reads the shared test vectors in place (protocol/test-vectors), never a copy. */
object Vectors {
    private val dir: File by lazy {
        val path = System.getProperty("reminislink.vectors") ?: error("reminislink.vectors is not set")
        File(path).also { check(it.isDirectory) { "missing test vectors at $it" } }
    }

    fun load(name: String): JsonObject = Json.parseToJsonElement(File(dir, "$name.json").readText()).jsonObject
}

fun JsonObject.str(key: String): String = getValue(key).jsonPrimitive.content
fun JsonObject.int(key: String): Int = getValue(key).jsonPrimitive.int
fun JsonObject.objects(key: String): List<JsonObject> = getValue(key).jsonArray.map { it.jsonObject }
fun JsonObject.array(key: String): JsonArray = getValue(key).jsonArray

fun String.hexToBytes(): ByteArray = chunked(2).map { it.toInt(16).toByte() }.toByteArray()
fun String.b64u(): ByteArray = Base64Url.decode(this) ?: error("not base64url: $this")
