package com.vidgrab.app

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
    val active = jobs.filter { it.active }

    LazyColumn(
        Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 24.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(14.dp)
    ) {
        item {
            Row(verticalAlignment = Alignment.CenterVertically) {
                AnimatedLogo(64.dp)
                Spacer(Modifier.width(14.dp))
                Column {
                    Text("VidGrab", style = MaterialTheme.typography.headlineLarge, fontWeight = FontWeight.Bold)
                    Text("Save videos from your favourite apps", style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        }
        item { LinkInput(vm) }
        item { SiteRow() }

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

        vm.clipboardLink?.let { link -> item { ClipboardCard(vm, link) } }

        if (vm.loading) item { LoadingCard() }
        vm.error?.let { item { LookupError(vm, it) } }
        vm.info?.let { item { InfoCard(vm, it) } }

        if (active.isNotEmpty()) {
            item {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("Downloading now", style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
                    TextButton(onClick = { vm.tab = MainActivity.TAB_DOWNLOADS }) { Text("See all") }
                }
            }
            items(active, key = { it.id }) { ActiveJobCard(it) }
        }

        if (vm.info == null && !vm.loading && active.isEmpty()) item { HowToCard() }
        item { MadeBy(Modifier.padding(top = 8.dp)) }
    }
}

@Composable
private fun LinkInput(vm: MainViewModel) {
    val ctx = LocalContext.current
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        OutlinedTextField(
            value = vm.url,
            onValueChange = { vm.url = it; vm.error = null },
            placeholder = { Text("Paste a video link") },
            leadingIcon = { Icon(Icons.Outlined.Link, null) },
            trailingIcon = {
                if (vm.url.isNotEmpty()) IconButton(onClick = { vm.url = ""; vm.info = null; vm.error = null }) {
                    Icon(Icons.Outlined.Close, "Clear")
                } else IconButton(onClick = {
                    val clip = ctx.getSystemService(ClipboardManager::class.java).primaryClip
                    val text = clip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(ctx)?.toString()
                    Engine.extractUrl(text)?.let { vm.lookUp(it) } ?: vm.messages.tryEmit("No link on the clipboard")
                }) { Icon(Icons.Outlined.ContentPaste, "Paste") }
            },
            singleLine = true,
            shape = RoundedCornerShape(16.dp),
            modifier = Modifier.fillMaxWidth(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go),
            keyboardActions = KeyboardActions(onGo = { vm.lookUp() }),
        )
        Button(
            onClick = { vm.lookUp() },
            enabled = !vm.loading && vm.url.isNotBlank(),
            modifier = Modifier.fillMaxWidth().height(50.dp),
            shape = RoundedCornerShape(16.dp),
        ) { Text("Find video", fontWeight = FontWeight.SemiBold) }
    }
}

@Composable
private fun SiteRow() {
    Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
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
    }
}

@Composable
private fun ClipboardCard(vm: MainViewModel, link: String) {
    ElevatedCard {
        Column(Modifier.padding(14.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.ContentCopy, null, tint = MaterialTheme.colorScheme.primary)
                Spacer(Modifier.width(10.dp))
                Column(Modifier.weight(1f)) {
                    Text("Copied ${Engine.siteOf(link)} link", fontWeight = FontWeight.SemiBold)
                    Text(link, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis,
                        color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            Row(Modifier.padding(top = 10.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = { vm.quickDownload(link) }) { Text("Download") }
                OutlinedButton(onClick = { vm.clipboardLink = null; vm.lookUp(link) }) { Text("Choose quality") }
                TextButton(onClick = { vm.clipboardLink = null }) { Text("Dismiss") }
            }
        }
    }
}

@Composable
private fun LoadingCard() {
    Card {
        Column(Modifier.padding(14.dp)) {
            Box(Modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(RoundedCornerShape(12.dp))
                .background(MaterialTheme.colorScheme.surfaceVariant), contentAlignment = Alignment.Center) {
                CircularProgressIndicator()
            }
            Text("Looking up video…", Modifier.padding(top = 10.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun LookupError(vm: MainViewModel, err: String) {
    val site = Engine.siteOf(vm.url)
    val needsLogin = site == "Instagram" || site == "Facebook"
    val loggedIn = LoginActivity.loggedInSites(LocalContext.current)
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
private fun InfoCard(vm: MainViewModel, info: Engine.Info) {
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

                QualityDropdown(info.options, vm.selected) { vm.selected = it }
                Button(
                    onClick = { vm.download() },
                    modifier = Modifier.fillMaxWidth().padding(top = 12.dp).height(50.dp),
                    shape = RoundedCornerShape(16.dp),
                ) {
                    Icon(Icons.Outlined.Download, null)
                    Spacer(Modifier.width(8.dp))
                    Text("Download", fontWeight = FontWeight.SemiBold)
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
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant)) {
        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            Box {
                Thumb(job, Modifier.size(width = 80.dp, height = 48.dp))
                DownloadingBadge(Modifier.align(Alignment.BottomEnd).offset(x = 6.dp, y = 6.dp))
            }
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(job.title, maxLines = 1, overflow = TextOverflow.Ellipsis, fontWeight = FontWeight.Medium)
                Spacer(Modifier.height(6.dp))
                if (job.percent > 0f) LinearProgressIndicator({ job.percent / 100f }, Modifier.fillMaxWidth())
                else LinearProgressIndicator(Modifier.fillMaxWidth())
                Text(statusText(job), style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 4.dp))
            }
            IconButton(onClick = { DownloadService.cancel(ctx, job.id) }) { Icon(Icons.Outlined.Close, "Cancel") }
        }
    }
}

fun statusText(job: Job): String = when (job.status) {
    JobStatus.QUEUED -> "Waiting…"
    JobStatus.WAITING_WIFI -> "Waiting for Wi-Fi…"
    JobStatus.DOWNLOADING ->
        if (job.percent <= 0f && job.speed.isEmpty()) "Starting…"
        else listOf("${job.percent.toInt()}%", job.speed, "ETA ${job.eta}").filter { it.isNotBlank() }.joinToString(" · ")
    JobStatus.SAVING -> "Saving to Downloads…"
    JobStatus.DONE -> "Saved"
    JobStatus.CANCELLED -> "Cancelled"
    JobStatus.ERROR -> job.error ?: "Failed"
}

@Composable
private fun HowToCard() {
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            Text("Faster ways to download", style = MaterialTheme.typography.titleMedium)
            Tip(Icons.Outlined.ContentCopy, "Copy, then open VidGrab", "A copied video link starts downloading by itself.")
            Tip(Icons.Outlined.Share, "Share → Download", "From YouTube, Instagram, TikTok or Facebook. You stay in that app.")
            Tip(Icons.Outlined.TouchApp, "Quick Settings tile", "Copy a link, swipe down, tap VidGrab. Add it in Settings.")
        }
    }
}

fun fmtDuration(s: Double): String {
    val t = s.toInt()
    val h = t / 3600; val m = t % 3600 / 60; val sec = t % 60
    return if (h > 0) "%d:%02d:%02d".format(h, m, sec) else "%d:%02d".format(m, sec)
}
