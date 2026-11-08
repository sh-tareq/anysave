package com.vidgrab.app

import android.content.Context
import android.content.Intent
import android.text.format.DateUtils
import android.widget.Toast
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material.icons.outlined.MusicNote
import androidx.compose.material.icons.outlined.PlayArrow
import androidx.compose.material.icons.outlined.PlaylistRemove
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material.icons.outlined.Videocam
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import coil.compose.SubcomposeAsyncImage

private enum class Filter(val label: String) { ALL("All"), VIDEO("Videos"), AUDIO("Audio"), FAILED("Failed") }

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DownloadsScreen(jobs: List<Job>) {
    var filter by remember { mutableStateOf(Filter.ALL) }
    var confirmClear by remember { mutableStateOf(false) }
    val active = jobs.filter { it.active }
    val finished = jobs.filter { it.finished }.filter {
        when (filter) {
            Filter.ALL -> it.status != JobStatus.CANCELLED
            Filter.VIDEO -> it.status == JobStatus.DONE && !it.audioOnly
            Filter.AUDIO -> it.status == JobStatus.DONE && it.audioOnly
            Filter.FAILED -> it.status == JobStatus.ERROR || it.status == JobStatus.CANCELLED
        }
    }

    LazyColumn(
        Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 24.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp)
    ) {
        item {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text("Downloads", style = MaterialTheme.typography.headlineLarge, fontWeight = FontWeight.Bold,
                    modifier = Modifier.weight(1f))
                if (jobs.any { it.finished }) TextButton(onClick = { confirmClear = true }) { Text("Clear list") }
            }
            Text("Saved in Downloads/VidGrab", style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        item {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Filter.entries.forEach { f ->
                    FilterChip(selected = filter == f, onClick = { filter = f }, label = { Text(f.label) })
                }
            }
        }
        if (active.isNotEmpty()) {
            item { SectionLabel("In progress") }
            items(active, key = { it.id }) { ActiveJobCard(it) }
        }
        if (finished.isNotEmpty()) {
            item { SectionLabel(if (filter == Filter.FAILED) "Failed or cancelled" else "Saved") }
            items(finished, key = { it.id }) { FinishedRow(it) }
        }
        if (active.isEmpty() && finished.isEmpty()) item { EmptyState() }
    }

    if (confirmClear) AlertDialog(
        onDismissRequest = { confirmClear = false },
        title = { Text("Clear the list?") },
        text = { Text("This only clears the list. Your downloaded files stay in Downloads/VidGrab.") },
        confirmButton = { TextButton(onClick = { Downloads.clearFinished(); confirmClear = false }) { Text("Clear") } },
        dismissButton = { TextButton(onClick = { confirmClear = false }) { Text("Cancel") } },
    )
}

@Composable
private fun SectionLabel(text: String) =
    Text(text, style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(top = 6.dp))

@Composable
private fun EmptyState() {
    Column(Modifier.fillMaxWidth().padding(top = 60.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        AnimatedLogo(96.dp)
        Text("Nothing here yet", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(top = 12.dp))
        Text("Copy or share a video link to start.", color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

/** Thumbnail: the site's image if we have it, else a frame from the saved video, else an icon. */
@Composable
fun Thumb(job: Job, modifier: Modifier) {
    val model: Any? = job.thumbnail ?: job.uri?.takeIf { !job.audioOnly && job.status == JobStatus.DONE }
    Box(modifier.clip(RoundedCornerShape(8.dp)).background(MaterialTheme.colorScheme.surfaceContainerHighest),
        contentAlignment = Alignment.Center) {
        val icon = @Composable {
            Icon(if (job.audioOnly) Icons.Outlined.MusicNote else Icons.Outlined.Videocam, null,
                tint = MaterialTheme.colorScheme.outline)
        }
        if (model == null) icon() else SubcomposeAsyncImage(
            model = model, contentDescription = null, contentScale = ContentScale.Crop,
            modifier = Modifier.fillMaxSize(), error = { icon() }, loading = { icon() }
        )
        if (job.audioOnly && model != null) Icon(Icons.Outlined.MusicNote, null, tint = Color.White,
            modifier = Modifier.align(Alignment.BottomEnd).padding(3.dp).size(16.dp)
                .background(Color(0x99000000), RoundedCornerShape(4.dp)))
    }
}

@Composable
private fun FinishedRow(job: Job) {
    val ctx = LocalContext.current
    var menu by remember { mutableStateOf(false) }
    val failed = job.status != JobStatus.DONE
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp))
            .clickable { if (failed) DownloadService.retry(ctx, job.id) else open(ctx, job) }
            .padding(vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Box {
            Thumb(job, Modifier.size(width = 112.dp, height = 63.dp))
            if (!failed && !job.audioOnly) Icon(Icons.Outlined.PlayArrow, null, tint = Color.White,
                modifier = Modifier.align(Alignment.Center).size(28.dp).background(Color(0x66000000), RoundedCornerShape(50)))
        }
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(job.title, maxLines = 2, overflow = TextOverflow.Ellipsis, fontWeight = FontWeight.Medium,
                style = MaterialTheme.typography.bodyMedium)
            if (failed) Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.ErrorOutline, null, Modifier.size(14.dp), tint = MaterialTheme.colorScheme.error)
                Spacer(Modifier.width(4.dp))
                Text(statusText(job), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error,
                    maxLines = 2, overflow = TextOverflow.Ellipsis)
            } else {
                val meta = listOfNotNull(
                    job.qualityLabel.substringBefore(" (").takeIf { it.isNotBlank() },
                    Engine.humanSize(job.sizeBytes),
                    DateUtils.getRelativeTimeSpanString(job.createdAt).toString(),
                )
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(top = 2.dp)) {
                    SiteIcon(job.site, 16.dp)
                    Spacer(Modifier.width(6.dp))
                    Text(meta.joinToString(" · "), style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
        }
        Box {
            IconButton(onClick = { menu = true }) { Icon(Icons.Outlined.MoreVert, "More") }
            DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                if (failed) {
                    DropdownMenuItem(text = { Text("Retry") }, leadingIcon = { Icon(Icons.Outlined.Refresh, null) },
                        onClick = { menu = false; DownloadService.retry(ctx, job.id) })
                } else {
                    DropdownMenuItem(text = { Text("Play") }, leadingIcon = { Icon(Icons.Outlined.PlayArrow, null) },
                        onClick = { menu = false; open(ctx, job) })
                    DropdownMenuItem(text = { Text("Share") }, leadingIcon = { Icon(Icons.Outlined.Share, null) },
                        onClick = { menu = false; share(ctx, job) })
                    DropdownMenuItem(text = { Text("Delete file") }, leadingIcon = { Icon(Icons.Outlined.Delete, null) },
                        onClick = { menu = false; deleteFile(ctx, job) })
                }
                DropdownMenuItem(text = { Text("Remove from list") }, leadingIcon = { Icon(Icons.Outlined.PlaylistRemove, null) },
                    onClick = { menu = false; Downloads.remove(job.id) })
            }
        }
    }
}

private fun open(ctx: Context, job: Job) {
    val i = Intent(Intent.ACTION_VIEW).setDataAndType(job.uri, job.mime)
        .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
    runCatching { ctx.startActivity(i) }
        .onFailure { Toast.makeText(ctx, "File not found or no app can open it", Toast.LENGTH_SHORT).show() }
}

private fun share(ctx: Context, job: Job) {
    val i = Intent(Intent.ACTION_SEND).setType(job.mime).putExtra(Intent.EXTRA_STREAM, job.uri)
        .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
    ctx.startActivity(Intent.createChooser(i, "Share"))
}

private fun deleteFile(ctx: Context, job: Job) {
    val ok = runCatching { job.uri?.let { ctx.contentResolver.delete(it, null, null) } ?: 0 }.getOrDefault(0) > 0
    Downloads.remove(job.id)
    Toast.makeText(
        ctx,
        if (ok) "Deleted" else "Removed from the list. Delete the file from your Files app (Downloads/VidGrab).",
        Toast.LENGTH_LONG
    ).show()
}
