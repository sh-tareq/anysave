package com.vidgrab.app

import android.content.Context
import android.net.Uri
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.update
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

enum class JobStatus { QUEUED, WAITING_WIFI, DOWNLOADING, SAVING, DONE, ERROR, CANCELLED }

data class Job(
    val id: String,
    val url: String,
    val title: String,
    val format: String,
    val sort: String?,
    val audioOnly: Boolean,
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
) {
    val finished get() = status == JobStatus.DONE || status == JobStatus.ERROR || status == JobStatus.CANCELLED
    val active get() = !finished

    fun toJson() = JSONObject().apply {
        put("id", id); put("url", url); put("title", title); put("format", format)
        put("sort", sort); put("audioOnly", audioOnly); put("qualityLabel", qualityLabel)
        put("site", site); put("thumbnail", thumbnail); put("status", status.name)
        put("uri", uri?.toString()); put("mime", mime); put("fileName", fileName)
        put("sizeBytes", sizeBytes); put("error", error); put("createdAt", createdAt)
    }

    companion object {
        fun fromJson(o: JSONObject): Job {
            fun s(k: String) = if (o.isNull(k)) null else o.optString(k)
            var status = runCatching { JobStatus.valueOf(o.getString("status")) }.getOrDefault(JobStatus.ERROR)
            var error = s("error")
            if (status != JobStatus.DONE && status != JobStatus.ERROR && status != JobStatus.CANCELLED) {
                status = JobStatus.ERROR // the app was closed mid-download
                error = "Interrupted. Tap Retry."
            }
            return Job(
                id = o.getString("id"), url = o.getString("url"), title = o.optString("title"),
                format = o.optString("format"), sort = s("sort"), audioOnly = o.optBoolean("audioOnly"),
                qualityLabel = o.optString("qualityLabel"), site = s("site"), thumbnail = s("thumbnail"),
                status = status, uri = s("uri")?.let(Uri::parse), mime = s("mime"), fileName = s("fileName"),
                sizeBytes = o.optLong("sizeBytes"), error = error, createdAt = o.optLong("createdAt"),
            )
        }
    }
}

/** Download list shared by the UI and DownloadService, saved to disk so history survives restarts. */
object Downloads {
    val jobs = MutableStateFlow<List<Job>>(emptyList())
    private var file: File? = null

    fun init(ctx: Context) {
        file = File(ctx.filesDir, "history.json")
        jobs.value = runCatching {
            val arr = JSONArray(file!!.readText())
            List(arr.length()) { Job.fromJson(arr.getJSONObject(it)) }
        }.getOrDefault(emptyList())
    }

    private fun save() {
        val f = file ?: return
        val arr = JSONArray().apply { jobs.value.take(500).forEach { put(it.toJson()) } }
        runCatching { f.writeText(arr.toString()) }
    }

    fun add(job: Job) { jobs.update { listOf(job) + it }; save() }

    fun get(id: String) = jobs.value.firstOrNull { it.id == id }

    /** Progress ticks don't hit the disk; status changes do. */
    fun update(id: String, change: (Job) -> Job) {
        var statusChanged = false
        jobs.update { list ->
            list.map {
                if (it.id != id) it else change(it).also { new ->
                    statusChanged = new.status != it.status || new.title != it.title
                }
            }
        }
        if (statusChanged) save()
    }

    fun remove(id: String) { jobs.update { list -> list.filterNot { it.id == id } }; save() }

    fun clearFinished() { jobs.update { list -> list.filterNot { it.finished } }; save() }

    fun isActive(url: String) = jobs.value.any { it.active && it.url == url }
}
