package com.anysave.app

import android.content.Context
import android.graphics.pdf.PdfDocument
import android.text.Html
import android.text.Layout
import android.text.Spanned
import android.text.StaticLayout
import android.text.TextPaint
import android.util.Xml
import org.xmlpull.v1.XmlPullParser
import java.io.File
import java.io.InputStream
import java.util.UUID
import java.util.zip.CRC32
import java.util.zip.ZipEntry
import java.util.zip.ZipFile
import java.util.zip.ZipOutputStream

/**
 * Documents: every input is first turned into simple HTML (headings, paragraphs, lists, bold, italic),
 * then written as PDF, text, a web page or an EPUB ebook. Pictures and complex layouts are left out.
 */
object DocTools {
    fun convert(ctx: Context, id: String, input: File, out: File, spec: ConvertSpec, onProgress: (Float) -> Unit) {
        val ext = input.extension.lowercase()
        if (ext == out.extension.lowercase()) throw ConvertError("The file is already ${ext.uppercase()}. Pick another format.")
        val title = input.nameWithoutExtension
        val html = toHtml(input)
        onProgress(40f)
        ConvertTools.check(id)
        if (html.isBlank() || Html.fromHtml(html, Html.FROM_HTML_MODE_LEGACY).isBlank()) {
            throw ConvertError(if (ext == "pdf") "No text found. This PDF may be scanned pictures of pages." else "No text found in this file.")
        }
        when (out.extension.lowercase()) {
            "pdf" -> pdf(id, html, out, spec, onProgress)
            "txt" -> out.writeText(Html.fromHtml(html, Html.FROM_HTML_MODE_LEGACY).toString().replace(Regex("\n{3,}"), "\n\n").trim() + "\n")
            "html" -> out.writeText(page(title, html))
            "epub" -> epub(title, html, out)
        }
        onProgress(100f)
    }

    // ---------- readers ----------

    /** Any supported document as simple HTML (also used by the live preview). */
    fun toHtml(input: File): String = when (input.extension.lowercase()) {
        "pdf" -> textToHtml(PdfTools.text(input))
        "docx" -> ZipFile(input).use { docx(it) }
        "odt" -> ZipFile(input).use { odt(it) }
        "epub" -> ZipFile(input).use { epub(it) }
        "md", "markdown" -> markdown(input.readText())
        "html", "htm", "xhtml" -> cleanHtml(input.readText())
        else -> textToHtml(input.readText())
    }

    private fun esc(s: String) = s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")

    fun textToHtml(text: String) = text.replace("\r\n", "\n").split(Regex("\n\\s*\n"))
        .filter { it.isNotBlank() }.joinToString("\n") { "<p>${esc(it.trim()).replace("\n", "<br>")}</p>" }

    private fun cleanHtml(s: String): String {
        val body = Regex("(?is)<body[^>]*>(.*)</body>").find(s)?.groupValues?.get(1) ?: s
        return body.replace(Regex("(?is)<(script|style|svg|noscript|head)[^>]*>.*?</\\1>"), "")
            .replace(Regex("(?is)<img[^>]*>"), "")
    }

    private fun parser(input: InputStream): XmlPullParser = Xml.newPullParser().apply {
        setFeature(XmlPullParser.FEATURE_PROCESS_NAMESPACES, false)
        setInput(input, null)
    }

    /** Word .docx: paragraphs, headings (Heading 1–6, Title), list items, bold/italic/underline. */
    private fun docx(zip: ZipFile): String {
        val entry = zip.getEntry("word/document.xml") ?: throw ConvertError("This doesn't look like a Word document.")
        val out = StringBuilder()
        zip.getInputStream(entry).use { ins ->
            val p = parser(ins)
            var para: StringBuilder? = null
            var style: String? = null
            var list = false
            var bold = false; var italic = false; var under = false
            var inText = false
            var size = 0; var maxSize = 0 // half-points; many documents make headings by just using bigger text
            while (p.next() != XmlPullParser.END_DOCUMENT) {
                when (p.eventType) {
                    XmlPullParser.START_TAG -> when (p.name) {
                        "w:p" -> { para = StringBuilder(); style = null; list = false; maxSize = 0 }
                        "w:sz" -> size = p.getAttributeValue(null, "w:val")?.toIntOrNull() ?: 0
                        "w:pStyle" -> style = p.getAttributeValue(null, "w:val")
                        "w:numPr" -> list = true
                        "w:r" -> { bold = false; italic = false; under = false; size = 0 }
                        "w:b" -> bold = p.getAttributeValue(null, "w:val").let { it == null || it == "1" || it == "true" }
                        "w:i" -> italic = p.getAttributeValue(null, "w:val").let { it == null || it == "1" || it == "true" }
                        "w:u" -> under = p.getAttributeValue(null, "w:val").let { it != null && it != "none" }
                        "w:t" -> inText = true
                        "w:tab" -> para?.append(" ")
                        "w:br", "w:cr" -> para?.append("<br>")
                    }
                    XmlPullParser.TEXT -> if (inText) {
                        var t = esc(p.text)
                        if (under) t = "<u>$t</u>"
                        if (italic) t = "<i>$t</i>"
                        if (bold) t = "<b>$t</b>"
                        para?.append(t)
                        if (p.text.isNotBlank()) maxSize = maxOf(maxSize, size)
                    }
                    XmlPullParser.END_TAG -> when (p.name) {
                        "w:t" -> inText = false
                        "w:tc" -> para?.append(" ")
                        "w:p" -> {
                            val text = para?.toString().orEmpty()
                            if (text.isNotBlank()) {
                                val level = style?.let { s ->
                                    Regex("(?i)^heading\\s*(\\d)").find(s)?.groupValues?.get(1)?.toIntOrNull()
                                        ?: if (s.equals("Title", true)) 1 else null
                                } ?: if (!list && text.length < 300) when {
                                    maxSize >= 36 -> 1   // 18 pt and up
                                    maxSize >= 30 -> 2   // 15 pt
                                    maxSize >= 27 -> 3
                                    else -> null
                                } else null
                                out.append(when {
                                    level != null -> "<h${level.coerceIn(1, 6)}>$text</h${level.coerceIn(1, 6)}>"
                                    list -> "<p>• $text</p>"
                                    else -> "<p>$text</p>"
                                }).append('\n')
                            }
                            para = null
                        }
                    }
                }
            }
        }
        return out.toString()
    }

    /** OpenDocument .odt: headings, paragraphs and list items. */
    private fun odt(zip: ZipFile): String {
        val entry = zip.getEntry("content.xml") ?: throw ConvertError("This doesn't look like an ODT document.")
        val out = StringBuilder()
        zip.getInputStream(entry).use { ins ->
            val p = parser(ins)
            val stack = ArrayDeque<Pair<String, StringBuilder>>()
            var listDepth = 0
            while (p.next() != XmlPullParser.END_DOCUMENT) {
                when (p.eventType) {
                    XmlPullParser.START_TAG -> when (p.name) {
                        "text:h" -> stack.addLast("h${p.getAttributeValue(null, "text:outline-level")?.toIntOrNull()?.coerceIn(1, 6) ?: 2}" to StringBuilder())
                        "text:p" -> stack.addLast("p" to StringBuilder())
                        "text:list-item" -> listDepth++
                        "text:s" -> stack.lastOrNull()?.second?.append(" ")
                        "text:tab" -> stack.lastOrNull()?.second?.append(" ")
                        "text:line-break" -> stack.lastOrNull()?.second?.append("<br>")
                    }
                    XmlPullParser.TEXT -> stack.lastOrNull()?.second?.append(esc(p.text))
                    XmlPullParser.END_TAG -> when (p.name) {
                        "text:h", "text:p" -> stack.removeLastOrNull()?.let { (tag, sb) ->
                            if (sb.isNotBlank()) {
                                val parent = stack.lastOrNull()
                                val html = if (tag == "p" && listDepth > 0) "<p>• $sb</p>" else "<$tag>$sb</$tag>"
                                if (parent != null) parent.second.append(sb) else out.append(html).append('\n')
                            }
                        }
                        "text:list-item" -> listDepth--
                    }
                }
            }
        }
        return out.toString()
    }

    /** EPUB: the chapters in reading order. */
    private fun epub(zip: ZipFile): String {
        fun read(path: String) = zip.getEntry(path)?.let { e -> zip.getInputStream(e).use { it.readBytes().toString(Charsets.UTF_8) } }
        val container = read("META-INF/container.xml") ?: throw ConvertError("This doesn't look like an EPUB ebook.")
        val opfPath = Regex("full-path=\"([^\"]+)\"").find(container)?.groupValues?.get(1) ?: throw ConvertError("This EPUB is damaged.")
        val opf = read(opfPath) ?: throw ConvertError("This EPUB is damaged.")
        val baseDir = opfPath.substringBeforeLast('/', "").let { if (it.isEmpty()) "" else "$it/" }
        val manifest = Regex("<item\\b[^>]*>").findAll(opf).associate { m ->
            val tag = m.value
            (Regex("\\bid=\"([^\"]+)\"").find(tag)?.groupValues?.get(1) ?: "") to (Regex("\\bhref=\"([^\"]+)\"").find(tag)?.groupValues?.get(1) ?: "")
        }
        val spine = Regex("<itemref\\b[^>]*idref=\"([^\"]+)\"").findAll(opf).map { it.groupValues[1] }.toList()
        return spine.mapNotNull { manifest[it] }.mapNotNull { href ->
            read(baseDir + java.net.URLDecoder.decode(href.substringBefore('#'), "UTF-8"))?.let { cleanHtml(it) }
        }.joinToString("\n")
    }

    /** A small Markdown reader: headings, lists, quotes, code, bold, italic, links. */
    fun markdown(md: String): String {
        val out = StringBuilder()
        val para = mutableListOf<String>()
        var code: StringBuilder? = null
        fun inline(s: String) = esc(s)
            .replace(Regex("!\\[[^]]*]\\([^)]*\\)"), "")
            .replace(Regex("\\[([^]]+)]\\(([^)]+)\\)"), "<a href=\"$2\">$1</a>")
            .replace(Regex("`([^`]+)`"), "<tt>$1</tt>")
            .replace(Regex("\\*\\*([^*]+)\\*\\*|__([^_]+)__")) { "<b>${it.groupValues[1].ifEmpty { it.groupValues[2] }}</b>" }
            .replace(Regex("(?<![*\\w])\\*([^*]+)\\*(?!\\*)|(?<![_\\w])_([^_]+)_(?!\\w)")) { "<i>${it.groupValues[1].ifEmpty { it.groupValues[2] }}</i>" }
        fun flush() { if (para.isNotEmpty()) { out.append("<p>${para.joinToString(" ") { inline(it) }}</p>\n"); para.clear() } }
        for (raw in md.replace("\r\n", "\n").lines()) {
            val line = raw.trimEnd()
            if (code != null) {
                if (line.trimStart().startsWith("```")) { out.append("<pre>${esc(code.toString().trimEnd())}</pre>\n"); code = null }
                else code.append(raw).append('\n')
                continue
            }
            val t = line.trimStart()
            when {
                t.startsWith("```") -> { flush(); code = StringBuilder() }
                t.isEmpty() -> flush()
                Regex("^#{1,6}\\s").containsMatchIn(t) -> {
                    flush(); val n = t.takeWhile { it == '#' }.length
                    out.append("<h$n>${inline(t.drop(n).trim().trimEnd('#').trim())}</h$n>\n")
                }
                Regex("^([-*+]|\\d+[.)])\\s+").containsMatchIn(t) -> {
                    flush()
                    val m = Regex("^([-*+]|\\d+[.)])\\s+").find(t)!!
                    val marker = if (m.groupValues[1][0].isDigit()) m.groupValues[1] else "•"
                    out.append("<p>$marker ${inline(t.substring(m.range.last + 1))}</p>\n")
                }
                t.startsWith(">") -> { flush(); out.append("<blockquote>${inline(t.trimStart('>').trim())}</blockquote>\n") }
                Regex("^(-{3,}|\\*{3,}|_{3,})$").matches(t) -> { flush(); out.append("<p>* * *</p>\n") }
                else -> para += t
            }
        }
        code?.let { out.append("<pre>${esc(it.toString())}</pre>\n") }
        flush()
        return out.toString()
    }

    // ---------- writers ----------

    private fun page(title: String, body: String) = """<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>${esc(title)}</title>
<style>body{font-family:system-ui,sans-serif;max-width:46em;margin:2em auto;padding:0 1em;line-height:1.6;color:#17181c}
h1,h2,h3{line-height:1.25}pre{background:#f4f5f7;padding:1em;overflow:auto}blockquote{border-left:3px solid #ccc;margin-left:0;padding-left:1em;color:#555}</style>
</head><body>
$body
</body></html>
"""

    /** Lays the text out on A4 or Letter pages. Text stays real text, so it can be searched and copied. */
    private fun pdf(id: String, html: String, out: File, spec: ConvertSpec, onProgress: (Float) -> Unit) {
        val (pw, ph) = if (spec.opt("page") == "letter") 612 to 792 else 595 to 842
        val margin = 56
        val size = when (spec.opt("font")) { "small" -> 10f; "large" -> 14f; else -> 12f }
        val text: Spanned = Html.fromHtml(html, Html.FROM_HTML_MODE_LEGACY)
        val paint = TextPaint(TextPaint.ANTI_ALIAS_FLAG).apply { textSize = size; color = android.graphics.Color.rgb(23, 24, 28) }
        val width = pw - 2 * margin
        val layout = StaticLayout.Builder.obtain(text, 0, text.length, paint, width)
            .setAlignment(Layout.Alignment.ALIGN_NORMAL)
            .setLineSpacing(size * 0.3f, 1f)
            .setIncludePad(false)
            .build()
        val doc = PdfDocument()
        try {
            val contentH = ph - 2 * margin
            var line = 0
            var pageNo = 1
            while (line < layout.lineCount) {
                ConvertTools.check(id)
                val top = layout.getLineTop(line)
                var last = line
                while (last + 1 < layout.lineCount && layout.getLineBottom(last + 1) - top <= contentH) last++
                val page = doc.startPage(PdfDocument.PageInfo.Builder(pw, ph, pageNo++).create())
                val c = page.canvas
                c.save()
                c.translate(margin.toFloat(), (margin - top).toFloat())
                c.clipRect(0, top, width, layout.getLineBottom(last))
                layout.draw(c)
                c.restore()
                doc.finishPage(page)
                line = last + 1
                onProgress(40f + 55f * line / layout.lineCount)
            }
            out.outputStream().use { doc.writeTo(it) }
        } finally { doc.close() }
    }

    private fun wellFormed(fragment: String): Boolean = runCatching {
        val p = android.util.Xml.newPullParser()
        p.setInput(java.io.StringReader("<body>$fragment</body>"))
        while (p.next() != XmlPullParser.END_DOCUMENT) { }
        true
    }.getOrDefault(false)

    /** A valid EPUB 3 ebook with one chapter. */
    private fun epub(title: String, html: String, out: File) {
        // Keep real headings (reading apps build the chapter list from them) when our HTML is clean XHTML;
        // otherwise let Android rewrite it into something valid.
        var n = 0
        val tidy = html.replace(Regex("(?i)<br\\s*/?>"), "<br/>").replace(Regex("(?i)<hr\\s*/?>"), "<hr/>").replace(Regex("(?i)<img[^>]*>"), "")
            .replace(Regex("(?i)<h([12])>")) { "<h${it.groupValues[1]} id=\"s${++n}\">" }
        val body = if (wellFormed(tidy)) tidy else Html.toHtml(Html.fromHtml(html, Html.FROM_HTML_MODE_LEGACY), Html.TO_HTML_PARAGRAPH_LINES_CONSECUTIVE)
            .replace("<br>", "<br/>").replace(Regex("(?i)<img[^>]*>"), "")
        val toc = if (body === tidy) Regex("""<h[12] id="(s\d+)">(.*?)</h[12]>""").findAll(tidy).map { m ->
            """<li><a href="chapter.xhtml#${m.groupValues[1]}">${m.groupValues[2].replace(Regex("<[^>]+>"), "")}</a></li>"""
        }.joinToString("") else ""
        val t = esc(title)
        val uid = "urn:uuid:${UUID.randomUUID()}"
        val modified = java.text.SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", java.util.Locale.US)
            .apply { timeZone = java.util.TimeZone.getTimeZone("UTC") }.format(java.util.Date())
        ZipOutputStream(out.outputStream()).use { z ->
            val mime = "application/epub+zip".toByteArray()
            z.putNextEntry(ZipEntry("mimetype").apply { method = ZipEntry.STORED; size = mime.size.toLong(); crc = CRC32().apply { update(mime) }.value })
            z.write(mime); z.closeEntry()
            fun add(name: String, text: String) { z.putNextEntry(ZipEntry(name)); z.write(text.toByteArray()); z.closeEntry() }
            add("META-INF/container.xml", """<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles>
<rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>""")
            add("OEBPS/content.opf", """<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="uid">$uid</dc:identifier><dc:title>$t</dc:title>
<dc:language>en</dc:language><meta property="dcterms:modified">$modified</meta></metadata>
<manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
<item id="c1" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest>
<spine><itemref idref="c1"/></spine></package>""")
            add("OEBPS/nav.xhtml", """<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>$t</title></head>
<body><nav epub:type="toc"><ol>${toc.ifEmpty { """<li><a href="chapter.xhtml">$t</a></li>""" }}</ol></nav></body></html>""")
            add("OEBPS/chapter.xhtml", """<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>$t</title></head><body>
$body
</body></html>""")
        }
    }
}
