package com.anysave.app

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import kotlin.math.max
import kotlin.math.min
import kotlin.math.roundToInt

/*
 * Captions: the words and lines the speech engine heard, how they look, and the files made from
 * them (SRT, VTT, text, and ASS for burning into the video). This is the same logic as the desktop
 * Captions page (extension/captions.js, host-go/cv_captions.go), so a caption looks the same on both.
 */

data class CapWord(val t: String, val s: Double, val e: Double)

/** One caption line. [words] are the speech engine's word times when known. */
data class CapCue(val s: Double, val e: Double, val text: String, val words: List<CapWord>? = null)

/** A bundled font. ratio = the font's line height per em: FFmpeg's font size is the line height. */
data class CapFont(val id: String, val name: String, val file: String, val ratio: Double, val bold: Int, val label: String, val bn: String, val hidden: Boolean = false)

data class CapStyle(
    val preset: String = "clean",
    val font: String = "noto",
    val size: Double = 5.2,        // % of the video's height
    val upper: Boolean = false,
    val color: String = "#ffffff",
    val outline: Double = 9.0,     // % of the font size
    val outlineColor: String = "#000000",
    val shadow: Boolean = true,
    val box: Boolean = false,
    val boxColor: String = "#000000",
    val boxOpacity: Int = 70,
    val mode: String = "none",     // none, word (word lights up), pop (and grows), reveal (appears as spoken)
    val hl: String = "#ffd400",
    val pos: String = "bottom",    // top, middle, bottom
    val offset: Double = 8.0,      // % of the height from the edge
) {
    fun toJson() = JSONObject().apply {
        put("preset", preset); put("font", font); put("size", size); put("upper", upper); put("color", color)
        put("outline", outline); put("outlineColor", outlineColor); put("shadow", shadow); put("box", box)
        put("boxColor", boxColor); put("boxOpacity", boxOpacity); put("mode", mode); put("hl", hl); put("pos", pos); put("offset", offset)
    }

    companion object {
        fun fromJson(o: JSONObject?): CapStyle {
            if (o == null) return CapStyle()
            val d = CapStyle()
            return CapStyle(
                o.optString("preset", d.preset), o.optString("font", d.font), o.optDouble("size", d.size), o.optBoolean("upper", d.upper),
                o.optString("color", d.color), o.optDouble("outline", d.outline), o.optString("outlineColor", d.outlineColor),
                o.optBoolean("shadow", d.shadow), o.optBoolean("box", d.box), o.optString("boxColor", d.boxColor),
                o.optInt("boxOpacity", d.boxOpacity), o.optString("mode", d.mode), o.optString("hl", d.hl),
                o.optString("pos", d.pos), o.optDouble("offset", d.offset),
            )
        }
    }
}

data class CapPreset(val id: String, val title: String, val desc: String, val style: CapStyle)

object Captions {
    val FONTS = listOf(
        CapFont("noto", "Noto Sans", "NotoSans-Bold.ttf", 1.519, 1, "Noto Sans", "notobn"),
        CapFont("mont", "Montserrat Black", "Montserrat-Black.ttf", 1.562, 0, "Montserrat", "hind"),
        CapFont("anton", "Anton", "Anton-Regular.ttf", 1.7334, 0, "Anton (tall)", "hind"),
        CapFont("hind", "Hind Siliguri", "HindSiliguri-Bold.ttf", 1.617, 1, "Hind Siliguri", "hind"),
        CapFont("notobn", "Noto Sans Bengali", "NotoSansBengali-Bold.ttf", 1.403, 1, "", "notobn", hidden = true),
    )
    fun font(id: String) = FONTS.firstOrNull { it.id == id } ?: FONTS[0]

    val PRESETS = listOf(
        CapPreset("clean", "Clean", "White, thin outline", CapStyle("clean", "noto", 5.2, false, "#ffffff", 9.0, "#000000", true, false, mode = "none", hl = "#ffd400", pos = "bottom", offset = 8.0)),
        CapPreset("pop", "Bold pop", "Big, word lights up", CapStyle("pop", "mont", 6.2, true, "#ffffff", 14.0, "#000000", false, false, mode = "pop", hl = "#ffd400", pos = "middle", offset = 0.0)),
        CapPreset("headline", "Headline", "Tall letters, green word", CapStyle("headline", "anton", 7.5, true, "#ffffff", 10.0, "#000000", true, false, mode = "word", hl = "#3ce37b", pos = "bottom", offset = 16.0)),
        CapPreset("boxed", "Boxed", "Text on a dark box", CapStyle("boxed", "noto", 4.8, false, "#ffffff", 0.0, "#000000", false, true, "#000000", 70, mode = "none", hl = "#ffd400", pos = "bottom", offset = 8.0)),
        CapPreset("reveal", "Word by word", "Words appear as spoken", CapStyle("reveal", "mont", 5.6, false, "#ffffff", 12.0, "#111111", false, false, mode = "reveal", hl = "#ffffff", pos = "bottom", offset = 14.0)),
        CapPreset("bangla", "Bangla", "Hind Siliguri, word lights up", CapStyle("bangla", "hind", 5.6, false, "#ffffff", 11.0, "#000000", true, false, mode = "word", hl = "#ffd400", pos = "bottom", offset = 9.0)),
    )

    /** Words per caption line: "" keeps the lines as the speech engine made them. */
    val CHUNKS = listOf("" to "As spoken", "1" to "1 word", "2" to "2 words", "3" to "3 words", "4" to "4 words", "6" to "6 words", "8" to "8 words")

    val BN_RUN = Regex("([ঀ-৿।॥‌‍]+)")

    // ---------- words ----------

    fun tokens(text: String) = text.split(Regex("\\s+")).filter { it.isNotEmpty() }

    /** Spreads a line's time over its words by length (when the speech engine's word times aren't known). */
    fun spread(c: CapCue): List<CapWord> {
        val t = tokens(c.text)
        val wt = t.map { it.codePointCount(0, it.length) + 2 }
        val sum = wt.sum().coerceAtLeast(1)
        var at = c.s
        return t.mapIndexed { i, x -> val d = (c.e - c.s) * wt[i] / sum; CapWord(x, at, at + d).also { at += d } }
    }

    /** The line's words with times: the engine's when they still fit the (maybe edited) text. */
    fun words(c: CapCue): List<CapWord> {
        val t = tokens(c.text)
        val w = c.words
        if (w != null && w.size == t.size) return if (w.map { it.t } == t) w else w.mapIndexed { i, x -> x.copy(t = t[i]) }
        return spread(c)
    }

    /** Moves a line's words along with its new start and end. */
    fun retime(c: CapCue, s: Double, e: Double): CapCue {
        val od = (c.e - c.s).takeIf { it != 0.0 } ?: 1.0
        return CapCue(s, e, c.text, c.words?.map { CapWord(it.t, s + (it.s - c.s) / od * (e - s), s + (it.e - c.s) / od * (e - s)) })
    }

    /** New lines of [n] words each, also cut at pauses and at the end of sentences. */
    fun chunk(cues: List<CapCue>, n: Int): List<CapCue> {
        val all = cues.flatMap { words(it) }
        val out = mutableListOf<CapCue>()
        var cur = mutableListOf<CapWord>()
        fun flush() { if (cur.isNotEmpty()) out += CapCue(cur.first().s, cur.last().e, cur.joinToString(" ") { it.t }, cur.toList()); cur = mutableListOf() }
        for (w in all) {
            val prev = cur.lastOrNull()
            if (prev != null && (cur.size >= n || w.s - prev.e > 0.6 || Regex("[.?!।]$").containsMatchIn(prev.t) ||
                    cur.joinToString(" ") { it.t }.length + w.t.length > 34)) flush()
            cur += w
        }
        flush()
        for (i in 0 until out.size - 1) if (out[i + 1].s - out[i].e < 0.3) out[i] = out[i].copy(e = out[i + 1].s) // no flicker between lines
        return out
    }

    // ---------- reading the speech engine's result ----------

    /**
     * Lines and word times from whisper's full JSON (-ojf). Read byte by byte: a token can hold half
     * of a letter (Bangla and other non-Latin scripts), so the text is only decoded once the token
     * bytes of a line are joined.
     */
    fun fromWhisperJson(file: File): List<CapCue> {
        // Latin-1 maps every byte to one char, so the JSON parser keeps the raw bytes.
        val doc = JSONObject(String(file.readBytes(), Charsets.ISO_8859_1))
        val segs = doc.optJSONArray("transcription") ?: return emptyList()
        val out = mutableListOf<CapCue>()
        for (i in 0 until segs.length()) {
            val seg = segs.getJSONObject(i)
            val toks = seg.optJSONArray("tokens") ?: JSONArray()
            val buf = java.io.ByteArrayOutputStream()
            data class Span(val a: Int, val b: Int, val from: Double, val to: Double)
            val spans = mutableListOf<Span>()
            for (k in 0 until toks.length()) {
                val t = toks.getJSONObject(k)
                val bytes = rawBytes(t.optString("text"))
                if (bytes.isEmpty() || (bytes.size >= 2 && bytes[0] == '['.code.toByte() && bytes[1] == '_'.code.toByte())) continue
                val off = t.optJSONObject("offsets") ?: continue
                spans += Span(buf.size(), buf.size() + bytes.size, off.optDouble("from") / 1000, off.optDouble("to") / 1000)
                buf.write(bytes)
            }
            val segOff = seg.optJSONObject("offsets")
            val segFrom = (segOff?.optDouble("from") ?: 0.0) / 1000
            val segTo = (segOff?.optDouble("to") ?: 0.0) / 1000
            val b = buf.toByteArray()
            val text = cleanText(decodeLoose(b))
            if (text.isEmpty()) continue
            val words = mutableListOf<CapWord>()
            fun at(p: Int) = spans.firstOrNull { p >= it.a && p < it.b } ?: spans.last()
            var p = 0
            var ok = spans.isNotEmpty()
            while (ok && p < b.size) {
                while (p < b.size && isSpace(b[p])) p++
                var q = p
                while (q < b.size && !isSpace(b[q])) q++
                if (q > p) {
                    val w = decodeStrict(b.copyOfRange(p, q))
                    if (w == null) { ok = false; break }
                    val word = CapWord(w, at(p).from, at(q - 1).to)
                    if (words.isNotEmpty() && !hasLetter(word.t)) { // "," "." "?" go with the word before
                        val last = words.removeAt(words.size - 1)
                        words += last.copy(t = last.t + word.t, e = word.e)
                    } else words += word
                }
                p = q
            }
            val fixed = words.map { if (it.e < it.s + 0.05) it.copy(e = it.s + 0.05) else it }
            val s = fixed.firstOrNull()?.s ?: segFrom
            val e = max(fixed.lastOrNull()?.e ?: segTo, segTo).coerceAtLeast(s + 0.2)
            out += CapCue(s, e, text, fixed.takeIf { ok && it.isNotEmpty() })
        }
        // Lines never overlap.
        for (i in 0 until out.size - 1) if (out[i].e > out[i + 1].s) out[i] = out[i].copy(e = max(out[i].s + 0.1, out[i + 1].s))
        return out
    }

    /** Chars of a Latin-1 string back to bytes; anything above 255 came from a \u escape: UTF-8. */
    private fun rawBytes(s: String): ByteArray {
        val o = java.io.ByteArrayOutputStream()
        for (ch in s) if (ch.code < 256) o.write(ch.code) else o.write(ch.toString().toByteArray(Charsets.UTF_8))
        return o.toByteArray()
    }

    private fun isSpace(c: Byte) = c == ' '.code.toByte() || c == '\n'.code.toByte() || c == '\t'.code.toByte() || c == '\r'.code.toByte()

    private fun decodeStrict(b: ByteArray): String? = runCatching {
        Charsets.UTF_8.newDecoder().decode(java.nio.ByteBuffer.wrap(b)).toString()
    }.getOrNull()

    /** Decodes and drops broken letters (half a character at a line's edge). */
    private fun decodeLoose(b: ByteArray): String = String(b, Charsets.UTF_8).replace("�", "")

    private fun hasLetter(s: String) = s.any { it.isLetterOrDigit() || Character.getType(it) == Character.NON_SPACING_MARK.toInt() ||
        Character.getType(it) == Character.COMBINING_SPACING_MARK.toInt() }

    private fun cleanText(s: String) = collapseRepeats(s).replace("঵", "").trim()

    /** "and then and then and then and then" → "and then": the speech engine sometimes gets stuck. */
    fun collapseRepeats(s: String): String {
        val w = s.split(Regex("\\s+")).filter { it.isNotEmpty() }
        if (w.size < 3) return s
        fun key(x: String) = x.trim('.', ',', ';', ':', '!', '?', '।', '\'', '"')
        val out = mutableListOf<String>()
        var i = 0
        while (i < w.size) {
            var done = false
            var n = 8
            while (n >= 1 && !done) {
                if (i + 3 * n <= w.size) {
                    var reps = 1
                    while (i + (reps + 1) * n <= w.size && (0 until n).all { key(w[i + it]) == key(w[i + reps * n + it]) }) reps++
                    if (reps >= 3) { out += w.subList(i, i + n); i += reps * n; done = true }
                }
                n--
            }
            if (!done) { out += w[i]; i++ }
        }
        return out.joinToString(" ")
    }

    // ---------- files ----------

    fun srtTime(t: Double, sep: Char = ','): String {
        val ms = (max(0.0, t) * 1000).roundToInt().toLong()
        return "%02d:%02d:%02d%c%03d".format(ms / 3600000, ms / 60000 % 60, ms / 1000 % 60, sep, ms % 1000)
    }
    fun toSrt(cues: List<CapCue>) = cues.mapIndexed { i, c -> "${i + 1}\n${srtTime(c.s)} --> ${srtTime(c.e)}\n${c.text}\n" }.joinToString("\n")
    fun toVtt(cues: List<CapCue>) = "WEBVTT\n\n" + cues.joinToString("\n") { "${srtTime(it.s, '.')} --> ${srtTime(it.e, '.')}\n${it.text}\n" }
    fun toTxt(cues: List<CapCue>) = cues.joinToString(" ") { it.text }.replace(Regex("\\s+([.,!?।])"), "$1") + "\n"

    /** Reads SRT or VTT (to bring your own subtitles in). */
    fun parseSubs(text: String): List<CapCue> {
        val re = Regex("""(\d+):(\d\d):(\d\d)[,.](\d{1,3})\s*-->\s*(\d+):(\d\d):(\d\d)[,.](\d{1,3})""")
        val short = Regex("""(?:^|\s)(\d\d):(\d\d)[.](\d{3})\s*-->\s*(\d\d):(\d\d)[.](\d{3})""")
        val out = mutableListOf<CapCue>()
        for (block in text.removePrefix("﻿").replace("\r\n", "\n").split(Regex("\n\\s*\n"))) {
            val lines = block.trim().lines()
            for ((i, l) in lines.withIndex()) {
                val m = re.find(l)
                val (s, e) = when {
                    m != null -> {
                        val g = m.groupValues.drop(1).map { it.toDouble() }
                        (g[0] * 3600 + g[1] * 60 + g[2] + g[3] / 1000) to (g[4] * 3600 + g[5] * 60 + g[6] + g[7] / 1000)
                    }
                    else -> short.find(l)?.let { s2 ->
                        val g = s2.groupValues.drop(1).map { it.toDouble() }
                        (g[0] * 60 + g[1] + g[2] / 1000) to (g[3] * 60 + g[4] + g[5] / 1000)
                    } ?: continue
                }
                val t = lines.drop(i + 1).joinToString(" ").replace(Regex("<[^>]+>"), "").trim()
                if (t.isNotEmpty()) out += CapCue(s, e, t)
                break
            }
        }
        return out
    }

    fun upper(t: String, style: CapStyle, lang: String): String =
        if (style.upper) t.uppercase(if (lang.isNotEmpty() && lang != "auto") java.util.Locale(lang) else java.util.Locale.ROOT) else t

    fun assColor(hex: String, alpha: Int = 0): String {
        val n = hex.removePrefix("#").padEnd(6, 'f')
        return "&H%02X%s%s%s".format(alpha, n.substring(4, 6), n.substring(2, 4), n.substring(0, 2)).uppercase()
    }
    fun assTime(t: Double): String {
        val cs = (max(0.0, t) * 100).roundToInt()
        return "%d:%02d:%02d.%02d".format(cs / 360000, cs / 6000 % 60, cs / 100 % 60, cs % 100)
    }
    private fun assText(t: String) = t.replace("\\", "∖").replace("{", "(").replace("}", ")")
    private fun r1(x: Double) = (x * 10).roundToInt() / 10.0
    private fun num(x: Double) = if (x == x.toLong().toDouble()) x.toLong().toString() else x.toString()

    /** What the preview shows, as ASS for FFmpeg (libass) to draw onto the video. */
    fun toAss(cues: List<CapCue>, s: CapStyle, w: Int, h: Int, lang: String): String {
        val f = font(s.font)
        val bn = font(f.bn)
        val px = s.size / 100 * h
        fun fs(x: CapFont) = r1(px * x.ratio)
        val bord = if (s.box) r1(px * 0.22) else r1(s.outline / 100 * px)
        val shad = if (!s.box && s.shadow) r1(bord * 0.6 + 2) else 0.0
        val an = when (s.pos) { "top" -> 8; "middle" -> 5; else -> 2 }
        val mv = if (s.pos == "middle") 0 else (s.offset / 100 * h).roundToInt()
        val ml = (w * 0.07).roundToInt()
        // Box: libass's BorderStyle 4 draws one box around the whole caption in BackColour, padded by Outline.
        val outlineC = if (s.box) "&HFF000000" else assColor(s.outlineColor)
        val backC = if (s.box) assColor(s.boxColor, ((1 - s.boxOpacity / 100.0) * 255).roundToInt()) else "&H73000000"
        val head = """[Script Info]
; Made by AnySave
ScriptType: v4.00+
PlayResX: $w
PlayResY: $h
WrapStyle: 0
ScaledBorderAndShadow: yes
YCbCr Matrix: None

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,${f.name},${num(fs(f))},${assColor(s.color)},${assColor(s.color)},$outlineC,$backC,${if (f.bold == 1) -1 else 0},0,0,0,100,100,0,0,${if (s.box) 4 else 1},${num(bord)},${num(shad)},$an,$ml,$ml,$mv,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
"""
        val color = assColor(s.color)
        val hl = assColor(s.hl)
        fun word(wd: CapWord, on: Boolean, hidden: Boolean): String {
            val parts = splitBn(upper(wd.t, s, lang))
            return parts.joinToString("") { p ->
                val pf = if (BN_RUN.matches(p)) bn else f
                // Only the letters' and outline's see-through (\1a, \3a): \alpha would also change the box.
                val a = if (hidden) "FF" else "00"
                val size = fs(pf) * if (on && s.mode == "pop") 1.15 else 1.0
                val tag = "\\fn${pf.name}\\fs${num(r1(size))}\\b${pf.bold}\\c${if (on) hl else color}\\1a&H$a&\\3a&H$a&" +
                    if (s.box) "" else "\\4a&H${if (hidden) "FF" else "73"}&"
                "{$tag}${assText(p)}"
            }
        }
        val lines = mutableListOf<String>()
        fun add(a: Double, b: Double, text: String) { if (b - a >= 0.01) lines += "Dialogue: 0,${assTime(a)},${assTime(b)},Default,,0,0,0,,$text" }
        for (c in cues) {
            val ws = words(c)
            if (ws.isEmpty()) continue
            if (s.mode == "none") { add(c.s, c.e, ws.joinToString(" ") { word(it, false, false) }); continue }
            if (s.mode == "reveal" && ws[0].s > c.s) add(c.s, ws[0].s, ws.joinToString(" ") { word(it, false, true) })
            for (k in ws.indices) {
                val a = if (k == 0 && s.mode != "reveal") c.s else max(c.s, ws[k].s)
                val b = if (k + 1 < ws.size) max(c.s, ws[k + 1].s) else c.e
                add(a, min(b, c.e), ws.withIndex().joinToString(" ") { (x, wd) -> word(wd, s.mode != "reveal" && x == k, s.mode == "reveal" && x > k) })
            }
        }
        return head + lines.joinToString("\n") + "\n"
    }

    /** Splits text into Bangla and non-Bangla runs (each gets its own font). */
    fun splitBn(t: String): List<String> {
        val out = mutableListOf<String>()
        var last = 0
        for (m in BN_RUN.findAll(t)) {
            if (m.range.first > last) out += t.substring(last, m.range.first)
            out += m.value
            last = m.range.last + 1
        }
        if (last < t.length) out += t.substring(last)
        return out.filter { it.isNotEmpty() }
    }

    // ---------- fonts ----------

    /** The bundled fonts as files (FFmpeg reads fonts from a folder). */
    fun fontsDir(ctx: Context): File {
        val dir = File(ctx.noBackupFilesDir, "fonts")
        dir.mkdirs()
        for (f in FONTS) {
            val out = File(dir, f.file)
            if (out.length() > 0) continue
            val tmp = File(dir, f.file + ".part")
            ctx.assets.open("fonts/${f.file}").use { i -> tmp.outputStream().use { i.copyTo(it) } }
            tmp.renameTo(out)
        }
        return dir
    }
}

/**
 * One video (or audio) being captioned: kept as JSON in the app's own storage so the work is there
 * after the app closes. [jobId] is the speech job while it runs.
 */
data class CapProject(
    val id: String,
    val source: String,          // content:// Uri of the user's file
    val name: String,
    val lang: String = "auto",
    val hasVideo: Boolean = true,
    val w: Int = 1920,
    val h: Int = 1080,
    val dur: Double = 0.0,
    val cues: List<CapCue> = emptyList(),
    /** The lines as the speech engine made them (re-splitting by words starts from these). */
    val orig: List<CapCue>? = null,
    val chunk: String = "",
    val style: CapStyle = CapStyle(),
    val jobId: String? = null,
    val created: Long = System.currentTimeMillis(),
    val edited: Long = System.currentTimeMillis(),
) {
    fun toJson(): String = JSONObject().apply {
        put("id", id); put("source", source); put("name", name); put("lang", lang); put("hasVideo", hasVideo)
        put("w", w); put("h", h); put("dur", dur); put("cues", cuesJson(cues)); orig?.let { put("orig", cuesJson(it)) }
        put("chunk", chunk); put("style", style.toJson()); put("jobId", jobId); put("created", created); put("edited", edited)
    }.toString()

    companion object {
        fun cuesJson(cs: List<CapCue>) = JSONArray().apply {
            cs.forEach { c ->
                put(JSONObject().apply {
                    put("s", c.s); put("e", c.e); put("text", c.text)
                    c.words?.let { ws -> put("words", JSONArray().apply { ws.forEach { put(JSONArray().put(it.t).put(it.s).put(it.e)) } }) }
                })
            }
        }
        fun cuesFrom(a: JSONArray?): List<CapCue> = if (a == null) emptyList() else (0 until a.length()).map { i ->
            val o = a.getJSONObject(i)
            val ws = o.optJSONArray("words")?.let { w -> (0 until w.length()).map { k -> w.getJSONArray(k).let { CapWord(it.getString(0), it.getDouble(1), it.getDouble(2)) } } }
            CapCue(o.getDouble("s"), o.getDouble("e"), o.getString("text"), ws)
        }
        fun fromJson(s: String): CapProject? = runCatching {
            val o = JSONObject(s)
            CapProject(o.getString("id"), o.getString("source"), o.getString("name"), o.optString("lang", "auto"), o.optBoolean("hasVideo", true),
                o.optInt("w", 1920), o.optInt("h", 1080), o.optDouble("dur", 0.0), cuesFrom(o.optJSONArray("cues")),
                o.optJSONArray("orig")?.let { cuesFrom(it) }, o.optString("chunk", ""), CapStyle.fromJson(o.optJSONObject("style")),
                if (o.isNull("jobId")) null else o.optString("jobId").ifBlank { null },
                o.optLong("created", System.currentTimeMillis()), o.optLong("edited", System.currentTimeMillis()))
        }.getOrNull()
    }
}

object CapProjects {
    private fun dir(ctx: Context) = File(ctx.filesDir, "captions").apply { mkdirs() }
    fun file(ctx: Context, id: String) = File(dir(ctx), "$id.json")

    fun all(ctx: Context): List<CapProject> =
        dir(ctx).listFiles { f -> f.name.endsWith(".json") }.orEmpty().mapNotNull { runCatching { CapProject.fromJson(it.readText()) }.getOrNull() }
            .sortedByDescending { it.edited }

    fun get(ctx: Context, id: String): CapProject? = file(ctx, id).takeIf { it.exists() }?.let { CapProject.fromJson(it.readText()) }

    @Synchronized
    fun save(ctx: Context, p: CapProject) {
        val f = file(ctx, p.id)
        val tmp = File(f.path + ".tmp")
        tmp.writeText(p.toJson())
        tmp.renameTo(f)
    }

    fun delete(ctx: Context, id: String) { file(ctx, id).delete() }
}
