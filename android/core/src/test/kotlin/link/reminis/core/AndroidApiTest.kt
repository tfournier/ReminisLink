package link.reminis.core

import java.io.File
import java.io.PrintWriter
import java.io.StringWriter
import java.util.spi.ToolProvider
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test

/**
 * `:core` is plain Kotlin/JVM, built against the JDK: nothing stops it from
 * calling a JDK method that Android 8 (API 26, the app's minSdk) does not
 * have, and Android lint does not check a JVM module. This test reads the
 * compiled classes of `:core` (`javap -c`), takes every method, field and
 * class of `java.*` / `javax.*` they use, and checks it against the API
 * database of the installed Android SDK (`platforms/android-N/data/api-versions.xml`,
 * the one lint uses): its class present on Android, and the member added at API 26 or before
 * (Codex review of increment 7: `InputStream.readNBytes(int)` is API 33).
 *
 * Without an Android SDK the test is skipped locally; on CI it fails.
 */
class AndroidApiTest {
    private class ApiClass(val since: Int, val supers: List<String>, val members: Map<String, Int>)

    private fun database(file: File): Map<String, ApiClass> {
        val classes = mutableMapOf<String, ApiClass>()
        var name: String? = null
        var since = 1
        var supers = mutableListOf<String>()
        var members = mutableMapOf<String, Int>()
        val attr = Regex("""(\w+)="([^"]*)"""")
        fun attrs(line: String) = attr.findAll(line).associate { it.groupValues[1] to it.groupValues[2].replace("&lt;", "<") }
        file.forEachLine { raw ->
            val line = raw.trim()
            when {
                line.startsWith("<class ") -> {
                    val a = attrs(line)
                    name = a["name"]
                    since = a["since"]?.toInt() ?: 1
                    supers = mutableListOf()
                    members = mutableMapOf()
                    if (a.containsKey("removed")) name = null
                }
                line.startsWith("</class>") -> {
                    name?.let { classes[it] = ApiClass(since, supers, members) }
                    name = null
                }
                name != null && (line.startsWith("<extends ") || line.startsWith("<implements ")) -> attrs(line)["name"]?.let { supers += it }
                name != null && (line.startsWith("<method ") || line.startsWith("<field ")) -> {
                    val a = attrs(line)
                    if (!a.containsKey("removed")) members[a.getValue("name")] = a["since"]?.toInt() ?: since
                }
            }
        }
        return classes
    }

    /** The API level of a member of [owner] (inherited members included), null when Android does not have it. */
    private fun level(db: Map<String, ApiClass>, owner: String, member: String, seen: MutableSet<String> = mutableSetOf()): Int? {
        if (!seen.add(owner)) return null
        val api = db[owner] ?: return null
        api.members[member]?.let { return maxOf(it, api.since) }
        return api.supers.mapNotNull { level(db, it, member, seen) }.minOrNull()?.let { maxOf(it, api.since) }
    }

    @Test
    fun everyJdkApiOfCoreExistsOnAndroid8() {
        val path = System.getProperty("reminislink.apiVersions")
        val onCi = System.getenv("CI") != null
        if (path == null || !File(path).isFile) {
            assertTrue("no Android SDK API database (platforms/android-N/data/api-versions.xml) on CI", !onCi)
            assumeTrue("no Android SDK installed", false)
        }
        val minSdk = System.getProperty("reminislink.minSdk", "26").toInt()
        val db = database(File(path!!))
        val classFiles = System.getProperty("reminislink.coreClasses").split(File.pathSeparator)
            .map(::File).filter { it.isDirectory }.flatMap { dir -> dir.walkTopDown().filter { it.extension == "class" }.toList() }
        assertTrue("no compiled class of :core found", classFiles.isNotEmpty())
        val javap = ToolProvider.findFirst("javap").orElseThrow()
        // `// Method java/io/InputStream.readNBytes:(I)[B`, `// Field ...`, `// class java/util/HexFormat`.
        val reference = Regex("""// (Method|InterfaceMethod|Field) (javax?/[\w/$]+)\.("?[^:"]+"?):(\S+)""")
        val classReference = Regex("""// class (javax?/[\w/$]+)""")
        val problems = sortedSetOf<String>()
        classFiles.chunked(200).forEach { chunk ->
            val out = StringWriter()
            val err = StringWriter()
            val code = javap.run(PrintWriter(out), PrintWriter(err), *(listOf("-c", "-p") + chunk.map { it.path }).toTypedArray())
            assertTrue("javap failed: $err", code == 0)
            for (line in out.toString().lineSequence()) {
                reference.find(line)?.let { m ->
                    val (kind, owner, rawName, descriptor) = m.destructured
                    val name = rawName.trim('"')
                    val member = if (kind == "Field") name else "$name$descriptor"
                    val api = level(db, owner, member)
                    when {
                        db[owner] == null -> problems += "$owner (class not on Android)"
                        // A member the database does not list (inherited from a hidden class, as StringBuilder.setLength
                        // from AbstractStringBuilder): unknown, not flagged, as lint does.
                        api == null -> Unit
                        api > minSdk -> problems += "$owner.$member (API $api)"
                    }
                }
                classReference.find(line)?.let { m ->
                    val owner = m.groupValues[1]
                    val api = db[owner]
                    if (api == null) problems += "$owner (class not on Android)" else if (api.since > minSdk) problems += "$owner (API ${api.since})"
                }
            }
        }
        assertTrue("JDK APIs used by :core that Android $minSdk does not have:\n" + problems.joinToString("\n"), problems.isEmpty())
    }
}
