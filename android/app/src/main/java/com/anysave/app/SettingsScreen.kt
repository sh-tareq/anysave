package com.anysave.app

import android.app.StatusBarManager
import android.content.ClipData
import android.content.ClipboardManager
import android.content.ComponentName
import android.content.Context
import android.graphics.drawable.Icon as AndroidIcon
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.AccountCircle
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.DeleteSweep
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.FolderCopy
import androidx.compose.material.icons.outlined.Speed
import androidx.compose.material.icons.outlined.HighQuality
import androidx.compose.material.icons.outlined.Gavel
import androidx.compose.material.icons.outlined.SystemUpdate
import androidx.compose.material.icons.outlined.TouchApp
import androidx.compose.material.icons.outlined.Wifi
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp

@Composable
fun SettingsScreen(vm: MainViewModel) {
    val ctx = LocalContext.current
    val quality by Settings.quality.collectAsState()
    val autoClip by Settings.autoClipboard.collectAsState()
    val wifiOnly by Settings.wifiOnly.collectAsState()
    val maxParallel by Settings.maxParallel.collectAsState()
    val speedLimitKbps by Settings.speedLimitKbps.collectAsState()
    val sortFolders by Settings.sortIntoFolders.collectAsState()
    val subLangs by Settings.subLangs.collectAsState()
    val embedChapters by Settings.embedChapters.collectAsState()
    val embedCover by Settings.embedCover.collectAsState()
    val proxy by Settings.proxy.collectAsState()
    val scheduleOn by Settings.scheduleOn.collectAsState()
    val scheduleStart by Settings.scheduleStart.collectAsState()
    val scheduleStop by Settings.scheduleStop.collectAsState()
    val scheduleQueue by Settings.scheduleQueue.collectAsState()
    val queues by Settings.queues.collectAsState()
    val newQueue by Settings.newQueue.collectAsState()
    var pickSubs by remember { mutableStateOf(false) }
    var editProxy by remember { mutableStateOf(false) }
    var editQueues by remember { mutableStateOf(false) }
    var pickScheduleQueue by remember { mutableStateOf(false) }
    var pickNewQueue by remember { mutableStateOf(false) }
    val exportList = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/json")) { uri ->
        if (uri != null) {
            val ok = runCatching { ctx.contentResolver.openOutputStream(uri)!!.use { it.write(Backup.exportJson().toByteArray()) } }.isSuccess
            vm.messages.tryEmit(if (ok) "List exported" else "Couldn't save the file")
        }
    }
    val importList = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) {
            val text = runCatching { ctx.contentResolver.openInputStream(uri)!!.use { it.readBytes().decodeToString() } }.getOrNull()
            if (text == null) vm.messages.tryEmit("Couldn't read that file")
            else {
                val (added, skipped) = Backup.import(ctx, text)
                vm.messages.tryEmit(when {
                    added == 0 && skipped == 0 -> "No links found in that file"
                    skipped > 0 -> "Imported $added links, $skipped skipped"
                    else -> "Imported $added links"
                })
            }
        }
    }
    fun pickTime(current: String, save: (String) -> Unit) {
        val m = Schedule.minutes(current) ?: 0
        android.app.TimePickerDialog(ctx, { _, h, min -> save("%02d:%02d".format(h, min)); Schedule.tick(ctx) }, m / 60, m % 60, true).show()
    }
    var pickQuality by remember { mutableStateOf(false) }
    var showLegal by remember { mutableStateOf(false) }
    var loggedIn by remember { mutableStateOf(LoginActivity.loggedInSites(ctx)) }
    val login = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        loggedIn = LoginActivity.loggedInSites(ctx)
    }
    val appVersion = remember { ctx.packageManager.getPackageInfo(ctx.packageName, 0).versionName }

    ScreenScaffold(title = "Settings") { pad ->
    Column(
        Modifier.fillMaxSize().padding(pad).verticalScroll(rememberScrollState())
            .padding(start = 16.dp, end = 16.dp, top = 6.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp)
    ) {

        Section("Downloads")
        SettingRow(Icons.Outlined.HighQuality, "Default quality", quality.label,
            "Used for one-tap downloads (copied links, Share → Download, Quick Settings tile)") { pickQuality = true }
        SwitchRow(Icons.Outlined.ContentCopy, "Auto-download copied links",
            "Open AnySave after copying a link from a video site and it starts downloading", autoClip) { Settings.setAutoClipboard(it) }
        SwitchRow(Icons.Outlined.Wifi, "Wi-Fi only", "Wait for Wi-Fi instead of using mobile data", wifiOnly) {
            Settings.setWifiOnly(it)
        }

        Section("Download manager")
        Column(Modifier.padding(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.Speed, null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
                Spacer(Modifier.width(16.dp))
                Column(Modifier.weight(1f)) {
                    Text("Downloads at the same time")
                    Text("Applies to the next downloads you start", style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            Row(Modifier.padding(start = 40.dp, top = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                (1..5).forEach { n ->
                    FilterChip(selected = maxParallel == n, onClick = { Settings.setMaxParallel(n) }, label = { Text("$n") })
                }
            }
        }
        Column(Modifier.padding(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.Speed, null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
                Spacer(Modifier.width(16.dp))
                Column(Modifier.weight(1f)) {
                    Text("Speed limit")
                    Text("Cap total download speed so other apps stay fast", style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            val speedPresets = listOf(0 to "Unlimited", 10240 to "10 MB/s", 5120 to "5 MB/s", 2048 to "2 MB/s", 512 to "500 KB/s")
            Row(
                Modifier.padding(start = 40.dp, top = 8.dp).horizontalScroll(rememberScrollState()),
                horizontalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                speedPresets.forEach { (kbps, label) ->
                    FilterChip(selected = speedLimitKbps == kbps, onClick = { Settings.setSpeedLimitKbps(kbps) }, label = { Text(label) })
                }
            }
        }
        SwitchRow(Icons.Outlined.FolderCopy, "Sort into folders",
            "Videos, Music, Pictures, Documents, Archives, Apps, Converted", sortFolders) { Settings.setSortIntoFolders(it) }

        Section("Videos")
        SettingRow(Icons.Outlined.HighQuality, "Subtitles inside videos",
            SUB_LANGS.filter { it.first in subLangs.split(',') }.joinToString(", ") { it.second }.ifBlank { "None" },
            "A video gets the ones it has") { pickSubs = true }
        SwitchRow(Icons.Outlined.FolderCopy, "Chapter marks", "Keep chapter marks inside videos", embedChapters) {
            Settings.setEmbedChapters(it)
        }
        SwitchRow(Icons.Outlined.FolderCopy, "Cover art", "Put the thumbnail inside videos and music", embedCover) {
            Settings.setEmbedCover(it)
        }

        Section("Schedule and queues")
        SwitchRow(Icons.Outlined.Speed, "Only download at set hours",
            "Outside them downloads wait as Scheduled and start by themselves. Start now on a download skips the wait.",
            scheduleOn) { Settings.setScheduleOn(it); Schedule.tick(ctx) }
        SettingRow(Icons.Outlined.Speed, "From", scheduleStart, null) { pickTime(scheduleStart) { Settings.setScheduleStart(it) } }
        SettingRow(Icons.Outlined.Speed, "Until", scheduleStop, null) { pickTime(scheduleStop) { Settings.setScheduleStop(it) } }
        SettingRow(Icons.Outlined.Folder, "The hours apply to",
            if (scheduleQueue.isBlank()) "All downloads" else "The \"$scheduleQueue\" queue only", null) { pickScheduleQueue = true }
        SettingRow(Icons.Outlined.Folder, "Queues", queues.ifBlank { "Only the main queue" },
            "Extra queues, names separated by commas (e.g. Night, Work)") { editQueues = true }
        SettingRow(Icons.Outlined.Folder, "New downloads go to", newQueue.ifBlank { "Main queue" }, null) { pickNewQueue = true }

        Section("Network")
        SettingRow(Icons.Outlined.Wifi, "Proxy", proxy.ifBlank { "This phone's proxy settings" },
            "http://host:port, socks5://host:port, or direct") { editProxy = true }

        Section("Backup")
        SettingRow(Icons.Outlined.FolderCopy, "Export list of links", "Save every link in the list to a file", null) {
            exportList.launch("anysave-links.json")
        }
        SettingRow(Icons.Outlined.FolderCopy, "Import list of links", "From an exported file, or any text file with links", null) {
            importList.launch(arrayOf("application/json", "text/*", "*/*"))
        }

        Section("Quick Settings tile")
        SettingRow(Icons.Outlined.TouchApp, "Add the AnySave tile",
            if (Build.VERSION.SDK_INT >= 33) "Tap to add it now"
            else "Swipe down twice, tap the pencil (Edit), and drag AnySave into your tiles",
            "Copy a link anywhere, swipe down, tap AnySave"
        ) {
            if (Build.VERSION.SDK_INT >= 33) {
                ctx.getSystemService(StatusBarManager::class.java).requestAddTileService(
                    ComponentName(ctx, DownloadTileService::class.java), "AnySave",
                    AndroidIcon.createWithResource(ctx, R.drawable.ic_stat_download), ctx.mainExecutor
                ) { }
            }
        }

        Section("Accounts")
        Text("Log in to download private posts and most Instagram videos. The login stays on this phone.",
            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(horizontal = 4.dp))
        LoginActivity.Site.entries.forEach { site ->
            val on = site.label in loggedIn
            Row(
                Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp))
                    .clickable { login.launch(LoginActivity.intent(ctx, site)) }.padding(horizontal = 12.dp, vertical = 2.dp),
                verticalAlignment = Alignment.CenterVertically
            ) {
                SiteIcon(site.label, 24.dp)
                Spacer(Modifier.width(14.dp))
                Text(site.label, Modifier.weight(1f))
                if (on) AssistChip(onClick = { login.launch(LoginActivity.intent(ctx, site)) }, label = { Text("Logged in") },
                    leadingIcon = { Icon(Icons.Outlined.AccountCircle, null, Modifier.size(18.dp)) })
                else TextButton(onClick = { login.launch(LoginActivity.intent(ctx, site)) },
                    contentPadding = PaddingValues(horizontal = 12.dp, vertical = 0.dp),
                    modifier = Modifier.height(36.dp)) { Text("Log in") }
            }
        }
        if (loggedIn.isNotEmpty()) TextButton(onClick = { LoginActivity.logOutAll(ctx); loggedIn = emptyList() }) {
            Text("Log out of all sites")
        }

        Section("Downloader")
        SettingRow(Icons.Outlined.SystemUpdate, "Update downloader",
            (vm.ytdlpVersion?.let { if (it.startsWith("yt-dlp")) it else "yt-dlp $it" } ?: "yt-dlp …") + if (vm.updating) " · updating…" else "",
            "Updates itself every 2 days. Update now if a site stops working."
        ) { if (!vm.updating && Downloads.jobs.value.none { it.active }) vm.updateDownloader()
            else if (!vm.updating) vm.messages.tryEmit("Wait for downloads to finish first") }

        Section("Storage")
        SettingRow(Icons.Outlined.Folder, "Save location",
            if (sortFolders) "Downloads/AnySave/<type>" else "Downloads/AnySave", null) {}
        SettingRow(Icons.Outlined.DeleteSweep, "Clear download list", "Files are kept", null) {
            Downloads.clearFinished(); vm.messages.tryEmit("List cleared")
        }

        Section("About")
        Row(Modifier.fillMaxWidth().padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            AnimatedLogo(56.dp)
            Spacer(Modifier.width(16.dp))
            Column {
                Text("AnySave $appVersion", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                Text("Made by $AUTHOR", color = MaterialTheme.colorScheme.primary)
            }
        }
        CopyrightNotice(Modifier.padding(horizontal = 4.dp))
        SettingRow(Icons.Outlined.ContentCopy, "Copy diagnostics",
            "App & yt-dlp versions, device info, recent errors (no personal links)", null) {
            val failed = Downloads.jobs.value.filter { it.status == JobStatus.ERROR }.take(5)
                .joinToString("\n") { "  - [${it.kind}] ${it.site ?: ""}: ${it.error ?: "unknown"}" }
            val text = buildString {
                appendLine("AnySave Android Diagnostics")
                appendLine("===========================")
                appendLine("App:       $appVersion")
                appendLine("yt-dlp:    ${vm.ytdlpVersion ?: "unknown"}")
                appendLine("Android:   ${Build.VERSION.RELEASE} (SDK ${Build.VERSION.SDK_INT})")
                appendLine("Device:    ${Build.MANUFACTURER} ${Build.MODEL} (${Build.SUPPORTED_ABIS.firstOrNull() ?: "unknown"})")
                appendLine("Logged in: ${if (loggedIn.isEmpty()) "none" else loggedIn.joinToString(", ")}")
                if (failed.isNotEmpty()) {
                    appendLine("\nRecent failed jobs:")
                    appendLine(failed)
                }
            }
            val cm = ctx.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
            cm.setPrimaryClip(ClipData.newPlainText("AnySave diagnostics", text))
            vm.messages.tryEmit("Diagnostics copied to clipboard")
        }
        SettingRow(Icons.Outlined.Gavel, "Legal & disclaimer",
            "Copyright, your responsibility, privacy, open-source licences", null) { showLegal = true }
    }
    }

    if (showLegal) LegalDialog { showLegal = false }

    if (pickSubs) AlertDialog(
        onDismissRequest = { pickSubs = false },
        title = { Text("Subtitles inside videos") },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                val picked = subLangs.split(',').filter { it.isNotBlank() }
                SUB_LANGS.forEach { (code, name) ->
                    val on = code in picked
                    Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).clickable {
                        Settings.setSubLangs(SUB_LANGS.map { it.first }.filter { if (it == code) !on else it in picked }.joinToString(","))
                    }, verticalAlignment = Alignment.CenterVertically) {
                        Checkbox(checked = on, onCheckedChange = null, modifier = Modifier.padding(8.dp))
                        Text(name)
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = { pickSubs = false }) { Text("Done") } },
    )
    if (editProxy) TextSettingDialog("Proxy", proxy, "http://host:port, socks5://host:port or direct",
        "Leave empty to use this phone's own proxy settings.",
        check = { if (Net.valid(it)) null else "Use http://host:port, socks5://host:port, direct, or leave it empty" },
        onSave = { Settings.setProxy(it) }) { editProxy = false }
    if (editQueues) TextSettingDialog("Queues", queues, "Night, Work", "Names separated by commas. Leave empty for only the main queue.",
        check = { null }, onSave = { Settings.setQueues(it); Schedule.tick(ctx) }) { editQueues = false }
    if (pickScheduleQueue) ChoiceDialog("The hours apply to",
        listOf("" to "All downloads") + Schedule.queueNames().map { it to "The \"$it\" queue only" }, scheduleQueue,
        onPick = { Settings.setScheduleQueue(it); Schedule.tick(ctx) }) { pickScheduleQueue = false }
    if (pickNewQueue) ChoiceDialog("New downloads go to",
        listOf("" to "Main queue") + Schedule.queueNames().map { it to it }, newQueue,
        onPick = { Settings.setNewQueue(it) }) { pickNewQueue = false }

    if (pickQuality) AlertDialog(
        onDismissRequest = { pickQuality = false },
        title = { Text("Default quality") },
        text = {
            Column {
                Quality.entries.forEach { q ->
                    Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp))
                        .clickable { Settings.setQuality(q); pickQuality = false }.padding(vertical = 4.dp),
                        verticalAlignment = Alignment.CenterVertically) {
                        RadioButton(selected = q == quality, onClick = { Settings.setQuality(q); pickQuality = false })
                        Text(q.label)
                    }
                }
                Text("If a video doesn't have this quality, the closest lower one is used.",
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 8.dp))
            }
        },
        confirmButton = { TextButton(onClick = { pickQuality = false }) { Text("Close") } },
    )
}

/** Subtitle languages offered: English, Bangla and the other most-used ones. */
private val SUB_LANGS = listOf(
    "en" to "English", "bn" to "বাংলা (Bangla)", "hi" to "हिन्दी (Hindi)", "ur" to "اردو (Urdu)", "ar" to "العربية (Arabic)",
    "es" to "Español", "fr" to "Français", "de" to "Deutsch", "pt" to "Português", "ru" to "Русский", "tr" to "Türkçe",
    "id" to "Indonesia", "zh" to "中文 (Chinese)", "ja" to "日本語 (Japanese)", "ko" to "한국어 (Korean)",
)

@Composable
private fun TextSettingDialog(
    title: String, initial: String, hint: String, note: String,
    check: (String) -> String?, onSave: (String) -> Unit, onClose: () -> Unit,
) {
    var text by remember { mutableStateOf(initial) }
    val problem = check(text.trim())
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text(title) },
        text = {
            Column {
                OutlinedTextField(value = text, onValueChange = { text = it }, singleLine = true,
                    placeholder = { Text(hint) }, isError = problem != null, modifier = Modifier.fillMaxWidth())
                Text(problem ?: note, style = MaterialTheme.typography.bodySmall,
                    color = if (problem != null) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 8.dp))
            }
        },
        confirmButton = { TextButton(enabled = problem == null, onClick = { onSave(text.trim()); onClose() }) { Text("Save") } },
        dismissButton = { TextButton(onClick = onClose) { Text("Cancel") } },
    )
}

@Composable
private fun ChoiceDialog(title: String, options: List<Pair<String, String>>, selected: String, onPick: (String) -> Unit, onClose: () -> Unit) {
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text(title) },
        text = {
            Column {
                options.forEach { (value, label) ->
                    Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).clickable { onPick(value); onClose() }.padding(vertical = 4.dp),
                        verticalAlignment = Alignment.CenterVertically) {
                        RadioButton(selected = value == selected, onClick = { onPick(value); onClose() })
                        Text(label)
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = onClose) { Text("Close") } },
    )
}

@Composable
private fun Section(title: String) =
    Text(title, style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(top = 16.dp, start = 4.dp))

@Composable
private fun SettingRow(icon: ImageVector, title: String, value: String, desc: String?, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).clickable(onClick = onClick).padding(12.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Icon(icon, null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.width(16.dp))
        Column(Modifier.weight(1f)) {
            Text(title)
            Text(value, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            desc?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
    }
}

@Composable
private fun SwitchRow(icon: ImageVector, title: String, desc: String, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).clickable { onChange(!checked) }.padding(12.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Icon(icon, null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.width(16.dp))
        Column(Modifier.weight(1f)) {
            Text(title)
            Text(desc, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Switch(checked = checked, onCheckedChange = onChange)
    }
}
