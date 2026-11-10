package com.vidgrab.app

import androidx.compose.animation.core.*
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Videocam
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.state.ToggleableState
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.DpSize
import androidx.compose.ui.unit.dp
import coil.compose.SubcomposeAsyncImage

/*
 * Pick-and-download lists, shared by the ad sheet and playlists/channels on Home:
 * a checkbox per row, Select all, one button for the selection, and each row's live progress.
 */

/** Finds the job for a list row: the one started from it (by id), else the newest for its link. */
class JobIndex(jobs: List<Job>) {
    private val byId = jobs.associateBy { it.id }
    // Newest first in Downloads, so walk oldest to newest and keep the last one.
    private val byUrl = jobs.asReversed().filter { it.status != JobStatus.CANCELLED }.associateBy { it.url }

    operator fun get(id: String) = byId[id]
    fun find(id: String?, url: String): Job? = id?.let { byId[it] }?.takeIf { it.status != JobStatus.CANCELLED } ?: byUrl[url]
}

/** A row can be ticked when nothing was started for it yet (or it was cancelled). */
fun Job?.isOpen() = this == null || status == JobStatus.CANCELLED

fun plural(n: Int, noun: String) = "$n $noun${if (n == 1) "" else "s"}"

@Composable
fun SelectAllToggle(keys: List<String>, selected: SnapshotStateList<String>) {
    val all = when {
        keys.isNotEmpty() && keys.all { it in selected } -> ToggleableState.On
        keys.none { it in selected } -> ToggleableState.Off
        else -> ToggleableState.Indeterminate
    }
    Row(
        Modifier.clip(RoundedCornerShape(8.dp)).toggleable(
            value = all == ToggleableState.On, role = Role.Checkbox,
            onValueChange = { on -> selected.clear(); if (on) selected.addAll(keys) },
        ).padding(end = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        TriStateCheckbox(state = all, onClick = null, modifier = Modifier.padding(12.dp))
        Text("Select all", style = MaterialTheme.typography.labelLarge, maxLines = 1, softWrap = false)
    }
}

/** One video or ad: tap the row to tick it, or the button on the right to download just this one. */
@Composable
fun BatchRow(
    title: String, subtitle: String, thumbnail: String?, thumbSize: DpSize,
    job: Job?, selected: Boolean, onSelect: (Boolean) -> Unit, noun: String, onDownload: () -> Unit,
) {
    val open = job.isOpen()
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp))
            .background(if (selected) MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.5f) else Color.Transparent)
            .toggleable(value = selected, enabled = open, role = Role.Checkbox, onValueChange = onSelect)
            .padding(vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Checkbox(checked = selected, onCheckedChange = null, enabled = open, modifier = Modifier.padding(horizontal = 8.dp))
        Box(Modifier.size(thumbSize).clip(RoundedCornerShape(10.dp))
            .background(MaterialTheme.colorScheme.surfaceVariant), contentAlignment = Alignment.Center) {
            val icon = @Composable { Icon(Icons.Outlined.Videocam, null, tint = MaterialTheme.colorScheme.outline) }
            if (thumbnail != null) SubcomposeAsyncImage(model = thumbnail, contentDescription = null,
                contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize(),
                error = { icon() }, loading = { icon() })
            else icon()
        }
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(title, fontWeight = FontWeight.Medium, maxLines = 2, overflow = TextOverflow.Ellipsis,
                style = MaterialTheme.typography.bodyMedium)
            val failed = job?.status == JobStatus.ERROR
            // Speed and time left while it runs, the reason when it failed; the badge shows the rest.
            val line = when (job?.status) {
                JobStatus.DOWNLOADING, JobStatus.PAUSED, JobStatus.WAITING_WIFI, JobStatus.ERROR -> statusText(job)
                else -> subtitle
            }
            if (line.isNotBlank()) Text(
                line, style = MaterialTheme.typography.bodySmall,
                color = if (failed) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1, overflow = TextOverflow.Ellipsis,
            )
        }
        Box(Modifier.padding(start = 4.dp, end = 4.dp).widthIn(min = 48.dp), contentAlignment = Alignment.Center) {
            JobStatusBadge(job, noun, onDownload)
        }
    }
}

/** Download button, then the job's live percent, then "Saved ✓" (tap to open) or Retry. */
@Composable
fun JobStatusBadge(job: Job?, noun: String, onDownload: () -> Unit) {
    val ctx = LocalContext.current
    @Composable
    fun Pill(text: String, container: Color, content: Color, onClick: (() -> Unit)? = null) {
        Text(text, style = MaterialTheme.typography.labelMedium, fontWeight = FontWeight.SemiBold, color = content,
            maxLines = 1, softWrap = false,
            modifier = Modifier.clip(RoundedCornerShape(50)).background(container)
                .then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier)
                .padding(horizontal = 10.dp, vertical = 6.dp))
    }
    when (job?.status) {
        null, JobStatus.CANCELLED -> FilledTonalIconButton(onClick = onDownload) {
            Icon(Icons.Outlined.Download, contentDescription = "Download this $noun")
        }
        JobStatus.DONE -> Pill("Saved ✓", MaterialTheme.colorScheme.primaryContainer,
            MaterialTheme.colorScheme.onPrimaryContainer) { openFile(ctx, job) }
        JobStatus.ERROR -> FilledTonalIconButton(
            onClick = { DownloadService.retry(ctx, job.id) },
            colors = IconButtonDefaults.filledTonalIconButtonColors(
                containerColor = MaterialTheme.colorScheme.errorContainer,
                contentColor = MaterialTheme.colorScheme.onErrorContainer),
        ) { Icon(Icons.Outlined.Refresh, "Retry") }
        JobStatus.QUEUED, JobStatus.WAITING_WIFI -> Pill("Queued", MaterialTheme.colorScheme.surfaceVariant,
            MaterialTheme.colorScheme.onSurfaceVariant)
        JobStatus.PAUSED -> Pill("Paused", MaterialTheme.colorScheme.surfaceVariant,
            MaterialTheme.colorScheme.onSurfaceVariant) { DownloadService.resume(ctx, job.id) }
        JobStatus.DOWNLOADING, JobStatus.SAVING -> Box(Modifier.size(40.dp), contentAlignment = Alignment.Center) {
            if (job.status == JobStatus.DOWNLOADING && job.percent > 0f) {
                CircularProgressIndicator(progress = { job.percent / 100f }, Modifier.fillMaxSize(), strokeWidth = 3.dp,
                    trackColor = MaterialTheme.colorScheme.surfaceVariant)
                Text("${job.percent.toInt()}", style = MaterialTheme.typography.labelSmall, fontWeight = FontWeight.SemiBold)
            } else CircularProgressIndicator(Modifier.fillMaxSize(), strokeWidth = 3.dp)
        }
    }
}

/**
 * The button under a list: "Download N selected …" while some are ticked, otherwise the overall
 * progress of the last batch ("Downloading 3 videos · 45%"), then "✓ 3 saved".
 */
@Composable
fun BatchButton(selectedCount: Int, noun: String, batch: List<Job>, onDownloadSelected: () -> Unit, modifier: Modifier = Modifier) {
    val ctx = LocalContext.current
    val jobs = batch.filter { it.status != JobStatus.CANCELLED }
    val shape = RoundedCornerShape(16.dp)
    val height = Modifier.fillMaxWidth().height(50.dp)
    when {
        selectedCount > 0 || jobs.isEmpty() -> Button(
            onClick = onDownloadSelected, enabled = selectedCount > 0, modifier = modifier.then(height), shape = shape,
        ) {
            Icon(Icons.Outlined.Download, null, Modifier.size(18.dp)); Spacer(Modifier.width(8.dp))
            Text(if (selectedCount > 0) "Download $selectedCount selected $noun${if (selectedCount > 1) "s" else ""}"
                else "Select ${noun}s to download", fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        jobs.any { !it.finished } -> {
            val pct = jobs.sumOf { if (it.finished) 100.0 else it.percent.toDouble() } / jobs.size
            ProgressButton(pct.toFloat() / 100f, "Downloading ${plural(jobs.size, noun)} · ${pct.toInt()}%", modifier.then(height), shape)
        }
        else -> {
            val saved = jobs.count { it.status == JobStatus.DONE }
            val failed = jobs.filter { it.status == JobStatus.ERROR }
            FilledTonalButton(
                onClick = { failed.forEach { DownloadService.retry(ctx, it.id) } }, enabled = failed.isNotEmpty(),
                modifier = modifier.then(height), shape = shape,
                colors = ButtonDefaults.filledTonalButtonColors(
                    disabledContainerColor = MaterialTheme.colorScheme.primaryContainer,
                    disabledContentColor = MaterialTheme.colorScheme.onPrimaryContainer),
            ) {
                Text(listOfNotNull("✓ $saved saved", failed.size.takeIf { it > 0 }?.let { "Retry $it failed" }).joinToString(" · "),
                    fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}

/**
 * A button-sized progress bar with moving stripes. [fraction] null = not started yet (stripes only).
 */
@Composable
fun ProgressButton(fraction: Float?, text: String, modifier: Modifier = Modifier, shape: Shape = RoundedCornerShape(16.dp),
                   onClick: (() -> Unit)? = null) {
    val t = rememberInfiniteTransition(label = "stripes")
    val shift by t.animateFloat(0f, 1f, infiniteRepeatable(tween(900, easing = LinearEasing)), label = "shift")
    val filled by animateFloatAsState((fraction ?: 1f).coerceIn(0f, 1f), label = "fill")
    val track = MaterialTheme.colorScheme.primaryContainer
    val fill = MaterialTheme.colorScheme.primary.copy(alpha = if (fraction == null) 0.18f else 0.42f)
    val stripe = Color.White.copy(alpha = 0.16f)
    Box(
        modifier.clip(shape).background(track)
            .drawBehind {
                val w = size.width * filled
                drawRect(fill, size = Size(w, size.height))
                clipRect(right = w) {
                    val step = 22.dp.toPx()
                    var x = -size.height + shift * step
                    while (x < w + size.height) {
                        drawLine(stripe, Offset(x, size.height), Offset(x + size.height, 0f), strokeWidth = step / 2.6f)
                        x += step
                    }
                }
            }
            .then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier)
            .padding(horizontal = 16.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(text, color = MaterialTheme.colorScheme.onPrimaryContainer, fontWeight = FontWeight.SemiBold,
            maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.labelLarge)
    }
}
