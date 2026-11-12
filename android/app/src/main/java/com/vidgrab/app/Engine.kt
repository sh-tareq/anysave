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

    /**
     * YouTube needs a JavaScript runtime to unlock its video formats (without one, yt-dlp
     * often finds only audio). The app ships QuickJS as libqjs.so: Android only lets apps run
     * programs from their native-library folder. See scripts/fetch-quickjs.sh.
     */
    fun applyJsRuntime(ctx: Context, req: YoutubeDLRequest) {
        val qjs = File(ctx.applicationInfo.nativeLibraryDir, "libqjs.so")
        if (qjs.exists()) req.addOption("--js-runtimes", "quickjs:${qjs.absolutePath}")
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
        /** yt-dlp answered with a list (e.g. a playlist on another site): offer the whole list. */
        val isPlaylist: Boolean = false,
    )

    /** One video of a playlist or channel, from a quick (flat) listing. */
    data class Entry(val url: String, val title: String, val duration: Double?, val thumbnail: String?)

    data class Playlist(val url: String, val title: String, val uploader: String?, val total: Int?, val entries: List<Entry>)

    /** Qualities offered for a whole playlist (sizes aren't known without looking up each video). */
    val batchOptions: List<Option> =
        listOf(Quality.BEST, Quality.P1080, Quality.P720, Quality.P480, Quality.AUDIO).map { it.toOption() }

    private fun JSONObject.str(k: String) = if (isNull(k)) null else optString(k).ifBlank { null }

    suspend fun info(ctx: Context, url: String): Info = withContext(Dispatchers.IO) {
        val req = YoutubeDLRequest(url)
            .addOption("-J")
            .addOption("--no-playlist")
            .addOption("--no-warnings")
        applyCookies(ctx, req)
        applyJsRuntime(ctx, req)
        val out = try {
            YoutubeDL.getInstance().execute(req).out
        } catch (e: YoutubeDLException) {
            throw Exception(cleanError(e.message))
        }
        var json = JSONObject(out)
        val entries = json.optJSONArray("entries")
        val isPlaylist = json.str("_type") == "playlist" && (entries?.length() ?: 0) > 1
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
            isPlaylist = isPlaylist,
        )
    }

    /** Lists the videos of a playlist or channel quickly (no per-video lookup), up to 300. */
    suspend fun playlist(ctx: Context, url: String): Playlist = withContext(Dispatchers.IO) {
        val req = YoutubeDLRequest(url)
            .addOption("--yes-playlist")
            .addOption("--flat-playlist")
            .addOption("-I", if (url.contains("list=RD")) "1:50" else "1:300") // a Mix never ends
            .addOption("-J")
            .addOption("--no-warnings")
        applyCookies(ctx, req)
        applyJsRuntime(ctx, req)
        val out = try {
            YoutubeDL.getInstance().execute(req).out
        } catch (e: YoutubeDLException) {
            throw Exception(cleanError(e.message))
        }
        val json = JSONObject(out)
        val arr = json.optJSONArray("entries")
        val list = mutableListOf<Entry>()
        val seen = hashSetOf<String>()
        if (arr != null) for (i in 0 until arr.length()) {
            val e = arr.optJSONObject(i) ?: continue
            // Channel pages can list their tabs or other playlists: only videos are wanted.
            if (e.str("_type") == "playlist" || e.str("ie_key").orEmpty().endsWith("Tab")) continue
            val link = e.str("url")?.takeIf { it.startsWith("http") }
                ?: e.str("webpage_url")?.takeIf { it.startsWith("http") } ?: continue
            // Items that just point back at the same page (carousels) aren't separate videos.
            if (link == url || !seen.add(link)) continue
            val thumbs = e.optJSONArray("thumbnails")
            var thumb: String? = null
            var best = 0
            if (thumbs != null) for (t in 0 until thumbs.length()) {
                val o = thumbs.optJSONObject(t) ?: continue
                val w = o.optInt("width", 0)
                if (w in 120..480 && w > best) { best = w; thumb = o.str("url") }
            }
            list += Entry(
                url = link,
                title = e.str("title") ?: "Video ${list.size + 1}",
                duration = if (e.isNull("duration")) null else e.optDouble("duration").takeUnless { it.isNaN() },
                thumbnail = thumb ?: e.str("thumbnail"),
            )
        }
        Playlist(
            url = url,
            title = json.str("title") ?: "Playlist",
            uploader = json.str("uploader") ?: json.str("channel"),
            total = json.optInt("playlist_count", 0).takeIf { it > 0 },
            entries = list,
        )
    }

    private fun youtubeUri(url: String): android.net.Uri? {
        val u = runCatching { android.net.Uri.parse(url) }.getOrNull() ?: return null
        val host = u.host.orEmpty().lowercase()
        return u.takeIf { host == "youtu.be" || host == "youtube.com" || host.endsWith(".youtube.com") }
    }

    /** A YouTube link to a single video (it may also carry a playlist, "watch?v=…&list=…"). */
    fun isYoutubeVideo(url: String): Boolean {
        val u = youtubeUri(url) ?: return false
        val path = u.path.orEmpty()
        return u.host == "youtu.be" || u.getQueryParameter("v") != null || path.startsWith("/shorts/") || path.startsWith("/live/")
    }

    /**
     * The playlist or channel a YouTube link points to, as the link to list, or null.
     * Channel roots list their Videos tab.
     */
    fun youtubeList(url: String): String? {
        val u = youtubeUri(url) ?: return null
        val list = u.getQueryParameter("list")
        // A Mix (RD…) only opens from the video it started from; YouTube won't show it as a playlist page.
        // A Mix of one video is "RD" + that video's ID, so its link can be rebuilt from the list alone.
        val v = u.getQueryParameter("v") ?: list?.takeIf { Regex("^RD[\\w-]{11}$").matches(it) }?.substring(2)
        if (!list.isNullOrBlank()) return if (list.startsWith("RD") && v != null)
            "https://www.youtube.com/watch?v=$v&list=$list"
        else "https://www.youtube.com/playlist?list=$list"
        val seg = u.path.orEmpty().split('/').filter { it.isNotEmpty() }
        if (seg.isEmpty() || u.host == "youtu.be") return null
        val base = when {
            seg[0].startsWith("@") -> 1
            seg[0] in setOf("channel", "c", "user") && seg.size >= 2 -> 2
            else -> return null
        }
        val root = "https://www.youtube.com/" + seg.take(base).joinToString("/")
        val tab = seg.getOrNull(base)
        return when (tab) {
            null, "featured" -> "$root/videos"
            "videos", "shorts", "streams" -> "$root/$tab"
            else -> null
        }
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
        return when {
            mb >= 1024 -> "%.1f GB".format(mb / 1024)
            mb >= 10 -> "%.0f MB".format(mb)
            mb >= 1 -> "%.1f MB".format(mb)
            else -> "%.0f KB".format((bytes / 1024.0).coerceAtLeast(1.0))
        }
    }

    /** yt-dlp errors can be long; keep the first ERROR line and a little after it. */
    fun cleanError(raw: String?): String {
        val lines = raw.orEmpty().lines().map { it.trim() }.filter { it.isNotEmpty() }
        val i = lines.indexOfFirst { it.startsWith("ERROR") }
        val msg = if (i >= 0) lines.subList(i, minOf(i + 3, lines.size)).joinToString(" ")
        else lines.lastOrNull() ?: "Something went wrong"
        return friendly(msg.replaceFirst("ERROR:", "").trim().ifBlank { "Something went wrong" })
    }

    /** Common yt-dlp failures in plain language. */
    private fun friendly(msg: String): String {
        val l = msg.lowercase()
        return when {
            "cloudflare" in l || "anti-bot" in l || "captcha" in l ->
                "This website blocks automated downloads, so AnySave can't download from it. Use the site's own download button if it has one."
            "unsupported url" in l -> "No downloadable video found on this page. Try opening it in the built-in browser and playing the video."
            "drm" in l -> "This video is DRM-protected, so it can't be downloaded."
            "http error 404" in l -> "The video or file wasn't found (404). The link may be broken or removed."
            "http error 403" in l -> "The website refused the download (403). It may need you to be logged in, or it blocks downloads."
            else -> msg
        }
    }

    fun siteOf(url: String): String? {
        val host = runCatching { android.net.Uri.parse(url).host.orEmpty().lowercase() }.getOrDefault("")
        return when {
            host == "ads.tiktok.com" -> "TikTok"
            host.endsWith("youtube.com") || host.endsWith("youtu.be") -> "YouTube"
            host.endsWith("facebook.com") || host.endsWith("fb.watch") -> "Facebook"
            host.endsWith("instagram.com") -> "Instagram"
            host.endsWith("tiktok.com") -> "TikTok"
            host.endsWith("vimeo.com") -> "Vimeo"
            else -> null
        }
    }

    /** Known site name, or a readable domain for any other site ("vimeo.com" -> "Vimeo"). */
    fun siteLabel(url: String): String {
        siteOf(url)?.let { return it }
        val host = runCatching { android.net.Uri.parse(url).host.orEmpty().lowercase() }.getOrDefault("")
            .removePrefix("www.").removePrefix("m.")
        val name = host.split('.').let { if (it.size >= 2) it[it.size - 2] else it.firstOrNull() }.orEmpty()
        return name.replaceFirstChar { it.uppercase() }.ifBlank { "Web" }
    }

    /** Pulls the first link out of shared text like "Check this out https://…". */
    fun extractUrl(text: String?): String? = extractUrls(text).firstOrNull()

    /** Every link in a text (several pasted or shared at once), expanding `[01-10]` patterns, without repeats. */
    fun extractUrls(text: String?): List<String> =
        text?.let { t ->
            Regex("""https?://\S+""").findAll(t)
                .map { it.value.trimEnd('.', ',', ')', '"', '\'') }
                .flatMap { expandUrlPattern(it).asSequence() }
                .distinct()
                .toList()
        } ?: emptyList()

    /** Expands numeric range patterns like `https://example.com/file[01-10].zip` (up to 500 items). */
    fun expandUrlPattern(url: String): List<String> {
        val m = Regex("""^(.*)\[(\d{1,5})-(\d{1,5})\](.*)$""").find(url) ?: return listOf(url)
        val prefix = m.groupValues[1]
        val startStr = m.groupValues[2]
        val endStr = m.groupValues[3]
        val suffix = m.groupValues[4]
        val start = startStr.toIntOrNull() ?: return listOf(url)
        val end = endStr.toIntOrNull() ?: return listOf(url)
        if (end < start || end - start > 500) return listOf(url)
        val pad = if (startStr.length > 1 && startStr.startsWith('0')) startStr.length else 0
        return (start..end).map { i ->
            val num = if (pad > 0) i.toString().padStart(pad, '0') else i.toString()
            "$prefix$num$suffix"
        }
    }
}
