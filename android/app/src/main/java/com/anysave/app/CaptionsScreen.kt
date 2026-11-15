package com.anysave.app

import android.content.Context
import android.content.Intent
import android.graphics.Typeface
import android.media.MediaMetadataRetriever
import android.net.Uri
import android.provider.OpenableColumns
import android.widget.Toast
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.annotation.OptIn
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.outlined.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shadow
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.MediaItem
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.PlayerView
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import kotlin.math.max
import kotlin.math.min
import kotlin.math.roundToInt

/** What the screen read about a chosen file. */
private data class CapPicked(val uri: Uri, val name: String, val dur: Double, val w: Int, val h: Int, val hasVideo: Boolean, val hasAudio: Boolean)

private suspend fun probe(ctx: Context, uri: Uri): CapPicked? = withContext(Dispatchers.IO) {
    runCatching {
        var name = "video"
        ctx.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { if (it.moveToFirst()) name = it.getString(0) ?: name }
        val r = MediaMetadataRetriever()
        try {
            r.setDataSource(ctx, uri)
            val dur = r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)?.toLongOrNull()?.div(1000.0) ?: 0.0
            val video = r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_HAS_VIDEO) == "yes"
            val audio = r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_HAS_AUDIO) == "yes"
            var w = r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_WIDTH)?.toIntOrNull() ?: 0
            var h = r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_HEIGHT)?.toIntOrNull() ?: 0
            val rot = r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_ROTATION)?.toIntOrNull() ?: 0
            if (rot == 90 || rot == 270) { val t = w; w = h; h = t }
            if (!video || w <= 0 || h <= 0) { w = 1920; h = 1080 }
            CapPicked(uri, name, dur, w, h, video, audio)
        } finally { r.release() }
    }.getOrNull()
}

private fun minutes(secs: Double): String {
    val m = (secs / 60).roundToInt()
    return when { secs < 60 -> "under a minute"; m <= 1 -> "about 1 minute"; m < 90 -> "about $m minutes"; else -> "about ${"%.1f".format(secs / 3600)} hours" }
}

private fun clock(t: Double): String {
    val x = max(0.0, t)
    val m = (x / 60).toInt()
    return "%d:%05.2f".format(m, x - m * 60)
}

private fun prefs(ctx: Context) = ctx.getSharedPreferences("captions", Context.MODE_PRIVATE)

private fun defaultLang(ctx: Context): String = prefs(ctx).getString("lang", null)
    ?: java.util.Locale.getDefault().language.takeIf { l -> Speech.LANGS.any { it.first == l } } ?: "en"

@kotlin.OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CaptionsScreen(jobs: List<Job>, onOpenDownloads: () -> Unit) {
    val ctx = LocalContext.current
    var projects by remember { mutableStateOf(CapProjects.all(ctx)) }
    var openId by rememberSaveable { mutableStateOf<String?>(null) }
    var setup by remember { mutableStateOf<CapPicked?>(null) }
    var setupFor by remember { mutableStateOf<CapProject?>(null) } // "Listen again" for an open project
    var fromDownloads by remember { mutableStateOf(false) }
    var reading by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    // Finished speech jobs write into their project: read them again when a job changes.
    val jobKey = jobs.filter { it.kind == JobKind.CONVERT }.joinToString { "${it.id}:${it.status}" }
    LaunchedEffect(jobKey) { projects = withContext(Dispatchers.IO) { CapProjects.all(ctx) } }

    fun picked(uri: Uri) {
        reading = true
        scope.launch {
            val p = probe(ctx, uri)
            reading = false
            when {
                p == null -> Toast.makeText(ctx, "AnySave can't read that file.", Toast.LENGTH_LONG).show()
                !p.hasAudio -> Toast.makeText(ctx, "${p.name} has no sound to make captions from.", Toast.LENGTH_LONG).show()
                else -> setup = p
            }
        }
    }
    val pick = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) {
            runCatching { ctx.contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION) }
            picked(uri)
        }
    }

    val open = projects.firstOrNull { it.id == openId }
    if (open != null) {
        BackHandler { openId = null }
        ProjectScreen(open, jobs, onBack = { openId = null }, onChanged = { p -> projects = projects.map { if (it.id == p.id) p else it } },
            onListenAgain = { setupFor = open }, onDelete = {
                CapProjects.delete(ctx, open.id)
                projects = projects.filter { it.id != open.id }
                openId = null
            }, onOpenDownloads = onOpenDownloads)
    } else {
        ScreenScaffold("Captions", "Speech to captions, on your phone") { pad ->
            LazyColumn(Modifier.fillMaxSize().padding(pad), contentPadding = PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                item { CapHero(reading, onPick = { pick.launch(arrayOf("video/*", "audio/*")) }, onDownloads = { fromDownloads = true }) }
                if (!Speech.available(ctx)) item {
                    Text("Making captions needs a newer phone processor (64-bit ARM). You can still bring in a subtitle file, style it and save it onto a video.",
                        style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.error)
                }
                if (projects.isNotEmpty()) item {
                    Text("Your captions", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold, modifier = Modifier.padding(top = 8.dp))
                }
                items(projects, key = { it.id }) { p -> ProjectRow(p, jobs.firstOrNull { it.id == p.jobId }) { openId = p.id } }
            }
        }
    }

    val s = setup
    val again = setupFor
    if (s != null || again != null) {
        SetupSheet(
            name = s?.name ?: again!!.name, dur = s?.dur ?: again!!.dur,
            onDismiss = { setup = null; setupFor = null },
            onSubs = { cues ->
                val p = if (again != null) again.copy(cues = cues, orig = cues, jobId = null, chunk = "", edited = System.currentTimeMillis())
                else CapProject(id = "c" + System.currentTimeMillis().toString(36), source = s!!.uri.toString(), name = s.name, lang = "auto",
                    hasVideo = s.hasVideo, w = s.w, h = s.h, dur = s.dur, cues = cues, orig = cues)
                CapProjects.save(ctx, p)
                projects = CapProjects.all(ctx)
                setup = null; setupFor = null; openId = p.id
            },
        ) { lang, accurate ->
            prefs(ctx).edit().putString("lang", lang).apply()
            val p = if (again != null) again.copy(lang = lang) else {
                val style = if (lang == "bn") Captions.PRESETS.first { it.id == "bangla" }.style else
                    prefs(ctx).getString("style", null)?.let { CapStyle.fromJson(org.json.JSONObject(it)) } ?: CapStyle()
                CapProject(id = "c" + System.currentTimeMillis().toString(36), source = s!!.uri.toString(), name = s.name, lang = lang,
                    hasVideo = s.hasVideo, w = s.w, h = s.h, dur = s.dur, style = style)
            }
            CapProjects.save(ctx, p)
            CaptionJobs.startMake(ctx, p, accurate)
            projects = CapProjects.all(ctx)
            setup = null; setupFor = null; openId = p.id
        }
    }

    if (fromDownloads) {
        val done = jobs.filter { j ->
            j.status == JobStatus.DONE && j.uri != null && FileKinds.extOf(j.fileName ?: "") in setOf("mp4", "mkv", "webm", "mov", "m4a", "mp3", "opus", "ogg", "wav", "aac", "flac", "3gp", "m4v")
        }
        ModalBottomSheet(onDismissRequest = { fromDownloads = false }) {
            Column(Modifier.padding(horizontal = 16.dp)) {
                Text("Choose from your downloads", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                if (done.isEmpty()) Text("No finished videos or audio yet.", Modifier.padding(vertical = 24.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
                LazyColumn(Modifier.heightIn(max = 480.dp), contentPadding = PaddingValues(vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    items(done, key = { it.id }) { j ->
                        Surface(onClick = { fromDownloads = false; picked(j.uri!!) }, shape = RoundedCornerShape(12.dp)) {
                            Row(Modifier.padding(6.dp), verticalAlignment = Alignment.CenterVertically) {
                                Thumb(j, Modifier.size(width = 80.dp, height = 48.dp))
                                Spacer(Modifier.width(10.dp))
                                Text(j.fileName ?: j.title, Modifier.weight(1f), maxLines = 2, overflow = TextOverflow.Ellipsis)
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
private fun CapHero(reading: Boolean, onPick: () -> Unit, onDownloads: () -> Unit) {
    Surface(shape = RoundedCornerShape(20.dp), color = MaterialTheme.colorScheme.surfaceContainerLowest,
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
        Column(Modifier.fillMaxWidth().padding(22.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            Box(Modifier.size(64.dp).clip(CircleShape).background(MaterialTheme.colorScheme.primaryContainer), contentAlignment = Alignment.Center) {
                Icon(Icons.Outlined.ClosedCaption, null, Modifier.size(32.dp), tint = MaterialTheme.colorScheme.primary)
            }
            Spacer(Modifier.height(14.dp))
            Text("Add captions to a video", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
            Text("AnySave listens and writes down what's said, on your phone. Fix any word, pick a style, then save the video with captions or a subtitle file.",
                style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center,
                modifier = Modifier.padding(top = 4.dp))
            Spacer(Modifier.height(18.dp))
            Button(onClick = onPick, enabled = !reading, modifier = Modifier.fillMaxWidth().height(50.dp), shape = RoundedCornerShape(14.dp)) {
                if (reading) CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
                else Icon(Icons.Outlined.FolderOpen, null, Modifier.size(20.dp))
                Spacer(Modifier.width(8.dp))
                Text("Choose a video or audio", fontWeight = FontWeight.SemiBold)
            }
            Spacer(Modifier.height(4.dp))
            TextButton(onClick = onDownloads) { Text("Pick from my downloads") }
        }
    }
}

@Composable
private fun ProjectRow(p: CapProject, job: Job?, onClick: () -> Unit) {
    val status = when {
        job != null && job.active -> "Working · ${job.percent.toInt()}%"
        job != null && job.status == JobStatus.ERROR -> "Didn't finish: ${job.error ?: "error"}"
        p.jobId != null && job == null -> "Stopped"
        p.cues.isEmpty() -> "No captions yet"
        else -> "${p.cues.size} lines · ${Speech.LANGS.firstOrNull { it.first == p.lang }?.second ?: p.lang}"
    }
    Surface(onClick = onClick, shape = RoundedCornerShape(14.dp), color = MaterialTheme.colorScheme.surfaceContainerLow) {
        Row(Modifier.fillMaxWidth().padding(14.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(if (p.hasVideo) Icons.Outlined.Movie else Icons.Outlined.MusicNote, null, tint = MaterialTheme.colorScheme.primary)
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(p.name, maxLines = 1, overflow = TextOverflow.Ellipsis, fontWeight = FontWeight.Medium)
                Text(status, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            if (job != null && job.active) CircularProgressIndicator(progress = { job.percent / 100f }, Modifier.size(24.dp), strokeWidth = 3.dp)
            else Icon(Icons.Outlined.ChevronRight, null, tint = MaterialTheme.colorScheme.outline)
        }
    }
}

@kotlin.OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
private fun SetupSheet(name: String, dur: Double, onDismiss: () -> Unit, onSubs: (List<CapCue>) -> Unit, onStart: (String, Boolean) -> Unit) {
    val ctx = LocalContext.current
    var lang by remember { mutableStateOf(defaultLang(ctx)) }
    var accurate by remember { mutableStateOf(false) }
    val canListen = Speech.available(ctx)
    val model = Speech.modelFor(ctx, lang, accurate)
    val pickSubs = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri == null) return@rememberLauncherForActivityResult
        val cues = runCatching { ctx.contentResolver.openInputStream(uri)!!.use { Captions.parseSubs(it.readBytes().toString(Charsets.UTF_8)) } }.getOrDefault(emptyList())
        if (cues.isEmpty()) Toast.makeText(ctx, "That file has no subtitle lines AnySave can read (SRT or VTT).", Toast.LENGTH_LONG).show()
        else onSubs(cues)
    }
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(Modifier.padding(horizontal = 20.dp).padding(bottom = 24.dp).verticalScroll(rememberScrollState())) {
            Text(name, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold, maxLines = 2, overflow = TextOverflow.Ellipsis)
            if (dur > 0) Text("Length ${clock(dur).substringBefore('.')}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.height(16.dp))
            if (canListen) {
                Text("Language spoken", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
                Spacer(Modifier.height(6.dp))
                FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    Speech.LANGS.forEach { (code, label) -> FilterChip(selected = lang == code, onClick = { lang = code }, label = { Text(label) }) }
                }
                if (lang != "en" && lang != "bn") {
                    Row(Modifier.fillMaxWidth().padding(top = 8.dp).clickable { accurate = !accurate }, verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text("More accurate")
                            Text("A bigger model (547 MB). Much slower on phones.", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                        Switch(checked = accurate, onCheckedChange = { accurate = it })
                    }
                }
                Spacer(Modifier.height(12.dp))
                val notes = buildList {
                    if (!Speech.has(ctx, model)) add("The first time, AnySave downloads the ${model.label} (${Engine.humanSize(model.size)}) once. After that it works offline.")
                    if (lang == "bn") add("Bangla needs the bigger, accurate model. It is slow on phones; a computer with AnySave is much faster.")
                    if (dur > 0) add("Listening takes ${minutes(dur * Speech.speed(ctx, model))} on this phone. It keeps going if you leave the app.")
                }
                notes.forEach { Text(it, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(bottom = 6.dp)) }
                Spacer(Modifier.height(8.dp))
                Button(onClick = { onStart(lang, accurate) }, modifier = Modifier.fillMaxWidth().height(50.dp), shape = RoundedCornerShape(14.dp)) {
                    Icon(Icons.Outlined.Hearing, null, Modifier.size(20.dp)); Spacer(Modifier.width(8.dp))
                    Text("Make captions", fontWeight = FontWeight.SemiBold)
                }
            }
            TextButton(onClick = { pickSubs.launch(arrayOf("*/*")) }, modifier = Modifier.fillMaxWidth()) { Text("I already have a subtitle file (SRT or VTT)") }
        }
    }
}

// ---------- one project ----------

@kotlin.OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ProjectScreen(
    start: CapProject, jobs: List<Job>, onBack: () -> Unit, onChanged: (CapProject) -> Unit,
    onListenAgain: () -> Unit, onDelete: () -> Unit, onOpenDownloads: () -> Unit,
) {
    val ctx = LocalContext.current
    var p by remember(start.id) { mutableStateOf(start) }
    // A finished speech job wrote new lines into the file.
    LaunchedEffect(start) { if (start.jobId != p.jobId || (p.cues.isEmpty() && start.cues.isNotEmpty())) p = start }
    val job = jobs.firstOrNull { it.id == p.jobId }
    val listening = p.jobId != null && job != null && !job.finished
    LaunchedEffect(job?.status) {
        if (p.jobId != null && (job == null || job.status == JobStatus.DONE)) {
            CapProjects.get(ctx, p.id)?.let { p = it; onChanged(it) }
        }
    }
    // Save edits a moment after the last change (never while the speech job owns the file).
    LaunchedEffect(p) {
        if (p.jobId != null) return@LaunchedEffect
        delay(500)
        withContext(Dispatchers.IO) { CapProjects.save(ctx, p) }
        prefs(ctx).edit().putString("style", p.style.toJson().toString()).apply()
        onChanged(p)
    }
    fun edit(f: (CapProject) -> CapProject) { p = f(p).copy(edited = System.currentTimeMillis()) }

    var menu by remember { mutableStateOf(false) }
    var confirmDelete by remember { mutableStateOf(false) }
    var tab by rememberSaveable { mutableIntStateOf(0) }
    var saveJob by remember { mutableStateOf<String?>(null) }
    var saveMenu by remember { mutableStateOf(false) }
    val saving = jobs.firstOrNull { it.id == saveJob }

    ScreenScaffold(p.name, if (p.cues.isNotEmpty()) "${p.cues.size} lines" else null,
        leading = { IconButton(onClick = onBack, Modifier.size(32.dp)) { Icon(Icons.AutoMirrored.Outlined.ArrowBack, "Back") } },
        actions = {
            Box {
                IconButton(onClick = { menu = true }) { Icon(Icons.Outlined.MoreVert, "More") }
                DropdownMenu(menu, { menu = false }) {
                    DropdownMenuItem(text = { Text("Listen again") }, enabled = !listening, onClick = { menu = false; onListenAgain() },
                        leadingIcon = { Icon(Icons.Outlined.Hearing, null) })
                    DropdownMenuItem(text = { Text("Delete these captions") }, onClick = { menu = false; confirmDelete = true },
                        leadingIcon = { Icon(Icons.Outlined.Delete, null) })
                }
            }
        },
        bottomBar = {
            if (p.cues.isNotEmpty() && !listening) Surface(tonalElevation = 3.dp) {
                Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp)) {
                    if (saving != null) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Text(when (saving.status) {
                                JobStatus.DONE -> "Saved in Downloads"
                                JobStatus.ERROR -> saving.error ?: "Couldn't save."
                                JobStatus.CANCELLED -> "Cancelled"
                                else -> "Saving · ${saving.percent.toInt()}%"
                            }, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium,
                                color = if (saving.status == JobStatus.ERROR) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface)
                            TextButton(onClick = onOpenDownloads) { Text("Show") }
                        }
                        if (saving.active) LinearProgressIndicator(progress = { saving.percent / 100f }, Modifier.fillMaxWidth().padding(bottom = 8.dp))
                    }
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        if (p.hasVideo) Button(onClick = { saveJob = CaptionJobs.startBurn(ctx, p) }, Modifier.weight(1f).height(48.dp),
                            shape = RoundedCornerShape(14.dp), enabled = saving?.active != true) {
                            Icon(Icons.Outlined.Movie, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp)); Text("Save video")
                        }
                        Box(Modifier.weight(1f)) {
                            OutlinedButton(onClick = { saveMenu = true }, Modifier.fillMaxWidth().height(48.dp), shape = RoundedCornerShape(14.dp)) {
                                Icon(Icons.Outlined.Subtitles, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp)); Text("Save subtitles")
                            }
                            DropdownMenu(saveMenu, { saveMenu = false }) {
                                listOf("srt" to "SRT (most players)", "vtt" to "VTT (web)", "txt" to "Plain text").forEach { (f, label) ->
                                    DropdownMenuItem(text = { Text(label) }, onClick = { saveMenu = false; saveJob = CaptionJobs.startFile(ctx, p, f) })
                                }
                            }
                        }
                    }
                }
            }
        },
    ) { pad ->
        Column(Modifier.fillMaxSize().padding(pad)) {
            when {
                listening -> Listening(job!!, p)
                p.jobId != null && job?.status == JobStatus.ERROR -> Column(Modifier.padding(20.dp)) {
                    Text("AnySave couldn't finish listening.", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                    Text(job.error ?: "", color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(vertical = 8.dp))
                    Button(onClick = {
                        val best = ConvertSpec.fromJson(job.spec).opt("model") == "best"
                        val id = CaptionJobs.startMake(ctx, p.copy(jobId = null), best)
                        p = p.copy(jobId = id)
                    }) { Text("Try again") }
                }
                p.cues.isEmpty() -> Column(Modifier.padding(20.dp)) {
                    Text("No captions yet.", style = MaterialTheme.typography.titleMedium)
                    TextButton(onClick = onListenAgain) { Text("Listen to the file") }
                }
                else -> Editor(p, tab, { tab = it }, ::edit)
            }
        }
    }

    if (confirmDelete) AlertDialog(
        onDismissRequest = { confirmDelete = false },
        title = { Text("Delete these captions?") },
        text = { Text("Your edits are lost. The video itself and anything already saved in Downloads stay.") },
        confirmButton = { TextButton(onClick = {
            confirmDelete = false
            p.jobId?.let { DownloadService.cancel(ctx, it) }
            onDelete()
        }) { Text("Delete") } },
        dismissButton = { TextButton(onClick = { confirmDelete = false }) { Text("Keep") } },
    )
}

@Composable
private fun Listening(job: Job, p: CapProject) {
    val ctx = LocalContext.current
    Column(Modifier.fillMaxWidth().padding(24.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        Spacer(Modifier.height(24.dp))
        CircularProgressIndicator(progress = { job.percent / 100f }, Modifier.size(72.dp), strokeWidth = 6.dp)
        Spacer(Modifier.height(16.dp))
        // The job first downloads the model (if it isn't here yet), then listens.
        val model = if (ConvertSpec.fromJson(job.spec).opt("model") == "best") Speech.BEST else Speech.STANDARD
        val getting = !Speech.has(ctx, model)
        Text(if (getting) "Getting the speech model… ${job.percent.toInt()}%" else "Listening… ${job.percent.toInt()}%",
            style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
        if (getting) Text("${Engine.humanSize(model.size)}, only the first time", style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant)
        val eta = job.eta.takeIf { it.isNotBlank() && it != "?" && it != "Preparing…" }
        Text(listOfNotNull(eta?.let { "$it left" }, "you can leave this screen").joinToString(" · "),
            style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center)
        Spacer(Modifier.height(20.dp))
        OutlinedButton(onClick = { DownloadService.cancel(ctx, job.id) }) { Text("Stop") }
    }
}

@Composable
private fun Editor(p: CapProject, tab: Int, setTab: (Int) -> Unit, edit: ((CapProject) -> CapProject) -> Unit) {
    val ctx = LocalContext.current
    val player = remember(p.source) {
        ExoPlayer.Builder(ctx).build().apply { setMediaItem(MediaItem.fromUri(Uri.parse(p.source))); prepare() }
    }
    DisposableEffect(player) { onDispose { player.release() } }
    var t by remember { mutableDoubleStateOf(0.0) }
    var playing by remember { mutableStateOf(false) }
    LaunchedEffect(player) { while (true) { t = player.currentPosition / 1000.0; playing = player.isPlaying; delay(40) } }
    val current = p.cues.indexOfFirst { t >= it.s && t < it.e }

    VideoWithCaptions(player, p, t)
    TabRow(selectedTabIndex = tab) {
        Tab(tab == 0, { setTab(0) }, text = { Text("Lines") })
        Tab(tab == 1, { setTab(1) }, text = { Text("Style") })
    }
    Box(Modifier.fillMaxWidth()) {
        if (tab == 0) Lines(p, current, playing, onSeek = { player.seekTo((it * 1000).toLong() + 20) }, edit = edit)
        else StylePanel(p, edit)
    }
}

@OptIn(UnstableApi::class)
@Composable
private fun VideoWithCaptions(player: ExoPlayer, p: CapProject, t: Double) {
    val aspect = if (p.hasVideo) p.w.toFloat() / p.h else 16f / 9f
    BoxWithConstraints(Modifier.fillMaxWidth().background(Color.Black), contentAlignment = Alignment.Center) {
        val maxH = 260.dp
        val h = min(maxWidth.value / aspect, maxH.value).dp
        val w = (h.value * aspect).dp
        Box(Modifier.size(w, h)) {
            AndroidView(
                factory = { c -> PlayerView(c).apply {
                    this.player = player
                    setShowNextButton(false); setShowPreviousButton(false)
                    controllerShowTimeoutMs = 2000
                    setShutterBackgroundColor(android.graphics.Color.BLACK)
                } },
                update = { it.player = player },
                modifier = Modifier.fillMaxSize(),
            )
            CaptionOverlay(p.cues, p.style, p.lang, t, w.value, h.value)
        }
    }
}

private fun parseColor(hex: String, fallback: Color = Color.White): Color =
    runCatching { Color(android.graphics.Color.parseColor(hex)) }.getOrDefault(fallback)

private val fontCache = HashMap<String, FontFamily>()
private fun fontFamily(ctx: Context, f: CapFont): FontFamily = fontCache.getOrPut(f.id) {
    runCatching { FontFamily(Typeface.createFromAsset(ctx.assets, "fonts/${f.file}")) }.getOrDefault(FontFamily.Default)
}

/** The caption at time [t], drawn the way FFmpeg will draw it (same sizes, outline, box and highlight). */
@Composable
private fun CaptionOverlay(cues: List<CapCue>, s: CapStyle, lang: String, t: Double, boxW: Float, boxH: Float) {
    val ctx = LocalContext.current
    val c = cues.firstOrNull { t >= it.s && t < it.e } ?: return
    val ws = Captions.words(c)
    if (ws.isEmpty()) return
    val f = Captions.font(s.font)
    val fam = fontFamily(ctx, f)
    val famBn = fontFamily(ctx, Captions.font(f.bn))
    val density = LocalDensity.current
    // Sizes in dp of the preview box, as fractions of its height like in the ASS file.
    val px = s.size / 100 * boxH
    val fontSize = with(density) { px.dp.toSp() }
    val bord = if (s.box) px * 0.22 else s.outline / 100 * px
    val k = ws.indexOfLast { it.s <= t }.coerceAtLeast(0)
    val shown = ws.indexOfLast { it.s <= t }
    val color = parseColor(s.color)
    val hl = parseColor(s.hl, Color.Yellow)
    val outline = parseColor(s.outlineColor, Color.Black)

    fun text(strokeLayer: Boolean): AnnotatedString = buildAnnotatedString {
        ws.forEachIndexed { x, w ->
            if (x > 0) append(" ")
            val on = (s.mode == "word" || s.mode == "pop") && x == k
            val hidden = s.mode == "reveal" && x > shown
            for (part in Captions.splitBn(Captions.upper(w.t, s, lang))) {
                val col = when { hidden -> Color.Transparent; strokeLayer -> outline; on -> hl; else -> color }
                withStyle(SpanStyle(color = col, fontFamily = if (Captions.BN_RUN.matches(part)) famBn else fam,
                    fontSize = if (on && s.mode == "pop") fontSize * 1.15f else fontSize)) { append(part) }
            }
        }
    }
    val align = when (s.pos) { "top" -> Alignment.TopCenter; "middle" -> Alignment.Center; else -> Alignment.BottomCenter }
    val edge = (s.offset / 100 * boxH).dp
    Box(Modifier.fillMaxSize().padding(horizontal = (boxW * 0.07f).dp)
        .padding(top = if (s.pos == "top") edge else 0.dp, bottom = if (s.pos == "bottom") edge else 0.dp), contentAlignment = align) {
        val boxMod = if (s.box) Modifier.background(parseColor(s.boxColor, Color.Black).copy(alpha = s.boxOpacity / 100f), RoundedCornerShape(2.dp))
            .padding(bord.dp) else Modifier
        Box(boxMod) {
            val base = TextStyle(textAlign = TextAlign.Center, lineHeight = fontSize * 1.15f)
            // Same shadow as the ASS file: (outline × 0.6 + 2) video pixels down and right, 55% black.
            val shad = with(density) { (bord * 0.6 + 2.0 * boxH / 1080).dp.toPx() }
            val shadow = if (s.shadow && !s.box) Shadow(Color.Black.copy(alpha = 0.55f), Offset(shad, shad), 0f) else null
            if (!s.box && bord > 0) Text(text(true), style = base.copy(
                drawStyle = Stroke(width = with(density) { (bord * 2).dp.toPx() }, join = StrokeJoin.Round), shadow = shadow))
            Text(text(false), style = base.copy(shadow = if (bord <= 0) shadow else null))
        }
    }
}

@Composable
private fun Lines(p: CapProject, current: Int, playing: Boolean, onSeek: (Double) -> Unit, edit: ((CapProject) -> CapProject) -> Unit) {
    val list = rememberLazyListState()
    LaunchedEffect(current, playing) {
        if (playing && current >= 0) {
            val vis = list.layoutInfo.visibleItemsInfo
            if (vis.none { it.index == current } || vis.lastOrNull()?.index == current) list.animateScrollToItem(max(0, current - 1))
        }
    }
    LazyColumn(state = list, modifier = Modifier.fillMaxSize(), contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp)) {
        item {
            Text("Tap a time to jump there. Change any word; the timing follows.", style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(4.dp))
        }
        itemsIndexed(p.cues, key = { i, c -> "$i:${c.s}" }) { i, c ->
            val now = i == current
            Surface(shape = RoundedCornerShape(12.dp),
                color = if (now) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surfaceContainerLow) {
                Row(Modifier.fillMaxWidth().padding(start = 4.dp, end = 0.dp), verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.clip(RoundedCornerShape(8.dp)).clickable { onSeek(c.s) }.padding(8.dp)) {
                        Text(clock(c.s), style = MaterialTheme.typography.labelMedium, fontWeight = FontWeight.SemiBold, color = MaterialTheme.colorScheme.primary)
                        Text(clock(c.e), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    var value by remember(c.text) { mutableStateOf(c.text) }
                    TextField(value, { v ->
                        value = v.replace('\n', ' ')
                        edit { pr -> pr.copy(cues = pr.cues.mapIndexed { x, old -> if (x == i) old.copy(text = value) else old }) }
                    }, Modifier.weight(1f), textStyle = MaterialTheme.typography.bodyLarge,
                        colors = TextFieldDefaults.colors(focusedContainerColor = Color.Transparent, unfocusedContainerColor = Color.Transparent,
                            focusedIndicatorColor = Color.Transparent, unfocusedIndicatorColor = Color.Transparent))
                    IconButton(onClick = { edit { pr -> pr.copy(cues = pr.cues.filterIndexed { x, _ -> x != i }) } }) {
                        Icon(Icons.Outlined.Close, "Delete line", tint = MaterialTheme.colorScheme.outline)
                    }
                }
            }
        }
        item { Spacer(Modifier.height(24.dp)) }
    }
}

private val SWATCHES = listOf("#ffffff", "#ffd400", "#3ce37b", "#4fc3f7", "#ff5c8a", "#ff8a00", "#000000")

@kotlin.OptIn(ExperimentalLayoutApi::class)
@Composable
private fun StylePanel(p: CapProject, edit: ((CapProject) -> CapProject) -> Unit) {
    val s = p.style
    fun style(f: (CapStyle) -> CapStyle) = edit { it.copy(style = f(it.style)) }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
        Text("Look", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
        LazyRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            items(Captions.PRESETS) { pr ->
                val on = s.preset == pr.id
                Surface(onClick = { style { pr.style.copy(preset = pr.id) } }, shape = RoundedCornerShape(12.dp),
                    color = if (on) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surfaceContainerLow,
                    border = if (on) BorderStroke(2.dp, MaterialTheme.colorScheme.primary) else null) {
                    Column(Modifier.width(130.dp).padding(10.dp)) {
                        Text(pr.title, fontWeight = FontWeight.SemiBold)
                        Text(pr.desc, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 2)
                    }
                }
            }
        }

        Text("Words per caption", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
        FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            Captions.CHUNKS.forEach { (v, label) ->
                FilterChip(selected = p.chunk == v, label = { Text(label) }, onClick = {
                    edit { pr ->
                        val n = v.toIntOrNull()
                        val src = pr.orig ?: pr.cues
                        pr.copy(chunk = v, cues = if (n == null) src else Captions.chunk(src, n))
                    }
                })
            }
        }
        Text("Changing this starts again from what AnySave heard, so text you fixed is lost.", style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant)

        Text("Highlight", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
        FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            listOf("none" to "None", "word" to "Word lights up", "pop" to "Word pops", "reveal" to "Word by word").forEach { (v, label) ->
                FilterChip(selected = s.mode == v, onClick = { style { it.copy(mode = v) } }, label = { Text(label) })
            }
        }

        Text("Font", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
        FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            Captions.FONTS.filter { !it.hidden }.forEach { f ->
                FilterChip(selected = s.font == f.id, onClick = { style { it.copy(font = f.id) } }, label = { Text(f.label) })
            }
        }

        LabeledSlider("Size", s.size.toFloat(), 3f..10f) { v -> style { it.copy(size = (v * 10).roundToInt() / 10.0) } }

        Text("Place", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            listOf("top" to "Top", "middle" to "Middle", "bottom" to "Bottom").forEach { (v, label) ->
                FilterChip(selected = s.pos == v, onClick = { style { it.copy(pos = v, offset = if (v == "middle") 0.0 else it.offset.takeIf { o -> o > 0 } ?: 8.0) } }, label = { Text(label) })
            }
        }
        if (s.pos != "middle") LabeledSlider("Distance from the edge", s.offset.toFloat(), 0f..30f) { v -> style { it.copy(offset = v.roundToInt().toDouble()) } }

        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("ALL CAPITALS", Modifier.weight(1f)); Switch(s.upper, { v -> style { it.copy(upper = v) } })
        }
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("Dark box behind the text", Modifier.weight(1f)); Switch(s.box, { v -> style { it.copy(box = v) } })
        }
        if (!s.box) {
            LabeledSlider("Outline", s.outline.toFloat(), 0f..20f) { v -> style { it.copy(outline = v.roundToInt().toDouble()) } }
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text("Shadow", Modifier.weight(1f)); Switch(s.shadow, { v -> style { it.copy(shadow = v) } })
            }
        }
        ColorRow("Text colour", s.color) { c -> style { it.copy(color = c) } }
        if (s.mode == "word" || s.mode == "pop") ColorRow("Highlight colour", s.hl) { c -> style { it.copy(hl = c) } }
        Spacer(Modifier.height(24.dp))
    }
}

@Composable
private fun LabeledSlider(label: String, value: Float, range: ClosedFloatingPointRange<Float>, onChange: (Float) -> Unit) {
    Column {
        Text(label, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
        Slider(value.coerceIn(range), onChange, valueRange = range)
    }
}

@Composable
private fun ColorRow(label: String, value: String, onPick: (String) -> Unit) {
    Column {
        Text(label, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
        Spacer(Modifier.height(6.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            SWATCHES.forEach { c ->
                val on = c.equals(value, ignoreCase = true)
                Box(Modifier.size(34.dp).clip(CircleShape).background(parseColor(c))
                    .border(if (on) 3.dp else 1.dp, if (on) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.outlineVariant, CircleShape)
                    .clickable { onPick(c) })
            }
        }
    }
}
