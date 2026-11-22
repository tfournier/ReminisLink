package link.reminis.core

import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.longOrNull

// `POST /games/{gameId}/patch` (protocol/openapi.yaml, `PatchRequest`) and
// the helpers of the Patch screen (docs/specs/06-app-ios.md §4.5, M6).
// Mirror of PatchModels.swift.

/** Where the patch file is on the console. */
sealed interface PatchSource {
    fun toJson(): JsonObject

    /** A staging file, by the `stagingId` its upload returned. */
    data class Staging(val stagingId: String) : PatchSource {
        override fun toJson() = JsonObject(mapOf("type" to JsonPrimitive("staging"), "stagingId" to JsonPrimitive(stagingId)))
    }

    /** A patch of the SD card (`id` of `GET /sd/files?type=patch`). */
    data class Sd(val sdFileId: String) : PatchSource {
        override fun toJson() = JsonObject(mapOf("type" to JsonPrimitive("sd"), "sdFileId" to JsonPrimitive(sdFileId)))
    }
}

/**
 * `new-file` creates a new ROM next to the original; `replace` replaces the
 * ROM (the original goes to the trash; the save, states and cheats stay).
 */
enum class PatchOutputMode(val wire: String) { NEW_FILE("new-file"), REPLACE("replace") }

/** `POST /games/{gameId}/patch`. `fileName` is only sent for `new-file`. */
data class PatchRequest(val patch: PatchSource, val mode: PatchOutputMode, val fileName: String? = null) {
    fun toJson(): String {
        val output = buildMap {
            put("mode", JsonPrimitive(mode.wire))
            if (mode == PatchOutputMode.NEW_FILE && fileName != null) put("fileName", JsonPrimitive(fileName))
        }
        return JsonObject(mapOf("patch" to patch.toJson(), "output" to JsonObject(output))).toString()
    }
}

/** Kinds of a game (`GameKind`); an unknown kind is treated like `folder-m3u`. */
object GameKinds {
    const val FILE = "file"
}

/** Why the Patch screen is not offered for a game. */
enum class PatchRefusal {
    /** `m3u`, `folder-m3u`, `folder-cue` (or unknown): several files. */
    MULTI_DISC,

    /** A `.zip` or `.7z` ROM. */
    ARCHIVE,
}

/** Names on the Patch screen. */
object PatchNaming {
    val patchExtensions = setOf("ips", "bps", "ups")

    /** Archives the console does not patch: the ROM must be extracted first. */
    val archiveExtensions = setOf("zip", "7z")

    /** `<name> (patched).<ext>`, as the pak names a new file by default. */
    fun suggestedFileName(romFileName: String): String {
        val ext = AddKind.fileExtension(romFileName)
        if (ext.isEmpty()) return "$romFileName (patched)"
        val stem = romFileName.dropLast(ext.length + 1)
        return "$stem (patched).${romFileName.takeLast(ext.length)}"
    }

    /**
     * The name typed for a new file, trimmed, or null if the pak would
     * refuse it (docs/specs/03-protocole.md §5): empty, a directory part, a
     * control or format character, a leading dot, a Windows reserved name,
     * more than 255 bytes.
     */
    fun validatedFileName(text: String): String? {
        val name = text.trim()
        if (name.isEmpty() || name.startsWith(".") || name.toByteArray(Charsets.UTF_8).size > 255) return null
        if ('/' in name || '\\' in name) return null
        var i = 0
        while (i < name.length) {
            val cp = name.codePointAt(i)
            if (cp < 0x20 || cp == 0x7f || Character.getType(cp) == Character.FORMAT.toInt()) return null
            i += Character.charCount(cp)
        }
        val stem = name.substringBefore('.').trim().uppercase()
        val reserved = setOf("CON", "PRN", "AUX", "NUL") + (1..9).flatMap { listOf("COM$it", "LPT$it") }
        if (stem in reserved) return null
        return name
    }

    /**
     * What the typed name becomes, as the pak does it: a name that does not
     * already end with `.<extension of the ROM>` (ignoring case) gets it.
     * Null when the pak would refuse it.
     */
    fun outputName(text: String, romFileName: String): String? {
        var name = validatedFileName(text) ?: return null
        val ext = AddKind.fileExtension(romFileName)
        if (ext.isNotEmpty() && !name.lowercase().endsWith(".$ext")) {
            name += "." + romFileName.takeLast(ext.length)
            if (validatedFileName(name) == null) return null
        }
        return name
    }

    /** Why a game cannot be patched, or null. */
    fun refusal(kind: String, fileName: String): PatchRefusal? = when {
        kind != GameKinds.FILE -> PatchRefusal.MULTI_DISC
        AddKind.fileExtension(fileName) in archiveExtensions -> PatchRefusal.ARCHIVE
        else -> null
    }

    /** A file name with the extension of a patch (`.ips`, `.bps`, `.ups`): a shared one opens the Patch screen. */
    fun isPatchFileName(name: String): Boolean = AddKind.fileExtension(name) in patchExtensions
}

/** The ROM a patch expects and the one on the console (`source-mismatch` details), every field optional. */
data class PatchMismatchDetails(
    val expectedSize: Long? = null,
    val actualSize: Long? = null,
    val expectedCrc32: String? = null,
    val actualCrc32: String? = null,
)

/** `details.reason` of `422 patch_mismatch`: `source-mismatch` or `already-patched`. */
val ApiException.Server.patchMismatchReason: String?
    get() = if (code == ErrorCodes.PATCH_MISMATCH) (details["reason"] as? JsonPrimitive)?.contentOrNull else null

/** The sizes and CRC32s of a `patch_mismatch`, null when it has none. */
val ApiException.Server.patchMismatchDetails: PatchMismatchDetails?
    get() {
        if (code != ErrorCodes.PATCH_MISMATCH) return null
        fun long(key: String) = (details[key] as? JsonPrimitive)?.longOrNull
        fun text(key: String) = (details[key] as? JsonPrimitive)?.takeIf { it.isString }?.content
        val d = PatchMismatchDetails(long("expectedSize"), long("actualSize"), text("expectedCrc32"), text("actualCrc32"))
        return if (d == PatchMismatchDetails()) null else d
    }
