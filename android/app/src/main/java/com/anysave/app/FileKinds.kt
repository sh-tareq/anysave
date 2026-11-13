package com.anysave.app

import android.webkit.MimeTypeMap
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLDecoder

/** Folder names inside Downloads/AnySave, plus helpers to tell file links from video pages. */
object FileKinds {
    enum class Category(val folder: String) {
        VIDEOS("Videos"), MUSIC("Music"), PICTURES("Pictures"), DOCUMENTS("Documents"),
        ARCHIVES("Archives"), APPS("Apps"), CONVERTED("Converted"), OTHER("Other")
    }

    private val video = setOf("mp4", "mkv", "webm", "mov", "avi", "m4v", "3gp", "flv", "wmv", "ts", "mpg", "mpeg")
    private val audio = setOf("mp3", "m4a", "aac", "wav", "flac", "ogg", "opus", "wma", "amr")
    private val image = setOf("jpg", "jpeg", "png", "gif", "webp", "heic", "bmp", "svg", "tif", "tiff")
    private val docs = setOf("pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "txt", "csv", "rtf", "odt", "ods", "epub", "md", "json", "xml")
    private val archives = setOf("zip", "rar", "7z", "tar", "gz", "tgz", "bz2", "xz", "iso", "dmg")
    private val apps = setOf("apk", "xapk", "apks", "exe", "msi", "pkg", "deb", "rpm", "appimage")
    /** Extensions that mean "this link is a file", not a web page. */
    val fileExtensions = video + audio + image + docs + archives + apps + setOf("bin", "img")

    /** "report.pdf" -> "pdf", "https://x.com/a/file.zip?x=1" -> "zip", "readme" -> "". */
    fun extOf(name: String?) = name.orEmpty().substringBefore('?').substringBefore('#')
        .substringAfterLast('/').substringAfterLast('.', "").lowercase()

    fun categoryOf(name: String?, mime: String?, kind: JobKind, audioOnly: Boolean = false): Category {
        if (kind == JobKind.CONVERT) return Category.CONVERTED
        val ext = extOf(name)
        val m = mime.orEmpty()
        return when {
            audioOnly || ext in audio || m.startsWith("audio/") -> Category.MUSIC
            ext in video || m.startsWith("video/") || kind == JobKind.VIDEO -> Category.VIDEOS
            ext in image || m.startsWith("image/") -> Category.PICTURES
            ext in docs || m == "application/pdf" || m.startsWith("text/") -> Category.DOCUMENTS
            ext in archives || m.contains("zip") || m.contains("compressed") || m.contains("x-tar") -> Category.ARCHIVES
            ext in apps || m == "application/vnd.android.package-archive" -> Category.APPS
            else -> Category.OTHER
        }
    }

    /** Quick guess from the URL alone: ends in a known file extension. */
    fun looksLikeFile(url: String) = extOf(url.substringBefore('#')) in fileExtensions

    data class Probe(val isFile: Boolean, val name: String?, val mime: String?, val length: Long, val resumable: Boolean, val status: Int = 200)

    /**
     * Asks the server what a link is (HEAD request). A link that returns HTML is a web page
     * (try it as a video page); anything else is a file to download directly.
     */
    fun probe(url: String): Probe? = probeWith(url, FILE_USER_AGENT)?.takeIf { it.status != 403 } ?: probeWith(url, USER_AGENT)

    // HEAD first. Some servers don't answer HEAD (they hang up or say 405), so fall back to a
    // GET for the first byte only, which every file server understands.
    private fun probeWith(url: String, ua: String): Probe? =
        probeRequest(url, ua, head = true)?.takeIf { it.status < 400 || it.status == 403 }
            ?: probeRequest(url, ua, head = false)

    private fun probeRequest(url: String, ua: String, head: Boolean): Probe? = runCatching {
        val c = Net.open(URL(url)).apply {
            requestMethod = if (head) "HEAD" else "GET"; instanceFollowRedirects = true
            connectTimeout = 8000; readTimeout = 8000
            setRequestProperty("User-Agent", ua)
            if (!head) setRequestProperty("Range", "bytes=0-0")
        }
        try {
            c.connect()
            val code = c.responseCode
            val type = c.contentType?.substringBefore(';')?.trim()?.lowercase()
            val disposition = c.getHeaderField("Content-Disposition")
            val name = disposition?.let { fileNameFromDisposition(it) }
                ?: URLDecoder.decode(c.url.path.substringAfterLast('/'), "UTF-8").ifBlank { null }
            val isHtml = type == null || type.startsWith("text/html") || type.contains("xhtml")
            // Answer to the 1-byte GET: the real size is after the slash ("bytes 0-0/104857600").
            val length = if (code == 206) c.getHeaderField("Content-Range")?.substringAfterLast('/')?.trim()?.toLongOrNull() ?: -1L
                else c.contentLengthLong
            Probe(
                isFile = code in 200..299 && (!isHtml || disposition?.contains("attachment") == true),
                name = name, mime = type, length = length,
                resumable = code == 206 || c.getHeaderField("Accept-Ranges")?.contains("bytes") == true,
                status = code,
            )
        } finally { c.disconnect() }
    }.getOrNull()

    fun fileNameFromDisposition(d: String): String? {
        Regex("""filename\*=UTF-8''([^;]+)""", RegexOption.IGNORE_CASE).find(d)?.let {
            return URLDecoder.decode(it.groupValues[1], "UTF-8")
        }
        return Regex("""filename="?([^";]+)"?""", RegexOption.IGNORE_CASE).find(d)?.groupValues?.get(1)
    }

    fun mimeFromName(name: String?): String? =
        MimeTypeMap.getSingleton().getMimeTypeFromExtension(extOf(name))

    /** Honest name for direct file downloads; some servers block fake browser names. */
    const val FILE_USER_AGENT = "AnySave/1.0 (Android; +https://github.com/sh-tareq/anysave)"

    const val USER_AGENT =
        "Mozilla/5.0 (Linux; Android 10) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Mobile Safari/537.36"
}
