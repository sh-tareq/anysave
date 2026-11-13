package com.anysave.app

import android.content.Context
import android.net.Uri
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.update
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

enum class JobStatus { QUEUED, WAITING_WIFI, SCHEDULED, DOWNLOADING, PAUSED, SAVING, DONE, ERROR, CANCELLED }

/** VIDEO: page link through yt-dlp. FILE: any direct file link. CONVERT: a local file through FFmpeg. */
enum class JobKind { VIDEO, FILE, CONVERT }

data class Job(
    val id: String,
    /** Page/file URL, or for CONVERT the content:// Uri of the input file. */
    val url: String,
    val title: String,
    val format: String = "",
    val sort: String? = null,
    val audioOnly: Boolean = false,
    val qualityLabel: String = "",
    val site: String? = null,
    val thumbnail: String? = null,
    val status: JobStatus = JobStatus.QUEUED,
    val percent: Float = 0f,
    val speed: String = "",
    val eta: String = "",
    val uri: Uri? = null,
    val mime: String? = null,
    val fileName: String? = null,
    val sizeBytes: Long = 0,
    val error: String? = null,
    val createdAt: Long = System.currentTimeMillis(),
    /** Save under [title] instead of the name yt-dlp picks (ad videos, whose links make huge names). */
    val fixedName: Boolean = false,
    val kind: JobKind = JobKind.VIDEO,
    /** Converter settings (JSON) for CONVERT jobs. */
    val spec: String? = null,
    /** Bytes downloaded so far / expected (FILE jobs; lets them resume). */
    val doneBytes: Long = 0,
    val totalBytes: Long = 0,
    /** The user's queue this download is in (blank = the main queue). */
    val queue: String = "",
    /** "Start now" or a manual resume: runs even outside the scheduled hours. */
    val ignoreSchedule: Boolean = false,
    /** SHA-256 the finished file must have (pasted with the link), and the one it has. */
    val expectedSha256: String? = null,
    val sha256: String? = null,
) {
    val finished get() = status == JobStatus.DONE || status == JobStatus.ERROR || status == JobStatus.CANCELLED
    val active get() = !finished && status != JobStatus.PAUSED
    val canPause get() = kind != JobKind.CONVERT && (status == JobStatus.DOWNLOADING || status == JobStatus.QUEUED ||
        status == JobStatus.WAITING_WIFI || status == JobStatus.SCHEDULED)
    val category get() = FileKinds.categoryOf(fileName ?: title, mime, kind, audioOnly)

    fun toJson() = JSONObject().apply {
        put("id", id); put("url", url); put("title", title); put("format", format)
        put("sort", sort); put("audioOnly", audioOnly); put("qualityLabel", qualityLabel)
        put("site", site); put("thumbnail", thumbnail); put("status", status.name)
        put("uri", uri?.toString()); put("mime", mime); put("fileName", fileName)
        put("sizeBytes", sizeBytes); put("error", error); put("createdAt", createdAt)
        put("fixedName", fixedName); put("kind", kind.name); put("spec", spec)
        put("doneBytes", doneBytes); put("totalBytes", totalBytes)
        put("queue", queue); put("ignoreSchedule", ignoreSchedule)
        put("expectedSha256", expectedSha256); put("sha256", sha256)
    }

    companion object {
        fun fromJson(o: JSONObject): Job {
            fun s(k: String) = if (o.isNull(k)) null else o.optString(k)
            val kind = runCatching { JobKind.valueOf(o.getString("kind")) }.getOrDefault(JobKind.VIDEO)
            var status = runCatching { JobStatus.valueOf(o.getString("status")) }.getOrDefault(JobStatus.ERROR)
            var error = s("error")
            if (status != JobStatus.DONE && status != JobStatus.ERROR && status != JobStatus.CANCELLED &&
                status != JobStatus.PAUSED && status != JobStatus.SCHEDULED) { // scheduled ones keep waiting for their time
                // The app was closed mid-download: downloads keep their partial file and can resume.
                if (kind == JobKind.CONVERT) { status = JobStatus.ERROR; error = "Interrupted. Tap Retry." }
                else status = JobStatus.PAUSED
            }
            return Job(
                id = o.getString("id"), url = o.getString("url"), title = o.optString("title"),
                format = o.optString("format"), sort = s("sort"), audioOnly = o.optBoolean("audioOnly"),
                qualityLabel = o.optString("qualityLabel"), site = s("site"), thumbnail = s("thumbnail"),
                status = status, uri = s("uri")?.let(Uri::parse), mime = s("mime"), fileName = s("fileName"),
                sizeBytes = o.optLong("sizeBytes"), error = error, createdAt = o.optLong("createdAt"),
                fixedName = o.optBoolean("fixedName"), kind = kind, spec = s("spec"),
                doneBytes = o.optLong("doneBytes"), totalBytes = o.optLong("totalBytes"),
                queue = o.optString("queue"), ignoreSchedule = o.optBoolean("ignoreSchedule"),
                expectedSha256 = s("expectedSha256"), sha256 = s("sha256"),
            )
        }
    }
}

/** Download list shared by the UI and DownloadService, saved to disk so history survives restarts. */
object Downloads {
    val jobs = MutableStateFlow<List<Job>>(emptyList())
    private var file: File? = null

    fun init(ctx: Context) {
        val f = File(ctx.filesDir, "history.json")
        file = f
        jobs.value = if (!f.exists()) emptyList() else runCatching {
            val arr = JSONArray(f.readText())
            List(arr.length()) { Job.fromJson(arr.getJSONObject(it)) }
        }.getOrElse {
            // Keep the unreadable file instead of overwriting it on the next save.
            f.renameTo(File(ctx.filesDir, "history.bad-${System.currentTimeMillis()}.json"))
            emptyList()
        }
    }

    /** Called from several threads; writes a temp file then renames it, so a crash can't leave half a file. */
    @Synchronized
    private fun save() {
        val f = file ?: return
        val arr = JSONArray().apply { jobs.value.take(500).forEach { put(it.toJson()) } }
        runCatching {
            val tmp = File(f.parentFile, f.name + ".tmp")
            tmp.writeText(arr.toString())
            if (!tmp.renameTo(f)) { f.delete(); tmp.renameTo(f) }
        }
    }

    fun add(job: Job) { jobs.update { listOf(job) + it }; save() }

    /** Many at once (a playlist): one save instead of one per job. */
    fun addAll(list: List<Job>) { jobs.update { list.reversed() + it }; save() }

    fun get(id: String) = jobs.value.firstOrNull { it.id == id }

    /** Progress ticks don't hit the disk; status changes do. */
    fun update(id: String, change: (Job) -> Job) {
        var statusChanged = false
        jobs.update { list ->
            list.map {
                if (it.id != id) it else change(it).also { new ->
                    statusChanged = new.status != it.status || new.title != it.title || new.url != it.url ||
                        new.queue != it.queue || new.ignoreSchedule != it.ignoreSchedule || new.sha256 != it.sha256
                }
            }
        }
        if (statusChanged) save()
    }

    fun remove(id: String) { jobs.update { list -> list.filterNot { it.id == id } }; save(); dropPartial(id) }

    fun clearFinished() {
        val gone = jobs.value.filter { it.finished }.map { it.id }
        jobs.update { list -> list.filterNot { it.finished } }; save()
        gone.forEach(::dropPartial)
    }

    /** Failed downloads keep their partial files for Retry; delete them once the job leaves the list. */
    private fun dropPartial(id: String) {
        file?.parentFile?.let { File(it, "partial/$id").deleteRecursively() }
    }

    fun isActive(url: String) = jobs.value.any { it.active && it.url == url }
}
