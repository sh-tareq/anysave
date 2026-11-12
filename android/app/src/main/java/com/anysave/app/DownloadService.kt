package com.anysave.app

import android.app.Notification
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.media.MediaScannerConnection
import android.net.ConnectivityManager
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.provider.MediaStore
import android.provider.OpenableColumns
import android.widget.Toast
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import com.yausername.youtubedl_android.YoutubeDL
import com.yausername.youtubedl_android.YoutubeDLException
import com.yausername.youtubedl_android.YoutubeDLRequest
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import java.io.File
import java.io.IOException
import java.io.RandomAccessFile
import java.net.HttpURLConnection
import java.net.URL
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import kotlin.coroutines.resume

/**
 * Runs downloads (videos through yt-dlp, any file directly) and conversions (FFmpeg) in the
 * foreground, several at once, so they continue when the app is closed. Downloads can be paused
 * and resumed: partial files are kept in filesDir/partial/<id> until finished or cancelled.
 */
class DownloadService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val running = MutableStateFlow(0)
    private val activeCount = AtomicInteger(0)
    /** Newest startId of any command; only touched on the main thread. */
    private var lastStartId = 0
    private val main = Handler(Looper.getMainLooper())
    /** Jobs that may show a progress notification, and when each last updated it. */
    private val lastNotify = ConcurrentHashMap<String, Long>()
    /** Pause/cancel signal flags for active multi-connection FILE downloads. */
    private val filePaused = ConcurrentHashMap<String, AtomicBoolean>()

    private class Stopped : Exception()

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        ServiceCompat.startForeground(
            this, NOTIF_PROGRESS, summaryNotification(),
            if (Build.VERSION.SDK_INT >= 29) ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC else 0
        )
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        lastStartId = startId
        val id = intent?.getStringExtra(EXTRA_ID)
        when (intent?.action) {
            ACTION_START -> if (id != null) {
                activeCount.incrementAndGet()
                updateSummary()
                scope.launch {
                    try { withSlot { runJob(id) } }
                    finally {
                        endProgress(id)
                        activeCount.decrementAndGet()
                        main.post { updateSummary(); stopIfIdle() }
                    }
                }
            }
            ACTION_CANCEL -> id?.let { stopJob(it, JobStatus.CANCELLED) }
            ACTION_PAUSE -> id?.let { stopJob(it, JobStatus.PAUSED) }
        }
        // A pause/cancel can arrive after the last job ended; don't leave the notification behind.
        stopIfIdle()
        return START_NOT_STICKY
    }

    /** Main thread only, like onStartCommand, so a start that just arrived is always counted. */
    private fun stopIfIdle() {
        if (activeCount.get() == 0 && stopSelfResult(lastStartId)) {
            ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
        }
    }

    /** Waits for a free slot. Reads the setting each time, so a change applies to the next downloads. */
    private suspend fun <T> withSlot(block: suspend () -> T): T {
        while (true) {
            val now = running.value
            if (now < Settings.maxParallel.value.coerceIn(1, 5)) {
                if (running.compareAndSet(now, now + 1)) break else continue
            }
            combine(running, Settings.maxParallel) { r, m -> r < m.coerceIn(1, 5) }.first { it }
        }
        try { return block() } finally { running.update { it - 1 } }
    }

    /** Pause keeps the partial file; cancel deletes it. */
    private fun stopJob(id: String, to: JobStatus) {
        val job = Downloads.get(id) ?: return
        if (job.finished) return
        Downloads.update(id) { it.copy(status = to, speed = "", eta = "") }
        when (job.kind) {
            JobKind.VIDEO -> YoutubeDL.getInstance().destroyProcessById(id)
            JobKind.CONVERT -> Converter.cancel(id)
            JobKind.FILE -> filePaused[id]?.set(true)
        }
        if (to == JobStatus.CANCELLED) partialDir(id).deleteRecursively()
    }

    private fun partialDir(id: String) = File(filesDir, "partial/$id")

    private fun isMetered() = getSystemService(ConnectivityManager::class.java).isActiveNetworkMetered

    private fun stillWanted(id: String) = Downloads.get(id)?.status.let {
        it != JobStatus.PAUSED && it != JobStatus.CANCELLED && it != null
    }

    private suspend fun runJob(id: String) {
        val job = Downloads.get(id) ?: return
        if (job.status != JobStatus.QUEUED) return // paused or cancelled while waiting
        lastNotify[id] = 0L // from now on this job may show its notification

        Engine.state.first { it != Engine.State.STARTING } // setup / yt-dlp update first

        if (job.kind != JobKind.CONVERT && Settings.wifiOnly.value && isMetered()) {
            Downloads.update(id) { it.copy(status = JobStatus.WAITING_WIFI) }
            notifyProgress(job, "Waiting for Wi-Fi…", 0f, true, force = true)
            while (isMetered()) {
                delay(5000)
                if (Downloads.get(id)?.status != JobStatus.WAITING_WIFI) return
            }
        }

        val dir = partialDir(id).apply { mkdirs() }
        var keepPartial = false
        try {
            Downloads.update(id) { it.copy(status = JobStatus.DOWNLOADING) }
            val file = when (job.kind) {
                JobKind.VIDEO -> downloadVideo(job, dir)
                JobKind.FILE -> downloadFile(job, dir)
                JobKind.CONVERT -> convert(job, dir)
            }
            if (!stillWanted(id)) throw Stopped()
            Downloads.update(id) { it.copy(status = JobStatus.SAVING, percent = 100f) }
            val current = Downloads.get(id) ?: job
            val mime = current.mime?.takeIf { it.contains('/') && it != "application/octet-stream" }
                ?: FileKinds.mimeFromName(file.name) ?: "application/octet-stream"
            val size = file.length()
            val folder = FileKinds.categoryOf(file.name, mime, job.kind, job.audioOnly).folder
            val uri = saveToDownloads(file, mime, if (Settings.sortIntoFolders.value) folder else null)
            Downloads.update(id) {
                it.copy(status = JobStatus.DONE, uri = uri, mime = mime, fileName = file.name, sizeBytes = size,
                    percent = 100f, speed = "", eta = "")
            }
            doneNotification(Downloads.get(id) ?: job, uri, mime)
        } catch (e: Throwable) {
            val now = Downloads.get(id)?.status
            when {
                now == JobStatus.PAUSED -> keepPartial = true
                now == JobStatus.CANCELLED -> {}
                e is Stopped || e is YoutubeDL.CanceledException -> {}
                else -> {
                    val msg = if (e is YoutubeDLException) Engine.cleanError(e.message) else (e.message ?: e.toString())
                    Downloads.update(id) { it.copy(status = JobStatus.ERROR, error = msg, speed = "", eta = "") }
                    // Downloads resume from their partial files with Retry (yt-dlp continues .part files).
                    keepPartial = job.kind != JobKind.CONVERT
                    errorNotification(Downloads.get(id) ?: job)
                }
            }
        } finally {
            if (!keepPartial) dir.deleteRecursively()
        }
    }

    // ---------- videos (yt-dlp) ----------

    private fun downloadVideo(job: Job, dir: File): File {
        val id = job.id
        val req = YoutubeDLRequest(job.url)
            .addOption("-f", job.format.ifBlank { "bv*+ba/b" })
            .addOption("-S", job.sort ?: Engine.H264_SORT)
            .addOption("-o", File(dir, outputTemplate(job)).absolutePath)
            .addOption("--no-playlist")
            .addOption("--no-warnings")
            .addOption("--newline")
            .addOption("--progress")
            .addOption("--no-simulate")
            .addOption("--continue") // resume .part files after a pause
            .addOption("--concurrent-fragments", "4")
            .addOption(
                "--progress-template",
                "download:ASP %(progress.downloaded_bytes)s|%(progress.total_bytes)s|" +
                    "%(progress.total_bytes_estimate)s|%(progress.speed)s|%(progress.eta)s"
            )
            // Title/thumbnail for one-tap downloads that skipped the lookup
            .addOption("--print", "video:VGT %(title)s")
            .addOption("--print", "video:VGI %(thumbnail)s")
            .addOption("--print", "after_move:ASF %(filepath)s")
        val limitKbps = Settings.speedLimitKbps.value
        if (limitKbps > 0) req.addOption("--limit-rate", "${limitKbps}K")
        if (job.audioOnly) req.addOption("-x").addOption("--audio-format", "mp3")
        else req.addOption("--merge-output-format", "mp4")
        Engine.applyCookies(this, req)
        Engine.applyJsRuntime(this, req)

        var finalPath: String? = null
        YoutubeDL.getInstance().execute(req, id) { _, _, line ->
            when {
                line.startsWith("ASP ") -> onVideoProgress(id, line.removePrefix("ASP "))
                line.startsWith("ASF ") -> finalPath = line.removePrefix("ASF ").trim()
                line.startsWith("VGT ") && !job.fixedName -> {
                    val t = line.removePrefix("VGT ").trim()
                    if (t.isNotEmpty() && t != "NA") Downloads.update(id) { it.copy(title = t) }
                }
                line.startsWith("VGI ") -> {
                    val t = line.removePrefix("VGI ").trim()
                    if (t.startsWith("http")) Downloads.update(id) { it.copy(thumbnail = t) }
                }
            }
        }
        return finalPath?.let(::File)?.takeIf { it.exists() }
            ?: dir.walkTopDown().filter { it.isFile && !it.name.endsWith(".part") }.maxByOrNull { it.length() }
            ?: throw Exception("Download finished but no file was found")
    }

    /** File name pattern: yt-dlp's title + ID, or our own name for ad videos. */
    private fun outputTemplate(job: Job): String {
        if (!job.fixedName) return "%(title).100B [%(id).40B]%(height& {}p|)s.%(ext)s"
        return "${safeName(job.title).replace("%", "%%")}.%(ext)s"
    }

    private fun onVideoProgress(id: String, raw: String) {
        val p = raw.split("|") + List(5) { "" }
        fun n(s: String) = s.trim().toDoubleOrNull() ?: 0.0
        val done = n(p[0])
        val total = n(p[1]).takeIf { it > 0 } ?: n(p[2])
        val pct = if (done > 0 && total > 0) (done / total * 100).toFloat().coerceIn(0f, 100f) else 0f
        Downloads.update(id) { it.copy(percent = pct, speed = humanSpeed(n(p[3])), eta = humanEta(p[4])) }
        Downloads.get(id)?.let { notifyProgress(it, null, pct, total <= 0) }
    }

    // ---------- any file (multi-connection HTTP, resumable) ----------

    private suspend fun downloadFile(job: Job, dir: File): File {
        val id = job.id
        val currentJob = Downloads.get(id) ?: job
        val paused = AtomicBoolean(false)
        filePaused[id] = paused
        try {
            val res = FastFileEngine.download(
                url = currentJob.url,
                workDir = dir,
                preferredFilename = currentJob.fileName,
                userAgent = FileKinds.USER_AGENT,
                paused = paused,
                onMeta = { rawName, totalBytes ->
                    val name = safeName(currentJob.fileName ?: rawName).let { n ->
                        if (FileKinds.extOf(n).isNotEmpty()) n
                        else android.webkit.MimeTypeMap.getSingleton()
                            .getExtensionFromMimeType(currentJob.mime?.substringBefore(';')?.trim())?.let { "$n.$it" } ?: n
                    }
                    val mime = currentJob.mime ?: FileKinds.mimeFromName(name)
                    Downloads.update(id) {
                        it.copy(
                            fileName = name,
                            title = if (it.title.isBlank() || it.title == it.url) name else it.title,
                            mime = mime ?: it.mime,
                            totalBytes = if (totalBytes > 0) totalBytes else it.totalBytes,
                        )
                    }
                },
                onProgress = { p ->
                    if (!stillWanted(id)) {
                        paused.set(true)
                        return@download
                    }
                    val eta = if (p.etaSeconds >= 0) humanEta(p.etaSeconds.toString()) else "?"
                    val speed = humanSpeed(p.speedBps.toDouble())
                    Downloads.update(id) {
                        it.copy(
                            percent = p.percent,
                            doneBytes = p.doneBytes,
                            totalBytes = if (p.totalBytes > 0) p.totalBytes else it.totalBytes,
                            speed = speed,
                            eta = eta,
                        )
                    }
                    Downloads.get(id)?.let { notifyProgress(it, null, p.percent, p.totalBytes <= 0) }
                },
            )
            if (!stillWanted(id)) throw Stopped()
            Downloads.update(id) { it.copy(doneBytes = res.totalBytes, totalBytes = res.totalBytes) }
            val desiredName = Downloads.get(id)?.fileName ?: res.filename
            if (desiredName != res.file.name) {
                val target = File(dir, safeName(desiredName))
                if (res.file.renameTo(target)) return target
            }
            return res.file
        } catch (e: CancellationException) {
            throw Stopped()
        } finally {
            filePaused.remove(id)
        }
    }

    // ---------- conversion (FFmpeg) ----------

    private fun convert(job: Job, dir: File): File {
        val spec = ConvertSpec.fromJson(job.spec)
        val src = Uri.parse(job.url)
        val inName = displayName(src) ?: job.title
        val input = File(dir, "input.${FileKinds.extOf(inName).ifBlank { "mp4" }}")
        Downloads.update(job.id) { it.copy(eta = "Preparing…") }
        contentResolver.openInputStream(src)?.use { inp -> input.outputStream().use { inp.copyTo(it) } }
            ?: throw Exception("Couldn't open the file")
        val base = safeName(inName.substringBeforeLast('.'))
        val suffix = listOfNotNull(spec.height?.let { "${it}p" }, spec.targetMb?.let { "${it}MB" },
            if (spec.startSec != null || spec.endSec != null) "trim" else null).joinToString(" ")
        val output = File(dir, "$base${if (suffix.isNotEmpty()) " ($suffix)" else ""}.${spec.format}")
        val started = System.currentTimeMillis()
        Converter.convert(this, job.id, input, output, spec) { pct ->
            val secs = (System.currentTimeMillis() - started) / 1000.0
            val eta = if (pct > 2) humanEta((secs / pct * (100 - pct)).toString()) else "?"
            Downloads.update(job.id) { it.copy(percent = pct, eta = eta, speed = "") }
            Downloads.get(job.id)?.let { notifyProgress(it, "Converting · ${pct.toInt()}%", pct, false) }
        }
        input.delete()
        return output
    }

    private fun displayName(uri: Uri): String? = runCatching {
        contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use {
            if (it.moveToFirst()) it.getString(0) else null
        }
    }.getOrNull()

    // ---------- saving ----------

    private fun safeName(s: String) =
        s.replace(Regex("""[\\/:*?"<>|\n\r\t]+"""), " ").replace(Regex("\\s+"), " ").trim().take(120).ifBlank { "download" }

    /** Copies the finished file to Downloads/AnySave[/folder] where gallery and file apps can see it. */
    private suspend fun saveToDownloads(file: File, mime: String, folder: String?): Uri {
        val relDir = Environment.DIRECTORY_DOWNLOADS + "/AnySave" + (folder?.let { "/$it" } ?: "")
        if (Build.VERSION.SDK_INT >= 29) {
            val resolver = contentResolver
            var values = ContentValues()
            var uri: Uri? = null
            // Android 10 refuses a name that is already in the media database (even if the
            // file was deleted, or belongs to an earlier install), so try "name (1)" etc.
            for (n in 0..20) {
                val name = if (n == 0) file.name else "${file.nameWithoutExtension} ($n).${file.extension}"
                values = ContentValues().apply {
                    put(MediaStore.Downloads.DISPLAY_NAME, name)
                    put(MediaStore.Downloads.MIME_TYPE, mime)
                    put(MediaStore.Downloads.RELATIVE_PATH, relDir)
                    put(MediaStore.Downloads.IS_PENDING, 1)
                }
                uri = runCatching { resolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values) }.getOrNull()
                if (uri != null) break
            }
            if (uri == null) throw Exception("Couldn't create the file in Downloads")
            try {
                resolver.openOutputStream(uri)!!.use { out -> file.inputStream().use { it.copyTo(out) } }
                values.clear()
                values.put(MediaStore.Downloads.IS_PENDING, 0)
                resolver.update(uri, values, null, null)
            } catch (e: Throwable) {
                // Don't leave a hidden, half-written entry in Downloads.
                runCatching { resolver.delete(uri, null, null) }
                throw e
            }
            return uri
        }
        // Android 8/9: write to the public folder, then ask the media scanner for a shareable Uri.
        @Suppress("DEPRECATION")
        val dir = File(Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS),
            "AnySave" + (folder?.let { "/$it" } ?: ""))
        dir.mkdirs()
        var dest = File(dir, file.name)
        var n = 1
        while (dest.exists()) dest = File(dir, "${file.nameWithoutExtension} (${n++}).${file.extension}")
        file.copyTo(dest)
        return suspendCancellableCoroutine { cont ->
            MediaScannerConnection.scanFile(this, arrayOf(dest.absolutePath), arrayOf(mime)) { _, uri ->
                cont.resume(uri ?: Uri.fromFile(dest))
            }
        }
    }

    // ---------- notifications ----------

    private fun progressId(id: String) = ("progress:$id").hashCode()

    /** Each job has its own notification, throttled on its own. */
    private fun notifyProgress(job: Job, text: String?, pct: Float, indeterminate: Boolean, force: Boolean = false) {
        val last = lastNotify[job.id] ?: return // job already ended
        val now = System.currentTimeMillis()
        if (!force && now - last < 700) return
        lastNotify[job.id] = now
        runCatching {
            getSystemService(NotificationManager::class.java)
                .notify(progressId(job.id), progressNotification(job, text, pct, indeterminate))
        }
    }

    private fun endProgress(id: String) {
        lastNotify.remove(id)
        runCatching { getSystemService(NotificationManager::class.java).cancel(progressId(id)) }
    }

    private fun openAppIntent() = PendingIntent.getActivity(
        this, 0,
        Intent(this, MainActivity::class.java)
            .putExtra(MainActivity.EXTRA_TAB, MainActivity.TAB_DOWNLOADS)
            .addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
    )

    /** The service's own notification; job notifications are grouped under it. */
    private fun summaryNotification(): Notification {
        val n = activeCount.get()
        return NotificationCompat.Builder(this, AnySaveApp.CH_PROGRESS)
            .setSmallIcon(R.drawable.ic_stat_download)
            .setContentTitle("AnySave")
            .setContentText(if (n > 0) "$n active" else "Starting…")
            .setOnlyAlertOnce(true)
            .setOngoing(true)
            .setGroup(GROUP)
            .setGroupSummary(true)
            .setContentIntent(openAppIntent())
            .build()
    }

    private fun updateSummary() {
        runCatching { getSystemService(NotificationManager::class.java).notify(NOTIF_PROGRESS, summaryNotification()) }
    }

    private fun progressNotification(job: Job, text: String?, pct: Float, indeterminate: Boolean): Notification {
        val b = NotificationCompat.Builder(this, AnySaveApp.CH_PROGRESS)
            .setSmallIcon(R.drawable.ic_stat_download)
            .setContentTitle(job.title)
            .setContentText(text ?: listOf("${pct.toInt()}%", job.speed, job.eta.takeIf { it.isNotBlank() }?.let { "ETA $it" }.orEmpty())
                .filter { it.isNotBlank() }.joinToString(" · "))
            .setOnlyAlertOnce(true)
            .setOngoing(true)
            .setGroup(GROUP)
            .setProgress(100, pct.toInt(), indeterminate)
            .setContentIntent(openAppIntent())
        if (job.kind != JobKind.CONVERT) b.addAction(0, "Pause", serviceIntent(ACTION_PAUSE, job.id))
        b.addAction(0, "Cancel", serviceIntent(ACTION_CANCEL, job.id))
        return b.build()
    }

    private fun serviceIntent(action: String, id: String) = PendingIntent.getService(
        this, (action + id).hashCode(),
        Intent(this, DownloadService::class.java).setAction(action).putExtra(EXTRA_ID, id),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
    )

    private fun doneNotification(job: Job, uri: Uri, mime: String) {
        val view = Intent(Intent.ACTION_VIEW).setDataAndType(uri, mime)
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        val pi = PendingIntent.getActivity(this, job.id.hashCode(), view, PendingIntent.FLAG_IMMUTABLE)
        val n = NotificationCompat.Builder(this, AnySaveApp.CH_DONE)
            .setSmallIcon(R.drawable.ic_stat_download)
            .setContentTitle(if (job.kind == JobKind.CONVERT) "Conversion complete" else "Download complete")
            .setContentText(job.fileName ?: job.title)
            .setAutoCancel(true)
            .setContentIntent(pi)
            .build()
        runCatching { getSystemService(NotificationManager::class.java).notify(job.id.hashCode(), n) }
    }

    private fun errorNotification(job: Job) {
        val n = NotificationCompat.Builder(this, AnySaveApp.CH_DONE)
            .setSmallIcon(R.drawable.ic_stat_download)
            .setContentTitle(if (job.kind == JobKind.CONVERT) "Conversion failed" else "Download failed")
            .setContentText(job.title)
            .setAutoCancel(true)
            .setContentIntent(openAppIntent())
            .build()
        runCatching { getSystemService(NotificationManager::class.java).notify(job.id.hashCode(), n) }
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }

    companion object {
        private const val NOTIF_PROGRESS = 1
        private const val GROUP = "active"
        private const val ACTION_START = "start"
        private const val ACTION_CANCEL = "cancel"
        private const val ACTION_PAUSE = "pause"
        private const val EXTRA_ID = "id"

        private val seq = AtomicInteger(0)

        /** Unique even when many jobs start in the same millisecond (a playlist). */
        fun newId() = System.currentTimeMillis().toString() + (seq.incrementAndGet() % 10000).toString().padStart(4, '0')

        fun start(ctx: Context, job: Job) {
            if (!Settings.acceptedTerms) return
            Downloads.add(job)
            enqueue(ctx, job.id)
        }

        /** Several jobs at once; each waits in the queue (Settings.maxParallel run together). */
        fun startMany(ctx: Context, jobs: List<Job>) {
            if (!Settings.acceptedTerms || jobs.isEmpty()) return
            Downloads.addAll(jobs)
            jobs.forEach { enqueue(ctx, it.id) }
        }

        /** A video from a playlist or channel list, as a job ready for [start] or [startMany]. */
        fun entryJob(entry: Engine.Entry, opt: Engine.Option) = Job(
            id = newId(), url = entry.url, title = entry.title,
            format = opt.format, sort = opt.sort, audioOnly = opt.audioOnly,
            qualityLabel = opt.label, site = Engine.siteOf(entry.url) ?: Engine.siteLabel(entry.url),
            thumbnail = entry.thumbnail,
        )

        /**
         * One of several links pasted or shared at once, like a single quick download:
         * files directly, the rest through yt-dlp. Ad library pages are skipped (they need the ad browser).
         */
        fun startLink(ctx: Context, url: String): Boolean {
            val lib = AdLibraryActivity.libraryOf(url)
            if (lib != null && !(lib == AdLibraryActivity.Library.META && url.contains("id="))) return false
            return if (FileKinds.looksLikeFile(url) && Engine.siteOf(url) == null) startFile(ctx, url)
            else startQuick(ctx, url)
        }

        private fun enqueue(ctx: Context, id: String) {
            ctx.startForegroundService(Intent(ctx, DownloadService::class.java).setAction(ACTION_START).putExtra(EXTRA_ID, id))
        }

        /** Download a video page immediately with the default quality from Settings. */
        fun startQuick(ctx: Context, url: String, quality: Quality = Settings.quality.value): Boolean {
            if (Downloads.isActive(url)) {
                Toast.makeText(ctx, "Already downloading this link", Toast.LENGTH_SHORT).show()
                return false
            }
            val opt = quality.toOption()
            start(ctx, Job(
                id = newId(), url = url, title = "${Engine.siteLabel(url)} video",
                format = opt.format, sort = opt.sort, audioOnly = opt.audioOnly,
                qualityLabel = quality.label, site = Engine.siteOf(url) ?: Engine.siteLabel(url),
            ))
            return true
        }

        /** Returns the new job's id, so the screen can show its progress. */
        fun startFromInfo(ctx: Context, info: Engine.Info, opt: Engine.Option): String {
            val id = newId()
            start(ctx, Job(
                id = id, url = info.url, title = info.title,
                format = opt.format, sort = opt.sort, audioOnly = opt.audioOnly,
                qualityLabel = opt.label, site = Engine.siteOf(info.url) ?: Engine.siteLabel(info.url), thumbnail = info.thumbnail,
            ))
            return id
        }

        /** Any direct file link (PDF, ZIP, APK, image, video file...). */
        fun startFile(ctx: Context, url: String, name: String? = null, mime: String? = null): Boolean {
            if (Downloads.isActive(url)) {
                Toast.makeText(ctx, "Already downloading this link", Toast.LENGTH_SHORT).show()
                return false
            }
            start(ctx, Job(
                id = newId(), url = url, title = name ?: url.substringAfterLast('/').substringBefore('?').ifBlank { url },
                kind = JobKind.FILE, fileName = name, mime = mime, site = Engine.siteLabel(url),
                qualityLabel = "File",
            ))
            return true
        }

        fun startConvert(ctx: Context, input: Uri, name: String, spec: ConvertSpec) {
            start(ctx, Job(
                id = newId(), url = input.toString(), title = name, kind = JobKind.CONVERT,
                spec = spec.toJson(), qualityLabel = spec.label(), audioOnly = spec.isAudio,
            ))
        }

        fun pause(ctx: Context, id: String) = send(ctx, ACTION_PAUSE, id)

        fun resume(ctx: Context, id: String) {
            val job = Downloads.get(id) ?: return
            if (job.status != JobStatus.PAUSED && job.status != JobStatus.ERROR) return
            Downloads.update(id) { it.copy(status = JobStatus.QUEUED, error = null) }
            enqueue(ctx, id)
        }

        /** Updates the URL of a paused or failed download and resumes from its saved partial segments. */
        fun refreshUrl(ctx: Context, id: String, newUrl: String) {
            val clean = newUrl.trim()
            if (!clean.startsWith("http://") && !clean.startsWith("https://")) {
                Toast.makeText(ctx, "Paste a valid http:// or https:// link", Toast.LENGTH_SHORT).show()
                return
            }
            val job = Downloads.get(id) ?: return
            if (job.status != JobStatus.PAUSED && job.status != JobStatus.ERROR) return
            Downloads.update(id) { it.copy(url = clean, status = JobStatus.QUEUED, error = null) }
            enqueue(ctx, id)
        }

        /** Retry: downloads resume from their partial files; conversions start over. */
        fun retry(ctx: Context, id: String) {
            val old = Downloads.get(id) ?: return
            if (old.kind != JobKind.CONVERT) { resume(ctx, id); return }
            Downloads.remove(id)
            start(ctx, old.copy(id = newId(), status = JobStatus.QUEUED, percent = 0f, error = null,
                speed = "", eta = "", createdAt = System.currentTimeMillis()))
        }

        fun cancel(ctx: Context, id: String) {
            val job = Downloads.get(id) ?: return
            if (job.finished) return
            if (job.status == JobStatus.PAUSED) {
                Downloads.update(id) { it.copy(status = JobStatus.CANCELLED) }
                File(ctx.filesDir, "partial/$id").deleteRecursively()
                return
            }
            send(ctx, ACTION_CANCEL, id)
        }

        private fun send(ctx: Context, action: String, id: String) {
            ctx.startService(Intent(ctx, DownloadService::class.java).setAction(action).putExtra(EXTRA_ID, id))
        }

        fun humanSpeed(bps: Double): String {
            if (bps <= 0) return ""
            var v = bps
            for (u in listOf("B/s", "KB/s", "MB/s", "GB/s")) {
                if (v < 1024) return "%.1f %s".format(v, u)
                v /= 1024
            }
            return "%.1f TB/s".format(v)
        }

        fun humanEta(s: String): String {
            val sec = s.trim().toDoubleOrNull()?.toInt() ?: return "?"
            return if (sec < 3600) "%d:%02d".format(sec / 60, sec % 60)
            else "%d:%02d:%02d".format(sec / 3600, sec % 3600 / 60, sec % 60)
        }
    }
}
