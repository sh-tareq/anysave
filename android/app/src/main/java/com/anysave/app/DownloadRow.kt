package com.anysave.app

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.text.format.DateUtils
import android.widget.Toast
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

// ---------- small helpers shared by the lists ----------

/** "4.2 MB/s" / "512.0 KB/s" -> bytes per second (0 when unknown). */
fun bytesPerSec(speed: String): Double {
    val m = Regex("""([\d.]+)\s*([KMGT]?)i?B/s""", RegexOption.IGNORE_CASE).find(speed) ?: return 0.0
    val n = m.groupValues[1].toDoubleOrNull() ?: return 0.0
    val mult = when (m.groupValues[2].uppercase()) { "K" -> 1024.0; "M" -> 1024.0 * 1024; "G" -> 1024.0 * 1024 * 1024; "T" -> 1024.0 * 1024 * 1024 * 1024; else -> 1.0 }
    return n * mult
}

/** Total speed in the same words everywhere: "12.8 MB/s", "640 KB/s". */
fun fmtSpeed(bps: Double): String = when {
    bps <= 0 -> ""
    bps >= 1024 * 1024 -> "%.1f MB/s".format(bps / 1024 / 1024)
    else -> "%d KB/s".format((bps / 1024).toInt().coerceAtLeast(1))
}

/** "1:12" / "1:02:05" -> "1 min 12 s" / "1 h 2 min"; anything else as it is. */
fun fmtEta(eta: String): String {
    if (eta.endsWith("…")) return "" // a message ("Getting the video ready…"), already shown as the status
    val parts = eta.trim().split(':').map { it.toIntOrNull() ?: return eta.takeIf { e -> e != "?" }.orEmpty() }
    val sec = when (parts.size) { 2 -> parts[0] * 60 + parts[1]; 3 -> parts[0] * 3600 + parts[1] * 60 + parts[2]; else -> return "" }
    val h = sec / 3600; val m = sec % 3600 / 60; val s = sec % 60
    return when { h > 0 -> "$h h${if (m > 0) " $m min" else ""}"; m > 0 -> "$m min${if (s > 0 && m < 10) " $s s" else ""}"; else -> "$s s" }
}

private val WAITING = setOf(JobStatus.QUEUED, JobStatus.WAITING_WIFI, JobStatus.SCHEDULED)
val Job.isWaiting get() = status in WAITING
val Job.isRunning get() = status == JobStatus.DOWNLOADING || status == JobStatus.SAVING
fun Job.size(): Long = sizeBytes.takeIf { it > 0 } ?: totalBytes

/** The same status words as the desktop manager. */
fun statusWords(job: Job): String = when (job.status) {
    JobStatus.QUEUED -> "Queued · starts when a download finishes"
    JobStatus.WAITING_WIFI -> "Waiting for Wi-Fi"
    JobStatus.SCHEDULED -> "Scheduled · starts at ${Settings.scheduleStart.value}"
    JobStatus.DOWNLOADING -> when {
        job.kind == JobKind.CONVERT && job.percent <= 0f -> job.eta.takeIf { it.endsWith("…") } ?: "Converting…"
        job.kind == JobKind.CONVERT -> "Converting · ${job.percent.toInt()}%"
        job.percent <= 0f && job.speed.isEmpty() -> job.eta.takeIf { it.endsWith("…") } ?: "Starting…"
        job.totalBytes > 0 && job.kind == JobKind.FILE ->
            "${job.percent.toInt()}% · ${Engine.humanSize(job.doneBytes) ?: "0 MB"} of ${Engine.humanSize(job.totalBytes)}"
        else -> "${job.percent.toInt()}%"
    }
    JobStatus.PAUSED -> "Paused · ${job.percent.toInt()}%"
    JobStatus.SAVING -> "Saving…"
    JobStatus.DONE -> "Done"
    JobStatus.CANCELLED -> "Cancelled"
    JobStatus.ERROR -> job.error ?: "Failed"
}

// ---------- pieces ----------

/** Coloured square with the file's extension, like the desktop's (MP4 purple, PDF blue…). */
@Composable
fun FileBadge(job: Job, size: Dp = 44.dp) {
    val ext = FileKinds.extOf(job.fileName ?: job.title).uppercase().take(4).ifEmpty {
        when (job.category) {
            FileKinds.Category.VIDEOS -> "MP4"; FileKinds.Category.MUSIC -> if (job.audioOnly) "MP3" else "AUD"
            else -> "FILE"
        }
    }
    Box(
        Modifier.size(size).clip(RoundedCornerShape(10.dp)).background(AnySaveColors.category(job.category)),
        contentAlignment = Alignment.Center,
    ) {
        Text(ext, color = Color.White, fontWeight = FontWeight.Bold, fontSize = if (ext.length > 3) 10.sp else 11.sp, maxLines = 1)
    }
}

/** Thumbnail for videos and pictures that have one, else the file badge; a tick when done. */
@Composable
fun RowIcon(job: Job, selecting: Boolean, selected: Boolean) {
    val visual = job.thumbnail != null || (job.status == JobStatus.DONE && job.uri != null &&
        (job.category == FileKinds.Category.VIDEOS || job.category == FileKinds.Category.PICTURES))
    Box {
        if (visual) Thumb(job, Modifier.size(width = 72.dp, height = 44.dp)) else FileBadge(job)
        val st = AnySaveColors.status
        when {
            selecting -> Box(
                Modifier.align(Alignment.TopStart).offset((-6).dp, (-6).dp).size(20.dp).clip(CircleShape)
                    .background(if (selected) st.progress else MaterialTheme.colorScheme.surfaceContainer)
                    .border(2.dp, if (selected) st.progress else MaterialTheme.colorScheme.outline, CircleShape),
                contentAlignment = Alignment.Center,
            ) { if (selected) Icon(Icons.Outlined.Check, null, tint = Color.White, modifier = Modifier.size(14.dp)) }
            job.status == JobStatus.DONE -> Box(
                Modifier.align(Alignment.BottomEnd).offset(5.dp, 5.dp).size(18.dp).clip(CircleShape)
                    .background(st.ok).border(2.dp, MaterialTheme.colorScheme.surfaceContainer, CircleShape),
                contentAlignment = Alignment.Center,
            ) { Icon(Icons.Outlined.Check, null, tint = Color.White, modifier = Modifier.size(11.dp)) }
        }
    }
}

/** Thin rounded progress bar: blue while downloading, amber when paused, moving when the size isn't known yet. */
@Composable
fun SlimProgress(job: Job, modifier: Modifier = Modifier) {
    val st = AnySaveColors.status
    val color = if (job.status == JobStatus.PAUSED) st.warn else st.progress
    val track = if (job.status == JobStatus.PAUSED) st.warnSoft else st.progressTrack
    val m = modifier.fillMaxWidth().height(5.dp).clip(RoundedCornerShape(3.dp))
    if (job.status == JobStatus.DOWNLOADING && job.percent <= 0f || job.status == JobStatus.SAVING)
        LinearProgressIndicator(m, color = color, trackColor = track, strokeCap = androidx.compose.ui.graphics.StrokeCap.Round)
    else LinearProgressIndicator({ (job.percent / 100f).coerceIn(0f, 1f) }, m, color = color, trackColor = track,
        strokeCap = androidx.compose.ui.graphics.StrokeCap.Round, gapSize = 0.dp, drawStopIndicator = {})
}

@Composable
private fun Dot(color: Color) = Box(Modifier.padding(end = 6.dp).size(7.dp).clip(CircleShape).background(color))

/** One status line: coloured dot + words, as on the desktop. */
@Composable
fun StatusText(job: Job, maxLines: Int = 1) {
    val st = AnySaveColors.status
    val color = when {
        job.status == JobStatus.DONE -> st.ok
        job.status == JobStatus.ERROR -> st.err
        job.status == JobStatus.PAUSED -> st.warn
        job.isWaiting || job.status == JobStatus.CANCELLED -> MaterialTheme.colorScheme.onSurfaceVariant
        else -> MaterialTheme.colorScheme.onSurfaceVariant
    }
    Row(verticalAlignment = Alignment.CenterVertically) {
        if (!job.isRunning) Dot(color)
        Text(statusWords(job), style = MaterialTheme.typography.bodySmall, color = color,
            fontWeight = if (job.status == JobStatus.DONE || job.status == JobStatus.ERROR || job.status == JobStatus.PAUSED) FontWeight.SemiBold else FontWeight.Normal,
            maxLines = maxLines, overflow = TextOverflow.Ellipsis)
    }
}

private fun whenText(ctx: Context, t: Long): String =
    if (DateUtils.isToday(t)) android.text.format.DateFormat.getTimeFormat(ctx).format(java.util.Date(t))
    else DateUtils.formatDateTime(ctx, t, DateUtils.FORMAT_SHOW_DATE or DateUtils.FORMAT_ABBREV_MONTH)

/**
 * One download, the same on Home and in Downloads. Tap: a finished file opens; anything else opens its
 * details. Long-press: select (when [onLongPress] is given). The small button does the obvious next step.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun DownloadRow(
    job: Job,
    selecting: Boolean = false,
    selected: Boolean = false,
    onToggleSelect: (() -> Unit)? = null,
    onLongPress: (() -> Unit)? = null,
) {
    val ctx = LocalContext.current
    var details by remember { mutableStateOf(false) }
    val st = AnySaveColors.status
    val shape = RoundedCornerShape(14.dp)
    Row(
        Modifier.fillMaxWidth().clip(shape)
            .background(if (selected) st.selected else MaterialTheme.colorScheme.surfaceContainer)
            .then(if (selected) Modifier.border(1.dp, st.selectedLine, shape) else Modifier)
            .combinedClickable(
                onClick = {
                    when {
                        selecting -> onToggleSelect?.invoke()
                        job.status == JobStatus.DONE -> openFile(ctx, job)
                        else -> details = true
                    }
                },
                onLongClick = onLongPress,
            )
            .padding(start = 12.dp, top = 10.dp, bottom = 10.dp, end = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RowIcon(job, selecting, selected)
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(displayName(job), maxLines = 1, overflow = TextOverflow.Ellipsis, fontWeight = FontWeight.SemiBold,
                style = MaterialTheme.typography.bodyLarge)
            val sub = listOfNotNull(
                job.site?.takeIf { job.kind != JobKind.CONVERT },
                job.qualityLabel.takeIf { it.isNotBlank() && job.kind == JobKind.VIDEO && job.status != JobStatus.DONE },
                job.queue.takeIf { it.isNotBlank() && !job.finished }?.let { "$it queue" },
                if (job.status == JobStatus.DONE) Engine.humanSize(job.size()) else null,
                if (job.finished) whenText(ctx, job.createdAt) else null,
            ).joinToString(" · ")
            if (sub.isNotEmpty()) Text(sub, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (job.isRunning || job.status == JobStatus.PAUSED) {
                Spacer(Modifier.height(7.dp))
                SlimProgress(job)
                Spacer(Modifier.height(5.dp))
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Box(Modifier.weight(1f)) { StatusText(job) }
                    val right = listOf(fmtSpeed(bytesPerSec(job.speed)), fmtEta(job.eta)).filter { it.isNotBlank() }
                    if (job.isRunning && right.isNotEmpty()) Text(right.joinToString(" · "), style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
                }
            } else if (job.status != JobStatus.DONE) {
                Spacer(Modifier.height(3.dp))
                StatusText(job)
            }
        }
        if (!selecting) {
            val (icon, label, action) = when {
                job.status == JobStatus.SCHEDULED -> Triple(Icons.Outlined.PlayArrow, "Start now") { DownloadService.startNow(ctx, job.id) }
                job.status == JobStatus.PAUSED -> Triple(Icons.Outlined.PlayArrow, "Resume") { DownloadService.resume(ctx, job.id) }
                job.canPause -> Triple(Icons.Outlined.Pause, "Pause") { DownloadService.pause(ctx, job.id) }
                job.status == JobStatus.ERROR || job.status == JobStatus.CANCELLED ->
                    Triple(Icons.Outlined.Refresh, "Retry") { DownloadService.retry(ctx, job.id) }
                else -> Triple(Icons.Outlined.MoreVert, "Details") { details = true }
            }
            IconButton(onClick = action) { Icon(icon, label, tint = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
    }
    if (details) DownloadDetails(job.id) { details = false }
}

// ---------- details panel ----------

/** Everything about one download and everything you can do with it, from the bottom of the screen. */
@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun DownloadDetails(id: String, onClose: () -> Unit) {
    val ctx = LocalContext.current
    val jobs by Downloads.jobs.collectAsState()
    val job = jobs.firstOrNull { it.id == id }
    if (job == null) { LaunchedEffect(Unit) { onClose() }; return }
    var refreshLink by remember { mutableStateOf(false) }
    var checkSum by remember { mutableStateOf(false) }
    var confirmDelete by remember { mutableStateOf(false) }
    var pickQueue by remember { mutableStateOf(false) }

    ModalBottomSheet(onDismissRequest = onClose, containerColor = MaterialTheme.colorScheme.surfaceContainer) {
        Column(Modifier.verticalScroll(rememberScrollState()).navigationBarsPadding().padding(start = 20.dp, end = 20.dp, bottom = 16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                FileBadge(job, 40.dp)
                Spacer(Modifier.width(12.dp))
                Text(displayName(job), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold,
                    maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
            if (job.isRunning || job.status == JobStatus.PAUSED) {
                Spacer(Modifier.height(14.dp)); SlimProgress(job)
            }
            Spacer(Modifier.height(12.dp))
            val rows = buildList {
                add("Status" to statusWords(job).takeIf { job.status != JobStatus.ERROR }.orEmpty().ifEmpty { "Failed" })
                if (job.status == JobStatus.ERROR) add("Problem" to (job.error ?: "Unknown error"))
                if (job.isRunning) {
                    fmtSpeed(bytesPerSec(job.speed)).takeIf { it.isNotEmpty() }?.let { add("Speed" to it) }
                    fmtEta(job.eta).takeIf { it.isNotEmpty() }?.let { add("Time left" to it) }
                }
                if (job.url.startsWith("http")) add("Address" to job.url)
                job.fileName?.let { add(if (job.status == JobStatus.DONE) "Saved as" to it else "Saving as" to it) }
                Engine.humanSize(job.size())?.let { add("Size" to it) }
                job.qualityLabel.takeIf { it.isNotBlank() && it != "File" }?.let { add("Quality" to it) }
                job.site?.let { add("Site" to it) }
                if (job.kind != JobKind.CONVERT) add("Queue" to job.queue.ifBlank { "Main queue" })
                add("Added" to DateUtils.formatDateTime(ctx, job.createdAt,
                    DateUtils.FORMAT_SHOW_DATE or DateUtils.FORMAT_SHOW_TIME or DateUtils.FORMAT_ABBREV_MONTH))
                (job.sha256 ?: job.expectedSha256)?.let { add("SHA-256" to it) }
            }
            rows.forEach { (k, v) ->
                Row(Modifier.padding(vertical = 4.dp)) {
                    Text(k, Modifier.width(92.dp), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Text(v, style = MaterialTheme.typography.bodySmall, maxLines = if (k == "Problem") 6 else 3, overflow = TextOverflow.Ellipsis,
                        color = if (k == "Problem") AnySaveColors.status.err else MaterialTheme.colorScheme.onSurface)
                }
            }
            Spacer(Modifier.height(16.dp))
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                @Composable
                fun B(icon: ImageVector, label: String, primary: Boolean = false, onClick: () -> Unit) =
                    if (primary) Button(onClick = onClick, shape = RoundedCornerShape(10.dp), contentPadding = PaddingValues(horizontal = 14.dp)) {
                        Icon(icon, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp)); Text(label)
                    } else OutlinedButton(onClick = onClick, shape = RoundedCornerShape(10.dp), contentPadding = PaddingValues(horizontal = 14.dp)) {
                        Icon(icon, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp)); Text(label)
                    }
                if (job.status == JobStatus.DONE) {
                    B(Icons.Outlined.PlayArrow, "Open", primary = true) { openFile(ctx, job) }
                    B(Icons.Outlined.Share, "Share") { shareFile(ctx, job) }
                }
                if (job.canPause && job.status != JobStatus.SCHEDULED) B(Icons.Outlined.Pause, "Pause", primary = true) { DownloadService.pause(ctx, job.id) }
                if (job.status == JobStatus.SCHEDULED) {
                    B(Icons.Outlined.PlayArrow, "Start now", primary = true) { DownloadService.startNow(ctx, job.id) }
                    B(Icons.Outlined.Pause, "Pause") { DownloadService.pause(ctx, job.id) }
                }
                if (job.status == JobStatus.PAUSED) B(Icons.Outlined.PlayArrow, "Resume", primary = true) { DownloadService.resume(ctx, job.id) }
                if (job.status == JobStatus.ERROR || job.status == JobStatus.CANCELLED)
                    B(Icons.Outlined.Refresh, "Retry", primary = true) { DownloadService.retry(ctx, job.id) }
                if (job.url.startsWith("http")) B(Icons.Outlined.ContentCopy, "Copy link") {
                    ctx.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("link", job.url))
                    Toast.makeText(ctx, "Link copied", Toast.LENGTH_SHORT).show()
                }
                if ((job.status == JobStatus.PAUSED || job.status == JobStatus.ERROR) && job.kind != JobKind.CONVERT)
                    B(Icons.Outlined.Link, "Refresh expired link") { refreshLink = true }
                if (job.status == JobStatus.DONE && job.uri != null) B(Icons.Outlined.VerifiedUser, "Check SHA-256") { checkSum = true }
                val queues = Schedule.queueNames()
                if (queues.isNotEmpty() && job.kind != JobKind.CONVERT && (job.isWaiting || job.status == JobStatus.PAUSED)) Box {
                    B(Icons.Outlined.Folder, "Queue: ${job.queue.ifBlank { "Main" }}") { pickQueue = true }
                    DropdownMenu(expanded = pickQueue, onDismissRequest = { pickQueue = false }) {
                        (listOf("") + queues).forEach { q ->
                            DropdownMenuItem(text = { Text(q.ifBlank { "Main queue" }) }, enabled = q != job.queue,
                                onClick = { pickQueue = false; DownloadService.moveToQueue(ctx, job.id, q) })
                        }
                    }
                }
                if (job.status == JobStatus.DONE) B(Icons.Outlined.Delete, "Delete file") { confirmDelete = true }
                if (job.finished) B(Icons.Outlined.PlaylistRemove, "Remove from list") { onClose(); Downloads.remove(job.id) }
                else B(Icons.Outlined.Close, "Cancel download") { DownloadService.cancel(ctx, job.id) }
            }
        }
    }
    if (refreshLink) RefreshLinkDialog(job) { refreshLink = false }
    if (checkSum) ChecksumDialog(job) { checkSum = false }
    if (confirmDelete) AlertDialog(
        onDismissRequest = { confirmDelete = false },
        title = { Text("Delete this file?") },
        text = { Text("${job.fileName ?: job.title} will be removed from your phone. This can't be undone.") },
        confirmButton = { TextButton(onClick = { confirmDelete = false; onClose(); deleteFile(ctx, job) }) { Text("Delete") } },
        dismissButton = { TextButton(onClick = { confirmDelete = false }) { Text("Cancel") } },
    )
}

/** Total speed and counts, shown under the list while anything is going on (the desktop's status bar). */
@Composable
fun SpeedStrip(jobs: List<Job>, modifier: Modifier = Modifier) {
    val running = jobs.count { it.isRunning }
    val waiting = jobs.count { it.isWaiting || it.status == JobStatus.PAUSED }
    if (running == 0 && waiting == 0) return
    val total = jobs.filter { it.isRunning }.sumOf { bytesPerSec(it.speed) }
    Surface(modifier.fillMaxWidth(), color = MaterialTheme.colorScheme.surfaceContainer, tonalElevation = 0.dp) {
        Column {
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            Row(Modifier.padding(horizontal = 16.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                if (running > 0) {
                    Icon(Icons.Outlined.ArrowDownward, null, tint = AnySaveColors.status.progress, modifier = Modifier.size(16.dp))
                    Spacer(Modifier.width(4.dp))
                    Text(fmtSpeed(total).ifEmpty { "Starting…" }, style = MaterialTheme.typography.labelLarge, fontWeight = FontWeight.Bold)
                    Spacer(Modifier.width(12.dp))
                } else {
                    Icon(Icons.Outlined.Pause, null, tint = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.size(16.dp))
                    Spacer(Modifier.width(6.dp))
                }
                Text(listOfNotNull(
                    running.takeIf { it > 0 }?.let { "$it downloading" },
                    waiting.takeIf { it > 0 }?.let { "$it waiting" },
                ).joinToString(" · "), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}
