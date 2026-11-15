package com.anysave.app

import android.util.Xml
import org.json.JSONArray
import org.json.JSONObject
import org.json.JSONTokener
import org.xmlpull.v1.XmlPullParser
import java.io.File
import java.util.zip.ZipEntry
import java.util.zip.ZipFile
import java.util.zip.ZipOutputStream

/** Spreadsheet data: CSV, TSV, JSON and Excel .xlsx (first sheet), each to any of the others. */
object DataTools {
    data class Table(val header: List<String>, val rows: List<List<String>>)

    fun convert(input: File, out: File) {
        val inExt = input.extension.lowercase()
        if (inExt == out.extension.lowercase()) throw ConvertError("The file is already ${inExt.uppercase()}. Pick another format.")
        val table = when (inExt) {
            "xlsx" -> readXlsx(input)
            "json" -> readJson(input.readText())
            "tsv" -> readCsv(input.readText(), '\t')
            else -> readCsv(input.readText(), null)
        }
        if (table.header.isEmpty() && table.rows.isEmpty()) throw ConvertError("No data found in this file.")
        when (out.extension.lowercase()) {
            "csv" -> out.writeText("﻿" + writeDelimited(table, ',')) // the mark makes Excel read non-English letters right
            "tsv" -> out.writeText(writeDelimited(table, '\t'))
            "json" -> out.writeText(writeJson(table))
            "xlsx" -> writeXlsx(table, out)
        }
    }

    // ---------- CSV ----------

    fun readCsv(textIn: String, delim: Char?): Table {
        val text = textIn.removePrefix("﻿")
        val firstLine = text.lineSequence().firstOrNull().orEmpty()
        val d = delim ?: listOf(',', ';', '\t', '|').maxByOrNull { c -> firstLine.count { it == c } } ?: ','
        val rows = mutableListOf<List<String>>()
        var row = mutableListOf<String>()
        val cell = StringBuilder()
        var quoted = false
        var i = 0
        while (i < text.length) {
            val c = text[i]
            if (quoted) {
                if (c == '"') { if (i + 1 < text.length && text[i + 1] == '"') { cell.append('"'); i++ } else quoted = false }
                else cell.append(c)
            } else when (c) {
                '"' -> quoted = true
                d -> { row += cell.toString(); cell.clear() }
                '\r' -> {}
                '\n' -> { row += cell.toString(); cell.clear(); rows += row; row = mutableListOf() }
                else -> cell.append(c)
            }
            i++
        }
        if (cell.isNotEmpty() || row.isNotEmpty()) { row += cell.toString(); rows += row }
        val clean = rows.filter { r -> r.any { it.isNotBlank() } }
        return Table(clean.firstOrNull().orEmpty(), clean.drop(1))
    }

    private fun writeDelimited(t: Table, d: Char): String {
        fun q(s: String) = if (s.any { it == d || it == '"' || it == '\n' || it == '\r' }) "\"" + s.replace("\"", "\"\"") + "\"" else s
        return (listOf(t.header) + t.rows).joinToString("\r\n") { r -> r.joinToString(d.toString()) { q(it) } } + "\r\n"
    }

    // ---------- JSON ----------

    fun readJson(text: String): Table {
        var v = JSONTokener(text.removePrefix("﻿")).nextValue()
        if (v is JSONObject) { // {"items": [...]} -> the first list inside
            v = v.keys().asSequence().map { (v as JSONObject).get(it) }.firstOrNull { it is JSONArray } ?: JSONArray().put(v)
        }
        val arr = v as? JSONArray ?: throw ConvertError("This JSON isn't a list of rows.")
        val header = LinkedHashSet<String>()
        val objs = (0 until arr.length()).map { arr.get(it) }
        if (objs.all { it is JSONArray }) {
            val rows = objs.map { a -> (a as JSONArray).let { j -> List(j.length()) { str(j.opt(it)) } } }
            return Table(rows.firstOrNull().orEmpty(), rows.drop(1))
        }
        objs.forEach { o -> if (o is JSONObject) o.keys().forEach { header += it } }
        if (header.isEmpty()) return Table(listOf("value"), objs.map { listOf(str(it)) })
        val h = header.toList()
        return Table(h, objs.map { o -> h.map { k -> if (o is JSONObject) str(o.opt(k)) else "" } })
    }

    private fun str(v: Any?): String = when (v) { null, JSONObject.NULL -> ""; else -> v.toString() }

    private fun writeJson(t: Table): String {
        val arr = JSONArray()
        t.rows.forEach { r ->
            val o = JSONObject()
            t.header.forEachIndexed { i, k ->
                val s = r.getOrElse(i) { "" }
                val n = s.takeIf { Regex("^-?(0|[1-9]\\d{0,14})(\\.\\d+)?$").matches(it) }
                o.put(k.ifBlank { "column ${i + 1}" }, n?.let { if (it.contains('.')) it.toDouble() else it.toLong() } ?: s)
            }
            arr.put(o)
        }
        return arr.toString(2)
    }

    // ---------- Excel ----------

    private fun colIndex(ref: String): Int {
        var n = 0
        for (c in ref) { if (!c.isLetter()) break; n = n * 26 + (c.uppercaseChar() - 'A' + 1) }
        return n - 1
    }

    fun readXlsx(file: File): Table = ZipFile(file).use { zip ->
        val shared = mutableListOf<String>()
        zip.getEntry("xl/sharedStrings.xml")?.let { e ->
            zip.getInputStream(e).use { ins ->
                val p = Xml.newPullParser().apply { setInput(ins, null) }
                var cur: StringBuilder? = null; var inT = false
                while (p.next() != XmlPullParser.END_DOCUMENT) when (p.eventType) {
                    XmlPullParser.START_TAG -> when (p.name) { "si" -> cur = StringBuilder(); "t" -> inT = true }
                    XmlPullParser.TEXT -> if (inT) cur?.append(p.text)
                    XmlPullParser.END_TAG -> when (p.name) { "t" -> inT = false; "si" -> { shared += cur.toString(); cur = null } }
                }
            }
        }
        val sheet = zip.getEntry("xl/worksheets/sheet1.xml")
            ?: zip.entries().asSequence().filter { it.name.startsWith("xl/worksheets/sheet") && it.name.endsWith(".xml") }.minByOrNull { it.name }
            ?: throw ConvertError("This doesn't look like an Excel file.")
        val rows = mutableListOf<List<String>>()
        zip.getInputStream(sheet).use { ins ->
            val p = Xml.newPullParser().apply { setInput(ins, null) }
            var row: MutableMap<Int, String>? = null
            var col = 0; var type: String? = null; var value: StringBuilder? = null; var inV = false
            var nextCol = 0
            while (p.next() != XmlPullParser.END_DOCUMENT) when (p.eventType) {
                XmlPullParser.START_TAG -> when (p.name) {
                    "row" -> { row = sortedMapOf(); nextCol = 0 }
                    "c" -> { col = p.getAttributeValue(null, "r")?.let { colIndex(it) } ?: nextCol; type = p.getAttributeValue(null, "t"); value = StringBuilder() }
                    "v", "t" -> inV = true
                }
                XmlPullParser.TEXT -> if (inV) value?.append(p.text)
                XmlPullParser.END_TAG -> when (p.name) {
                    "v", "t" -> inV = false
                    "c" -> {
                        val raw = value?.toString().orEmpty()
                        row?.put(col, when (type) {
                            "s" -> shared.getOrElse(raw.toIntOrNull() ?: -1) { "" }
                            "b" -> if (raw == "1") "TRUE" else "FALSE"
                            else -> raw.toDoubleOrNull()?.let { d -> if (d == Math.floor(d) && kotlin.math.abs(d) < 1e15) d.toLong().toString() else raw } ?: raw
                        })
                        nextCol = col + 1
                    }
                    "row" -> row?.let { r -> if (r.isNotEmpty()) { val w = r.keys.max() + 1; rows += List(w) { r[it] ?: "" } } }
                }
            }
        }
        val width = rows.maxOfOrNull { it.size } ?: 0
        val padded = rows.map { it + List(width - it.size) { "" } }
        Table(padded.firstOrNull().orEmpty(), padded.drop(1))
    }

    private fun colName(i: Int): String { var n = i + 1; val sb = StringBuilder(); while (n > 0) { sb.insert(0, 'A' + (n - 1) % 26); n = (n - 1) / 26 }; return sb.toString() }

    private fun x(s: String) = s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;").replace("\"", "&quot;")
        .filter { it == '\t' || it == '\n' || it == '\r' || it >= ' ' }

    private fun writeXlsx(t: Table, out: File) {
        val sheet = StringBuilder("""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>""")
        (listOf(t.header) + t.rows).forEachIndexed { ri, r ->
            sheet.append("<row r=\"${ri + 1}\">")
            r.forEachIndexed { ci, v ->
                val ref = "${colName(ci)}${ri + 1}"
                if (ri > 0 && Regex("^-?(0|[1-9]\\d{0,14})(\\.\\d+)?$").matches(v)) sheet.append("<c r=\"$ref\"><v>$v</v></c>")
                else if (v.isNotEmpty()) sheet.append("<c r=\"$ref\" t=\"inlineStr\"${if (ri == 0) " s=\"1\"" else ""}><is><t xml:space=\"preserve\">${x(v)}</t></is></c>")
            }
            sheet.append("</row>")
        }
        sheet.append("</sheetData></worksheet>")
        val files = linkedMapOf(
            "[Content_Types].xml" to """<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>
</Types>""",
            "_rels/.rels" to """<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>""",
            "xl/workbook.xml" to """<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>""",
            "xl/_rels/workbook.xml.rels" to """<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>""",
            "xl/styles.xml" to """<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>
<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>
<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>
<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>
<cellXfs count="2"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/></cellXfs>
</styleSheet>""",
            "xl/worksheets/sheet1.xml" to sheet.toString(),
        )
        ZipOutputStream(out.outputStream()).use { z ->
            files.forEach { (name, text) -> z.putNextEntry(ZipEntry(name)); z.write(text.toByteArray()); z.closeEntry() }
        }
    }
}
