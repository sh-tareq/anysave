package com.vidgrab.app

import android.content.Context
import com.yausername.ffmpeg.FFmpeg
import com.yausername.youtubedl_android.YoutubeDL
import com.yausername.youtubedl_android.YoutubeDLException
import com.yausername.youtubedl_android.YoutubeDLRequest
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.File

/** Wraps yt-dlp (via youtubedl-android): setup, video lookup, quality options. */
object Engine {
    enum class State { STARTING, READY, FAILED }

    val state = MutableStateFlow(State.STARTING)
    var startError: String? = null
        private set

    const val H264_SORT = "res,fps,vcodec:h264,acodec:m4a"

    /**
     * Unpacks Python + yt-dlp + ffmpeg on first launch, then refreshes yt-dlp if due.
     * READY is only set afterwards: running yt-dlp while the updater is rewriting
     * it fails with "zipimport.ZipImportError: bad local file header".
     * Call off the main thread.
     */
    fun start(ctx: Context) {
        try {
            YoutubeDL.getInstance().init(ctx)
            FFmpeg.getInstance().init(ctx)
        } catch (e: Throwable) {
            startError = e.message ?: e.toString()
            state.value = State.FAILED
            return
        }
        autoUpdate(ctx)
        state.value = State.READY
    }

    /** Sites change often; keep yt-dlp fresh by checking at most every 2 days. */
    private fun autoUpdate(ctx: Context) {
        val prefs = ctx.getSharedPreferences("vidgrab", Context.MODE_PRIVATE)
        val last = prefs.getLong("lastUpdateCheck", 0)
        if (System.currentTimeMillis() - last < 2 * 24 * 3600 * 1000L) return
        runCatching {
            YoutubeDL.getInstance().updateYoutubeDL(ctx, YoutubeDL.UpdateChannel.STABLE)
            prefs.edit().putLong("lastUpdateCheck", System.currentTimeMillis()).apply()
        }
    }

    /** Manual "Update downloader". Blocks new lookups while yt-dlp is being replaced. */
    fun updateNow(ctx: Context): String {
        state.value = State.STARTING
        try {
            val s = YoutubeDL.getInstance().updateYoutubeDL(ctx, YoutubeDL.UpdateChannel.STABLE)
            ctx.getSharedPreferences("vidgrab", Context.MODE_PRIVATE).edit()
                .putLong("lastUpdateCheck", System.currentTimeMillis()).apply()
            return if (s == YoutubeDL.UpdateStatus.ALREADY_UP_TO_DATE) "Already up to date"
            else "Updated to ${YoutubeDL.getInstance().versionName(ctx)}"
        } catch (e: Throwable) {
            return "Update failed: ${e.message}"
        } finally {
            state.value = State.READY
        }
    }

    fun cookieFile(ctx: Context) = File(ctx.filesDir, "cookies.txt")

    fun applyCookies(ctx: Context, req: YoutubeDLRequest) {
        val f = cookieFile(ctx)
        if (f.exists() && f.length() > 0) req.addOption("--cookies", f.absolutePath)
    }

    data class Option(
        val label: String,
        val format: String,
        val sort: String? = null,
        val audioOnly: Boolean = false,
        val sizeBytes: Long? = null, // estimate, when the site reports sizes
    )

    data class Info(
        val url: String,
        val title: String,
        val thumbnail: String?,
        val uploader: String?,
        val duration: Double?,
        val options: List<Option>,
    )

    suspend fun info(ctx: Context, url: String): Info = withContext(Dispatchers.IO) {
        val req = YoutubeDLRequest(url)
            .addOption("-J")
            .addOption("--no-playlist")
            .addOption("--no-warnings")
        applyCookies(ctx, req)
        val out = try {
            YoutubeDL.getInstance().execute(req).out
        } catch (e: YoutubeDLException) {
            throw Exception(cleanError(e.message))
        }
        var json = JSONObject(out)
        val entries = json.optJSONArray("entries")
        if (entries != null) { // e.g. Instagram carousel: describe the first item
            for (i in 0 until entries.length()) {
                val e = entries.optJSONObject(i)
                if (e != null) { json = e; break }
            }
        }
        val title = json.optString("title").ifBlank { json.optString("description").take(80) }
        Info(
            url = url,
            title = title.ifBlank { "Untitled" },
            thumbnail = json.optString("thumbnail").ifBlank { null },
            uploader = json.optString("uploader").ifBlank { json.optString("channel") }.ifBlank { null },
            duration = if (json.isNull("duration")) null else json.optDouble("duration"),
            options = buildOptions(json),
        )
    }

    private class Fmt(val height: Int, val vcodec: String, val acodec: String, val fps: Double, val size: Long?, val ext: String)

    private fun buildOptions(json: JSONObject): List<Option> {
        val arr = json.optJSONArray("formats")
        val fmts = mutableListOf<Fmt>()
        if (arr != null) for (i in 0 until arr.length()) {
            val f = arr.optJSONObject(i) ?: continue
            val size = when {
                !f.isNull("filesize") -> f.optLong("filesize")
                !f.isNull("filesize_approx") -> f.optLong("filesize_approx")
                else -> null
            }?.takeIf { it > 0 }
            fmts += Fmt(
                height = if (f.isNull("height")) 0 else f.optInt("height"),
                vcodec = f.optString("vcodec", "none").ifBlank { "none" },
                acodec = f.optString("acodec", "none").ifBlank { "none" },
                fps = f.optDouble("fps", 0.0).takeUnless { it.isNaN() } ?: 0.0,
                size = size,
                ext = f.optString("ext"),
            )
        }
        val video = fmts.filter { it.vcodec != "none" && it.height > 0 }
        val audioOnly = fmts.filter { it.vcodec == "none" && it.acodec != "none" }
        // Best audio, preferring m4a like the H264_SORT order does
        val bestAudio = audioOnly.sortedWith(compareByDescending<Fmt> { it.ext == "m4a" }.thenByDescending { it.size ?: 0 }).firstOrNull()

        /** Roughly what yt-dlp picks for "≤ h": highest res, then fps, then H.264. */
        fun estimate(maxH: Int?): Long? {
            val pick = video.filter { maxH == null || it.height <= maxH }
                .sortedWith(compareByDescending<Fmt> { it.height }.thenByDescending { it.fps }
                    .thenByDescending { it.vcodec.startsWith("avc") })
                .firstOrNull() ?: return null
            val v = pick.size ?: return null
            return if (pick.acodec != "none") v else v + (bestAudio?.size ?: 0)
        }

        val heights = video.map { it.height }.toSortedSet(compareByDescending { it })
        val avc = video.filter { it.vcodec.startsWith("avc") }.map { it.height }.toSet()
        // Highest resolution first; at equal resolution prefer H.264 + AAC so the
        // MP4 plays on every phone. YouTube only offers H.264 up to 1080p.
        val opts = mutableListOf(Option("Best quality (MP4)", "bv*+ba/b", H264_SORT, sizeBytes = estimate(null)))
        for (h in heights) {
            if (h < 144) continue
            val suffix = if (avc.isNotEmpty() && h !in avc) " (VP9/AV1)" else ""
            opts += Option("${h}p MP4$suffix", "bv*[height<=$h]+ba/b[height<=$h]", H264_SORT, sizeBytes = estimate(h))
        }
        opts += Option("Audio only (MP3)", "ba/b", audioOnly = true, sizeBytes = bestAudio?.size)
        return opts
    }

    fun humanSize(bytes: Long?): String? {
        if (bytes == null || bytes <= 0) return null
        val mb = bytes / 1_048_576.0
        return if (mb >= 1024) "%.1f GB".format(mb / 1024) else if (mb >= 10) "%.0f MB".format(mb) else "%.1f MB".format(mb)
    }

    /** yt-dlp errors can be long; keep the first ERROR line and a little after it. */
    fun cleanError(raw: String?): String {
        val lines = raw.orEmpty().lines().map { it.trim() }.filter { it.isNotEmpty() }
        val i = lines.indexOfFirst { it.startsWith("ERROR") }
        val msg = if (i >= 0) lines.subList(i, minOf(i + 3, lines.size)).joinToString(" ")
        else lines.lastOrNull() ?: "Something went wrong"
        return msg.replaceFirst("ERROR:", "").trim().ifBlank { "Something went wrong" }
    }

    fun siteOf(url: String): String? {
        val host = runCatching { android.net.Uri.parse(url).host.orEmpty().lowercase() }.getOrDefault("")
        return when {
            host.endsWith("youtube.com") || host.endsWith("youtu.be") -> "YouTube"
            host.endsWith("facebook.com") || host.endsWith("fb.watch") -> "Facebook"
            host.endsWith("instagram.com") -> "Instagram"
            host.endsWith("tiktok.com") -> "TikTok"
            else -> null
        }
    }

    /** Pulls the first link out of shared text like "Check this out https://…". */
    fun extractUrl(text: String?): String? =
        text?.let { Regex("""https?://\S+""").find(it)?.value?.trimEnd('.', ',', ')', '"', '\'') }
}
