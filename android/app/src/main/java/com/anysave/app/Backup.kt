package com.anysave.app

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject

/** Export the list of links to a file, and add links from such a file (or any text with links). */
object Backup {
    fun exportJson(): String {
        val items = JSONArray()
        Downloads.jobs.value.filter { it.kind != JobKind.CONVERT && it.url.startsWith("http") }
            .sortedBy { it.createdAt }
            .forEach { j ->
                items.put(JSONObject().apply {
                    put("url", j.url); put("title", j.title)
                    put("kind", if (j.kind == JobKind.FILE) "file" else "video")
                    put("status", j.status.name.lowercase())
                    if (j.audioOnly) put("audioOnly", true)
                    if (j.format.isNotBlank()) put("format", j.format)
                    j.sort?.let { put("sort", it) }
                    (j.expectedSha256 ?: j.sha256)?.takeIf { it.isNotBlank() }?.let { put("sha256", it) }
                })
            }
        return JSONObject().put("app", "AnySave").put("version", 1)
            .put("exportedAt", System.currentTimeMillis()).put("items", items).toString(2)
    }

    /** Links found in an exported list, or in any text. */
    fun parse(text: String): List<String> {
        runCatching {
            val t = text.trim()
            val arr = if (t.startsWith("[")) JSONArray(t) else JSONObject(t).optJSONArray("items")
            if (arr != null) {
                return List(arr.length()) { i ->
                    when (val it = arr.get(i)) {
                        is JSONObject -> it.optString("url")
                        else -> it.toString()
                    }
                }.filter { it.startsWith("http://") || it.startsWith("https://") }.distinct()
            }
        }
        return Engine.extractUrls(text)
    }

    /** The items an exported list marks as files: url to the file name it had (if it had one). */
    private fun fileItems(text: String): Map<String, String?> = runCatching {
        val arr = JSONObject(text.trim()).optJSONArray("items") ?: return emptyMap()
        buildMap {
            for (i in 0 until arr.length()) {
                val o = arr.optJSONObject(i) ?: continue
                if (o.optString("kind") == "file") put(o.optString("url"), o.optString("title").takeIf { it.contains('.') })
            }
        }
    }.getOrDefault(emptyMap())

    /** Adds the links; ones already waiting or downloading are skipped. Returns added to skipped. */
    fun import(ctx: Context, text: String): Pair<Int, Int> {
        var added = 0
        var skipped = 0
        val files = fileItems(text)
        for (url in parse(text).take(500)) {
            if (Downloads.isActive(url)) { skipped++; continue }
            // Items the list marks as files download as files, like the desktop app does.
            val ok = if (url in files) DownloadService.startFile(ctx, url, files[url]) else DownloadService.startLink(ctx, url)
            if (ok) added++ else skipped++
        }
        return added to skipped
    }
}
