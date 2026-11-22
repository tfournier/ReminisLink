package link.reminis.core

/** One event of a Server-Sent Events stream, once dispatched (mirror of `SSEEvent` of `SSEParser.swift`). */
data class SseEvent(
    /** The last `id` seen in the stream (kept from one event to the next), null before any. */
    val id: String?,
    /** `event`, or `message` when the event named none. */
    val type: String,
    /** The `data` lines joined with `\n`. */
    val data: String,
)

/** What a line or a blank line of the stream produced. */
sealed interface SseItem {
    data class Event(val event: SseEvent) : SseItem

    /** A comment line (`: keep-alive`), without its colon and first space. */
    data class Comment(val text: String) : SseItem
}

/**
 * Incremental parser of `text/event-stream` (the WHATWG "event stream
 * interpretation"), fed byte by byte or in chunks. Port of `SSEParser.swift`:
 *
 * - Lines end with CRLF, LF or CR (a CRLF split between two chunks counts
 *   once); a leading UTF-8 BOM is ignored.
 * - `data` lines accumulate (joined with `\n`), `event` names the type,
 *   `id` sets the last event id (ignored if it holds NUL), `retry` and
 *   unknown fields are ignored; a line without a colon is a field with an
 *   empty value; one space after the colon is removed.
 * - A blank line dispatches the event, unless no `data` line came.
 * - An event cut by the end of the stream is dropped.
 * - A line longer than [MAX_LINE_BYTES] is dropped whole, and an event whose
 *   `data` lines add up to more than [MAX_EVENT_BYTES] is dropped whole (up
 *   to the next blank line): memory stays bounded.
 */
class SseParser {
    private val line = java.io.ByteArrayOutputStream()
    private var lineTooLong = false
    private var lastWasCR = false
    private var isFirstLine = true
    private val data = StringBuilder()
    private var dataBytes = 0
    private var hasData = false
    private var eventTooLarge = false
    private var eventType = ""
    private var lastEventId: String? = null

    /** Feeds one byte; returns what a completed line produced, if anything. */
    fun feed(byte: Byte): SseItem? = when (byte) {
        LF -> if (lastWasCR) {
            lastWasCR = false
            null
        } else {
            endLine()
        }
        CR -> {
            lastWasCR = true
            endLine()
        }
        else -> {
            lastWasCR = false
            if (line.size() < MAX_LINE_BYTES) line.write(byte.toInt()) else lineTooLong = true
            null
        }
    }

    /** Feeds bytes; returns what they produced, in order. */
    fun feed(bytes: ByteArray, offset: Int = 0, length: Int = bytes.size - offset): List<SseItem> {
        val items = ArrayList<SseItem>()
        for (i in offset until offset + length) feed(bytes[i])?.let(items::add)
        return items
    }

    private fun endLine(): SseItem? {
        var bytes = line.toByteArray()
        line.reset()
        if (isFirstLine) {
            isFirstLine = false
            if (bytes.size >= 3 && bytes[0] == 0xEF.toByte() && bytes[1] == 0xBB.toByte() && bytes[2] == 0xBF.toByte()) {
                bytes = bytes.copyOfRange(3, bytes.size)
            }
        }
        if (lineTooLong) {
            lineTooLong = false
            return null
        }
        return process(bytes)
    }

    private fun process(bytes: ByteArray): SseItem? {
        if (bytes.isEmpty()) return dispatch()
        val text = String(bytes, Charsets.UTF_8)
        if (text.startsWith(":")) return SseItem.Comment(text.substring(1).removePrefix(" "))
        val colon = text.indexOf(':')
        val field = if (colon >= 0) text.substring(0, colon) else text
        val value = if (colon >= 0) text.substring(colon + 1).removePrefix(" ") else ""
        when (field) {
            "event" -> eventType = value
            "data" -> if (!eventTooLarge) {
                val size = value.toByteArray(Charsets.UTF_8).size
                if (dataBytes + size + 1 > MAX_EVENT_BYTES) {
                    eventTooLarge = true
                    data.setLength(0)
                    dataBytes = 0
                } else {
                    data.append(value).append('\n')
                    dataBytes += size + 1
                    hasData = true
                }
            }
            "id" -> if ('\u0000' !in value) lastEventId = value
            // `retry` and unknown fields: ignored.
            else -> Unit
        }
        return null
    }

    private fun dispatch(): SseItem? {
        val item = if (hasData && !eventTooLarge) {
            SseItem.Event(SseEvent(lastEventId, eventType.ifEmpty { "message" }, data.toString().removeSuffix("\n")))
        } else {
            null
        }
        data.setLength(0)
        dataBytes = 0
        hasData = false
        eventTooLarge = false
        eventType = ""
        return item
    }

    companion object {
        const val MAX_LINE_BYTES = 1 shl 20
        const val MAX_EVENT_BYTES = 1 shl 20
        private const val LF: Byte = 0x0A
        private const val CR: Byte = 0x0D
    }
}
