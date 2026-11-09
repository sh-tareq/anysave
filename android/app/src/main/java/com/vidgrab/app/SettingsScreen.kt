package com.vidgrab.app

import android.app.StatusBarManager
import android.content.ComponentName
import android.graphics.drawable.Icon as AndroidIcon
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
    val sortFolders by Settings.sortIntoFolders.collectAsState()
    var pickQuality by remember { mutableStateOf(false) }
    var showLegal by remember { mutableStateOf(false) }
    var loggedIn by remember { mutableStateOf(LoginActivity.loggedInSites(ctx)) }
    val login = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        loggedIn = LoginActivity.loggedInSites(ctx)
    }
    val appVersion = remember { ctx.packageManager.getPackageInfo(ctx.packageName, 0).versionName }

    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(start = 16.dp, end = 16.dp, top = 24.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp)
    ) {
        Text("Settings", style = MaterialTheme.typography.headlineLarge, fontWeight = FontWeight.Bold)

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
        SwitchRow(Icons.Outlined.FolderCopy, "Sort into folders",
            "Videos, Music, Pictures, Documents, Archives, Apps, Converted", sortFolders) { Settings.setSortIntoFolders(it) }

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
                SiteIcon(site.label, 26.dp)
                Spacer(Modifier.width(14.dp))
                Text(site.label, Modifier.weight(1f))
                if (on) AssistChip(onClick = { login.launch(LoginActivity.intent(ctx, site)) }, label = { Text("Logged in") },
                    leadingIcon = { Icon(Icons.Outlined.AccountCircle, null, Modifier.size(18.dp)) })
                else TextButton(onClick = { login.launch(LoginActivity.intent(ctx, site)) }) { Text("Log in") }
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
        SettingRow(Icons.Outlined.Gavel, "Legal & disclaimer",
            "Copyright, your responsibility, privacy, open-source licences", null) { showLegal = true }
    }

    if (showLegal) LegalDialog { showLegal = false }

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
