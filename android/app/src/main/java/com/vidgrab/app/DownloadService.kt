package com.vidgrab.app

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
import android.os.IBinder
import android.provider.MediaStore
import android.webkit.MimeTypeMap
import android.widget.Toast
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import com.yausername.youtubedl_android.YoutubeDL
import com.yausername.youtubedl_android.YoutubeDLException
import com.yausername.youtubedl_android.YoutubeDLRequest
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import java.io.File
import kotlin.coroutines.resume

/** Runs downloads one after another in the foreground, so they continue when the app is closed. */
class DownloadService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val queue = Channel<String>(Channel.UNLIMITED)
    @Volatile private var running: String? = null
    private var pending = 0
    @Volatile private var lastStartId = 0
    private var lastNotify = 0L

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        ServiceCompat.startForeground(
            this, NOTIF_PROGRESS, progressNotification(null, "Starting…", 0f, true),
            if (Build.VERSION.SDK_INT >= 29) ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC else 0
        )
        scope.launch {
            for (id in queue) {
                runJob(id)
                val left = synchronized(this@DownloadService) { --pending }
                if (left == 0 && stopSelfResult(lastStartId)) {
                    // Only stops if no newer download was queued in the meantime.
                    ServiceCompat.stopForeground(this@DownloadService, ServiceCompat.STOP_FOREGROUND_REMOVE)
                }
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val id = intent?.getStringExtra(EXTRA_ID) ?: return START_NOT_STICKY
        when (intent.action) {
            ACTION_START -> {
                synchronized(this) { pending++; lastStartId = startId }
                queue.trySend(id)
            }
            ACTION_CANCEL -> cancelJob(id)
        }
        return START_NOT_STICKY
    }

    private fun cancelJob(id: String) {
        Downloads.update(id) { if (it.finished) it else it.copy(status = JobStatus.CANCELLED) }
        if (running == id) YoutubeDL.getInstance().destroyProcessById(id)
    }

    private fun isMetered() =
        getSystemService(ConnectivityManager::class.java).isActiveNetworkMetered

    private suspend fun runJob(id: String) {
        var job = Downloads.get(id) ?: return
        if (job.status != JobStatus.QUEUED) return // cancelled while waiting

        // Wait for setup / yt-dlp update to finish before running it.
        Engine.state.first { it != Engine.State.STARTING }

        if (Settings.wifiOnly.value && isMetered()) {
            Downloads.update(id) { it.copy(status = JobStatus.WAITING_WIFI) }
            notifyProgress(job, "Waiting for Wi-Fi…", 0f, true, force = true)
            while (isMetered()) {
                delay(5000)
                if (Downloads.get(id)?.status != JobStatus.WAITING_WIFI) return // cancelled
            }
        }

        running = id
        val workDir = File(cacheDir, "dl/$id").apply { deleteRecursively(); mkdirs() }
        try {
            Downloads.update(id) { it.copy(status = JobStatus.DOWNLOADING) }
            val req = YoutubeDLRequest(job.url)
                .addOption("-f", job.format)
                .addOption("-S", job.sort ?: Engine.H264_SORT)
                .addOption("-o", File(workDir, outputTemplate(job)).absolutePath)
                .addOption("--no-playlist")
                .addOption("--no-warnings")
                .addOption("--newline")
                .addOption("--progress")
                .addOption("--no-simulate")
                .addOption(
                    "--progress-template",
                    "download:VGP %(progress.downloaded_bytes)s|%(progress.total_bytes)s|" +
                        "%(progress.total_bytes_estimate)s|%(progress.speed)s|%(progress.eta)s"
                )
                // Title/thumbnail for one-tap downloads that skipped the lookup
                .addOption("--print", "video:VGT %(title)s")
                .addOption("--print", "video:VGI %(thumbnail)s")
                .addOption("--print", "after_move:VGF %(filepath)s")
            if (job.audioOnly) req.addOption("-x").addOption("--audio-format", "mp3")
            else req.addOption("--merge-output-format", "mp4")
            Engine.applyCookies(this, req)

            var finalPath: String? = null
            YoutubeDL.getInstance().execute(req, id) { _, _, line ->
                when {
                    line.startsWith("VGP ") -> onProgress(id, line.removePrefix("VGP "))
                    line.startsWith("VGF ") -> finalPath = line.removePrefix("VGF ").trim()
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
            val file = finalPath?.let(::File)?.takeIf { it.exists() }
                ?: workDir.walkTopDown().filter { it.isFile }.maxByOrNull { it.length() }
                ?: throw Exception("Download finished but no file was found")

            Downloads.update(id) { it.copy(status = JobStatus.SAVING, percent = 100f) }
            val mime = mimeOf(file)
            val size = file.length()
            val uri = saveToDownloads(file, mime)
            Downloads.update(id) {
                it.copy(status = JobStatus.DONE, uri = uri, mime = mime, fileName = file.name, sizeBytes = size, percent = 100f)
            }
            job = Downloads.get(id) ?: job
            doneNotification(job, uri, mime)
        } catch (e: YoutubeDL.CanceledException) {
            Downloads.update(id) { it.copy(status = JobStatus.CANCELLED) }
        } catch (e: Throwable) {
            if (Downloads.get(id)?.status != JobStatus.CANCELLED) {
                val msg = if (e is YoutubeDLException) Engine.cleanError(e.message) else (e.message ?: e.toString())
                Downloads.update(id) { it.copy(status = JobStatus.ERROR, error = msg) }
            }
        } finally {
            running = null
            workDir.deleteRecursively()
        }
    }

    /** File name pattern: yt-dlp's title + ID, or our own name for ad videos. */
    private fun outputTemplate(job: Job): String {
        if (!job.fixedName) return "%(title).100B [%(id).40B]%(height& {}p|)s.%(ext)s"
        val safe = job.title.replace(Regex("""[\\/:*?"<>|\n\r\t]+"""), " ").replace("%", "%%").trim().take(90)
        return "${safe.ifBlank { "video" }}.%(ext)s"
    }

    private fun onProgress(id: String, raw: String) {
        val p = raw.split("|") + List(5) { "" }
        fun n(s: String) = s.trim().toDoubleOrNull() ?: 0.0
        val done = n(p[0])
        val total = n(p[1]).takeIf { it > 0 } ?: n(p[2])
        val pct = if (done > 0 && total > 0) (done / total * 100).toFloat().coerceIn(0f, 100f) else 0f
        Downloads.update(id) { it.copy(percent = pct, speed = humanSpeed(n(p[3])), eta = humanEta(p[4])) }
        Downloads.get(id)?.let { notifyProgress(it, null, pct, total <= 0) }
    }

    private fun notifyProgress(job: Job, text: String?, pct: Float, indeterminate: Boolean, force: Boolean = false) {
        val now = System.currentTimeMillis()
        if (!force && now - lastNotify < 700) return
        lastNotify = now
        runCatching {
            getSystemService(NotificationManager::class.java)
                .notify(NOTIF_PROGRESS, progressNotification(job, text, pct, indeterminate))
        }
    }

    // ---------- saving ----------

    private fun mimeOf(f: File): String =
        MimeTypeMap.getSingleton().getMimeTypeFromExtension(f.extension.lowercase())
            ?: if (f.extension.equals("mp3", true)) "audio/mpeg" else "video/mp4"

    /** Copies the finished file to Downloads/VidGrab where gallery and file apps can see it. */
    private suspend fun saveToDownloads(file: File, mime: String): Uri {
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
                    put(MediaStore.Downloads.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS + "/VidGrab")
                    put(MediaStore.Downloads.IS_PENDING, 1)
                }
                uri = runCatching { resolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values) }.getOrNull()
                if (uri != null) break
            }
            if (uri == null) throw Exception("Couldn't create the file in Downloads")
            resolver.openOutputStream(uri)!!.use { out -> file.inputStream().use { it.copyTo(out) } }
            values.clear()
            values.put(MediaStore.Downloads.IS_PENDING, 0)
            resolver.update(uri, values, null, null)
            return uri
        }
        // Android 8/9: write to the public folder, then ask the media scanner for a shareable Uri.
        val dir = File(Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS), "VidGrab")
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

    private fun openAppIntent() = PendingIntent.getActivity(
        this, 0,
        Intent(this, MainActivity::class.java)
            .putExtra(MainActivity.EXTRA_TAB, MainActivity.TAB_DOWNLOADS)
            .addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
    )

    private fun progressNotification(job: Job?, text: String?, pct: Float, indeterminate: Boolean): Notification {
        val b = NotificationCompat.Builder(this, VidGrabApp.CH_PROGRESS)
            .setSmallIcon(R.drawable.ic_stat_download)
            .setContentTitle(job?.title ?: "VidGrab")
            .setContentText(text ?: listOf("${pct.toInt()}%", job?.speed.orEmpty(), job?.eta?.let { "ETA $it" }.orEmpty())
                .filter { it.isNotBlank() }.joinToString(" · "))
            .setOnlyAlertOnce(true)
            .setOngoing(true)
            .setProgress(100, pct.toInt(), indeterminate)
            .setContentIntent(openAppIntent())
        if (job != null) {
            val cancel = PendingIntent.getService(
                this, job.id.hashCode(),
                Intent(this, DownloadService::class.java).setAction(ACTION_CANCEL).putExtra(EXTRA_ID, job.id),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
            )
            b.addAction(0, "Cancel", cancel)
        }
        return b.build()
    }

    private fun doneNotification(job: Job, uri: Uri, mime: String) {
        val view = Intent(Intent.ACTION_VIEW).setDataAndType(uri, mime)
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        val pi = PendingIntent.getActivity(this, job.id.hashCode(), view, PendingIntent.FLAG_IMMUTABLE)
        val n = NotificationCompat.Builder(this, VidGrabApp.CH_DONE)
            .setSmallIcon(R.drawable.ic_stat_download)
            .setContentTitle("Download complete")
            .setContentText(job.title)
            .setAutoCancel(true)
            .setContentIntent(pi)
            .build()
        runCatching { getSystemService(NotificationManager::class.java).notify(job.id.hashCode(), n) }
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }

    companion object {
        private const val NOTIF_PROGRESS = 1
        private const val ACTION_START = "start"
        private const val ACTION_CANCEL = "cancel"
        private const val EXTRA_ID = "id"

        private fun newId() = System.currentTimeMillis().toString() + (0..999).random()

        fun start(ctx: Context, job: Job) {
            if (!Settings.acceptedTerms) return
            Downloads.add(job)
            enqueue(ctx, job.id)
        }

        private fun enqueue(ctx: Context, id: String) {
            val i = Intent(ctx, DownloadService::class.java).setAction(ACTION_START).putExtra(EXTRA_ID, id)
            ctx.startForegroundService(i)
        }

        /** Download a link immediately with the default quality from Settings. Returns false if already running. */
        fun startQuick(ctx: Context, url: String, quality: Quality = Settings.quality.value): Boolean {
            if (Downloads.isActive(url)) {
                Toast.makeText(ctx, "Already downloading this link", Toast.LENGTH_SHORT).show()
                return false
            }
            val opt = quality.toOption()
            start(
                ctx,
                Job(
                    id = newId(), url = url,
                    title = "${Engine.siteOf(url) ?: "Video"} video",
                    format = opt.format, sort = opt.sort, audioOnly = opt.audioOnly,
                    qualityLabel = quality.label, site = Engine.siteOf(url),
                )
            )
            return true
        }

        fun startFromInfo(ctx: Context, info: Engine.Info, opt: Engine.Option) {
            start(
                ctx,
                Job(
                    id = newId(), url = info.url, title = info.title,
                    format = opt.format, sort = opt.sort, audioOnly = opt.audioOnly,
                    qualityLabel = opt.label, site = Engine.siteOf(info.url), thumbnail = info.thumbnail,
                )
            )
        }

        fun retry(ctx: Context, id: String) {
            val old = Downloads.get(id) ?: return
            Downloads.remove(id)
            start(ctx, old.copy(id = newId(), status = JobStatus.QUEUED, percent = 0f, error = null,
                speed = "", eta = "", createdAt = System.currentTimeMillis()))
        }

        fun cancel(ctx: Context, id: String) {
            val job = Downloads.get(id) ?: return
            if (job.finished) return
            ctx.startService(Intent(ctx, DownloadService::class.java).setAction(ACTION_CANCEL).putExtra(EXTRA_ID, id))
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
