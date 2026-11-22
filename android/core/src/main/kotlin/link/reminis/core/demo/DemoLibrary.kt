package link.reminis.core.demo

import link.reminis.core.Base64Url

/**
 * The made-up library of the demo mode (M16), the same as the iOS demo
 * (`DemoLibrary.sample` of ReminisLinkDemo): invented systems, tags,
 * extensions and game names. No commercial game, no trademark, no ROM
 * content.
 */
object DemoLibrary {
    class Game(
        /** Path relative to the system folder, `/`-separated. */
        val path: String,
        val kind: String = "file",
        val sizeBytes: Long,
        val modifiedAtSeconds: Long,
        val hasSave: Boolean = false,
        val stateSlots: List<Int> = emptyList(),
        val lockedStateCount: Int = 0,
        val cheatCount: Int = 0,
        val hasArt: Boolean = false,
    ) {
        val fileName: String get() = path.substringAfterLast('/')

        /** The file name without its extension (the folder name for a folder game). */
        val name: String
            get() {
                if (kind.startsWith("folder")) return fileName
                val dot = fileName.lastIndexOf('.')
                return if (dot > 0) fileName.substring(0, dot) else fileName
            }

        /** The parent folder inside the system ("" at the root). */
        val parent: String get() = if ('/' in path) path.substringBeforeLast('/') else ""
    }

    class System(
        val folder: String,
        val label: String,
        val tag: String,
        val extensions: List<String>,
        val emulatorInstalled: Boolean = true,
        val games: List<Game>,
    ) {
        val id: String get() = id(folder)
    }

    /** Opaque ids are base64url of the path relative to `Roms/`, like the pak's. */
    fun id(path: String): String = Base64Url.encode(path.toByteArray(Charsets.UTF_8))

    private const val BASE = 1_790_000_000L
    private const val DAY = 86_400L

    /** Made-up systems with invented tags and extensions; the tags match no NextUI system. */
    val sample: List<System> = listOf(
        System(
            "Handheld Advance (HA)", "Handheld Advance", "HA", listOf("hab"),
            games = listOf(
                Game(
                    "Thistledown Isles.hab", sizeBytes = 16_777_216, modifiedAtSeconds = BASE, hasSave = true,
                    stateSlots = listOf(0, 1, 2), lockedStateCount = 1, cheatCount = 12, hasArt = true,
                ),
                Game("Brambleway Dash.hab", sizeBytes = 4_194_304, modifiedAtSeconds = BASE + DAY * 3),
                Game("Homebrew/Quillwork Tactics.hab", sizeBytes = 1_048_576, modifiedAtSeconds = BASE + DAY, hasSave = true, hasArt = true),
                Game(
                    "Homebrew/Cartographe Égaré.hab", sizeBytes = 2_097_152, modifiedAtSeconds = BASE + DAY * 2,
                    stateSlots = listOf(0), cheatCount = 2,
                ),
            ),
        ),
        System(
            "01) Pocket Classic (PCL)", "Pocket Classic", "PCL", listOf("pcr"),
            games = listOf(
                Game("Mosslight Maze 🌙.pcr", sizeBytes = 262_144, modifiedAtSeconds = BASE, hasSave = true, stateSlots = listOf(0), hasArt = true),
            ),
        ),
        System(
            "Disc Console (DCN)", "Disc Console", "DCN", listOf("cue", "m3u"), emulatorInstalled = false,
            games = listOf(
                Game(
                    "Orrery of Nine Tides", kind = "folder-m3u", sizeBytes = 7_340_032, modifiedAtSeconds = BASE,
                    hasSave = true, stateSlots = listOf(0, 1), lockedStateCount = 2,
                ),
            ),
        ),
    )
}
