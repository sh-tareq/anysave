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
import androidx.compose.material.icons.automirrored.outlined.Sort
import androidx.compose.material.icons.outlined.Checklist
import androidx.compose.material.icons.outlined.DoneAll
import androidx.compose.material.icons.outlined.Pause
import androidx.compose.material.icons.outlined.SelectAll
import androidx.activity.compose.BackHandler
import androidx.compose.runtime.saveable.rememberSaveable
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

/** The desktop sidebar's filters, as one swipeable row of chips with counts. */
private enum class Filter(val label: String) {
    ALL("All"), ACTIVE("In progress"), DONE("Finished"), WAITING("Waiting"), FAILED("Failed"),
    VIDEOS("Videos"), MUSIC("Music"), PICTURES("Pictures"), DOCUMENTS("Documents"), ARCHIVES("Archives & apps"),
    CONVERTED("Converted"), OTHER("Other"),
}

private enum class Sort(val label: String) { NORMAL("In progress first, then newest"), NAME("Name (A–Z)"), SIZE("Size (largest first)"), OLDEST("Oldest first") }

private fun inFilter(j: Job, f: Filter): Boolean = when (f) {
    Filter.ALL -> true
    Filter.ACTIVE -> j.isRunning
    Filter.DONE -> j.status == JobStatus.DONE
    Filter.WAITING -> j.isWaiting || j.status == JobStatus.PAUSED
    Filter.FAILED -> j.status == JobStatus.ERROR
    Filter.VIDEOS -> j.category == FileKinds.Category.VIDEOS
    Filter.MUSIC -> j.category == FileKinds.Category.MUSIC || (j.kind == JobKind.CONVERT && j.audioOnly)
    Filter.PICTURES -> j.category == FileKinds.Category.PICTURES
    Filter.DOCUMENTS -> j.category == FileKinds.Category.DOCUMENTS
    Filter.ARCHIVES -> j.category == FileKinds.Category.ARCHIVES || j.category == FileKinds.Category.APPS
    Filter.CONVERTED -> j.kind == JobKind.CONVERT
    Filter.OTHER -> j.category == FileKinds.Category.OTHER
}

/** "Big Buck Bunny [BigBuckBunny_124] 720p.mp4" -> "Big Buck Bunny 720p" (IDs and extension hidden). */
fun displayName(job: Job): String {
    val raw = if (job.status == JobStatus.DONE) job.fileName ?: job.title else job.title
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

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DownloadsScreen(jobs: List<Job>, onLookUp: (String) -> Unit) {
    val ctx = LocalContext.current
    var filter by rememberSaveable { mutableStateOf(Filter.ALL) }
    var sort by rememberSaveable { mutableStateOf(Sort.NORMAL) }
    var confirmClear by remember { mutableStateOf(false) }
    var confirmRemove by remember { mutableStateOf(false) }
    var addLink by remember { mutableStateOf(false) }
    var searching by remember { mutableStateOf(false) }
    var query by rememberSaveable { mutableStateOf("") }
    var overflow by remember { mutableStateOf(false) }
    var sortMenu by remember { mutableStateOf(false) }
    val selected = remember { mutableStateListOf<String>() }
    val selecting = selected.isNotEmpty()
    // Ids that left the list can't stay selected.
    LaunchedEffect(jobs) { selected.retainAll(jobs.map { it.id }.toSet()) }
    BackHandler(enabled = selecting) { selected.clear() }

    val done = jobs.filter { it.status == JobStatus.DONE }
    val pausable = jobs.filter { it.canPause && it.status != JobStatus.SCHEDULED }
    val resumable = jobs.filter { it.status == JobStatus.PAUSED }
    val failed = jobs.filter { it.status == JobStatus.ERROR }
    fun matches(j: Job) = query.isBlank() ||
        listOfNotNull(j.title, j.fileName, j.site, j.url).any { it.contains(query.trim(), ignoreCase = true) }
    val shown = jobs.filter { inFilter(it, filter) && matches(it) }.let { list ->
        when (sort) {
            Sort.NORMAL -> list.sortedWith(compareBy<Job> { it.finished }.thenByDescending { it.createdAt })
            Sort.NAME -> list.sortedWith(compareBy(String.CASE_INSENSITIVE_ORDER) { displayName(it) })
            Sort.SIZE -> list.sortedByDescending { it.size() }
            Sort.OLDEST -> list.sortedBy { it.createdAt }
        }
    }
    val counts = Filter.entries.associateWith { f -> jobs.count { inFilter(it, f) } }

    ScreenScaffold(
        title = if (selecting) "${selected.size} selected" else "Downloads",
        subtitle = if (selecting) null else "${done.size} saved · ${Engine.humanSize(done.sumOf { it.size() }) ?: "0 MB"} · Downloads/AnySave",
        leading = if (selecting) ({ IconButton(onClick = { selected.clear() }) { Icon(Icons.Outlined.Close, "Stop selecting") } }) else null,
        actions = {
            if (selecting) {
                val sel = jobs.filter { it.id in selected }
                IconButton(onClick = { selected.clear(); selected.addAll(shown.map { it.id }) }) { Icon(Icons.Outlined.SelectAll, "Select all") }
                if (sel.any { it.canPause && it.status != JobStatus.SCHEDULED }) IconButton(onClick = {
                    sel.filter { it.canPause }.forEach { DownloadService.pause(ctx, it.id) }
                }) { Icon(Icons.Outlined.Pause, "Pause") }
                if (sel.any { it.status == JobStatus.PAUSED || it.status == JobStatus.SCHEDULED || it.status == JobStatus.ERROR }) IconButton(onClick = {
                    sel.forEach {
                        when (it.status) {
                            JobStatus.PAUSED -> DownloadService.resume(ctx, it.id)
                            JobStatus.SCHEDULED -> DownloadService.startNow(ctx, it.id)
                            JobStatus.ERROR -> DownloadService.retry(ctx, it.id)
                            else -> {}
                        }
                    }
                }) { Icon(Icons.Outlined.PlayArrow, "Resume") }
                IconButton(onClick = { confirmRemove = true }) { Icon(Icons.Outlined.Delete, "Remove") }
            } else {
                IconButton(onClick = { searching = !searching; if (!searching) query = "" }) {
                    Icon(if (searching) Icons.Outlined.Close else Icons.Outlined.Search, "Search")
                }
                Box {
                    IconButton(onClick = { sortMenu = true }) { Icon(Icons.AutoMirrored.Outlined.Sort, "Sort") }
                    DropdownMenu(expanded = sortMenu, onDismissRequest = { sortMenu = false }) {
                        Sort.entries.forEach { s ->
                            DropdownMenuItem(text = { Text(s.label) }, onClick = { sort = s; sortMenu = false },
                                leadingIcon = { RadioButton(selected = sort == s, onClick = null) })
                        }
                    }
                }
                Box {
                    IconButton(onClick = { overflow = true }) { Icon(Icons.Outlined.MoreVert, "More") }
                    DropdownMenu(expanded = overflow, onDismissRequest = { overflow = false }) {
                        DropdownMenuItem(text = { Text("Select") }, leadingIcon = { Icon(Icons.Outlined.Checklist, null) },
                            enabled = shown.isNotEmpty(), onClick = { overflow = false; shown.firstOrNull()?.let { selected.add(it.id) } })
                        if (failed.isNotEmpty()) DropdownMenuItem(text = { Text("Retry all failed") }, leadingIcon = { Icon(Icons.Outlined.Refresh, null) },
                            onClick = { overflow = false; failed.forEach { DownloadService.retry(ctx, it.id) } })
                        DropdownMenuItem(text = { Text("Clear finished from list") }, leadingIcon = { Icon(Icons.Outlined.DoneAll, null) },
                            enabled = jobs.any { it.finished }, onClick = { overflow = false; confirmClear = true })
                    }
                }
            }
        },
        floatingActionButton = {
            if (!selecting) ExtendedFloatingActionButton(onClick = { addLink = true },
                icon = { Icon(Icons.Outlined.Add, null) }, text = { Text("Add") })
        },
        bottomBar = { SpeedStrip(jobs) },
    ) { pad ->
        Column(Modifier.fillMaxSize().padding(pad)) {
            if (searching) OutlinedTextField(
                value = query, onValueChange = { query = it }, singleLine = true,
                placeholder = { Text("Search by name, site or link") },
                leadingIcon = { Icon(Icons.Outlined.Search, null) },
                shape = RoundedCornerShape(14.dp), modifier = Modifier.fillMaxWidth().padding(start = 16.dp, end = 16.dp, top = 10.dp),
            )
            // Filters with counts. Empty type filters stay hidden so the row stays short.
            Row(Modifier.horizontalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = 10.dp),
                horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Filter.entries.forEach { f ->
                    val n = counts[f] ?: 0
                    if (f.ordinal > Filter.DONE.ordinal && n == 0 && filter != f) return@forEach
                    FilterChip(
                        selected = filter == f, onClick = { filter = f },
                        label = { Text(f.label) },
                        trailingIcon = if (n > 0) ({
                            Text("$n", style = MaterialTheme.typography.labelSmall, fontWeight = FontWeight.Bold,
                                color = when {
                                    f == Filter.FAILED -> AnySaveColors.status.err
                                    f == Filter.ACTIVE -> AnySaveColors.status.progress
                                    else -> MaterialTheme.colorScheme.onSurfaceVariant
                                })
                        }) else null,
                        shape = RoundedCornerShape(10.dp),
                    )
                }
            }
            // The desktop toolbar's two big buttons, when they'd do something.
            if (!selecting && (pausable.isNotEmpty() || resumable.isNotEmpty())) Row(
                Modifier.padding(start = 8.dp, end = 16.dp), verticalAlignment = Alignment.CenterVertically,
            ) {
                TextButton(onClick = { resumable.forEach { DownloadService.resume(ctx, it.id) } }, enabled = resumable.isNotEmpty()) {
                    Icon(Icons.Outlined.PlayArrow, null, Modifier.size(18.dp)); Spacer(Modifier.width(4.dp)); Text("Resume all")
                }
                TextButton(onClick = { pausable.forEach { DownloadService.pause(ctx, it.id) } }, enabled = pausable.isNotEmpty()) {
                    Icon(Icons.Outlined.Pause, null, Modifier.size(18.dp)); Spacer(Modifier.width(4.dp)); Text("Pause all")
                }
            }
            LazyColumn(
                Modifier.weight(1f).fillMaxWidth(),
                contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 2.dp, bottom = 96.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                // The normal order reads like a phone app: what's going on, then saved files by day.
                val groups: List<Pair<String?, List<Job>>> = if (sort != Sort.NORMAL) listOf(null to shown) else buildList {
                    val open = shown.filter { !it.finished }
                    if (open.isNotEmpty()) add("In progress & waiting" to open)
                    shown.filter { it.status == JobStatus.DONE }.groupBy { dayGroup(it.createdAt) }.forEach { (d, l) -> add(d to l) }
                    val bad = shown.filter { it.status == JobStatus.ERROR || it.status == JobStatus.CANCELLED }
                    if (bad.isNotEmpty()) add("Couldn't download" to bad)
                }
                groups.forEach { (title, list) ->
                    if (title != null) item(key = "h-$title") { SectionLabel(title) }
                    items(list, key = { it.id }) { j ->
                        DownloadRow(
                            j, selecting = selecting, selected = j.id in selected,
                            onToggleSelect = { if (j.id in selected) selected.remove(j.id) else selected.add(j.id) },
                            onLongPress = { if (j.id !in selected) selected.add(j.id) },
                        )
                    }
                }
                if (shown.isEmpty()) item {
                    if (query.isNotBlank() || filter != Filter.ALL) Text("Nothing matches.", Modifier.padding(vertical = 32.dp),
                        color = MaterialTheme.colorScheme.onSurfaceVariant)
                    else EmptyState()
                }
            }
        }
    }

    if (addLink) AddDownloadDialog(onLookUp = { addLink = false; onLookUp(it) }) { addLink = false }

    if (confirmClear) AlertDialog(
        onDismissRequest = { confirmClear = false },
        title = { Text("Clear finished from the list?") },
        text = { Text("Only the list is cleared. Your files stay in Downloads/AnySave.") },
        confirmButton = { TextButton(onClick = { Downloads.clearFinished(); confirmClear = false }) { Text("Clear") } },
        dismissButton = { TextButton(onClick = { confirmClear = false }) { Text("Cancel") } },
    )
    if (confirmRemove) {
        val sel = jobs.filter { it.id in selected }
        val running = sel.count { !it.finished }
        AlertDialog(
            onDismissRequest = { confirmRemove = false },
            title = { Text("Remove ${sel.size} from the list?") },
            text = { Text(if (running > 0) "$running of them haven't finished and will be cancelled. Saved files stay in Downloads/AnySave."
                else "Only the list changes. Your files stay in Downloads/AnySave.") },
            confirmButton = { TextButton(onClick = {
                sel.filter { !it.finished }.forEach { DownloadService.cancel(ctx, it.id) }
                sel.forEach { Downloads.remove(it.id) }
                selected.clear(); confirmRemove = false
            }) { Text("Remove") } },
            dismissButton = { TextButton(onClick = { confirmRemove = false }) { Text("Cancel") } },
        )
    }
}

/**
 * Paste a link. A file shows its name and size first (and can be renamed); a video page goes to Home,
 * which shows the qualities with their sizes. Several links or a pattern start straight away.
 */
@Composable
private fun AddDownloadDialog(onLookUp: (String) -> Unit, onClose: () -> Unit) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    var link by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var offer by remember { mutableStateOf<FileOffer?>(null) }
    LaunchedEffect(Unit) {
        val clip = ctx.getSystemService(android.content.ClipboardManager::class.java).primaryClip
        Engine.extractUrl(clip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(ctx)?.toString())?.let { link = it }
    }
    offer?.let { o ->
        FileOfferDialog(o, onStart = { name -> DownloadService.startFile(ctx, o.url, name, o.mime); onClose() }, onClose = onClose)
        return
    }
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text("Add download") },
        text = {
            Column {
                Text("Any link or batch pattern (e.g. file[01-10].zip): video pages or direct files. To check a file, paste its SHA-256 checksum after the link.",
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
                // A SHA-256 pasted after one link: the finished file is checked against it.
                val sha = Regex("""(?i)(?:^|[\s,;])(?:sha-?256\s*[:=]?\s*)?([0-9a-f]{64})(?=$|[\s,;])""").find(link)?.groupValues?.get(1)?.lowercase()
                busy = true
                scope.launch {
                    if (sha != null && urls.size == 1) {
                        val url = urls.first()
                        val probe = withContext(Dispatchers.IO) { FileKinds.probe(url) }
                        DownloadService.startFile(ctx, url, probe?.name, probe?.mime, sha256 = sha)
                        onClose()
                    } else if (urls.size > 1) {
                        urls.forEach { DownloadService.startLink(ctx, it) }
                        onClose()
                    } else {
                        val url = urls.first()
                        val known = Engine.siteOf(url) != null
                        val probe = if (known) null else withContext(Dispatchers.IO) { FileKinds.probe(url) }
                        when {
                            !known && (FileKinds.looksLikeFile(url) || probe?.isFile == true) -> {
                                if (Settings.askBeforeAdd.value) { offer = FileOffer.of(url, probe); busy = false }
                                else { DownloadService.startFile(ctx, url, probe?.name, probe?.mime); onClose() }
                            }
                            Settings.askBeforeAdd.value -> onLookUp(url)
                            else -> { DownloadService.startQuick(ctx, url); onClose() }
                        }
                    }
                }
            }) { Text(if (busy) "Checking…" else "Next") }
        },
        dismissButton = { TextButton(onClick = onClose) { Text("Cancel") } },
    )
}

/** What a file link turned out to be, shown before it downloads. */
data class FileOffer(val url: String, val name: String, val mime: String?, val length: Long) {
    companion object {
        fun of(url: String, p: FileKinds.Probe?) = FileOffer(
            url, p?.name ?: android.net.Uri.decode(url.substringBefore('?').substringBefore('#').substringAfterLast('/')).ifBlank { "download" },
            p?.mime, p?.length ?: -1,
        )
    }
}

/** The "before you download" window for a file: name (editable), size, where it goes. */
@Composable
fun FileOfferDialog(o: FileOffer, onStart: (String) -> Unit, onClose: () -> Unit) {
    var name by remember { mutableStateOf(o.name) }
    val ext = FileKinds.extOf(o.name)
    // Keep the file type when someone renames without typing it.
    fun finalName() = name.trim().ifBlank { o.name }.let { if (ext.isNotEmpty() && FileKinds.extOf(it).isEmpty()) "$it.$ext" else it }
    val probeJob = Job(id = "", url = o.url, title = o.name, fileName = o.name, mime = o.mime, kind = JobKind.FILE)
    val folder = if (Settings.sortIntoFolders.value) "Downloads/AnySave/${probeJob.category.folder}" else "Downloads/AnySave"
    AlertDialog(
        onDismissRequest = onClose,
        icon = { FileBadge(probeJob, 48.dp) },
        title = { Text(o.name, maxLines = 2, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.titleMedium) },
        text = {
            Column {
                Text(listOfNotNull(Engine.humanSize(o.length) ?: "Size not given by the site", Engine.siteLabel(o.url)).joinToString(" · "),
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Spacer(Modifier.height(14.dp))
                OutlinedTextField(value = name, onValueChange = { name = it }, singleLine = true, label = { Text("Save as") },
                    modifier = Modifier.fillMaxWidth(), shape = RoundedCornerShape(12.dp))
                Spacer(Modifier.height(8.dp))
                Text("Saved in $folder", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        },
        confirmButton = { Button(onClick = { onStart(finalName()) }) { Text("Start download") } },
        dismissButton = { TextButton(onClick = onClose) { Text("Cancel") } },
    )
}


/** Compare a finished file with a checksum from the site it came from. */
@Composable
fun ChecksumDialog(job: Job, onClose: () -> Unit) {
    val ctx = LocalContext.current
    var typed by remember { mutableStateOf("") }
    var actual by remember { mutableStateOf(job.sha256) }
    var failed by remember { mutableStateOf(false) }
    LaunchedEffect(job.id) {
        if (actual == null) {
            val sum = withContext(Dispatchers.IO) {
                runCatching {
                    val md = java.security.MessageDigest.getInstance("SHA-256")
                    ctx.contentResolver.openInputStream(job.uri!!)!!.use { input ->
                        val buf = ByteArray(64 * 1024)
                        while (true) { val n = input.read(buf); if (n <= 0) break; md.update(buf, 0, n) }
                    }
                    md.digest().joinToString("") { "%02x".format(it) }
                }.getOrNull()
            }
            if (sum == null) failed = true else { actual = sum; Downloads.update(job.id) { it.copy(sha256 = sum) } }
        }
    }
    val want = typed.replace(Regex("""(?i)^\s*sha-?256\s*[:=]?\s*"""), "").trim().lowercase()
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text("Check SHA-256") },
        text = {
            Column {
                Text(displayName(job), maxLines = 1, overflow = TextOverflow.Ellipsis, fontWeight = FontWeight.Medium)
                Spacer(Modifier.height(8.dp))
                when {
                    failed -> Text("Couldn't read the file. It may have been moved or deleted.", color = MaterialTheme.colorScheme.error)
                    actual == null -> Text("Reading the file…")
                    else -> Text("This file: $actual", style = MaterialTheme.typography.bodySmall)
                }
                Spacer(Modifier.height(10.dp))
                OutlinedTextField(value = typed, onValueChange = { typed = it }, singleLine = true,
                    placeholder = { Text("Paste the checksum to compare") }, modifier = Modifier.fillMaxWidth())
                if (want.isNotEmpty() && actual != null) {
                    Spacer(Modifier.height(8.dp))
                    when {
                        !want.matches(Regex("[0-9a-f]{64}")) -> Text("A SHA-256 checksum has 64 characters (0-9 and a-f).",
                            style = MaterialTheme.typography.bodySmall)
                        want == actual -> Text("✓ It matches. The file is exactly the one the site published.",
                            color = AnySaveColors.status.ok, fontWeight = FontWeight.SemiBold)
                        else -> Text("✗ It does NOT match. The file may be damaged or changed. Download it again.",
                            color = MaterialTheme.colorScheme.error)
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = onClose) { Text("Close") } },
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

fun shareFile(ctx: Context, job: Job) {
    val i = Intent(Intent.ACTION_SEND).setType(job.mime).putExtra(Intent.EXTRA_STREAM, job.uri)
        .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
    ctx.startActivity(Intent.createChooser(i, "Share"))
}

fun deleteFile(ctx: Context, job: Job) {
    val ok = runCatching { job.uri?.let { ctx.contentResolver.delete(it, null, null) } ?: 0 }.getOrDefault(0) > 0
    Downloads.remove(job.id)
    Toast.makeText(
        ctx,
        if (ok) "Deleted" else "Removed from the list. Delete the file from your Files app (Downloads/AnySave).",
        Toast.LENGTH_LONG
    ).show()
}
