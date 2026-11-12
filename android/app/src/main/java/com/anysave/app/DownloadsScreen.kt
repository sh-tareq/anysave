package com.anysave.app

import android.content.Context
import android.content.Intent
import android.text.format.DateUtils
import android.widget.Toast
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.ExperimentalFoundationApi
import coil.request.videoFramePercent
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Android
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.FolderZip
import androidx.compose.material.icons.outlined.Image
import androidx.compose.material.icons.outlined.InsertDriveFile
import androidx.compose.material.icons.outlined.Transform
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Link
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

private enum class Filter(val label: String) {
    ALL("All"), VIDEO("Videos"), AUDIO("Music"), FILES("Files"), CONVERTED("Converted"), FAILED("Failed")
}

/** "Big Buck Bunny [BigBuckBunny_124] 720p.mp4" -> "Big Buck Bunny 720p" (IDs and extension hidden). */
fun displayName(job: Job): String {
    val raw = if (job.status == JobStatus.DONE && job.kind != JobKind.VIDEO) job.fileName ?: job.title
    else if (job.status == JobStatus.DONE) job.fileName ?: job.title else job.title
    val noExt = if (FileKinds.extOf(raw).length in 2..5) raw.substringBeforeLast('.') else raw
    return noExt.replace(Regex("""\s*\[[^\]]{4,}\]"""), "").replace(Regex("\\s+"), " ").trim().ifBlank { raw }
}

private fun dayGroup(t: Long): String {
    val now = java.util.Calendar.getInstance()
    val c = java.util.Calendar.getInstance().apply { timeInMillis = t }
    fun sameDay(a: java.util.Calendar, b: java.util.Calendar) =
        a.get(java.util.Calendar.YEAR) == b.get(java.util.Calendar.YEAR) && a.get(java.util.Calendar.DAY_OF_YEAR) == b.get(java.util.Calendar.DAY_OF_YEAR)
    if (sameDay(now, c)) return "Today"
    now.add(java.util.Calendar.DAY_OF_YEAR, -1)
    if (sameDay(now, c)) return "Yesterday"
    if (System.currentTimeMillis() - t < 7L * 24 * 3600 * 1000) return "This week"
    return "Earlier"
}

@OptIn(ExperimentalMaterial3Api::class, ExperimentalFoundationApi::class)
@Composable
fun DownloadsScreen(jobs: List<Job>) {
    val ctx = LocalContext.current
    var filter by remember { mutableStateOf(Filter.ALL) }
    var confirmClear by remember { mutableStateOf(false) }
    var addLink by remember { mutableStateOf(false) }
    var searching by remember { mutableStateOf(false) }
    var query by remember { mutableStateOf("") }
    var overflow by remember { mutableStateOf(false) }

    val active = jobs.filter { it.active }
    val paused = jobs.filter { it.status == JobStatus.PAUSED }
    val done = jobs.filter { it.status == JobStatus.DONE }
    val failed = jobs.filter { it.status == JobStatus.ERROR }
    fun matches(j: Job) = query.isBlank() ||
        listOfNotNull(j.title, j.fileName, j.site).any { it.contains(query.trim(), ignoreCase = true) }
    fun inFilter(j: Job) = when (filter) {
        Filter.ALL -> true
        Filter.VIDEO -> j.category == FileKinds.Category.VIDEOS
        Filter.AUDIO -> j.category == FileKinds.Category.MUSIC || (j.kind == JobKind.CONVERT && j.audioOnly)
        Filter.FILES -> j.category !in setOf(FileKinds.Category.VIDEOS, FileKinds.Category.MUSIC, FileKinds.Category.CONVERTED)
        Filter.CONVERTED -> j.kind == JobKind.CONVERT
        Filter.FAILED -> false
    }
    val saved = if (filter == Filter.FAILED) emptyList() else done.filter { inFilter(it) && matches(it) }
    val failedShown = if (filter == Filter.FAILED || filter == Filter.ALL) failed.filter(::matches) else emptyList()
    val counts = mapOf(
        Filter.ALL to done.size, Filter.VIDEO to done.count { it.category == FileKinds.Category.VIDEOS },
        Filter.AUDIO to done.count { it.category == FileKinds.Category.MUSIC || (it.kind == JobKind.CONVERT && it.audioOnly) },
        Filter.FILES to done.count { it.category !in setOf(FileKinds.Category.VIDEOS, FileKinds.Category.MUSIC, FileKinds.Category.CONVERTED) },
        Filter.CONVERTED to done.count { it.kind == JobKind.CONVERT }, Filter.FAILED to failed.size,
    )

    ScreenScaffold(
        title = "Downloads",
        subtitle = "${done.size} saved · ${Engine.humanSize(done.sumOf { it.sizeBytes }) ?: "0 MB"} · Downloads/AnySave",
        actions = {
            IconButton(onClick = { searching = !searching; if (!searching) query = "" }) {
                Icon(if (searching) Icons.Outlined.Close else Icons.Outlined.Search, "Search")
            }
            Box {
                IconButton(onClick = { overflow = true }) { Icon(Icons.Outlined.MoreVert, "More") }
                DropdownMenu(expanded = overflow, onDismissRequest = { overflow = false }) {
                    if (active.any { it.canPause }) DropdownMenuItem(text = { Text("Pause all") },
                        onClick = { overflow = false; active.filter { it.canPause }.forEach { DownloadService.pause(ctx, it.id) } })
                    if (paused.isNotEmpty()) DropdownMenuItem(text = { Text("Resume all") },
                        onClick = { overflow = false; paused.forEach { DownloadService.resume(ctx, it.id) } })
                    if (failed.isNotEmpty()) DropdownMenuItem(text = { Text("Retry all failed") },
                        onClick = { overflow = false; failed.forEach { DownloadService.retry(ctx, it.id) } })
                    DropdownMenuItem(text = { Text("Clear finished from list") }, enabled = jobs.any { it.finished },
                        onClick = { overflow = false; confirmClear = true })
                }
            }
        },
        floatingActionButton = {
            ExtendedFloatingActionButton(onClick = { addLink = true },
                icon = { Icon(Icons.Outlined.Add, null) }, text = { Text("Add") })
        },
    ) { pad ->
        LazyColumn(
            Modifier.fillMaxSize().padding(pad),
            contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 6.dp, bottom = 96.dp),
            verticalArrangement = Arrangement.spacedBy(4.dp)
        ) {
            if (searching) item {
                OutlinedTextField(
                    value = query, onValueChange = { query = it }, singleLine = true,
                    placeholder = { Text("Search by name or site") },
                    leadingIcon = { Icon(Icons.Outlined.Search, null) },
                    shape = RoundedCornerShape(16.dp), modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                )
            }
            // One swipeable row of filters with counts
            item {
                Row(Modifier.horizontalScroll(rememberScrollState()).padding(top = 8.dp, bottom = 4.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Filter.entries.forEach { f ->
                        val n = counts[f] ?: 0
                        if (f == Filter.FAILED && n == 0 && filter != f) return@forEach
                        FilterChip(selected = filter == f, onClick = { filter = f },
                            label = { Text(if (n > 0 && f != Filter.ALL) "${f.label} $n" else f.label) })
                    }
                }
            }
            if (active.isNotEmpty() || paused.isNotEmpty()) {
                item { SectionLabel("In progress") }
                items(active + paused, key = { it.id }) { ActiveJobCard(it) }
            }
            // Saved files grouped by day
            saved.groupBy { dayGroup(it.createdAt) }.forEach { (day, list) ->
                item(key = "h-$day") { SectionLabel(day) }
                items(list, key = { it.id }) { FinishedRow(it) }
            }
            if (failedShown.isNotEmpty()) {
                item(key = "h-failed") { SectionLabel("Couldn't download") }
                items(failedShown, key = { it.id }) { FinishedRow(it) }
            }
            if (active.isEmpty() && paused.isEmpty() && saved.isEmpty() && failedShown.isEmpty()) item {
                if (query.isNotBlank() || filter != Filter.ALL) Text("Nothing matches.", Modifier.padding(vertical = 32.dp),
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
                else EmptyState()
            }
        }
    }

    if (addLink) AddDownloadDialog { addLink = false }

    if (confirmClear) AlertDialog(
        onDismissRequest = { confirmClear = false },
        title = { Text("Clear finished from the list?") },
        text = { Text("Only the list is cleared. Your files stay in Downloads/AnySave.") },
        confirmButton = { TextButton(onClick = { Downloads.clearFinished(); confirmClear = false }) { Text("Clear") } },
        dismissButton = { TextButton(onClick = { confirmClear = false }) { Text("Cancel") } },
    )
}

/** Paste any link: files download directly, everything else goes through the video downloader. */
@Composable
private fun AddDownloadDialog(onClose: () -> Unit) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    var link by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        val clip = ctx.getSystemService(android.content.ClipboardManager::class.java).primaryClip
        Engine.extractUrl(clip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(ctx)?.toString())?.let { link = it }
    }
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text("Add download") },
        text = {
            Column {
                Text("Any link or batch pattern (e.g. file[01-10].zip): video pages or direct files.",
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Spacer(Modifier.height(10.dp))
                OutlinedTextField(value = link, onValueChange = { link = it }, singleLine = true,
                    placeholder = { Text("https://…") }, modifier = Modifier.fillMaxWidth())
            }
        },
        confirmButton = {
            Button(enabled = !busy && Engine.extractUrls(link).isNotEmpty(), onClick = {
                val urls = Engine.extractUrls(link)
                if (urls.isEmpty()) return@Button
                busy = true
                scope.launch {
                    if (urls.size > 1) {
                        urls.forEach { DownloadService.startLink(ctx, it) }
                    } else {
                        val url = urls.first()
                        val known = Engine.siteOf(url) != null
                        val probe = if (known) null else withContext(Dispatchers.IO) { FileKinds.probe(url) }
                        if (!known && (FileKinds.looksLikeFile(url) || probe?.isFile == true))
                            DownloadService.startFile(ctx, url, probe?.name, probe?.mime)
                        else DownloadService.startQuick(ctx, url)
                    }
                    onClose()
                }
            }) { Text(if (busy) "Checking…" else "Download") }
        },
        dismissButton = { TextButton(onClick = onClose) { Text("Cancel") } },
    )
}

@Composable
private fun SectionLabel(text: String) =
    Text(text, style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(top = 14.dp, bottom = 4.dp))

@Composable
private fun EmptyState() {
    Column(Modifier.fillMaxWidth().padding(top = 60.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        AnimatedLogo(96.dp)
        Text("Nothing here yet", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(top = 12.dp))
        Text("Paste, copy or share a link to start.", color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

/** Thumbnail: the site's image if we have it, else a frame from the saved video, else an icon. */
@Composable
fun Thumb(job: Job, modifier: Modifier) {
    val cat = job.category
    // Pictures and videos can show the saved file itself; other types get an icon.
    val visual = cat == FileKinds.Category.VIDEOS || cat == FileKinds.Category.PICTURES ||
        (cat == FileKinds.Category.CONVERTED && !job.audioOnly)
    val model: Any? = job.thumbnail ?: job.uri?.takeIf { visual && job.status == JobStatus.DONE }
    Box(modifier.clip(RoundedCornerShape(8.dp)).background(MaterialTheme.colorScheme.surfaceContainerHighest),
        contentAlignment = Alignment.Center) {
        val icon = @Composable {
            Icon(when (cat) {
                FileKinds.Category.MUSIC -> Icons.Outlined.MusicNote
                FileKinds.Category.PICTURES -> Icons.Outlined.Image
                FileKinds.Category.DOCUMENTS -> Icons.Outlined.Description
                FileKinds.Category.ARCHIVES -> Icons.Outlined.FolderZip
                FileKinds.Category.APPS -> Icons.Outlined.Android
                FileKinds.Category.CONVERTED -> if (job.audioOnly) Icons.Outlined.MusicNote else Icons.Outlined.Transform
                FileKinds.Category.OTHER -> Icons.Outlined.InsertDriveFile
                FileKinds.Category.VIDEOS -> Icons.Outlined.Videocam
            }, null, tint = MaterialTheme.colorScheme.outline)
        }
        val ctx = LocalContext.current
        val request = remember(model) {
            if (model is android.net.Uri) coil.request.ImageRequest.Builder(ctx).data(model).videoFramePercent(0.1).build() else model
        }
        if (model == null) icon() else SubcomposeAsyncImage(
            model = request, contentDescription = null, contentScale = ContentScale.Crop,
            modifier = Modifier.fillMaxSize(), error = { icon() }, loading = { icon() }
        )
        if (job.audioOnly && model != null) Icon(Icons.Outlined.MusicNote, null, tint = Color.White,
            modifier = Modifier.align(Alignment.BottomEnd).padding(3.dp).size(16.dp)
                .background(Color(0x99000000), RoundedCornerShape(4.dp)))
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun FinishedRow(job: Job) {
    val ctx = LocalContext.current
    var menu by remember { mutableStateOf(false) }
    var confirmDelete by remember { mutableStateOf(false) }
    var refreshLink by remember { mutableStateOf(false) }
    val failed = job.status != JobStatus.DONE
    val ext = FileKinds.extOf(job.fileName).uppercase().take(4)
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp))
            .combinedClickable(onClick = { if (failed) menu = true else openFile(ctx, job) }, onLongClick = { menu = true })
            .padding(vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Box {
            Thumb(job, Modifier.size(width = 88.dp, height = 50.dp))
            if (!failed && (job.category == FileKinds.Category.VIDEOS || (job.kind == JobKind.CONVERT && !job.audioOnly && job.mime?.startsWith("video") == true)))
                Icon(Icons.Outlined.PlayArrow, null, tint = Color.White,
                    modifier = Modifier.align(Alignment.Center).size(22.dp).background(Color(0x66000000), RoundedCornerShape(50)))
        }
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(displayName(job), maxLines = 1, overflow = TextOverflow.Ellipsis, fontWeight = FontWeight.Medium,
                style = MaterialTheme.typography.bodyLarge)
            if (failed) {
                Text(statusText(job), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error,
                    maxLines = 2, overflow = TextOverflow.Ellipsis)
            } else {
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(top = 2.dp)) {
                    if (ext.isNotEmpty()) {
                        Text(ext, style = MaterialTheme.typography.labelSmall, fontWeight = FontWeight.Bold,
                            color = MaterialTheme.colorScheme.onSecondaryContainer,
                            modifier = Modifier.background(MaterialTheme.colorScheme.secondaryContainer, RoundedCornerShape(4.dp))
                                .padding(horizontal = 5.dp, vertical = 1.dp))
                        Spacer(Modifier.width(6.dp))
                    }
                    if (job.site != null && job.kind != JobKind.CONVERT) { SiteIcon(job.site, 14.dp); Spacer(Modifier.width(5.dp)) }
                    val time = android.text.format.DateFormat.getTimeFormat(ctx).format(java.util.Date(job.createdAt))
                    val when_ = if (dayGroup(job.createdAt) in setOf("Today", "Yesterday")) time
                        else DateUtils.formatDateTime(ctx, job.createdAt, DateUtils.FORMAT_SHOW_DATE or DateUtils.FORMAT_ABBREV_MONTH)
                    Text(listOfNotNull(Engine.humanSize(job.sizeBytes), when_).joinToString(" · "),
                        style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
        }
        if (failed) FilledTonalIconButton(onClick = { DownloadService.retry(ctx, job.id) }) {
            Icon(Icons.Outlined.Refresh, "Retry")
        }
        Box {
            IconButton(onClick = { menu = true }) { Icon(Icons.Outlined.MoreVert, "More") }
            DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                if (failed) {
                    DropdownMenuItem(text = { Text("Retry") }, leadingIcon = { Icon(Icons.Outlined.Refresh, null) },
                        onClick = { menu = false; DownloadService.retry(ctx, job.id) })
                    if (job.kind != JobKind.CONVERT) {
                        DropdownMenuItem(text = { Text("Refresh expired link") }, leadingIcon = { Icon(Icons.Outlined.Link, null) },
                            onClick = { menu = false; refreshLink = true })
                    }
                } else {
                    DropdownMenuItem(text = { Text("Open") }, leadingIcon = { Icon(Icons.Outlined.PlayArrow, null) },
                        onClick = { menu = false; openFile(ctx, job) })
                    DropdownMenuItem(text = { Text("Share") }, leadingIcon = { Icon(Icons.Outlined.Share, null) },
                        onClick = { menu = false; share(ctx, job) })
                    DropdownMenuItem(text = { Text("Delete file") }, leadingIcon = { Icon(Icons.Outlined.Delete, null) },
                        onClick = { menu = false; confirmDelete = true })
                }
                DropdownMenuItem(text = { Text("Remove from list") }, leadingIcon = { Icon(Icons.Outlined.PlaylistRemove, null) },
                    onClick = { menu = false; Downloads.remove(job.id) })
            }
        }
    }

    if (confirmDelete) AlertDialog(
        onDismissRequest = { confirmDelete = false },
        title = { Text("Delete this file?") },
        text = { Text("${job.fileName ?: job.title} will be removed from your phone. This can't be undone.") },
        confirmButton = { TextButton(onClick = { confirmDelete = false; deleteFile(ctx, job) }) { Text("Delete") } },
        dismissButton = { TextButton(onClick = { confirmDelete = false }) { Text("Cancel") } },
    )

    if (refreshLink) RefreshLinkDialog(job) { refreshLink = false }
}

/** Paste a fresh signed download URL for an expired link and resume from saved partial bytes. */
@Composable
fun RefreshLinkDialog(job: Job, onClose: () -> Unit) {
    val ctx = LocalContext.current
    var newUrl by remember { mutableStateOf("") }
    LaunchedEffect(Unit) {
        val clip = ctx.getSystemService(android.content.ClipboardManager::class.java).primaryClip
        Engine.extractUrl(clip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(ctx)?.toString())?.let {
            if (it != job.url) newUrl = it
        }
    }
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text("Refresh expired link") },
        text = {
            Column {
                Text(
                    "Paste a fresh download link for \"${displayName(job)}\". AnySave will keep the bytes already downloaded and continue from where it stopped.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
                Spacer(Modifier.height(10.dp))
                OutlinedTextField(
                    value = newUrl,
                    onValueChange = { newUrl = it },
                    singleLine = true,
                    placeholder = { Text("https://…") },
                    modifier = Modifier.fillMaxWidth()
                )
            }
        },
        confirmButton = {
            Button(
                enabled = Engine.extractUrl(newUrl) != null,
                onClick = {
                    val u = Engine.extractUrl(newUrl) ?: return@Button
                    DownloadService.refreshUrl(ctx, job.id, u)
                    onClose()
                }
            ) { Text("Resume") }
        },
        dismissButton = { TextButton(onClick = onClose) { Text("Cancel") } },
    )
}

/** Opens a saved file with the default app. */
fun openFile(ctx: Context, job: Job) {
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
        if (ok) "Deleted" else "Removed from the list. Delete the file from your Files app (Downloads/AnySave).",
        Toast.LENGTH_LONG
    ).show()
}
