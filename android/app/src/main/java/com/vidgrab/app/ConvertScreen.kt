package com.vidgrab.app

import android.content.Intent
import android.media.MediaMetadataRetriever
import android.net.Uri
import android.provider.OpenableColumns
import android.widget.Toast
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.MusicNote
import androidx.compose.material.icons.outlined.Transform
import androidx.compose.material.icons.outlined.VideoLibrary
import androidx.compose.material.icons.outlined.Videocam
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

private data class Picked(val uri: Uri, val name: String, val size: Long, val durationSec: Double?, val isAudio: Boolean)

@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun ConvertScreen(jobs: List<Job>, onStarted: () -> Unit) {
    val ctx = LocalContext.current
    var picked by remember { mutableStateOf<Picked?>(null) }
    var pendingUri by remember { mutableStateOf<Uri?>(null) }
    var fromDownloads by remember { mutableStateOf(false) }

    var format by remember { mutableStateOf("mp4") }
    var height by remember { mutableStateOf<Int?>(null) }
    var targetMb by remember { mutableStateOf<Int?>(null) }
    var trim by remember { mutableStateOf(false) }
    var range by remember { mutableStateOf(0f..1f) }
    var removeAudio by remember { mutableStateOf(false) }

    val pick = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) {
            runCatching { ctx.contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION) }
            pendingUri = uri
        }
    }
    // Read name, size and length of the chosen file
    LaunchedEffect(pendingUri) {
        val uri = pendingUri ?: return@LaunchedEffect
        picked = withContext(Dispatchers.IO) {
            var name = "file"; var size = 0L
            ctx.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use {
                if (it.moveToFirst()) { name = it.getString(0) ?: name; size = it.getLong(1) }
            }
            val mime = ctx.contentResolver.getType(uri).orEmpty()
            val dur = runCatching {
                MediaMetadataRetriever().run {
                    setDataSource(ctx, uri)
                    val ms = extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)?.toLong()
                    release(); ms?.div(1000.0)
                }
            }.getOrNull()
            Picked(uri, name, size, dur, mime.startsWith("audio/") || FileKinds.extOf(name) in listOf("mp3", "m4a", "wav", "flac", "ogg", "opus", "aac"))
        }
        picked?.let { if (it.isAudio && format !in ConvertSpec.AUDIO_FORMATS) format = "mp3" }
        range = 0f..1f; trim = false
    }

    val audioOut = format in ConvertSpec.AUDIO_FORMATS
    val dur = picked?.durationSec
    val converting = jobs.filter { it.kind == JobKind.CONVERT && it.active }

    ScreenScaffold(title = "Convert", subtitle = "Change the format, size or length of any video or audio") { pad ->
    LazyColumn(
        Modifier.fillMaxSize().padding(pad),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 14.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(14.dp)
    ) {

        // 1. Choose a file
        item {
            ElevatedCard {
                Column(Modifier.padding(14.dp)) {
                    val p = picked
                    if (p == null) {
                        Text("1. Choose a file", fontWeight = FontWeight.SemiBold)
                        Spacer(Modifier.height(10.dp))
                    } else {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Icon(if (p.isAudio) Icons.Outlined.MusicNote else Icons.Outlined.Videocam, null,
                                tint = MaterialTheme.colorScheme.primary)
                            Spacer(Modifier.width(10.dp))
                            Column(Modifier.weight(1f)) {
                                Text(p.name, fontWeight = FontWeight.SemiBold, maxLines = 2, overflow = TextOverflow.Ellipsis)
                                Text(listOfNotNull(Engine.humanSize(p.size), p.durationSec?.let(::fmtDuration)).joinToString(" · "),
                                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                        }
                        Spacer(Modifier.height(10.dp))
                    }
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        OutlinedButton(onClick = { pick.launch(arrayOf("video/*", "audio/*")) }, modifier = Modifier.weight(1f)) {
                            Icon(Icons.Outlined.FolderOpen, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp))
                            Text(if (p == null) "From phone" else "Change", maxLines = 1)
                        }
                        OutlinedButton(onClick = { fromDownloads = true }, modifier = Modifier.weight(1f)) {
                            Icon(Icons.Outlined.VideoLibrary, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp))
                            Text("Downloads", maxLines = 1)
                        }
                    }
                }
            }
        }

        if (picked != null) {
            // 2. Format
            item {
                Section("2. Convert to") {
                    Text("Video", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        ConvertSpec.VIDEO_FORMATS.forEach { f ->
                            FilterChip(selected = format == f, onClick = { format = f }, label = { Text(f.uppercase()) },
                                enabled = picked?.isAudio != true)
                        }
                    }
                    Text("Audio", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(top = 6.dp))
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        ConvertSpec.AUDIO_FORMATS.forEach { f ->
                            FilterChip(selected = format == f, onClick = { format = f }, label = { Text(f.uppercase()) })
                        }
                    }
                }
            }
            // 3. Size and quality (video only)
            if (!audioOut) item {
                Section("3. Size") {
                    Text("Resolution", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        listOf(null, 1080, 720, 480, 360).forEach { h ->
                            FilterChip(selected = height == h, onClick = { height = h }, label = { Text(h?.let { "${it}p" } ?: "Original") })
                        }
                    }
                    if (format != "gif") {
                        Text("File size", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(top = 6.dp))
                        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            listOf(null, 10, 25, 50, 100).forEach { mb ->
                                FilterChip(selected = targetMb == mb, onClick = { targetMb = mb },
                                    label = { Text(mb?.let { "Under $it MB" } ?: "Normal") }, enabled = mb == null || dur != null)
                            }
                        }
                        if (targetMb == 25) Text("Good for WhatsApp and email", style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant)
                        Row(Modifier.fillMaxWidth().padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                            Text("Remove sound", Modifier.weight(1f))
                            Switch(checked = removeAudio, onCheckedChange = { removeAudio = it })
                        }
                    }
                }
            }
            // 4. Trim
            if (dur != null && dur > 1) item {
                Section(if (audioOut) "3. Trim" else "4. Trim") {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text("Cut to a part of the file", Modifier.weight(1f))
                        Switch(checked = trim, onCheckedChange = { trim = it })
                    }
                    if (trim) {
                        RangeSlider(value = range, onValueChange = { range = it })
                        Row {
                            Text("Start ${fmtDuration(range.start * dur)}", style = MaterialTheme.typography.bodySmall)
                            Spacer(Modifier.weight(1f))
                            Text("End ${fmtDuration(range.endInclusive * dur)}", style = MaterialTheme.typography.bodySmall)
                        }
                        Text("Length ${fmtDuration((range.endInclusive - range.start) * dur)}",
                            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
            }
            // Convert
            item {
                val spec = ConvertSpec(
                    format = format,
                    height = if (audioOut) null else height,
                    targetMb = if (audioOut || format == "gif") null else targetMb,
                    startSec = if (trim && dur != null && range.start > 0f) (range.start * dur).toDouble() else null,
                    endSec = if (trim && dur != null && range.endInclusive < 1f) (range.endInclusive * dur).toDouble() else null,
                    removeAudio = !audioOut && removeAudio,
                )
                Button(
                    onClick = {
                        val p = picked ?: return@Button
                        if (!Settings.acceptedTerms) { Toast.makeText(ctx, "Accept the terms first", Toast.LENGTH_SHORT).show(); return@Button }
                        DownloadService.startConvert(ctx, p.uri, p.name, spec)
                        Toast.makeText(ctx, "Converting to ${spec.label()}", Toast.LENGTH_SHORT).show()
                    },
                    modifier = Modifier.fillMaxWidth().height(52.dp), shape = RoundedCornerShape(16.dp),
                ) {
                    Icon(Icons.Outlined.Transform, null); Spacer(Modifier.width(8.dp))
                    Text("Convert · ${spec.label()}", fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
                Text("Saved in Downloads/AnySave/Converted", style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 6.dp))
            }
        }

        if (converting.isNotEmpty()) {
            item { Text("Converting now", style = MaterialTheme.typography.titleMedium) }
            items(converting, key = { it.id }) { ActiveJobCard(it) }
        }
        item { MadeBy() }
    }
    }

    // Pick from finished downloads (videos and music)
    if (fromDownloads) {
        val media = jobs.filter {
            it.status == JobStatus.DONE && it.uri != null &&
                (it.category == FileKinds.Category.VIDEOS || it.category == FileKinds.Category.MUSIC ||
                    (it.kind == JobKind.CONVERT && it.mime?.let { m -> m.startsWith("video") || m.startsWith("audio") } == true))
        }
        ModalBottomSheet(onDismissRequest = { fromDownloads = false }) {
            Column(Modifier.padding(horizontal = 16.dp)) {
                Text("Choose from your downloads", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                if (media.isEmpty()) Text("No downloaded videos or music yet.", Modifier.padding(vertical = 24.dp),
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
                LazyColumn(Modifier.heightIn(max = 480.dp), contentPadding = PaddingValues(vertical = 12.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    items(media, key = { it.id }) { j ->
                        Surface(onClick = { pendingUri = j.uri; fromDownloads = false }, shape = RoundedCornerShape(12.dp)) {
                            Row(Modifier.padding(6.dp), verticalAlignment = Alignment.CenterVertically) {
                                Thumb(j, Modifier.size(width = 80.dp, height = 48.dp))
                                Spacer(Modifier.width(10.dp))
                                Column(Modifier.weight(1f)) {
                                    Text(j.fileName ?: j.title, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                    Text(listOfNotNull(Engine.humanSize(j.sizeBytes), j.site).joinToString(" · "),
                                        style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                }
                            }
                        }
                    }
                }
                Spacer(Modifier.height(16.dp))
            }
        }
    }
}

@Composable
private fun Section(title: String, content: @Composable ColumnScope.() -> Unit) {
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.fillMaxWidth().padding(14.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Text(title, fontWeight = FontWeight.SemiBold)
            content()
        }
    }
}
