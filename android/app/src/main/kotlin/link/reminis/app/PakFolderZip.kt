package link.reminis.app

import java.io.InputStream
import java.io.OutputStream
import java.text.Normalizer

/**
 * Zips a `.pak` folder of the phone for an installation (M12; iOS zips it
 * with `NSFileCoordinator .forUploading`). The tree is walked iteratively,
 * never recursively, so a document provider that lists a folder inside
 * itself, or a very deep tree, cannot overflow the stack (Codex review 1):
 * - at most [MAX_DEPTH] levels of folders;
 * - each document id is visited once (a cycle is cut);
 * - a folder's children are read one row at a time from the provider, and
 *   the reading stops as soon as [MAX_ENTRIES] entries are seen (a huge
 *   folder is never loaded whole);
 * - [MAX_BYTES] of file content in all.
 * Beyond a limit, the pak would refuse the archive anyway:
 * [LimitExceeded] is thrown and nothing is sent.
 */
object PakFolderZip {
    const val MAX_BYTES = 1L shl 30
    const val MAX_ENTRIES = 10_000
    const val MAX_DEPTH = 32

    /** A document of the tree. */
    data class Child(val documentId: String, val name: String, val isFolder: Boolean)

    /** The documents of a tree, as a document provider gives them. */
    interface Tree {
        /** The children of a folder, read lazily; closed by the caller. */
        fun children(folderId: String): CloseableIterator<Child>

        fun open(documentId: String): InputStream
    }

    interface CloseableIterator<T> : Iterator<T>, AutoCloseable

    class LimitExceeded(message: String) : Exception(message)

    /** Writes the zip of the tree rooted at [rootId] to [out], its entries under `<folderName>/`. */
    fun zip(tree: Tree, rootId: String, folderName: String, out: OutputStream) {
        var entries = 0
        var bytes = 0L
        val visited = mutableSetOf(rootId)
        // Folders still to read: (document id, zip prefix, depth).
        val pending = ArrayDeque<Triple<String, String, Int>>()
        pending.addLast(Triple(rootId, "$folderName/", 1))
        java.util.zip.ZipOutputStream(out).use { zip ->
            while (pending.isNotEmpty()) {
                val (folderId, prefix, depth) = pending.removeFirst()
                tree.children(folderId).use { children ->
                    while (children.hasNext()) {
                        val child = children.next()
                        val name = Normalizer.normalize(child.name, Normalizer.Form.NFC)
                        // Never a path: a name with a separator, or a dot name, is skipped.
                        if (name.isEmpty() || name == "." || name == ".." || name.contains('/') || name.contains('\\')) continue
                        // A document met again (a cycle, or one listed twice) is not zipped twice.
                        if (!visited.add(child.documentId)) continue
                        if (++entries > MAX_ENTRIES) throw LimitExceeded("too many entries")
                        if (child.isFolder) {
                            if (depth >= MAX_DEPTH) throw LimitExceeded("too deep")
                            zip.putNextEntry(java.util.zip.ZipEntry("$prefix$name/"))
                            zip.closeEntry()
                            pending.addLast(Triple(child.documentId, "$prefix$name/", depth + 1))
                            continue
                        }
                        zip.putNextEntry(java.util.zip.ZipEntry("$prefix$name"))
                        tree.open(child.documentId).use { input ->
                            val buffer = ByteArray(256 * 1024)
                            while (true) {
                                val n = input.read(buffer)
                                if (n < 0) break
                                bytes += n
                                if (bytes > MAX_BYTES) throw LimitExceeded("too large")
                                zip.write(buffer, 0, n)
                            }
                        }
                        zip.closeEntry()
                    }
                }
            }
        }
    }
}
