package com.vidgrab.app

import android.content.Context
import android.media.MediaMetadataRetriever
import org.json.JSONObject
import java.io.File
import java.util.concurrent.ConcurrentHashMap

/** What the user picked on the Convert screen. */
data class ConvertSpec(
    val format: String = "mp4",      // mp4 webm mkv mov gif mp3 m4a wav opus
    val height: Int? = null,         // null = keep
    val targetMb: Int? = null,       // null = normal quality
    val startSec: Double? = null,
    val endSec: Double? = null,
    val removeAudio: Boolean = false,
) {
    val isAudio get() = format in AUDIO_FORMATS
    fun toJson() = JSONObject().apply {
        put("format", format); put("height", height); put("targetMb", targetMb)
        put("startSec", startSec); put("endSec", endSec); put("removeAudio", removeAudio)
    }.toString()

    /** Short description, e.g. "MP4 · 720p · under 25 MB · 0:05–0:30". */
    fun label(): String = listOfNotNull(
        format.uppercase(),
        height?.let { "${it}p" },
        targetMb?.let { "under $it MB" },
        if (startSec != null || endSec != null) "${fmt(startSec ?: 0.0)}–${endSec?.let(::fmt) ?: "end"}" else null,
        if (removeAudio && !isAudio) "no sound" else null,
    ).joinToString(" · ")

    companion object {
        val VIDEO_FORMATS = listOf("mp4", "webm", "mkv", "mov", "gif")
        val AUDIO_FORMATS = listOf("mp3", "m4a", "wav", "opus")
        private fun fmt(s: Double) = "%d:%02d".format(s.toInt() / 60, s.toInt() % 60)
        fun fromJson(s: String?): ConvertSpec = runCatching {
            val o = JSONObject(s!!)
            fun d(k: String) = if (o.isNull(k)) null else o.getDouble(k)
            fun i(k: String) = if (o.isNull(k)) null else o.getInt(k)
            ConvertSpec(o.optString("format", "mp4"), i("height"), i("targetMb"), d("startSec"), d("endSec"), o.optBoolean("removeAudio"))
        }.getOrDefault(ConvertSpec())
    }
}

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

    private fun run(ctx: Context, args: List<String>): Process =
        ProcessBuilder(listOf(ffmpegPath(ctx).absolutePath) + args).apply {
            environment().putAll(env(ctx))
            redirectErrorStream(false)
        }.start()

    /** Which encoders this FFmpeg build has (x264, VP9, Opus... vary between builds). */
    private fun encoders(ctx: Context): Set<String> = encoders ?: run(ctx, listOf("-hide_banner", "-encoders")).let { p ->
        val names = p.inputStream.bufferedReader().readLines()
            .mapNotNull { Regex("""^\s*[VAS][.A-Z]{5}\s+(\S+)""").find(it)?.groupValues?.get(1) }.toSet()
        p.waitFor()
        names.also { encoders = it }
    }

    fun durationSec(file: File): Double? = runCatching {
        MediaMetadataRetriever().run {
            setDataSource(file.absolutePath)
            val ms = extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)?.toLong()
            release()
            ms?.div(1000.0)
        }
    }.getOrNull()

    /**
     * Converts [input] to [output] with [spec]. Calls [onProgress] with 0–100.
     * Throws with FFmpeg's last error line on failure.
     */
    fun convert(ctx: Context, id: String, input: File, output: File, spec: ConvertSpec, onProgress: (Float) -> Unit) {
        val enc = encoders(ctx)
        val total = durationSec(input)
        val start = spec.startSec?.takeIf { it > 0 }
        val length = spec.endSec?.let { it - (start ?: 0.0) }?.takeIf { it > 0 }
        val outSeconds = length ?: total?.minus(start ?: 0.0)

        val a = mutableListOf("-hide_banner", "-y", "-nostats", "-progress", "pipe:1")
        start?.let { a += listOf("-ss", it.toString()) }
        a += listOf("-i", input.absolutePath)
        length?.let { a += listOf("-t", it.toString()) }

        val scale = spec.height?.let { "scale=-2:'min($it,ih)'" }
        val h264 = when {
            "libx264" in enc -> listOf("-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p")
            "h264_mediacodec" in enc -> listOf("-c:v", "h264_mediacodec")
            else -> listOf("-c:v", "mpeg4", "-q:v", "4")
        }
        // Stereo: surround tracks play badly on phones and in WhatsApp.
        val aac = listOf("-c:a", "aac", "-b:a", "128k", "-ac", "2")
        // Target size: share the budget between video and 128 kb/s audio.
        val videoBitrate = spec.targetMb?.let { mb ->
            val secs = outSeconds ?: return@let null
            ((mb * 0.96 * 8.0 * 1024 * 1024 / secs) - 128_000).toLong().coerceAtLeast(150_000) // 4% margin for container overhead
        }
        fun quality(crf: String) = videoBitrate?.let { listOf("-b:v", "$it", "-maxrate", "$it", "-bufsize", "${it * 2}") }
            ?: if (h264.contains("libx264")) listOf("-crf", crf) else emptyList()

        when (spec.format) {
            "mp4", "mov", "mkv" -> {
                scale?.let { a += listOf("-vf", it) }
                a += h264 + quality("23")
                a += if (spec.removeAudio) listOf("-an") else aac
                if (spec.format != "mkv") a += listOf("-movflags", "+faststart")
            }
            "webm" -> {
                scale?.let { a += listOf("-vf", it) }
                a += if ("libvpx-vp9" in enc) listOf("-c:v", "libvpx-vp9", "-row-mt", "1", "-deadline", "realtime", "-cpu-used", "8")
                else listOf("-c:v", "libvpx")
                a += videoBitrate?.let { listOf("-b:v", "$it") } ?: listOf("-crf", "33", "-b:v", "0")
                a += if (spec.removeAudio) listOf("-an")
                else if ("libopus" in enc) listOf("-c:a", "libopus", "-b:a", "96k") else listOf("-c:a", "libvorbis")
            }
            "gif" -> {
                val h = spec.height ?: 480
                a += listOf("-vf", "fps=12,scale=-2:'min($h,ih)':flags=lanczos,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse", "-loop", "0", "-an")
            }
            "mp3" -> a += listOf("-vn") + (if ("libmp3lame" in enc) listOf("-c:a", "libmp3lame", "-q:a", "2") else listOf("-c:a", "mp3"))
            "m4a" -> a += listOf("-vn", "-c:a", "aac", "-b:a", "192k", "-ac", "2")
            "wav" -> a += listOf("-vn", "-c:a", "pcm_s16le")
            "opus" -> a += listOf("-vn") + (if ("libopus" in enc) listOf("-c:a", "libopus", "-b:a", "128k") else listOf("-c:a", "opus", "-strict", "-2"))
        }
        a += output.absolutePath

        val p = run(ctx, a)
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
            val msg = synchronized(errTail) { errTail.lastOrNull { it.isNotBlank() && !it.startsWith("  ") } }
            throw Exception(if (code == 137 || code == 143) "Cancelled" else "Couldn't convert: ${msg ?: "FFmpeg error $code"}")
        }
        onProgress(100f)
    }

    fun cancel(id: String) { processes.remove(id)?.destroy() }
}
