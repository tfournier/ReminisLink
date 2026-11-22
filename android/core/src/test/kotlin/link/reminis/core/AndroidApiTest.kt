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
 * the one lint uses): the class must be on Android, and the member listed
 * there at API 26 or before. A member the database does not list fails too
 * (`Thread.startVirtualThread` exists in the JDK, not on Android), except
 * the members of [INHERITED_FROM_HIDDEN], checked one by one.
 *
 * Without an Android SDK the test is skipped locally; on CI it fails.
 */
class AndroidApiTest {
    private class ApiClass(val since: Int, val supers: List<String>, val members: Map<String, Int>)

    /** A use of the JDK found in a class file: `owner` and `member` (`name(descriptor)` or a field name), or a class alone. */
    private data class Use(val owner: String, val member: String?)

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

    /** The API level of a member of [owner] (inherited members included), null when the database does not list it. */
    private fun level(db: Map<String, ApiClass>, owner: String, member: String, seen: MutableSet<String> = mutableSetOf()): Int? {
        if (!seen.add(owner)) return null
        val api = db[owner] ?: return null
        api.members[member]?.let { return maxOf(it, api.since) }
        return api.supers.mapNotNull { level(db, it, member, seen) }.minOrNull()?.let { maxOf(it, api.since) }
    }

    /**
     * Every `java.*` / `javax.*` class, method and field the class files use,
     * read from their constant pools (`javap -v -p` of the JDK): the
     * references of the instructions, and the `MethodHandle` entries that the
     * `BootstrapMethods` of an invokedynamic point to (a Java method
     * reference such as `Thread::startVirtualThread`, whose instruction does
     * not name its target).
     */
    private fun uses(classFiles: List<File>): Set<Use> {
        val javap = ToolProvider.findFirst("javap").orElseThrow()
        // `#7 = Methodref #8.#9 // java/io/InputStream.readNBytes:(I)[B`, `= Fieldref`, `= InterfaceMethodref`,
        // `= MethodHandle 6:#31 // REF_invokeStatic java/lang/Thread.startVirtualThread:(...)`, `= Class #12 // java/util/HexFormat`.
        val reference = Regex("""= (Methodref|InterfaceMethodref|Fieldref)\s.*// (javax?/[\w/$]+)\.("?[^:"]+"?):(\S+)""")
        val handle = Regex("""= MethodHandle\s.*// REF_(\w+) (javax?/[\w/$]+)\.("?[^:"]+"?):(\S+)""")
        val classReference = Regex("""= Class\s.*// (javax?/[\w/$]+)\s*$""")
        val uses = mutableSetOf<Use>()
        classFiles.chunked(200).forEach { chunk ->
            val out = StringWriter()
            val err = StringWriter()
            val code = javap.run(PrintWriter(out), PrintWriter(err), *(listOf("-v", "-p") + chunk.map { it.path }).toTypedArray())
            assertTrue("javap failed: $err", code == 0)
            for (line in out.toString().lineSequence()) {
                reference.find(line)?.let { m ->
                    val (kind, owner, rawName, descriptor) = m.destructured
                    val name = rawName.trim('"')
                    uses += Use(owner, if (kind == "Fieldref") name else "$name$descriptor")
                }
                handle.find(line)?.let { m ->
                    val (_, owner, rawName, descriptor) = m.destructured
                    val name = rawName.trim('"')
                    // REF_getField / getStatic / putField / putStatic name a field; the REF_invoke* kinds a method.
                    uses += Use(owner, if (!descriptor.startsWith("(")) name else "$name$descriptor")
                }
                classReference.find(line)?.let { uses += Use(it.groupValues[1], null) }
            }
        }
        return uses
    }

    /** The uses Android [minSdk] does not have, as readable lines. */
    private fun problems(uses: Set<Use>, db: Map<String, ApiClass>, minSdk: Int): Set<String> = uses.mapNotNull { use ->
        if (use.owner in DESUGARED_BOOTSTRAPS) return@mapNotNull null
        val api = db[use.owner] ?: return@mapNotNull "${use.owner} (class not on Android)"
        if (use.member == null) return@mapNotNull if (api.since > minSdk) "${use.owner} (API ${api.since})" else null
        if ("${use.owner}.${use.member}" in INHERITED_FROM_HIDDEN) return@mapNotNull null
        val level = level(db, use.owner, use.member)
        when {
            level == null -> "${use.owner}.${use.member} (not listed for Android)"
            level > minSdk -> "${use.owner}.${use.member} (API $level)"
            else -> null
        }
    }.toSortedSet()

    private fun databaseOrSkip(): Map<String, ApiClass> {
        val path = System.getProperty("reminislink.apiVersions")
        if (path == null || !File(path).isFile) {
            assertTrue("no Android SDK API database (platforms/android-N/data/api-versions.xml) on CI", System.getenv("CI") == null)
            assumeTrue("no Android SDK installed", false)
        }
        return database(File(path!!))
    }

    private val minSdk get() = System.getProperty("reminislink.minSdk", "26").toInt()

    @Test
    fun everyJdkApiOfCoreExistsOnAndroid8() {
        val db = databaseOrSkip()
        val classFiles = System.getProperty("reminislink.coreClasses").split(File.pathSeparator)
            .map(::File).filter { it.isDirectory }.flatMap { dir -> dir.walkTopDown().filter { it.extension == "class" }.toList() }
        assertTrue("no compiled class of :core found", classFiles.isNotEmpty())
        val problems = problems(uses(classFiles), db, minSdk)
        assertTrue("JDK APIs used by :core that Android $minSdk does not have:\n" + problems.joinToString("\n"), problems.isEmpty())
    }

    /** The check itself: a JDK-only call (compiled into [VirtualThreadProbe]) and an API newer than 26 are caught. */
    @Test
    fun aJdkOnlyCallIsCaught() {
        val db = databaseOrSkip()
        val probe = File(VirtualThreadProbe::class.java.getResource("VirtualThreadProbe.class")!!.toURI())
        val problems = problems(uses(listOf(probe)), db, minSdk)
        assertTrue(problems.toString(), problems.any { it.startsWith("java/lang/Thread.startVirtualThread") })
        assertTrue(problems.toString(), problems.any { it.startsWith("java/io/InputStream.readNBytes") && it.endsWith("(API 33)") })
        assertTrue("an allowed member was flagged: $problems", problems.none { it.contains("StringBuilder.setLength") })
    }

    /** A Java method reference (`Thread::startVirtualThread`): its target is only in the BootstrapMethods' MethodHandle. */
    @Test
    fun aMethodReferenceIsCaught() {
        val db = databaseOrSkip()
        val probe = File(MethodReferenceProbe::class.java.getResource("MethodReferenceProbe.class")!!.toURI())
        val found = uses(listOf(probe))
        assertTrue(found.toString(), Use("java/lang/Thread", "startVirtualThread(Ljava/lang/Runnable;)Ljava/lang/Thread;") in found)
        val problems = problems(found, db, minSdk)
        assertTrue(problems.toString(), problems.any { it.startsWith("java/lang/Thread.startVirtualThread") })
    }

    companion object {
        /**
         * Members the API database does not list under the class the code
         * names, because Android declares them in a hidden superclass
         * (`AbstractStringBuilder`): present on Android since API 1, checked
         * in the Android reference of the public class. Add an entry only
         * after checking it the same way.
         */
        /**
         * Bootstrap methods of invokedynamic that D8 rewrites when it builds
         * the app (lambdas and method references, string concatenation of
         * Java 9+ bytecode): they never reach Android. Their MethodHandle
         * targets, the methods actually called, are still checked.
         */
        val DESUGARED_BOOTSTRAPS = setOf(
            "java/lang/invoke/LambdaMetafactory",
            "java/lang/invoke/StringConcatFactory",
        )

        val INHERITED_FROM_HIDDEN = setOf(
            // StringBuilder.setLength(int): Android reference, java.lang.StringBuilder, "Added in API level 1".
            "java/lang/StringBuilder.setLength(I)V",
        )
    }
}

/** Compiled with the tests only: calls the AndroidApiTest must catch (never run). */
@Suppress("unused")
internal class VirtualThreadProbe {
    fun virtualThread(): Thread = Thread.startVirtualThread {}

    fun bounded(input: java.io.InputStream): ByteArray = input.readNBytes(1)

    fun allowed(builder: StringBuilder) = builder.setLength(0)
}
