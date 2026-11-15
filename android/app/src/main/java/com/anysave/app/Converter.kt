package com.anysave.app

import android.content.Context
import android.media.MediaMetadataRetriever
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.util.concurrent.ConcurrentHashMap

/**
 * What the user picked on the Convert screen. [tool] says which converter runs (see [ConvertTools]);
 * the older fields are the video/audio settings, [opts] holds every other choice as text.
 */
data class ConvertSpec(
    val format: String = "mp4",      // output format (extension) for most tools
    val height: Int? = null,         // null = keep
    val targetMb: Int? = null,       // null = normal quality
    val startSec: Double? = null,
    val endSec: Double? = null,
    val removeAudio: Boolean = false,
    val tool: String = ConvertTools.MEDIA,
    /** Every input file (content:// Uris) in order. Empty for older jobs: then the job's url is the one input. */
    val inputs: List<String> = emptyList(),
    val opts: Map<String, String> = emptyMap(),
) {
    val isAudio get() = tool == ConvertTools.MEDIA && format in AUDIO_FORMATS
    fun opt(k: String): String? = opts[k]?.takeIf { it.isNotBlank() }

    fun toJson() = JSONObject().apply {
        put("format", format); put("height", height); put("targetMb", targetMb)
        put("startSec", startSec); put("endSec", endSec); put("removeAudio", removeAudio)
        put("tool", tool); put("inputs", JSONArray(inputs)); put("opts", JSONObject(opts))
    }.toString()

    /** Short description, e.g. "MP4 · H.265 · 720p · under 25 MB · 0:05–0:30". */
    fun label(): String {
        if (tool != ConvertTools.MEDIA) {
            val t = ConvertTools.byId(tool)
            val count = if (inputs.size > 1) "${inputs.size} files" else null
            return listOfNotNull(t?.short ?: tool, format.takeIf { t?.formats?.isNotEmpty() == true }?.uppercase(), count)
                .joinToString(" · ")
        }
        return listOfNotNull(
            format.uppercase(),
            opt("codec")?.takeIf { format in listOf("mp4", "mkv", "mov") }?.let { CODEC_LABELS[it] },
            height?.let { "${it}p" },
            opt("aspect")?.let { ASPECT_LABELS[it] },
            targetMb?.let { if (it == 0) "high quality" else "under $it MB" },
            opt("speed")?.takeIf { it != "1" }?.let { "${it}×" },
            if (startSec != null || endSec != null) "${fmt(startSec ?: 0.0)}–${endSec?.let(::fmt) ?: "end"}" else null,
            if (removeAudio && !isAudio) "no sound" else null,
        ).joinToString(" · ")
    }

    companion object {
        val VIDEO_FORMATS = listOf("mp4", "mkv", "mov", "webm", "gif")
        val AUDIO_FORMATS = listOf("mp3", "m4a", "wav", "flac", "opus", "ogg")
        val CODEC_LABELS = mapOf("h264" to "H.264", "h265" to "H.265", "av1" to "AV1")
        val ASPECT_LABELS = mapOf("9:16" to "vertical", "1:1" to "square", "4:5" to "4:5", "16:9" to "wide")
        private fun fmt(s: Double) = "%d:%02d".format(s.toInt() / 60, s.toInt() % 60)
        fun fromJson(s: String?): ConvertSpec = runCatching {
            val o = JSONObject(s!!)
            fun d(k: String) = if (o.isNull(k)) null else o.getDouble(k)
            fun i(k: String) = if (o.isNull(k)) null else o.getInt(k)
            val ins = o.optJSONArray("inputs")?.let { a -> List(a.length()) { a.getString(it) } }.orEmpty()
            val op = o.optJSONObject("opts")?.let { j -> j.keys().asSequence().associateWith { j.getString(it) } }.orEmpty()
            ConvertSpec(o.optString("format", "mp4"), i("height"), i("targetMb"), d("startSec"), d("endSec"),
                o.optBoolean("removeAudio"), o.optString("tool", ConvertTools.MEDIA), ins, op)
        }.getOrDefault(ConvertSpec())
    }
}

/** Thrown with a message that is already plain enough to show as it is. */
class ConvertError(msg: String) : Exception(msg)

/** Runs the FFmpeg binary that ships with youtubedl-android (libffmpeg.so in the native lib folder). */
object Converter {
    private val processes = ConcurrentHashMap<String, Process>()
    @Volatile private var encoders: Set<String>? = null

    private fun ffmpegPath(ctx: Context) = File(ctx.applicationInfo.nativeLibraryDir, "libffmpeg.so")

    private fun env(ctx: Context): Map<String, String> {
        val pkgs = File(ctx.noBackupFilesDir, "youtubedl-android/packages")
        return mapOf(
            "LD_LIBRARY_PATH" to listOf("ffmpeg", "python", "aria2c").joinToString(":") { File(pkgs, "$it/usr/lib").absolutePath },
            "TMPDIR" to ctx.cacheDir.absolutePath,
        )
    }

    private fun start(ctx: Context, args: List<String>): Process =
        ProcessBuilder(listOf(ffmpegPath(ctx).absolutePath) + args).apply {
            environment().putAll(env(ctx))
            redirectErrorStream(false)
        }.start()

    /** Which encoders this FFmpeg build has (x264, x265, SVT-AV1, WebP... vary between builds). */
    fun encoders(ctx: Context): Set<String> = encoders ?: start(ctx, listOf("-hide_banner", "-encoders")).let { p ->
        val names = p.inputStream.bufferedReader().readLines()
            .mapNotNull { Regex("""^\s*[VAS][.A-Z]{5}\s+(\S+)""").find(it)?.groupValues?.get(1) }.toSet()
        p.waitFor()
        names.also { encoders = it }
    }

    fun durationSec(file: File): Double? = meta(file) { it.extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)?.toLong()?.div(1000.0) }
    fun hasAudio(file: File): Boolean = meta(file) { it.extractMetadata(MediaMetadataRetriever.METADATA_KEY_HAS_AUDIO) == "yes" } ?: true
    fun hasVideo(file: File): Boolean = meta(file) { it.extractMetadata(MediaMetadataRetriever.METADATA_KEY_HAS_VIDEO) == "yes" } ?: false

    private fun <T> meta(file: File, read: (MediaMetadataRetriever) -> T?): T? = runCatching {
        val r = MediaMetadataRetriever()
        try { r.setDataSource(file.absolutePath); read(r) } finally { r.release() }
    }.getOrNull()

    // ---------- video / audio ----------

    /**
     * Converts [input] to [output] with [spec]. Calls [onProgress] with 0–100.
     * Throws with FFmpeg's last error line on failure.
     */
    fun convert(ctx: Context, id: String, input: File, output: File, spec: ConvertSpec, onProgress: (Float) -> Unit) {
        val enc = encoders(ctx)
        val total = durationSec(input)
        val start = spec.startSec?.takeIf { it > 0 }
        val length = spec.endSec?.let { it - (start ?: 0.0) }?.takeIf { it > 0 }
        val speed = spec.opt("speed")?.toDoubleOrNull()?.takeIf { it in 0.25..4.0 && it != 1.0 }
        val inSeconds = length ?: total?.minus(start ?: 0.0)
        val outSeconds = inSeconds?.div(speed ?: 1.0)

        val a = mutableListOf("-hide_banner", "-y", "-nostats", "-progress", "pipe:1")
        start?.let { a += listOf("-ss", it.toString()) }
        a += listOf("-i", input.absolutePath)
        length?.let { a += listOf("-t", it.toString()) }

        val audioOut = spec.format in ConvertSpec.AUDIO_FORMATS
        val vf = videoFilters(spec, speed)
        val af = audioFilters(spec, speed)
        val removeAudio = spec.removeAudio && !audioOut

        // Target size: share the budget between video and 128 kb/s audio. targetMb 0 = "high quality".
        val videoBitrate = spec.targetMb?.takeIf { it > 0 }?.let { mb ->
            val secs = outSeconds ?: return@let null
            val audioBits = if (removeAudio) 0 else 128_000
            ((mb * 0.96 * 8.0 * 1024 * 1024 / secs) - audioBits).toLong().coerceAtLeast(150_000) // 4% margin for container overhead
        }
        val hq = spec.targetMb == 0

        when (spec.format) {
            "mp4", "mov", "mkv" -> {
                if (vf.isNotEmpty()) a += listOf("-vf", vf.joinToString(","))
                a += videoCodec(enc, spec.opt("codec") ?: "h264", videoBitrate, hq, spec.format)
                spec.opt("fps")?.let { a += listOf("-r", it) }
                a += if (removeAudio) listOf("-an") else (af.toArgs() + listOf("-c:a", "aac", "-b:a", "128k", "-ac", "2"))
                if (spec.format != "mkv") a += listOf("-movflags", "+faststart")
            }
            "webm" -> {
                if (vf.isNotEmpty()) a += listOf("-vf", vf.joinToString(","))
                a += if ("libvpx-vp9" in enc) listOf("-c:v", "libvpx-vp9", "-row-mt", "1", "-deadline", "realtime", "-cpu-used", "8")
                else if ("libsvtav1" in enc) listOf("-c:v", "libsvtav1", "-preset", "10")
                else listOf("-c:v", "libvpx")
                a += videoBitrate?.let { listOf("-b:v", "$it") } ?: listOf("-crf", if (hq) "28" else "33", "-b:v", "0")
                spec.opt("fps")?.let { a += listOf("-r", it) }
                a += if (removeAudio) listOf("-an")
                else af.toArgs() + (if ("libopus" in enc) listOf("-c:a", "libopus", "-b:a", "96k") else listOf("-c:a", "libvorbis"))
            }
            "gif" -> {
                val h = spec.height ?: 480
                val fps = spec.opt("fps")?.toIntOrNull()?.coerceAtMost(25) ?: 12
                val pre = (videoFilters(spec.copy(height = null), speed) + "fps=$fps" + "scale=-2:'min($h,ih)':flags=lanczos").joinToString(",")
                a += listOf("-vf", "$pre,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse=dither=sierra2_4a", "-loop", "0", "-an")
            }
            else -> { // audio
                a += listOf("-vn") + af.toArgs()
                val kbps = spec.opt("abitrate")
                a += when (spec.format) {
                    "mp3" -> if ("libmp3lame" in enc) listOf("-c:a", "libmp3lame") + (kbps?.let { listOf("-b:a", "${it}k") } ?: listOf("-q:a", "2"))
                        else listOf("-c:a", "mp3")
                    "m4a" -> listOf("-c:a", "aac", "-b:a", "${kbps ?: "192"}k", "-ac", "2")
                    "wav" -> listOf("-c:a", "pcm_s16le")
                    // 16-bit unless the source was already lossless (it may be 24-bit studio audio)
                    "flac" -> listOf("-c:a", "flac") + if (input.extension.lowercase() in setOf("flac", "wav", "aiff", "aif", "alac")) emptyList() else listOf("-sample_fmt", "s16")
                    "opus" -> (if ("libopus" in enc) listOf("-c:a", "libopus") else listOf("-c:a", "opus", "-strict", "-2")) +
                        listOf("-b:a", "${kbps ?: "128"}k")
                    "ogg" -> listOf("-c:a", "libvorbis", "-b:a", "${kbps ?: "160"}k")
                    else -> emptyList()
                }
            }
        }
        a += output.absolutePath
        runFfmpeg(ctx, id, a, outSeconds, onProgress)
    }

    private fun List<String>.toArgs() = if (isEmpty()) emptyList() else listOf("-af", joinToString(","))

    private fun videoFilters(spec: ConvertSpec, speed: Double?): List<String> {
        val f = mutableListOf<String>()
        when (spec.opt("aspect")) { // crop to a shape, keeping the middle
            "9:16" -> f += "crop='min(iw,ih*9/16)':'min(ih,iw*16/9)'"
            "1:1" -> f += "crop='min(iw,ih)':'min(iw,ih)'"
            "4:5" -> f += "crop='min(iw,ih*4/5)':'min(ih,iw*5/4)'"
            "16:9" -> f += "crop='min(iw,ih*16/9)':'min(ih,iw*9/16)'"
        }
        when (spec.opt("rotate")) {
            "90" -> f += "transpose=1"
            "180" -> f += "transpose=1,transpose=1"
            "270" -> f += "transpose=2"
        }
        when (spec.opt("flip")) { "h" -> f += "hflip"; "v" -> f += "vflip" }
        spec.height?.let { f += "scale=-2:'min($it,ih)'" }
        if (spec.opt("stabilize") == "1") f += "deshake"
        speed?.let { f += "setpts=PTS/$it" }
        return f
    }

    private fun audioFilters(spec: ConvertSpec, speed: Double?): List<String> {
        val f = mutableListOf<String>()
        if (spec.opt("denoise") == "1") f += listOf("highpass=f=80", "afftdn=nr=12:nf=-30")
        speed?.let { s -> // atempo takes 0.5–2 per step
            var left = s
            while (left > 2.0) { f += "atempo=2.0"; left /= 2.0 }
            while (left < 0.5) { f += "atempo=0.5"; left /= 0.5 }
            f += "atempo=$left"
        }
        // loudnorm works at 192 kHz inside; bring it back to a normal rate (opus needs 48 kHz anyway).
        if (spec.opt("loudnorm") == "1") { f += "loudnorm=I=-16:TP=-1.5:LRA=11"; f += "aresample=48000" }
        return f
    }

    private fun videoCodec(enc: Set<String>, codec: String, bitrate: Long?, hq: Boolean, container: String): List<String> {
        fun rate(crf: String) = bitrate?.let { listOf("-b:v", "$it", "-maxrate", "$it", "-bufsize", "${it * 2}") } ?: listOf("-crf", crf)
        return when {
            codec == "h265" && "libx265" in enc ->
                listOf("-c:v", "libx265", "-preset", "veryfast", "-pix_fmt", "yuv420p", "-x265-params", "log-level=error") +
                    rate(if (hq) "22" else "26") + (if (container != "mkv") listOf("-tag:v", "hvc1") else emptyList())
            codec == "av1" && "libsvtav1" in enc ->
                listOf("-c:v", "libsvtav1", "-preset", "10", "-pix_fmt", "yuv420p") +
                    (bitrate?.let { listOf("-b:v", "$it") } ?: listOf("-crf", if (hq) "28" else "34"))
            "libx264" in enc -> listOf("-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p") + rate(if (hq) "19" else "23")
            else -> listOf("-c:v", "mpeg4", "-q:v", "4")
        }
    }

    /** Joins several videos (or audio files) one after another into one file. */
    fun join(ctx: Context, id: String, inputs: List<File>, output: File, onProgress: (Float) -> Unit) {
        val audioOnly = inputs.none { hasVideo(it) }
        val durations = inputs.map { durationSec(it) ?: 0.0 }
        val a = mutableListOf("-hide_banner", "-y", "-nostats", "-progress", "pipe:1")
        inputs.forEach { a += listOf("-i", it.absolutePath) }
        val parts = StringBuilder()
        val labels = StringBuilder()
        var silentInputs = 0
        if (audioOnly) {
            inputs.indices.forEach { i -> parts.append("[$i:a]aformat=sample_rates=44100:channel_layouts=stereo[a$i];"); labels.append("[a$i]") }
            parts.append("${labels}concat=n=${inputs.size}:v=0:a=1[outa]")
            a += listOf("-filter_complex", parts.toString(), "-map", "[outa]")
            a += when (output.extension) { "mp3" -> listOf("-c:a", "libmp3lame", "-q:a", "2"); else -> listOf("-c:a", "aac", "-b:a", "192k") }
        } else {
            // Every clip is fitted into the first clip's frame (black bars where shapes differ).
            val (w, h) = frameSize(inputs.first())
            inputs.forEachIndexed { i, f ->
                parts.append("[$i:v]scale=$w:$h:force_original_aspect_ratio=decrease,pad=$w:$h:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=30,format=yuv420p[v$i];")
                if (hasAudio(f)) parts.append("[$i:a]aformat=sample_rates=44100:channel_layouts=stereo[a$i];")
                else {
                    parts.append("anullsrc=r=44100:cl=stereo,atrim=duration=${durations[i].coerceAtLeast(0.1)}[a$i];")
                    silentInputs++
                }
                labels.append("[v$i][a$i]")
            }
            parts.append("${labels}concat=n=${inputs.size}:v=1:a=1[outv][outa]")
            a += listOf("-filter_complex", parts.toString(), "-map", "[outv]", "-map", "[outa]")
            a += videoCodec(encoders(ctx), "h264", null, false, "mp4") + listOf("-c:a", "aac", "-b:a", "160k", "-movflags", "+faststart")
        }
        a += output.absolutePath
        runFfmpeg(ctx, id, a, durations.sum().takeIf { it > 0 }, onProgress)
    }

    private fun frameSize(f: File): Pair<Int, Int> = meta(f) { r ->
        var w = r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_WIDTH)?.toIntOrNull() ?: return@meta null
        var h = r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_HEIGHT)?.toIntOrNull() ?: return@meta null
        val rot = r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_ROTATION)?.toIntOrNull() ?: 0
        if (rot == 90 || rot == 270) { val t = w; w = h; h = t }
        (w / 2 * 2) to (h / 2 * 2)
    } ?: (1280 to 720)

    /** Saves pictures from a video: one every [everySec] seconds (0 = every frame), into [outDir]. */
    fun frames(ctx: Context, id: String, input: File, outDir: File, everySec: Double, format: String, onProgress: (Float) -> Unit) {
        outDir.mkdirs()
        val vf = if (everySec > 0) "fps=1/$everySec" else "null"
        val a = listOf("-hide_banner", "-y", "-nostats", "-progress", "pipe:1", "-i", input.absolutePath, "-vf", vf) +
            (if (format == "jpg") listOf("-q:v", "2") else emptyList()) + File(outDir, "${outDir.name} %04d.$format").absolutePath
        runFfmpeg(ctx, id, a, durationSec(input), onProgress)
        if (outDir.listFiles().isNullOrEmpty()) throw ConvertError("No pictures came out of this video.")
    }

    /**
     * Adds subtitles to a video: burned into the picture (always visible) or as a track
     * the player can turn on and off.
     */
    fun addSubtitles(ctx: Context, id: String, video: File, subs: File, output: File, burn: Boolean, size: String?, onProgress: (Float) -> Unit) {
        val a = mutableListOf("-hide_banner", "-y", "-nostats", "-progress", "pipe:1", "-i", video.absolutePath)
        if (burn) {
            // libass needs a font folder; Android's own fonts cover most scripts.
            val fontSize = when (size) { "small" -> 16; "large" -> 28; else -> 22 }
            val path = subs.absolutePath.replace("\\", "/").replace(":", "\\:").replace("'", "\\'")
            val style = "FontName=Roboto,Fontsize=$fontSize,Bold=1,Outline=2,Shadow=0,MarginV=24"
            a += listOf("-vf", "subtitles='$path':fontsdir=/system/fonts:force_style='$style'")
            a += videoCodec(encoders(ctx), "h264", null, true, "mp4") + listOf("-c:a", "copy", "-movflags", "+faststart")
        } else {
            a += listOf("-i", subs.absolutePath, "-map", "0:v?", "-map", "0:a?", "-map", "1:0", "-c:v", "copy", "-c:a", "copy")
            a += if (output.extension == "mkv") listOf("-c:s", "srt") else listOf("-c:s", "mov_text", "-movflags", "+faststart")
        }
        a += output.absolutePath
        runFfmpeg(ctx, id, a, durationSec(video), onProgress)
    }

    /** Subtitle file to another subtitle format, optionally moved earlier/later by [shiftSec]. */
    fun subtitles(ctx: Context, id: String, input: File, output: File, shiftSec: Double, onProgress: (Float) -> Unit) {
        val a = mutableListOf("-hide_banner", "-y", "-nostats")
        if (shiftSec != 0.0) a += listOf("-itsoffset", shiftSec.toString())
        a += listOf("-i", input.absolutePath)
        if (output.extension == "lrc") a += listOf("-f", "lrc")
        a += output.absolutePath
        runFfmpeg(ctx, id, a, null, onProgress)
    }

    /** One picture to another format FFmpeg can write but Android can't (AVIF, TIFF, BMP, GIF, ICO). */
    fun image(ctx: Context, id: String, input: File, output: File, quality: Int) {
        // iPhone-style HEIC/AVIF photos are stored as a grid of small tiles. This FFmpeg only reads
        // the first tile, so stitch the whole grid into a PNG first.
        if (input.extension.lowercase() in setOf("heic", "heif", "avif", "hif")) {
            val grid = File(input.parentFile, "${input.nameWithoutExtension}.grid.png")
            if (stitchGrid(ctx, id, input, grid)) {
                try { if (output.extension.lowercase() == "png") grid.copyTo(output, overwrite = true) else image(ctx, id, grid, output, quality) }
                finally { grid.delete() }
                return
            }
        }
        val a = mutableListOf("-hide_banner", "-y", "-nostats", "-i", input.absolutePath)
        when (output.extension) {
            "avif" -> a += listOf("-c:v", "libaom-av1", "-still-picture", "1", "-crf", "${(63 - quality * 0.45).toInt().coerceIn(10, 50)}",
                "-cpu-used", "6", "-pix_fmt", "yuv420p")
            "ico" -> a += listOf("-vf", "scale='min(256,iw)':'min(256,ih)':force_original_aspect_ratio=decrease")
            "gif" -> a += listOf("-vf", "split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse")
        }
        a += listOf("-frames:v", "1", "-update", "1", output.absolutePath)
        runFfmpeg(ctx, id, a, null) {}
    }

    /** Returns false when [input] isn't a tiled picture. Tiles are in rows, left to right (HEIF rule). */
    private fun stitchGrid(ctx: Context, id: String, input: File, out: File): Boolean {
        val info = start(ctx, listOf("-hide_banner", "-i", input.absolutePath)).let { p ->
            p.inputStream.close()
            p.errorStream.bufferedReader().readText().also { p.waitFor() }
        }
        val m = Regex("""Tile Grid:.*?(\d{2,5})x(\d{2,5})""").find(info) ?: return false
        val w = m.groupValues[1].toInt(); val h = m.groupValues[2].toInt()
        val tile = File(input.parentFile, "${input.nameWithoutExtension}.tile.png")
        runCatching { runFfmpeg(ctx, id, listOf("-hide_banner", "-y", "-nostats", "-i", input.absolutePath, "-map", "0:0", "-frames:v", "1", "-update", "1", tile.absolutePath), null) {} }
        val b = android.graphics.BitmapFactory.Options().apply { inJustDecodeBounds = true }
        android.graphics.BitmapFactory.decodeFile(tile.absolutePath, b)
        tile.delete()
        val tw = b.outWidth; val th = b.outHeight
        if (tw <= 0 || th <= 0 || (tw >= w && th >= h)) return false
        val cols = (w + tw - 1) / tw; val rows = (h + th - 1) / th
        val n = cols * rows
        if (n > 1024) return false
        val ins = (0 until n).joinToString("") { "[0:$it]" }
        val layout = (0 until n).joinToString("|") { "${(it % cols) * tw}_${(it / cols) * th}" }
        return runCatching {
            runFfmpeg(ctx, id, listOf("-hide_banner", "-y", "-nostats", "-i", input.absolutePath,
                "-filter_complex", "${ins}xstack=inputs=$n:layout=$layout,crop=$w:$h:0:0", "-frames:v", "1", "-update", "1", out.absolutePath), null) {}
            out.length() > 0
        }.getOrDefault(false)
    }

    private fun runFfmpeg(ctx: Context, id: String, a: List<String>, outSeconds: Double?, onProgress: (Float) -> Unit) {
        val p = start(ctx, a)
        processes[id] = p
        val errTail = ArrayDeque<String>()
        val errThread = Thread {
            p.errorStream.bufferedReader().forEachLine { synchronized(errTail) { errTail.addLast(it); if (errTail.size > 30) errTail.removeFirst() } }
        }.apply { start() }
        p.inputStream.bufferedReader().forEachLine { line ->
            // -progress writes key=value lines; out_time_us is how far into the output we are.
            if (line.startsWith("out_time_us=") || line.startsWith("out_time_ms=")) {
                val us = line.substringAfter('=').toLongOrNull() ?: return@forEachLine
                if (outSeconds != null && outSeconds > 0) onProgress((us / 1e6 / outSeconds * 100).toFloat().coerceIn(0f, 99f))
            }
        }
        val code = p.waitFor()
        errThread.join(2000)
        processes.remove(id)
        if (code != 0) {
            val lines = synchronized(errTail) { errTail.toList() }
            android.util.Log.w("AnySave", "ffmpeg failed ($code): ${lines.takeLast(8).joinToString(" | ")}")
            val msg = lines.lastOrNull { it.isNotBlank() && !it.startsWith("  ") && !it.startsWith("Conversion failed") }
            throw Exception(if (code == 137 || code == 143) "Cancelled" else "Couldn't convert: ${msg ?: "FFmpeg error $code"}")
        }
        onProgress(100f)
    }

    fun cancel(id: String) {
        processes.remove(id)?.destroy()
        FastVideo.cancel(id)
        ConvertTools.cancel(id)
    }
}
