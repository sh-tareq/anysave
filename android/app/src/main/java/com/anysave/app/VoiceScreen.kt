package com.anysave.app

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.media.MediaPlayer
import android.media.MediaRecorder
import android.net.Uri
import android.os.Build
import android.provider.OpenableColumns
import android.widget.Toast
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import kotlin.math.roundToInt

// ---------- Studio: Captions, Clean up, Read aloud ----------

@Composable
fun StudioScreen(jobs: List<Job>, onOpenDownloads: () -> Unit) {
    var sub by rememberSaveable { mutableIntStateOf(0) }
    val tabs: @Composable () -> Unit = {
        TabRow(selectedTabIndex = sub) {
            listOf("Captions", "Clean up", "Read aloud").forEachIndexed { i, t -> Tab(sub == i, { sub = i }, text = { Text(t, maxLines = 1) },
                unselectedContentColor = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
    }
    when (sub) {
        0 -> CaptionsScreen(jobs, onOpenDownloads, tabs)
        1 -> CleanUpScreen(jobs, onOpenDownloads, tabs)
        else -> ReadAloudScreen(jobs, onOpenDownloads, tabs)
    }
}

/** The Studio tab's top bar and its three tabs, around one of them. */
@Composable
fun StudioFrame(tabs: @Composable () -> Unit, content: @Composable ColumnScope.() -> Unit) {
    ScreenScaffold("Studio", "Captions, voices and clean-up, on your phone") { pad ->
        Column(Modifier.fillMaxSize().padding(pad)) { tabs(); content() }
    }
}

// ---------- small shared pieces ----------

/** Plays one sound file at a time. */
class SoundPlayer {
    private var mp: MediaPlayer? = null
    var playing by mutableStateOf<String?>(null)
        private set

    fun toggle(key: String, f: File) {
        val was = playing
        stop()
        if (was == key) return
        runCatching {
            mp = MediaPlayer().apply {
                setDataSource(f.absolutePath)
                setOnCompletionListener { if (playing == key) stop() }
                prepare(); start()
            }
            playing = key
        }
    }

    fun stop() { runCatching { mp?.stop() }; mp?.release(); mp = null; playing = null }
}

@Composable
private fun rememberPlayer(): SoundPlayer {
    val p = remember { SoundPlayer() }
    DisposableEffect(p) { onDispose { p.stop() } }
    return p
}

private data class PickedFile(val uri: Uri, val name: String, val dur: Double, val video: Boolean)

private suspend fun readPicked(ctx: Context, uri: Uri): PickedFile? = withContext(Dispatchers.IO) {
    runCatching {
        var name = "recording"
        ctx.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { if (it.moveToFirst()) name = it.getString(0) ?: name }
        val r = android.media.MediaMetadataRetriever()
        try {
            r.setDataSource(ctx, uri)
            val dur = r.extractMetadata(android.media.MediaMetadataRetriever.METADATA_KEY_DURATION)?.toLongOrNull()?.div(1000.0) ?: 0.0
            if (r.extractMetadata(android.media.MediaMetadataRetriever.METADATA_KEY_HAS_AUDIO) != "yes") return@runCatching null
            PickedFile(uri, name, dur, r.extractMetadata(android.media.MediaMetadataRetriever.METADATA_KEY_HAS_VIDEO) == "yes")
        } finally { r.release() }
    }.getOrNull()
}

private fun timeText(s: Double): String { val t = s.roundToInt(); return "%d:%02d".format(t / 60, t % 60) }

private fun minutesText(secs: Double): String = when {
    secs < 60 -> "under a minute"
    secs < 90 -> "about 1 minute"
    else -> "about ${(secs / 60).roundToInt()} minutes"
}

/** A job's progress line with "Show" (opens Downloads). */
@Composable
private fun JobLine(job: Job?, doneText: String, onOpenDownloads: () -> Unit) {
    if (job == null) return
    Surface(shape = RoundedCornerShape(14.dp), color = MaterialTheme.colorScheme.surfaceContainerLow) {
        Column(Modifier.fillMaxWidth().padding(14.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(when (job.status) {
                    JobStatus.DONE -> doneText
                    JobStatus.ERROR -> job.error ?: "Something went wrong."
                    JobStatus.CANCELLED -> "Stopped"
                    else -> "Working · ${job.percent.toInt()}%" + (job.eta.takeIf { it.isNotBlank() && it != "?" && it != "Preparing…" }?.let { " · $it left" } ?: "")
                }, Modifier.weight(1f), color = if (job.status == JobStatus.ERROR) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface)
                if (job.active) TextButton(onClick = { DownloadService.cancel(LocalContextHolder.ctx!!, job.id) }) { Text("Stop") }
                else if (job.status == JobStatus.DONE) TextButton(onClick = onOpenDownloads) { Text("Show") }
            }
            if (job.active) LinearProgressIndicator(progress = { job.percent / 100f }, Modifier.fillMaxWidth().padding(top = 6.dp))
        }
    }
}

/** Lets small helpers reach the context without passing it everywhere. */
private object LocalContextHolder { var ctx: Context? = null }

@Composable
private fun Hero(icon: ImageVector, title: String, text: String, button: String, onPick: () -> Unit, onDownloads: () -> Unit) {
    Surface(shape = RoundedCornerShape(20.dp), color = MaterialTheme.colorScheme.surfaceContainerLowest,
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
        Column(Modifier.fillMaxWidth().padding(22.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            Box(Modifier.size(64.dp).clip(CircleShape).background(MaterialTheme.colorScheme.primaryContainer), contentAlignment = Alignment.Center) {
                Icon(icon, null, Modifier.size(32.dp), tint = MaterialTheme.colorScheme.primary)
            }
            Spacer(Modifier.height(14.dp))
            Text(title, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
            Text(text, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center,
                modifier = Modifier.padding(top = 4.dp))
            Spacer(Modifier.height(18.dp))
            Button(onClick = onPick, modifier = Modifier.fillMaxWidth().height(50.dp), shape = RoundedCornerShape(14.dp)) {
                Icon(Icons.Outlined.FolderOpen, null, Modifier.size(20.dp)); Spacer(Modifier.width(8.dp))
                Text(button, fontWeight = FontWeight.SemiBold)
            }
            Spacer(Modifier.height(4.dp))
            TextButton(onClick = onDownloads) { Text("Pick from my downloads") }
        }
    }
}

@kotlin.OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun DownloadsSheet(jobs: List<Job>, onDismiss: () -> Unit, onPick: (Uri) -> Unit) {
    val media = setOf("mp4", "mkv", "webm", "mov", "m4a", "mp3", "opus", "ogg", "wav", "aac", "flac", "3gp", "m4v")
    val done = jobs.filter { it.status == JobStatus.DONE && it.uri != null && FileKinds.extOf(it.fileName ?: "") in media }
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(Modifier.padding(horizontal = 16.dp)) {
            Text("Choose from your downloads", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
            if (done.isEmpty()) Text("No finished videos or audio yet.", Modifier.padding(vertical = 24.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
            LazyColumn(Modifier.heightIn(max = 480.dp), contentPadding = PaddingValues(vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                items(done, key = { it.id }) { j ->
                    Surface(onClick = { onPick(j.uri!!) }, shape = RoundedCornerShape(12.dp)) {
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

@Composable
private fun Label(t: String) = Text(t, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)

@Composable
private fun Note(t: String) = Text(t, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)

/** "Downloads X once" with a Get button and progress, for a voice pack that isn't here yet. */
@Composable
private fun PackNote(id: String) {
    val ctx = LocalContext.current
    val dl by Voice.downloading.collectAsStateWithLifecycle()
    val errs by Voice.errors.collectAsStateWithLifecycle()
    val changed by Voice.changed.collectAsStateWithLifecycle()
    if (id.isEmpty() || remember(id, changed) { Voice.ready(ctx, id) }) return
    val p = Voice.pack(id)
    val pct = dl[id]
    Surface(shape = RoundedCornerShape(12.dp), color = MaterialTheme.colorScheme.secondaryContainer.copy(alpha = 0.5f)) {
        Column(Modifier.fillMaxWidth().padding(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(if (pct != null) "Getting the ${p.label}… ${pct.toInt()}%" else "Needs the ${p.label} (${Engine.humanSize(p.size)}), downloaded once.",
                    Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium)
                if (pct == null) TextButton(onClick = { Voice.startDownload(ctx, id) }) { Text("Get") }
            }
            if (pct != null) LinearProgressIndicator(progress = { pct / 100f }, Modifier.fillMaxWidth().padding(top = 6.dp))
            errs[id]?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
        }
    }
}

// ---------- Clean up ----------

@kotlin.OptIn(ExperimentalLayoutApi::class)
@Composable
fun CleanUpScreen(jobs: List<Job>, onOpenDownloads: () -> Unit, tabs: @Composable () -> Unit) {
    val ctx = LocalContext.current
    LocalContextHolder.ctx = ctx.applicationContext
    val scope = rememberCoroutineScope()
    val player = rememberPlayer()
    var file by remember { mutableStateOf<PickedFile?>(null) }
    var fromDownloads by remember { mutableStateOf(false) }
    val prefs = ctx.getSharedPreferences("voice", Context.MODE_PRIVATE)
    var strength by remember { mutableStateOf(prefs.getString("strength", "strong")!!) }
    var level by remember { mutableStateOf(prefs.getBoolean("level", true)) }
    var format by remember { mutableStateOf("same") }
    var jobId by rememberSaveable { mutableStateOf<String?>(null) }
    var sample by remember { mutableStateOf<Pair<File, File>?>(null) }
    var sampling by remember { mutableStateOf(false) }
    var sampleErr by remember { mutableStateOf<String?>(null) }
    val dl by Voice.downloading.collectAsStateWithLifecycle()
    val changed by Voice.changed.collectAsStateWithLifecycle()

    fun picked(uri: Uri) {
        scope.launch {
            val p = readPicked(ctx, uri)
            if (p == null) Toast.makeText(ctx, "That file has no sound AnySave can read.", Toast.LENGTH_LONG).show()
            else { file = p; sample = null; sampleErr = null; jobId = null }
        }
    }
    val pick = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) { runCatching { ctx.contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION) }; picked(uri) }
    }
    LaunchedEffect(strength, level) { prefs.edit().putString("strength", strength).putBoolean("level", level).apply(); sample = null }

    StudioFrame(tabs) {
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            if (!Voice.available(ctx)) Text("Voice Lab isn't available on this phone's type of processor.", color = MaterialTheme.colorScheme.error)
            val f = file
            if (f == null) {
                Hero(Icons.Outlined.GraphicEq, "Clean up a recording", "Removes background noise (traffic, wind, hum, fans) so voices are clear. Videos keep their picture.",
                    "Choose a video or audio", { pick.launch(arrayOf("video/*", "audio/*")) }, { fromDownloads = true })
                return@Column
            }
            Surface(shape = RoundedCornerShape(14.dp), color = MaterialTheme.colorScheme.surfaceContainerLow) {
                Row(Modifier.fillMaxWidth().padding(14.dp), verticalAlignment = Alignment.CenterVertically) {
                    Icon(if (f.video) Icons.Outlined.Movie else Icons.Outlined.MusicNote, null, tint = MaterialTheme.colorScheme.primary)
                    Spacer(Modifier.width(12.dp))
                    Column(Modifier.weight(1f)) {
                        Text(f.name, maxLines = 1, overflow = TextOverflow.Ellipsis, fontWeight = FontWeight.Medium)
                        Note(timeText(f.dur))
                    }
                    TextButton(onClick = { pick.launch(arrayOf("video/*", "audio/*")) }) { Text("Change") }
                }
            }

            Label("How strong")
            val modes = listOf(Triple("strong", "Strong", "Best for voices"), Triple("light", "Light", "Fast"), Triple("basic", "Basic", "No download"))
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                modes.forEach { (id, t, d) ->
                    FilterChip(selected = strength == id, onClick = { strength = id }, label = { Text("$t · $d") })
                }
            }
            val speed = when (strength) { "strong" -> 1.0; "light" -> 0.25; else -> 0.1 }
            Note("Takes ${minutesText(f.dur * speed + 5)} on a 2018 phone; newer phones are faster.")
            PackNote(Voice.cleanMode(strength).first)
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) { Text("Even out the loudness"); Note("Quiet and loud parts end up at a comfortable level.") }
                Switch(level, { level = it })
            }

            // A 10-second sample, before and after.
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                val pack = Voice.cleanMode(strength).first
                val packOk = remember(pack, changed) { pack.isEmpty() || Voice.ready(ctx, pack) }
                OutlinedButton(enabled = !sampling && packOk && dl.isEmpty(), onClick = {
                    sampling = true; sampleErr = null; player.stop()
                    scope.launch {
                        val id = "pv-${System.currentTimeMillis()}"
                        val r = withContext(Dispatchers.IO) {
                            runCatching {
                                val work = File(ctx.cacheDir, "voice-sample").apply { deleteRecursively(); mkdirs() }
                                val local = previewCopy(ctx, f.uri, f.name) ?: throw Exception("Couldn't read the file.")
                                val at = if (f.dur > 30) f.dur * 0.25 else 0.0
                                val before = File(work, "before.wav")
                                Converter.ffmpeg(ctx, id, listOf("-ss", "%.2f".format(java.util.Locale.ROOT, at), "-i", local.absolutePath, "-t", "10",
                                    "-vn", "-ac", "1", "-c:a", "pcm_s16le", before.absolutePath), 10.0) {}
                                val clean = Voice.denoise(ctx, id, local, work, strength, at, 10.0) {}
                                val after = File(work, "after.wav")
                                Converter.ffmpeg(ctx, id, listOf("-i", clean.absolutePath, "-af", Voice.voiceFilters(strength, level).joinToString(","),
                                    "-c:a", "pcm_s16le", after.absolutePath), 10.0) {}
                                before to after
                            }
                        }
                        sampling = false
                        r.onSuccess { sample = it }.onFailure { sampleErr = it.message }
                    }
                }) {
                    if (sampling) { CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp); Spacer(Modifier.width(8.dp)) }
                    Text(if (sampling) "Making a sample…" else "Hear 10 seconds")
                }
                sample?.let { (b, a) ->
                    FilledTonalButton(onClick = { player.toggle("b", b) }) { Text(if (player.playing == "b") "Stop" else "Before") }
                    FilledTonalButton(onClick = { player.toggle("a", a) }) { Text(if (player.playing == "a") "Stop" else "After") }
                }
            }
            sampleErr?.let { Text(it, color = MaterialTheme.colorScheme.error) }

            Label("Save as")
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                listOf("same" to if (f.video) "Video (same picture)" else "Same as the file", "mp3" to "MP3", "m4a" to "M4A", "wav" to "WAV").forEach { (v, t) ->
                    FilterChip(selected = format == v, onClick = { format = v }, label = { Text(t) })
                }
            }
            val job = jobs.firstOrNull { it.id == jobId }
            Button(onClick = { player.stop(); jobId = VoiceJobs.startClean(ctx, f.uri, f.name, strength, level, format) },
                enabled = Voice.available(ctx) && job?.active != true, modifier = Modifier.fillMaxWidth().height(50.dp), shape = RoundedCornerShape(14.dp)) {
                Icon(Icons.Outlined.AutoFixHigh, null, Modifier.size(20.dp)); Spacer(Modifier.width(8.dp)); Text("Clean up", fontWeight = FontWeight.SemiBold)
            }
            JobLine(job, "Saved in Downloads", onOpenDownloads)
        }
    }
    if (fromDownloads) DownloadsSheet(jobs, { fromDownloads = false }) { fromDownloads = false; picked(it) }
}

// ---------- Read aloud ----------

@kotlin.OptIn(ExperimentalLayoutApi::class)
@Composable
fun ReadAloudScreen(jobs: List<Job>, onOpenDownloads: () -> Unit, tabs: @Composable () -> Unit) {
    val ctx = LocalContext.current
    LocalContextHolder.ctx = ctx.applicationContext
    val scope = rememberCoroutineScope()
    val player = rememberPlayer()
    val prefs = ctx.getSharedPreferences("voice", Context.MODE_PRIVATE)
    var text by rememberSaveable { mutableStateOf(prefs.getString("text", "") ?: "") }
    var voices by remember { mutableStateOf(Voice.voices(ctx)) }
    var voiceId by remember { mutableStateOf(prefs.getString("voice", "t1") ?: "t1") }
    val voice = voices.firstOrNull { it.id == voiceId } ?: voices.first()
    var group by remember { mutableStateOf(voice.group) }
    var speed by remember { mutableFloatStateOf(prefs.getFloat("speed", 1f)) }
    var format by remember { mutableStateOf(prefs.getString("format", "mp3") ?: "mp3") }
    var jobId by rememberSaveable { mutableStateOf<String?>(null) }
    var listening by remember { mutableStateOf(false) }
    var listenErr by remember { mutableStateOf<String?>(null) }
    var preview by remember { mutableStateOf<File?>(null) }
    var copyOpen by remember { mutableStateOf(false) }
    var confirmForget by remember { mutableStateOf(false) }
    var previewId by remember { mutableStateOf<String?>(null) }
    DisposableEffect(Unit) { onDispose { previewId?.let { ConvertTools.cancel(it); Voice.cancel(it) } } }
    LaunchedEffect(text, voiceId, speed, format) {
        delay(400)
        prefs.edit().putString("text", text.take(20000)).putString("voice", voiceId).putFloat("speed", speed).putString("format", format).apply()
        preview = null
    }
    // A copied voice is made by a job; read the list again when jobs change.
    val jobKey = jobs.joinToString { "${it.id}:${it.status}" }
    val dl by Voice.downloading.collectAsStateWithLifecycle()
    val changed by Voice.changed.collectAsStateWithLifecycle()
    LaunchedEffect(jobKey, changed) { voices = Voice.voices(ctx) }
    val packReady = remember(voice.pack, changed) { Voice.ready(ctx, voice.pack) }

    StudioFrame(tabs) {
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            if (!Voice.available(ctx)) Text("Voice Lab isn't available on this phone's type of processor.", color = MaterialTheme.colorScheme.error)
            OutlinedTextField(text, { text = it }, Modifier.fillMaxWidth().heightIn(min = 150.dp), label = { Text("Text to read aloud") },
                placeholder = { Text("Type or paste text…") }, supportingText = { Text("${text.length} characters") })

            Label("Voice")
            val groups = voices.map { it.group }.distinct().let { g -> if ("Your voices" in g) g else g.toMutableList().apply { add(2.coerceAtMost(size), "Your voices") } }
            FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                groups.forEach { g -> FilterChip(selected = group == g, onClick = {
                    group = g
                    if (voice.group != g) voices.firstOrNull { it.group == g }?.let { voiceId = it.id }
                }, label = { Text(g) }) }
            }
            val inGroup = voices.filter { it.group == group }
            FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                inGroup.forEach { v ->
                    FilterChip(selected = voiceId == v.id, onClick = { voiceId = v.id }, label = { Text(v.name) },
                        leadingIcon = if (v.gender.isNotEmpty()) { { Icon(if (v.gender == "man") Icons.Outlined.Man else Icons.Outlined.Woman, null, Modifier.size(16.dp)) } } else null)
                }
                if (group == "Your voices") AssistChip(onClick = { copyOpen = true }, label = { Text("Copy a voice") },
                    leadingIcon = { Icon(Icons.Outlined.Add, null, Modifier.size(16.dp)) })
            }
            when {
                group == "Your voices" && inGroup.isEmpty() -> Note("Make a voice from 10–20 seconds of someone talking (yours, or with their permission). It reads English text.")
                voice.group != group -> {}
                voice.pack == "kokoro" -> Note("These voices are slow on older phones (several times the length of the speech).")
                voice.pack == "pocket" -> Note("Copied voices read English text.")
            }
            if (voice.pack == "pocket" && voice.group == group) TextButton(onClick = { confirmForget = true }) { Text("Delete “${voice.name}”") }
            if (voice.group == group) PackNote(voice.pack)

            Label("Speed ${"%.1f".format(speed)}×")
            Slider(speed, { speed = (it * 10).roundToInt() / 10f }, valueRange = 0.5f..2f)
            Label("Save as")
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                listOf("mp3" to "MP3", "m4a" to "M4A", "wav" to "WAV").forEach { (v, t) -> FilterChip(selected = format == v, onClick = { format = v }, label = { Text(t) }) }
            }

            val job = jobs.firstOrNull { it.id == jobId }
            val ready = packReady && dl.isEmpty() && voice.group == group
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                OutlinedButton(enabled = text.isNotBlank() && ready && !listening, modifier = Modifier.weight(1f).height(50.dp), shape = RoundedCornerShape(14.dp), onClick = {
                    val p = preview
                    if (p != null) { player.toggle("say", p); return@OutlinedButton }
                    listening = true; listenErr = null; player.stop()
                    val id = "pv-${System.currentTimeMillis()}".also { previewId = it }
                    scope.launch {
                        val r = withContext(Dispatchers.IO) {
                            runCatching {
                                val work = File(ctx.cacheDir, "voice-say").apply { deleteRecursively(); mkdirs() }
                                // The first few sentences, for a quick listen.
                                val first = Voice.splitText(text).fold(StringBuilder()) { b, pc -> if (b.length < 300) b.append(pc.text).append("\n\n"); b }.toString()
                                Voice.speak(ctx, id, work, first, voice, speed.toDouble()) {}
                            }
                        }
                        listening = false
                        r.onSuccess { preview = it; player.toggle("say", it) }.onFailure { if (it.message != "Cancelled") listenErr = it.message }
                    }
                }) {
                    if (listening) { CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp); Spacer(Modifier.width(8.dp)) }
                    else Icon(if (player.playing == "say") Icons.Outlined.Stop else Icons.Outlined.PlayArrow, null, Modifier.size(20.dp))
                    Text(if (listening) "Reading…" else if (player.playing == "say") "Stop" else "Listen")
                }
                Button(enabled = text.isNotBlank() && Voice.available(ctx) && job?.active != true && voice.group == group, modifier = Modifier.weight(1f).height(50.dp),
                    shape = RoundedCornerShape(14.dp), onClick = { player.stop(); jobId = VoiceJobs.startSay(ctx, text, voice, speed.toDouble(), format) }) {
                    Icon(Icons.Outlined.Save, null, Modifier.size(20.dp)); Spacer(Modifier.width(6.dp)); Text("Save")
                }
            }
            if (!packReady && voice.group == group) Note("Listen works once the voices are here; Save downloads them by itself.")
            listenErr?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            JobLine(job, "Saved in Downloads", onOpenDownloads)
        }
    }

    if (copyOpen) CopyVoiceSheet(jobs, onDismiss = { copyOpen = false }) { newId ->
        voices = Voice.voices(ctx)
        voices.lastOrNull { it.pack == "pocket" }?.let { voiceId = it.id; group = "Your voices" }
        copyOpen = false
    }
    if (confirmForget) AlertDialog(
        onDismissRequest = { confirmForget = false },
        title = { Text("Delete “${voice.name}”?") },
        text = { Text("The copied voice is removed from this phone. Files already saved stay.") },
        confirmButton = { TextButton(onClick = {
            Voice.forgetVoice(ctx, voice.id); voices = Voice.voices(ctx); voiceId = "t1"; confirmForget = false
        }) { Text("Delete") } },
        dismissButton = { TextButton(onClick = { confirmForget = false }) { Text("Keep") } },
    )
}

// ---------- Copy a voice ----------

@kotlin.OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun CopyVoiceSheet(jobs: List<Job>, onDismiss: () -> Unit, onMade: (String) -> Unit) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val player = rememberPlayer()
    var sample by remember { mutableStateOf<Pair<Uri, String>?>(null) } // uri, label
    var name by remember { mutableStateOf("") }
    var consent by remember { mutableStateOf(false) }
    var recording by remember { mutableStateOf(false) }
    var recSecs by remember { mutableIntStateOf(0) }
    var recorder by remember { mutableStateOf<MediaRecorder?>(null) }
    var jobId by remember { mutableStateOf<String?>(null) }
    val recFile = remember { File(ctx.cacheDir, "voice-rec.m4a") }

    fun stopRec() {
        recorder?.let { r -> runCatching { r.stop() }; r.release() }
        recorder = null
        if (recording) {
            recording = false
            if (recFile.length() > 0) sample = Uri.fromFile(recFile) to "Your recording (${recSecs} s)"
        }
    }
    fun startRec() {
        player.stop()
        recFile.delete()
        val r = (if (Build.VERSION.SDK_INT >= 31) MediaRecorder(ctx) else @Suppress("DEPRECATION") MediaRecorder()).apply {
            setAudioSource(MediaRecorder.AudioSource.MIC)
            setOutputFormat(MediaRecorder.OutputFormat.MPEG_4)
            setAudioEncoder(MediaRecorder.AudioEncoder.AAC)
            setAudioSamplingRate(44100); setAudioEncodingBitRate(128000); setAudioChannels(1)
            setOutputFile(recFile.absolutePath)
        }
        runCatching { r.prepare(); r.start() }.onFailure {
            r.release(); Toast.makeText(ctx, "Couldn't use the microphone.", Toast.LENGTH_LONG).show(); return
        }
        recorder = r; recording = true; recSecs = 0; sample = null
        scope.launch { while (recording) { delay(1000); if (recording) recSecs++; if (recSecs >= 20) stopRec() } }
    }
    DisposableEffect(Unit) { onDispose { stopRec() } }
    val mic = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        if (ok) startRec() else Toast.makeText(ctx, "AnySave needs the microphone to record. You can choose a recording instead.", Toast.LENGTH_LONG).show()
    }
    val pick = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) {
            runCatching { ctx.contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION) }
            scope.launch {
                val p = readPicked(ctx, uri)
                if (p == null) Toast.makeText(ctx, "That file has no sound AnySave can read.", Toast.LENGTH_LONG).show()
                else { stopRec(); sample = uri to "${p.name} (${timeText(p.dur)})" }
            }
        }
    }
    val job = jobs.firstOrNull { it.id == jobId }
    // The job is taken off the list when the voice is made.
    LaunchedEffect(job == null, jobId) { if (jobId != null && job == null) onMade(jobId!!) }

    ModalBottomSheet(onDismissRequest = { if (job?.active != true) onDismiss() },
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.padding(horizontal = 20.dp).padding(bottom = 24.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("Copy a voice", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
            Note("Record or choose 10–20 seconds of one person talking clearly, with no music. The new voice reads English text.")
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                Button(onClick = {
                    if (recording) stopRec()
                    else if (ContextCompat.checkSelfPermission(ctx, Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED) startRec()
                    else mic.launch(Manifest.permission.RECORD_AUDIO)
                }, Modifier.weight(1f), colors = if (recording) ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error) else ButtonDefaults.buttonColors()) {
                    Icon(if (recording) Icons.Outlined.Stop else Icons.Outlined.Mic, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp))
                    Text(if (recording) "Stop · ${recSecs} s" else "Record")
                }
                OutlinedButton(onClick = { pick.launch(arrayOf("audio/*", "video/*")) }, Modifier.weight(1f), enabled = !recording) { Text("Choose a file") }
            }
            if (recording) Note("Talk normally for 10–20 seconds. It stops by itself at 20.")
            sample?.let { (uri, label) ->
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(Icons.Outlined.CheckCircle, null, tint = MaterialTheme.colorScheme.primary); Spacer(Modifier.width(8.dp))
                    Text(label, Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
                    if (uri.scheme == "file") TextButton(onClick = { player.toggle("rec", recFile) }) { Text(if (player.playing == "rec") "Stop" else "Play") }
                }
            }
            OutlinedTextField(name, { name = it.take(40) }, Modifier.fillMaxWidth(), label = { Text("Name for the voice") }, singleLine = true,
                placeholder = { Text("My voice") })
            Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).clickable { consent = !consent }, verticalAlignment = Alignment.Top) {
                Checkbox(consent, { consent = it })
                Text(Voice.CONSENT, Modifier.padding(top = 12.dp))
            }
            PackNote("pocket")
            Button(enabled = sample != null && consent && !recording && job?.active != true && Voice.available(ctx),
                onClick = {
                    player.stop()
                    // A recording in the app's cache: copy it so the job's file stays put.
                    val (uri, _) = sample!!
                    val src = if (uri.scheme == "file") Uri.fromFile(VoiceJobs.handoffFile(ctx, "m4a").also { recFile.copyTo(it) }) else uri
                    jobId = VoiceJobs.startCopy(ctx, src, name.ifBlank { "My voice" })
                }, modifier = Modifier.fillMaxWidth().height(50.dp), shape = RoundedCornerShape(14.dp)) { Text("Make voice", fontWeight = FontWeight.SemiBold) }
            if (job != null) {
                if (job.active) {
                    Text("Making the voice… ${job.percent.toInt()}%")
                    LinearProgressIndicator(progress = { job.percent / 100f }, Modifier.fillMaxWidth())
                } else if (job.status == JobStatus.ERROR) Text(job.error ?: "Couldn't make the voice.", color = MaterialTheme.colorScheme.error)
            }
        }
    }
}
