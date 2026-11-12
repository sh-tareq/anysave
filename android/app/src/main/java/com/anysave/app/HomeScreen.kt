package com.anysave.app

import android.content.ClipboardManager
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Pause
import androidx.compose.material.icons.outlined.PlayArrow
import androidx.compose.material.icons.outlined.Language
import androidx.compose.material.icons.outlined.TravelExplore
import androidx.compose.material.icons.outlined.Transform
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.ContentPaste
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.MusicNote
import androidx.compose.material.icons.outlined.CheckCircle
import androidx.compose.material.icons.outlined.HighQuality
import androidx.compose.material.icons.outlined.Videocam
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material.icons.outlined.TouchApp
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.VideoLibrary
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.DpSize
import androidx.compose.ui.unit.dp
import coil.compose.AsyncImage

val SiteColors = mapOf(
    "YouTube" to Color(0xFFFF0033),
    "Instagram" to Color(0xFFD62976),
    "TikTok" to Color(0xFF25F4EE),
    "Facebook" to Color(0xFF1877F2),
)

@Composable
fun HomeScreen(vm: MainViewModel, jobs: List<Job>) {
    val engine by Engine.state.collectAsState()
    val index = remember(jobs) { JobIndex(jobs) }
    val playlist = vm.playlist
    // Jobs already shown on the video card or in the playlist aren't listed twice.
    val shown = buildSet {
        if (vm.info != null) vm.infoJobId?.let { add(it) }
        if (playlist != null) addAll(vm.playlistStarted.values)
    }
    val active = jobs.filter { it.active && it.id !in shown }

    ScreenScaffold(
        title = "AnySave",
        subtitle = "Download videos & files. Convert anything.",
        leading = { androidx.compose.foundation.Image(androidx.compose.ui.res.painterResource(R.drawable.anysave_logo), null, Modifier.size(34.dp)) },
    ) { pad ->
    Box(Modifier.fillMaxSize().padding(pad)) {
    LazyColumn(
        Modifier.fillMaxSize(),
        // Room for the playlist's download button that floats at the bottom.
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 14.dp, bottom = if (playlist != null) 96.dp else 24.dp),
        verticalArrangement = Arrangement.spacedBy(14.dp)
    ) {
        item { LinkInput(vm) }
        item { SiteRowWrapped() }
        item { AdLibrariesCard() }

        if (engine == Engine.State.STARTING) item {
            Card(Modifier.pulse(), colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.secondaryContainer)) {
                Column(Modifier.padding(14.dp)) {
                    Text("Getting ready…", fontWeight = FontWeight.SemiBold)
                    Text("Setting up and updating the downloader. Only slow the first time.",
                        style = MaterialTheme.typography.bodySmall)
                    LinearProgressIndicator(Modifier.fillMaxWidth().padding(top = 8.dp))
                }
            }
        }
        if (engine == Engine.State.FAILED) item {
            ErrorCard("Setup failed: ${Engine.startError}", null)
        }

        if (vm.clipboardLinks.isNotEmpty()) item { ClipboardCard(vm, vm.clipboardLinks) }

        if (vm.loading) item { LoadingCard(if (vm.playlistLoading && !Engine.isYoutubeVideo(vm.url)) "Listing videos…" else "Looking up video…") }
        vm.error?.let { item { LookupError(vm, it) } }
        vm.info?.let { item { InfoCard(vm, it, vm.infoJobId?.let { id -> index[id] }) } }
        if (vm.playlistLoading && !vm.loading) item {
            Card {
                Column(Modifier.padding(14.dp)) {
                    Text("Listing the playlist's videos…", color = MaterialTheme.colorScheme.onSurfaceVariant)
                    LinearProgressIndicator(Modifier.fillMaxWidth().padding(top = 8.dp))
                }
            }
        }
        if (playlist != null) {
            item(key = "pl-head") { PlaylistHeader(vm, playlist, index) }
            items(playlist.entries, key = { "pl-" + it.url }) { e ->
                BatchRow(
                    title = e.title, subtitle = e.duration?.let { fmtDuration(it) }.orEmpty(),
                    thumbnail = e.thumbnail, thumbSize = DpSize(96.dp, 54.dp),
                    job = index.find(vm.playlistStarted[e.url], e.url), selected = e.url in vm.playlistSelected,
                    onSelect = { if (it) vm.playlistSelected += e.url else vm.playlistSelected -= e.url },
                    noun = "video", onDownload = { vm.downloadEntry(e) },
                )
            }
        }

        if (active.isNotEmpty()) {
            item {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("Downloading now", style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
                    TextButton(onClick = { vm.tab = MainActivity.TAB_DOWNLOADS }) { Text("See all") }
                }
            }
            items(active, key = { it.id }) { ActiveJobCard(it) }
        }

        if (vm.info == null && playlist == null && !vm.loading && active.isEmpty()) item { HowToCard() }
        item { MadeBy(Modifier.padding(top = 8.dp)) }
    }
    // The playlist's button stays in view while scrolling through its videos.
    if (playlist != null) Surface(
        Modifier.align(Alignment.BottomCenter).fillMaxWidth(),
        color = MaterialTheme.colorScheme.surface, tonalElevation = 3.dp, shadowElevation = 6.dp,
    ) {
        BatchButton(vm.playlistSelected.size, "video", vm.playlistBatch.mapNotNull { index[it] }, onDownloadSelected = {
            vm.downloadPlaylist(playlist.entries.filter { it.url in vm.playlistSelected })
        }, modifier = Modifier.padding(horizontal = 16.dp, vertical = 10.dp))
    }
    }
    }
}

/** Title, Select all and the quality for a playlist or channel; its videos follow as rows. */
@Composable
private fun PlaylistHeader(vm: MainViewModel, p: Engine.Playlist, index: JobIndex) {
    val open = p.entries.filter { index.find(vm.playlistStarted[it.url], it.url).isOpen() }.map { it.url }
    // Rows that started downloading can't stay ticked.
    LaunchedEffect(open.size) { vm.playlistSelected.retainAll(open.toSet()) }
    ElevatedCard {
        Column(Modifier.padding(14.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.VideoLibrary, null, tint = MaterialTheme.colorScheme.primary)
                Spacer(Modifier.width(10.dp))
                Column(Modifier.weight(1f)) {
                    Text(p.title, fontWeight = FontWeight.SemiBold, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    val count = if (p.total != null && p.total > p.entries.size) "First ${p.entries.size} of ${p.total} videos"
                        else plural(p.entries.size, "video")
                    Text(listOfNotNull(count, p.uploader).joinToString(" · "), style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
            QualityDropdown(Engine.batchOptions, vm.playlistQuality) { vm.playlistQuality = it }
            if (open.isNotEmpty()) Row(Modifier.padding(top = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                Text("Tick the videos you want, or", style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.weight(1f))
                SelectAllToggle(open, vm.playlistSelected)
            }
        }
    }
}

@Composable
private fun LinkInput(vm: MainViewModel) {
    val ctx = LocalContext.current
    val links = remember(vm.url) { Engine.extractUrls(vm.url) }
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        OutlinedTextField(
            value = vm.url,
            onValueChange = { vm.url = it; vm.error = null },
            placeholder = { Text("Paste a video, playlist or file link", maxLines = 1, overflow = TextOverflow.Ellipsis) },
            leadingIcon = { Icon(Icons.Outlined.Link, null) },
            trailingIcon = {
                if (vm.url.isNotEmpty()) IconButton(onClick = { vm.url = ""; vm.clearLookup() }) {
                    Icon(Icons.Outlined.Close, "Clear")
                } else IconButton(onClick = {
                    val clip = ctx.getSystemService(ClipboardManager::class.java).primaryClip
                    val text = clip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(ctx)?.toString()
                    val links = Engine.extractUrls(text)
                    when {
                        links.isEmpty() -> vm.messages.tryEmit("No link on the clipboard")
                        // Several: keep them all in the field, which offers "Download all N links".
                        links.size > 1 -> { vm.clearLookup(); vm.url = links.joinToString(" ") }
                        else -> vm.lookUp(links[0])
                    }
                }) { Icon(Icons.Outlined.ContentPaste, "Paste") }
            },
            singleLine = true,
            shape = RoundedCornerShape(16.dp),
            modifier = Modifier.fillMaxWidth(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go),
            keyboardActions = KeyboardActions(onGo = { if (links.size > 1) vm.downloadAll(links) else vm.lookUp() }),
        )
        if (links.size > 1) {
            Button(
                onClick = { vm.downloadAll(links) },
                modifier = Modifier.fillMaxWidth().height(50.dp),
                shape = RoundedCornerShape(16.dp),
            ) {
                Icon(Icons.Outlined.Download, null); Spacer(Modifier.width(8.dp))
                Text("Download all ${links.size} links", fontWeight = FontWeight.SemiBold)
            }
            TextButton(onClick = { vm.lookUp(links[0]) }, modifier = Modifier.fillMaxWidth()) {
                Text("Choose quality for the first link only")
            }
        } else Button(
            onClick = { vm.lookUp() },
            enabled = !vm.loading && vm.url.isNotBlank(),
            modifier = Modifier.fillMaxWidth().height(50.dp),
            shape = RoundedCornerShape(16.dp),
        ) { Text("Download", fontWeight = FontWeight.SemiBold) }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun SiteRowWrapped() {
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        SiteColors.keys.forEach { name ->
            Row(
                Modifier.clip(RoundedCornerShape(50)).background(MaterialTheme.colorScheme.surfaceVariant)
                    .padding(start = 6.dp, end = 12.dp, top = 6.dp, bottom = 6.dp),
                verticalAlignment = Alignment.CenterVertically
            ) {
                SiteIcon(name, 22.dp)
                Spacer(Modifier.width(7.dp))
                Text(name, style = MaterialTheme.typography.labelMedium, maxLines = 1, softWrap = false)
            }
        }
        Row(
            Modifier.clip(RoundedCornerShape(50)).background(MaterialTheme.colorScheme.primaryContainer)
                .padding(horizontal = 12.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text("+ 1,000 more", style = MaterialTheme.typography.labelMedium, maxLines = 1, softWrap = false,
                color = MaterialTheme.colorScheme.onPrimaryContainer)
        }
    }
}

/** Opens Meta Ad Library / TikTok Creative Center in the built-in ad browser. */
@Composable
private fun AdLibrariesCard() {
    val ctx = LocalContext.current
    ElevatedCard {
        Column(Modifier.padding(14.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.TravelExplore, null, tint = MaterialTheme.colorScheme.primary)
                Spacer(Modifier.width(10.dp))
                Column {
                    Text("Browse & ad libraries", fontWeight = FontWeight.SemiBold)
                    Text("Open any website, or browse ads, and save their videos", style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            FilledTonalButton(
                onClick = { ctx.startActivity(AdLibraryActivity.intent(ctx, AdLibraryActivity.Library.WEB)) },
                modifier = Modifier.fillMaxWidth().padding(top = 12.dp),
            ) {
                Icon(Icons.Outlined.Language, null, Modifier.size(20.dp)); Spacer(Modifier.width(8.dp))
                Text("Browse any website")
            }
            Row(Modifier.padding(top = 8.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                FilledTonalButton(
                    onClick = { ctx.startActivity(AdLibraryActivity.intent(ctx, AdLibraryActivity.Library.META)) },
                    modifier = Modifier.weight(1f), contentPadding = PaddingValues(horizontal = 10.dp, vertical = 10.dp)
                ) {
                    SiteIcon("Facebook", 20.dp); Spacer(Modifier.width(8.dp))
                    Text("Meta Ad Library", maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.labelLarge)
                }
                FilledTonalButton(
                    onClick = { ctx.startActivity(AdLibraryActivity.intent(ctx, AdLibraryActivity.Library.TIKTOK)) },
                    modifier = Modifier.weight(1f), contentPadding = PaddingValues(horizontal = 10.dp, vertical = 10.dp)
                ) {
                    SiteIcon("TikTok", 20.dp); Spacer(Modifier.width(8.dp))
                    Text("TikTok Creative", maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.labelLarge)
                }
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun ClipboardCard(vm: MainViewModel, links: List<String>) {
    val link = links.first()
    val many = links.size > 1
    ElevatedCard {
        Column(Modifier.padding(14.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.ContentCopy, null, tint = MaterialTheme.colorScheme.primary)
                Spacer(Modifier.width(10.dp))
                Column(Modifier.weight(1f)) {
                    Text(when {
                        many -> "Copied ${links.size} links"
                        Engine.siteOf(link) != null -> "Copied ${Engine.siteOf(link)} link"
                        else -> "Copied file link"
                    }, fontWeight = FontWeight.SemiBold)
                    Text(if (many) links.joinToString("  ·  ") { Engine.siteLabel(it) } else link, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis,
                        color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            // FlowRow: three buttons don't fit on one line on small phones.
            FlowRow(Modifier.padding(top = 10.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                if (many) Button(onClick = { vm.downloadAll(links) }) { Text("Download all ${links.size}") }
                else Button(onClick = { vm.quickDownload(link) }) { Text("Download") }
                if (!many && Engine.siteOf(link) != null)
                    OutlinedButton(onClick = { vm.clipboardLinks = emptyList(); vm.lookUp(link) }) { Text("Choose quality") }
                TextButton(onClick = { vm.clipboardLinks = emptyList() }) { Text("Dismiss") }
            }
        }
    }
}

@Composable
private fun LoadingCard(text: String) {
    Card {
        Column(Modifier.padding(14.dp)) {
            Box(Modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(RoundedCornerShape(12.dp))
                .background(MaterialTheme.colorScheme.surfaceVariant), contentAlignment = Alignment.Center) {
                CircularProgressIndicator()
            }
            Text(text, Modifier.padding(top = 10.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun LookupError(vm: MainViewModel, err: String) {
    val site = Engine.siteOf(vm.url) ?: Engine.siteLabel(vm.url)
    val canLogIn = LoginActivity.Site.entries.any { it.label == site }
    val needsLogin = site == "Instagram" || site == "Facebook" || (canLogIn && vm.needsLogin(err))
    val ctx = LocalContext.current
    val loggedIn = LoginActivity.loggedInSites(ctx)
    if (vm.errorIsWebPage && !needsLogin) {
        // Not a site yt-dlp knows: the built-in browser can often still see the video.
        ErrorCard(
            "AnySave can't read this page directly.",
            "Open it in the built-in browser and play the video: AnySave will list the video files it finds.",
            actionLabel = "Open in browser",
            onAction = { ctx.startActivity(AdLibraryActivity.intent(ctx, AdLibraryActivity.Library.WEB, vm.url)) }
        )
        return
    }
    ErrorCard(
        err,
        when {
            needsLogin && site !in loggedIn -> "$site often needs you to be logged in. Go to Settings → Accounts."
            needsLogin -> "You're logged in to $site. The post may be private or removed."
            else -> null
        },
        actionLabel = if (needsLogin && site !in loggedIn) "Open Settings" else "Try again",
        onAction = { if (needsLogin && site !in loggedIn) vm.tab = MainActivity.TAB_SETTINGS else vm.lookUp() }
    )
}

@Composable
fun ErrorCard(msg: String, hint: String?, actionLabel: String? = null, onAction: () -> Unit = {}) {
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.errorContainer)) {
        Row(Modifier.padding(14.dp)) {
            Icon(Icons.Outlined.ErrorOutline, null, tint = MaterialTheme.colorScheme.onErrorContainer)
            Spacer(Modifier.width(10.dp))
            Column {
                Text(msg, color = MaterialTheme.colorScheme.onErrorContainer)
                hint?.let { Text(it, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(top = 6.dp),
                    color = MaterialTheme.colorScheme.onErrorContainer) }
                actionLabel?.let { TextButton(onClick = onAction, contentPadding = PaddingValues(0.dp)) { Text(it) } }
            }
        }
    }
}

@Composable
private fun InfoCard(vm: MainViewModel, info: Engine.Info, job: Job?) {
    val ctx = LocalContext.current
    Card {
        Column {
            Box {
                AsyncImage(
                    model = info.thumbnail, contentDescription = null, contentScale = ContentScale.Crop,
                    modifier = Modifier.fillMaxWidth().aspectRatio(16f / 9f)
                        .clip(RoundedCornerShape(topStart = 12.dp, topEnd = 12.dp))
                        .background(MaterialTheme.colorScheme.surfaceVariant)
                )
                info.duration?.let {
                    Text(fmtDuration(it), color = Color.White, style = MaterialTheme.typography.labelMedium,
                        modifier = Modifier.align(Alignment.BottomEnd).padding(8.dp)
                            .background(Color(0xCC000000), RoundedCornerShape(6.dp)).padding(horizontal = 6.dp, vertical = 2.dp))
                }
                Engine.siteOf(info.url)?.let { site ->
                    Row(
                        Modifier.align(Alignment.TopStart).padding(8.dp)
                            .background(Color(0xCC000000), RoundedCornerShape(50))
                            .padding(start = 4.dp, end = 10.dp, top = 4.dp, bottom = 4.dp),
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        SiteIcon(site, 20.dp)
                        Spacer(Modifier.width(6.dp))
                        Text(site, color = Color.White, style = MaterialTheme.typography.labelMedium)
                    }
                }
            }
            Column(Modifier.padding(14.dp)) {
                Text(info.title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold,
                    maxLines = 2, overflow = TextOverflow.Ellipsis)
                info.uploader?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }

                // A different quality is a new download: the button goes back to "Download".
                QualityDropdown(info.options, vm.selected) { vm.selected = it; vm.infoJobId = null }
                val button = Modifier.fillMaxWidth().padding(top = 12.dp).height(50.dp)
                val shape = RoundedCornerShape(16.dp)
                when (job?.status) {
                    null, JobStatus.CANCELLED -> Button(onClick = { vm.download() }, modifier = button, shape = shape) {
                        Icon(Icons.Outlined.Download, null)
                        Spacer(Modifier.width(8.dp))
                        Text("Download", fontWeight = FontWeight.SemiBold)
                    }
                    JobStatus.DONE -> Button(onClick = { openFile(ctx, job) }, modifier = button, shape = shape) {
                        Icon(Icons.Outlined.CheckCircle, null)
                        Spacer(Modifier.width(8.dp))
                        Text("Saved ✓ · Open", fontWeight = FontWeight.SemiBold)
                    }
                    JobStatus.ERROR -> {
                        Button(
                            onClick = { DownloadService.retry(ctx, job.id) }, modifier = button, shape = shape,
                            colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error,
                                contentColor = MaterialTheme.colorScheme.onError),
                        ) {
                            Icon(Icons.Outlined.Refresh, null)
                            Spacer(Modifier.width(8.dp))
                            Text("Failed · Try again", fontWeight = FontWeight.SemiBold)
                        }
                        job.error?.let {
                            Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error,
                                maxLines = 3, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 6.dp))
                        }
                    }
                    JobStatus.QUEUED -> ProgressButton(null, "Queued", button, shape)
                    JobStatus.WAITING_WIFI -> ProgressButton(null, "Waiting for Wi-Fi…", button, shape)
                    JobStatus.PAUSED -> ProgressButton(job.percent / 100f, "Paused · ${job.percent.toInt()}% · Tap to resume", button, shape) {
                        DownloadService.resume(ctx, job.id)
                    }
                    JobStatus.SAVING -> ProgressButton(1f, "Saving…", button, shape)
                    JobStatus.DOWNLOADING -> {
                        val started = job.percent > 0f
                        val text = if (!started) job.eta.takeIf { it.endsWith("…") } ?: "Starting…"
                            else listOf("${job.percent.toInt()}%", job.speed, job.eta.takeIf { it.isNotBlank() && it != "?" }?.let { "$it left" }.orEmpty())
                                .filter { it.isNotBlank() }.joinToString(" · ")
                        ProgressButton(if (started) job.percent / 100f else null, text, button, shape)
                    }
                }
            }
        }
    }
}

/** Selected quality in a field; tap to open the full list (with sizes) in a dropdown. */
@Suppress("DEPRECATION") // menuAnchor() without arguments
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun QualityDropdown(options: List<Engine.Option>, selected: Int, onSelect: (Int) -> Unit) {
    var open by remember { mutableStateOf(false) }
    val current = options[selected]
    fun subtitle(o: Engine.Option) = Engine.humanSize(o.sizeBytes)?.let { "~$it" }

    ExposedDropdownMenuBox(expanded = open, onExpandedChange = { open = it },
        modifier = Modifier.padding(top = 14.dp)) {
        OutlinedTextField(
            value = current.label + (subtitle(current)?.let { "  ·  $it" } ?: ""),
            onValueChange = {},
            readOnly = true,
            label = { Text("Quality") },
            leadingIcon = { Icon(if (current.audioOnly) Icons.Outlined.MusicNote else Icons.Outlined.HighQuality, null) },
            trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded = open) },
            shape = RoundedCornerShape(14.dp),
            singleLine = true,
            modifier = Modifier.fillMaxWidth().menuAnchor(),
        )
        ExposedDropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            options.forEachIndexed { i, o ->
                DropdownMenuItem(
                    text = {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Text(o.label, Modifier.weight(1f),
                                fontWeight = if (i == selected) FontWeight.SemiBold else FontWeight.Normal)
                            subtitle(o)?.let {
                                Text(it, style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                        }
                    },
                    leadingIcon = {
                        Icon(
                            when {
                                i == selected -> Icons.Outlined.CheckCircle
                                o.audioOnly -> Icons.Outlined.MusicNote
                                else -> Icons.Outlined.Videocam
                            }, null,
                            tint = if (i == selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    },
                    onClick = { onSelect(i); open = false },
                    contentPadding = ExposedDropdownMenuDefaults.ItemContentPadding,
                )
            }
        }
    }
}

@Composable
fun ActiveJobCard(job: Job) {
    val ctx = LocalContext.current
    var refreshLink by remember { mutableStateOf(false) }
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant)) {
        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            Box {
                Thumb(job, Modifier.size(width = 80.dp, height = 48.dp))
                if (job.status != JobStatus.PAUSED)
                    DownloadingBadge(Modifier.align(Alignment.BottomEnd).offset(x = 6.dp, y = 6.dp))
            }
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(job.title, maxLines = 1, overflow = TextOverflow.Ellipsis, fontWeight = FontWeight.Medium)
                Spacer(Modifier.height(6.dp))
                if (job.percent > 0f || job.status == JobStatus.PAUSED) LinearProgressIndicator({ job.percent / 100f }, Modifier.fillMaxWidth())
                else LinearProgressIndicator(Modifier.fillMaxWidth())
                Text(statusText(job), style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 4.dp))
            }
            // Pause / resume (downloads only), refresh expired link, then cancel
            if (job.status == JobStatus.PAUSED) {
                if (job.kind != JobKind.CONVERT) {
                    IconButton(onClick = { refreshLink = true }) { Icon(Icons.Outlined.Link, "Refresh expired link") }
                }
                IconButton(onClick = { DownloadService.resume(ctx, job.id) }) { Icon(Icons.Outlined.PlayArrow, "Resume") }
            } else if (job.canPause) {
                IconButton(onClick = { DownloadService.pause(ctx, job.id) }) { Icon(Icons.Outlined.Pause, "Pause") }
            }
            IconButton(onClick = { DownloadService.cancel(ctx, job.id) }) { Icon(Icons.Outlined.Close, "Cancel") }
        }
    }
    if (refreshLink) RefreshLinkDialog(job) { refreshLink = false }
}

/** "12 MB of 80 MB" for direct file downloads. */
private fun sizeProgress(job: Job): String =
    if (job.kind == JobKind.FILE && job.totalBytes > 0)
        "${Engine.humanSize(job.doneBytes) ?: "0 MB"} of ${Engine.humanSize(job.totalBytes)}" else ""

fun statusText(job: Job): String = when (job.status) {
    JobStatus.QUEUED -> "Queued"
    JobStatus.WAITING_WIFI -> "Waiting for Wi-Fi…"
    JobStatus.DOWNLOADING -> when {
        job.kind == JobKind.CONVERT && job.percent <= 0f -> job.eta.takeIf { it.endsWith("…") } ?: "Converting…"
        job.kind == JobKind.CONVERT -> "Converting · ${job.percent.toInt()}% · ETA ${job.eta}"
        job.percent <= 0f && job.speed.isEmpty() -> job.eta.takeIf { it.endsWith("…") } ?: "Starting…"
        else -> listOf("${job.percent.toInt()}%", sizeProgress(job), job.speed, "ETA ${job.eta}")
            .filter { it.isNotBlank() }.joinToString(" · ")
    }
    JobStatus.PAUSED -> listOf("Paused", "${job.percent.toInt()}%", sizeProgress(job)).filter { it.isNotBlank() }.joinToString(" · ")
    JobStatus.SAVING -> "Saving to Downloads…"
    JobStatus.DONE -> "Saved ✓"
    JobStatus.CANCELLED -> "Cancelled"
    JobStatus.ERROR -> job.error ?: "Failed"
}

@Composable
private fun HowToCard() {
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            Text("Faster ways to download", style = MaterialTheme.typography.titleMedium)
            Tip(Icons.Outlined.ContentCopy, "Copy, then open AnySave", "A copied video or file link is picked up automatically.")
            Tip(Icons.Outlined.Share, "Share → Download", "From any app or browser. You stay where you were.")
            Tip(Icons.Outlined.TouchApp, "Quick Settings tile", "Copy a link, swipe down, tap AnySave. Add it in Settings.")
            Tip(Icons.Outlined.Transform, "Convert", "Turn videos into MP3, shrink them for WhatsApp, or trim them, in the Convert tab.")
        }
    }
}

fun fmtDuration(s: Double): String {
    val t = s.toInt()
    val h = t / 3600; val m = t % 3600 / 60; val sec = t % 60
    return if (h > 0) "%d:%02d:%02d".format(h, m, sec) else "%d:%02d".format(m, sec)
}
