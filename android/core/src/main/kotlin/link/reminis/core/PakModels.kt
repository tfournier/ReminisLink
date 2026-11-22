package link.reminis.core

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull

// Tool and emulator paks of the console (M12, `/paks`, the `pak` target of
// `/uploads`). Every identifier is opaque. Mirror of PakModels.swift.

/** `tool` (`Tools/<platform>/`) or `emu` (`Emus/<platform>/`), kept as a string: an unknown kind is kept. */
object PakKinds {
    const val TOOL = "tool"
    const val EMU = "emu"
}

/** Where to install a pak; `auto` lets the console deduce it. */
enum class PakInstallKind(val wire: String) { AUTO("auto"), TOOL("tool"), EMU("emu") }

/** An installed pak (`GET /paks`); lenient on the optional fields. */
@Serializable
data class Pak(
    val pakId: String,
    /** The folder name without `.pak`. */
    val name: String,
    val kind: String,
    /** Informative, never sent back. */
    val path: String = "",
    val sizeBytes: Long = 0,
    val modifiedAt: String = "",
    val version: String? = null,
    /** ReminisLink itself: never deleted nor replaced. */
    @SerialName("self") val isSelf: Boolean = false,
)

@Serializable
data class PakList(val paks: List<Pak>) {
    companion object {
        /** The tools, then the emulators, then any other kind, each by name ignoring case. */
        fun grouped(paks: List<Pak>): List<Pair<String, List<Pak>>> {
            val order = mutableListOf(PakKinds.TOOL, PakKinds.EMU)
            paks.forEach { if (it.kind !in order) order += it.kind }
            return order.mapNotNull { kind ->
                paks.filter { it.kind == kind }.sortedWith(compareBy(String.CASE_INSENSITIVE_ORDER) { it.name })
                    .takeIf { it.isNotEmpty() }?.let { kind to it }
            }
        }
    }
}

/**
 * Why the pak refused an archive (`422 unsupported_format` in the error of
 * a pak installation job). The pak's message is English; the reasons the
 * app knows get their own text, any other is [Other].
 */
sealed interface PakRefusal {
    data object NotAnArchive : PakRefusal
    data object NoPak : PakRefusal
    data object NoLaunchScript : PakRefusal
    data object SeveralPaks : PakRefusal
    data object UnsafeEntry : PakRefusal
    data object TooLarge : PakRefusal
    data object UnsupportedCompression : PakRefusal
    data object NoName : PakRefusal
    data class Other(val reason: String) : PakRefusal

    companion object {
        /** The reason in the pak's message (`Invalid pak: ` dropped). */
        fun reason(message: String): String = message.removePrefix("Invalid pak: ")

        fun from(message: String): PakRefusal {
            val reason = reason(message)
            val lower = reason.lowercase()
            fun has(text: String) = text in lower
            return when {
                has("not a zip") || has("corrupt archive") || has("unreadable entry") -> NotAnArchive
                has("no launch.sh") -> NoLaunchScript
                has("more than one pak") -> SeveralPaks
                has("no .pak folder") || has("pak folder is a file") || has("invalid pak folder name") ||
                    has("not a .pak folder nor an archive") -> NoPak
                has("cannot name the pak") -> NoName
                has("encrypted") || has("compression method") -> UnsupportedCompression
                (has("entries") && has("more than")) || has("uncompressed") || has("compressed more than") ||
                    has("central directory") -> TooLarge
                listOf(
                    "symbolic link", "absolute", "outside the pak folder", "path component", "backslash", "control character",
                    "nul byte", "utf-8", "same name", "neither a file nor a folder", "longer than", "character the sd card",
                    "empty entry name",
                ).any(::has) -> UnsafeEntry
                else -> Other(reason)
            }
        }
    }
}

/** `409 conflict` with `details.self: true`: ReminisLink cannot delete or replace itself while it runs. */
val ApiException.Server.isPakSelfConflict: Boolean
    get() = code == ErrorCodes.CONFLICT && (details["self"] as? JsonPrimitive)?.booleanOrNull == true

/**
 * `409 conflict` of a pak installation for an installed pak of the same
 * name: its name, from `details.existing` (`Tools/tg5040/Clock.pak` gives
 * `Clock`). Null for ReminisLink itself.
 */
val ApiException.Server.conflictingPakName: String?
    get() {
        if (code != ErrorCodes.CONFLICT || isPakSelfConflict) return null
        val existing = (details["existing"] as? JsonPrimitive)?.takeIf { it.isString }?.contentOrNull ?: return null
        val last = existing.substringAfterLast('/')
        return if (last.lowercase().endsWith(".pak")) last.dropLast(4) else last
    }

/** `422 unsupported_format` of a pak installation: why. */
val ApiException.Server.pakRefusal: PakRefusal?
    get() = if (code == ErrorCodes.UNSUPPORTED_FORMAT) PakRefusal.from(serverMessage) else null
