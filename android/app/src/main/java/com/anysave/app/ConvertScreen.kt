package com.anysave.app

import android.content.Intent
import android.media.MediaMetadataRetriever
import android.net.Uri
import android.provider.OpenableColumns
import android.widget.Toast
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** Lives as long as the app, so leaving the Convert tab doesn't lose the chosen files and settings. */
private object ConvertMemory {
    val files = mutableStateListOf<Picked>()
    val opts = mutableStateMapOf<String, String>()
    val toolId = mutableStateOf<String?>(null)
    val format = mutableStateOf("mp4")
    val height = mutableStateOf<Int?>(null)
    val targetMb = mutableStateOf<Int?>(null)
    val trim = mutableStateOf(false)
    val range = mutableStateOf(0f..1f)
    val removeAudio = mutableStateOf(false)
    var appliedFor: String? = null
}

private data class Picked(
    val uri: Uri, val name: String, val size: Long, val mime: String?, val kind: InKind,
    val durationSec: Double?, val width: Int?, val height: Int?,
) {
    fun preview() = PreviewFile(uri, name, size, kind, durationSec, width, height)
}

/** One choice in a picker sheet. */
private data class Choice(val value: String?, val label: String, val desc: String? = null)
private data class SheetReq(val title: String, val choices: List<Choice>, val selected: String?, val onPick: (String?) -> Unit)

private fun kindColor(k: InKind) = when (k) {
    InKind.VIDEO -> Color(0xFF7C5CFF); InKind.AUDIO -> Color(0xFFF07A2C); InKind.IMAGE -> Color(0xFFD4489C)
    InKind.PDF -> Color(0xFFE5484D); InKind.DOC -> Color(0xFF3D82F6); InKind.ARCHIVE -> Color(0xFF7B818C)
    InKind.SUBS -> Color(0xFF0EA5B7); InKind.DATA -> Color(0xFF1D9A52); InKind.OTHER -> Color(0xFF8A8F99)
}

private fun kindIcon(k: InKind) = when (k) {
    InKind.VIDEO -> Icons.Outlined.Movie; InKind.AUDIO -> Icons.Outlined.MusicNote; InKind.IMAGE -> Icons.Outlined.Image
    InKind.PDF -> Icons.Outlined.PictureAsPdf; InKind.DOC -> Icons.Outlined.Description; InKind.ARCHIVE -> Icons.Outlined.FolderZip
    InKind.SUBS -> Icons.Outlined.Subtitles; InKind.DATA -> Icons.Outlined.TableChart; InKind.OTHER -> Icons.Outlined.InsertDriveFile
}

private data class ToolLook(val icon: ImageVector, val color: Color, val tile: String)

private fun toolLook(id: String): ToolLook = when (id) {
    ConvertTools.MEDIA -> ToolLook(Icons.Outlined.Transform, Color(0xFF7C5CFF), "Convert")
    ConvertTools.JOIN -> ToolLook(Icons.Outlined.Link, Color(0xFF7C5CFF), "Join")
    ConvertTools.FRAMES -> ToolLook(Icons.Outlined.PhotoLibrary, Color(0xFFD4489C), "To pictures")
    ConvertTools.ADD_SUBS -> ToolLook(Icons.Outlined.ClosedCaption, Color(0xFF0EA5B7), "Add subtitles")
    ConvertTools.IMAGE -> ToolLook(Icons.Outlined.Transform, Color(0xFFD4489C), "Convert")
    ConvertTools.IMAGES_PDF -> ToolLook(Icons.Outlined.PictureAsPdf, Color(0xFFE5484D), "To PDF")
    ConvertTools.PDF_IMAGES -> ToolLook(Icons.Outlined.PhotoLibrary, Color(0xFFD4489C), "To pictures")
    ConvertTools.PDF_MERGE -> ToolLook(Icons.Outlined.MergeType, Color(0xFFE5484D), "Merge")
    ConvertTools.PDF_PAGES -> ToolLook(Icons.Outlined.ContentCut, Color(0xFFE5484D), "Pick pages")
    ConvertTools.PDF_COMPRESS -> ToolLook(Icons.Outlined.Compress, Color(0xFF1D9A52), "Smaller")
    ConvertTools.PDF_ROTATE -> ToolLook(Icons.Outlined.RotateRight, Color(0xFF3D82F6), "Rotate")
    ConvertTools.PDF_PROTECT -> ToolLook(Icons.Outlined.Lock, Color(0xFFB97206), "Password")
    ConvertTools.PDF_UNLOCK -> ToolLook(Icons.Outlined.LockOpen, Color(0xFFB97206), "Unlock")
    ConvertTools.DOC -> ToolLook(Icons.Outlined.Description, Color(0xFF3D82F6), "Convert")
    ConvertTools.UNPACK -> ToolLook(Icons.Outlined.Unarchive, Color(0xFF7B818C), "Unpack")
    ConvertTools.PACK -> ToolLook(Icons.Outlined.Archive, Color(0xFF7B818C), "Pack")
    ConvertTools.SUBS -> ToolLook(Icons.Outlined.Subtitles, Color(0xFF0EA5B7), "Convert")
    ConvertTools.DATA -> ToolLook(Icons.Outlined.TableChart, Color(0xFF1D9A52), "Convert")
    else -> ToolLook(Icons.Outlined.Transform, Color(0xFF8A8F99), id)
}

/** The groups shown before anything is chosen; tapping one opens the file picker for that kind. */
private data class Group(val title: String, val examples: String, val kind: InKind, val mimes: Array<String>)

private val groups = listOf(
    Group("Video", "MP4 · MKV · MOV · WebM · GIF", InKind.VIDEO, arrayOf("video/*")),
    Group("Audio", "MP3 · M4A · WAV · FLAC · Opus", InKind.AUDIO, arrayOf("audio/*")),
    Group("Pictures", "JPG · PNG · WebP · HEIC · AVIF", InKind.IMAGE, arrayOf("image/*")),
    Group("PDF", "Merge · split · shrink · lock", InKind.PDF, arrayOf("application/pdf")),
    Group("Documents", "Word · ODT · EPUB · Markdown", InKind.DOC, arrayOf("*/*")),
    Group("Archives", "ZIP · 7z · TAR · GZ", InKind.ARCHIVE, arrayOf("*/*")),
    Group("Subtitles", "SRT · VTT · ASS · LRC", InKind.SUBS, arrayOf("*/*")),
    Group("Spreadsheets", "Excel · CSV · TSV · JSON", InKind.DATA, arrayOf("*/*")),
)

/** Tools where the order of the files matters. */
private val ordered = setOf(ConvertTools.JOIN, ConvertTools.PDF_MERGE, ConvertTools.IMAGES_PDF)

private data class Preset(val label: String, val sub: String, val icon: ImageVector, val format: String, val codec: String,
                          val height: Int?, val aspect: String?, val targetMb: Int?)

private val presets = listOf(
    Preset("WhatsApp", "720p · H.264", Icons.Outlined.Chat, "mp4", "h264", 720, null, null),
    Preset("Reels · TikTok", "Vertical 9:16", Icons.Outlined.StayCurrentPortrait, "mp4", "h264", null, "9:16", 0),
    Preset("YouTube", "1080p · high quality", Icons.Outlined.SmartDisplay, "mp4", "h264", 1080, null, 0),
    Preset("Email", "480p · under 25 MB", Icons.Outlined.Email, "mp4", "h264", 480, null, 25),
    Preset("Smallest", "720p · H.265", Icons.Outlined.Compress, "mp4", "h265", 720, null, null),
)

private val formatInfo = mapOf(
    "mp4" to "Plays everywhere", "mkv" to "Keeps every track; best on computers", "mov" to "For Apple devices and editing apps",
    "webm" to "For websites", "gif" to "Short silent animation",
    "mp3" to "Plays everywhere", "m4a" to "Smaller than MP3 at the same quality", "wav" to "Uncompressed, very large",
    "flac" to "Lossless, smaller than WAV", "opus" to "Smallest, great for speech", "ogg" to "Open format",
    "jpg" to "Photos; works everywhere", "png" to "Sharp edges and transparency", "webp" to "Smaller than JPG, for the web",
    "avif" to "Smallest; newer phones and browsers", "bmp" to "Uncompressed, very large", "tiff" to "For printing and scanning",
    "ico" to "Website and app icons (256 px)",
    "pdf" to "Pages for reading and printing", "txt" to "Just the text", "html" to "A web page", "epub" to "An ebook for reading apps",
    "zip" to "Opens on every phone and computer", "7z" to "Smallest; Windows needs 7-Zip", "tar.gz" to "Common on Linux and Mac",
    "srt" to "Works almost everywhere", "vtt" to "For websites", "ass" to "Keeps styles, for video players", "lrc" to "Song lyrics",
    "xlsx" to "Excel, Google Sheets, Numbers", "csv" to "Plain table, opens anywhere", "tsv" to "Table with tabs", "json" to "For apps and developers",
)

private fun fmtLabel(f: String) = when (f) { "xlsx" -> "Excel"; "txt" -> "Text"; "html" -> "Web page"; "epub" -> "EPUB ebook"; "tar.gz" -> "TAR.GZ"; else -> f.uppercase() }

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ConvertScreen(jobs: List<Job>, onStarted: () -> Unit) {
    val ctx = LocalContext.current
    val m = ConvertMemory
    val files = m.files
    var pending by remember { mutableStateOf<List<Uri>>(emptyList()) }
    var fromDownloads by remember { mutableStateOf(false) }
    var sheet by remember { mutableStateOf<SheetReq?>(null) }

    var toolId by m.toolId
    val opts = m.opts
    var format by m.format
    var height by m.height
    var targetMb by m.targetMb
    var trim by m.trim
    var range by m.range
    var removeAudio by m.removeAudio
    var measured by remember { mutableStateOf<Long?>(null) }

    val pick = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        uris.forEach { runCatching { ctx.contentResolver.takePersistableUriPermission(it, Intent.FLAG_GRANT_READ_URI_PERMISSION) } }
        if (uris.isNotEmpty()) pending = uris
    }
    // Read name, size, type, length and picture size of the chosen files
    LaunchedEffect(pending) {
        val uris = pending.takeIf { it.isNotEmpty() } ?: return@LaunchedEffect
        val read = withContext(Dispatchers.IO) {
            uris.filter { u -> files.none { it.uri == u } }.map { uri ->
                var name = "file"; var size = 0L
                runCatching {
                    ctx.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use {
                        if (it.moveToFirst()) { name = it.getString(0) ?: name; size = it.getLong(1) }
                    }
                }
                val mime = ctx.contentResolver.getType(uri)
                val kind = ConvertTools.kindOf(name, mime)
                var dur: Double? = null; var w: Int? = null; var h: Int? = null
                if (kind == InKind.VIDEO || kind == InKind.AUDIO) runCatching {
                    val r = MediaMetadataRetriever()
                    try {
                        r.setDataSource(ctx, uri)
                        dur = r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)?.toLong()?.div(1000.0)
                        w = r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_WIDTH)?.toIntOrNull()
                        h = r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_HEIGHT)?.toIntOrNull()
                        val rot = r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_ROTATION)?.toIntOrNull() ?: 0
                        if (rot == 90 || rot == 270) { val t = w; w = h; h = t }
                    } finally { r.release() }
                }
                Picked(uri, name, size, mime, kind, dur, w, h)
            }
        }
        files += read
        pending = emptyList()
    }

    val kinds = files.map { it.kind }
    val tools = remember(kinds.toList()) { ConvertTools.toolsFor(kinds) }
    val tool = tools.firstOrNull { it.id == toolId }
    LaunchedEffect(tools) { if (tools.none { it.id == toolId }) toolId = tools.firstOrNull()?.id }
    // Sensible starting choices for each tool.
    LaunchedEffect(toolId, files.size) {
        val t = ConvertTools.byId(toolId ?: return@LaunchedEffect) ?: return@LaunchedEffect
        // Coming back to this tab: keep what the user had chosen.
        val key = "${t.id}|${files.size}"
        if (m.appliedFor == key) return@LaunchedEffect
        m.appliedFor = key
        val inExt = files.firstOrNull()?.name?.let { FileKinds.extOf(it) }
        if (t.id == ConvertTools.MEDIA) {
            val allAudio = kinds.all { it == InKind.AUDIO }
            if (allAudio && format !in ConvertSpec.AUDIO_FORMATS) format = "mp3"
            if (!allAudio && format !in t.formats) format = "mp4"
        } else if (format !in t.formats || format == inExt) {
            format = t.formats.firstOrNull { it != inExt } ?: t.formats.firstOrNull() ?: ""
        }
        if (t.id != ConvertTools.MEDIA && t.id != ConvertTools.IMAGE) height = null
        trim = false; range = 0f..1f
    }

    val single = files.singleOrNull()
    val dur = single?.durationSec
    val converting = jobs.filter { it.kind == JobKind.CONVERT && it.active }
    val media = tool?.id == ConvertTools.MEDIA
    val audioOut = media && format in ConvertSpec.AUDIO_FORMATS

    val spec = ConvertSpec(
        format = format,
        height = if (audioOut || !(media || tool?.id == ConvertTools.IMAGE)) null else height,
        targetMb = if (!media || audioOut || format == "gif") null else targetMb,
        startSec = if (media && trim && dur != null && range.start > 0f) (range.start * dur).toDouble() else null,
        endSec = if (media && trim && dur != null && range.endInclusive < 1f) (range.endInclusive * dur).toDouble() else null,
        removeAudio = media && !audioOut && removeAudio,
        tool = tool?.id ?: ConvertTools.MEDIA,
        opts = opts.toMap().filterValues { it.isNotBlank() },
    )
    val batch = media && files.size > 1

    fun choose(title: String, choices: List<Choice>, selected: String?, onPick: (String?) -> Unit) {
        sheet = SheetReq(title, choices, selected, onPick)
    }
    fun optSet(key: String, v: String?, default: String = "") { if (v.isNullOrEmpty() || v == default) opts.remove(key) else opts[key] = v }

    ScreenScaffold(
        title = "Convert",
        subtitle = "Video, audio, pictures, PDF, documents, archives and more",
        bottomBar = {
            if (tool != null) ActionBar(
                title = when {
                    batch -> "${files.size} files → ${spec.label()}"
                    media -> spec.label()
                    tool.formats.isNotEmpty() -> "${tool.title} · ${fmtLabel(format)}"
                    else -> tool.title
                },
                note = run {
                    val est = if (media && !batch) measured ?: MediaEstimate.bytes(single?.width, single?.height,
                        (spec.endSec ?: dur)?.minus(spec.startSec ?: 0.0), spec) else null
                    val fast = media && FastVideo.canDo(spec)
                    listOfNotNull(
                        est?.let { "${if (measured != null) "" else "about "}${Engine.humanSize(it)}" },
                        if (media && format in ConvertSpec.VIDEO_FORMATS && format != "gif") (if (fast) "fast, video chip" else "uses the processor") else null,
                        "on your phone",
                    ).joinToString(" · ")
                },
                fast = media && FastVideo.canDo(spec),
                enabled = readyToGo(tool, spec, opts),
                onConvert = {
                    if (!Settings.acceptedTerms) { Toast.makeText(ctx, "Accept the terms first", Toast.LENGTH_SHORT).show(); return@ActionBar }
                    if (batch) files.forEach { DownloadService.startConvert(ctx, listOf(it.uri), it.name, spec) }
                    else DownloadService.startConvert(ctx, files.map { it.uri }, files.first().name, spec)
                    Toast.makeText(ctx, if (batch) "Converting ${files.size} files" else "Converting… you can leave this screen", Toast.LENGTH_SHORT).show()
                },
            )
        },
    ) { pad ->
    Column(Modifier.fillMaxSize().padding(pad)) {
    // The preview stays pinned on top while the settings scroll under it, so every change is visible.
    if (files.isNotEmpty() && tool != null) {
        Column(Modifier.padding(start = 16.dp, end = 16.dp, top = 12.dp, bottom = 4.dp).heightIn(max = 330.dp).clipToBounds()) {
            ConvertPreview(tool, files.map { it.preview() }, spec, if (media && trim) range else null,
                onPagesChange = { optSet("pages", it) }, onEstimate = { measured = it },
                onLocked = { if (toolId != ConvertTools.PDF_UNLOCK && tools.any { it.id == ConvertTools.PDF_UNLOCK }) toolId = ConvertTools.PDF_UNLOCK })
        }
        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.6f))
    }
    LazyColumn(
        Modifier.fillMaxWidth().weight(1f),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 14.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(18.dp)
    ) {
        if (files.isEmpty()) {
            item { Hero(onPick = { pick.launch(arrayOf("*/*")) }, onDownloads = { fromDownloads = true }) }
            item { SectionTitle("Or start with") }
            items(groups.chunked(2)) { pair ->
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    pair.forEach { g -> GroupTile(g, Modifier.weight(1f)) { pick.launch(g.mimes) } }
                    if (pair.size == 1) Spacer(Modifier.weight(1f))
                }
            }
        } else {
            // Files
            item {
                Column {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(if (files.size == 1) "1 file" else "${files.size} files", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
                        Text("  ·  ${Engine.humanSize(files.sumOf { it.size }).orEmpty()}", style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.weight(1f))
                        TextButton(onClick = { files.clear(); opts.clear(); measured = null; m.appliedFor = null }) { Text("Clear all") }
                    }
                    val showOrder = tool?.id in ordered && files.size > 1
                    LazyRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        itemsIndexed(files, key = { _, p -> p.uri.toString() }) { i, p ->
                            FilePill(p, if (showOrder) i + 1 else null,
                                onLeft = if (showOrder && i > 0) ({ files.add(i - 1, files.removeAt(i)) }) else null,
                                onRight = if (showOrder && i < files.lastIndex) ({ files.add(i + 1, files.removeAt(i)) }) else null,
                                onRemove = { files.removeAt(i) })
                        }
                        item { AddPill(onPick = { pick.launch(arrayOf("*/*")) }, onDownloads = { fromDownloads = true }) }
                    }
                    if (showOrder) Text("Numbers show the order. Use the arrows to change it.", style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 6.dp))
                }
            }

            if (tools.isEmpty()) item {
                Surface(shape = RoundedCornerShape(16.dp), color = AnySaveColors.status.warnSoft) {
                    Text(if (kinds.any { it == InKind.OTHER }) "AnySave can't convert one of these files yet. Remove it to see what you can do with the rest."
                        else "These files can't be converted together. Remove some, or choose files of one kind.",
                        Modifier.padding(14.dp), style = MaterialTheme.typography.bodyMedium)
                }
            }

            // Tools
            if (tools.isNotEmpty()) item {
                Column {
                    SectionTitle("What to do")
                    Spacer(Modifier.height(8.dp))
                    val row = rememberLazyListState()
                    LaunchedEffect(toolId) { tools.indexOfFirst { it.id == toolId }.takeIf { it >= 0 }?.let { row.animateScrollToItem((it - 1).coerceAtLeast(0)) } }
                    LazyRow(state = row, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        items(tools, key = { it.id }) { t -> ToolTile(t, t.id == toolId) { toolId = t.id; measured = null } }
                    }
                    tool?.let { Text(if (it.id == ConvertTools.MEDIA && kinds.all { k -> k == InKind.AUDIO }) "Change the format or speed, trim, and clean up the sound." else it.desc, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 8.dp)) }
                }
            }

            // Ready-made settings for video
            if (media && !kinds.all { it == InKind.AUDIO }) item {
                Column {
                    SectionTitle("Ready-made")
                    Spacer(Modifier.height(8.dp))
                    LazyRow(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        items(presets) { p ->
                            val on = format == p.format && (opts["codec"] ?: "h264") == p.codec && height == p.height && opts["aspect"] == p.aspect && targetMb == p.targetMb
                            PresetCard(p, on) {
                                format = p.format; height = p.height; targetMb = p.targetMb
                                optSet("codec", p.codec, "h264"); optSet("aspect", p.aspect)
                            }
                        }
                    }
                }
            }

            // Settings
            if (tool != null) item {
                val inExt = files.firstOrNull()?.name?.let { FileKinds.extOf(it) }
                val outFormats = tool.formats.filter { media || it != inExt }
                val hasVideo = kinds.any { it == InKind.VIDEO }
                val formatChoices = outFormats.filter { !media || hasVideo || it in ConvertSpec.AUDIO_FORMATS }.map { Choice(it, fmtLabel(it), if (it == "gif" && !media) "Simple graphics, 256 colours" else formatInfo[it]) }
                SettingsCard {
                    @Composable fun formatRow(title: String = "Format") = OptionRow(Icons.Outlined.InsertDriveFile, title, fmtLabel(format)) {
                        choose(title, formatChoices, format) { format = it!! }
                    }
                    @Composable fun opt(icon: ImageVector, title: String, key: String, choices: List<Choice>, default: String = "") {
                        val cur = opts[key] ?: default
                        OptionRow(icon, title, choices.firstOrNull { (it.value ?: "") == cur }?.label ?: cur) {
                            choose(title, choices, cur) { optSet(key, it, default) }
                        }
                    }
                    when (tool.id) {
                        ConvertTools.MEDIA -> {
                            formatRow()
                            if (!audioOut) {
                                if (format in setOf("mp4", "mkv", "mov")) opt(Icons.Outlined.Memory, "Video codec", "codec", listOf(
                                    Choice("h264", "H.264", "Plays everywhere"), Choice("h265", "H.265", "About half the size. Most phones and new computers"),
                                    Choice("av1", "AV1", "Smallest, but slow to make; older devices can't play it")), "h264")
                                OptionRow(Icons.Outlined.HighQuality, "Resolution", height?.let { if (it == 2160) "4K" else "${it}p" } ?: "Original") {
                                    choose("Resolution", listOf(null to "Original", 2160 to "4K", 1440 to "1440p", 1080 to "1080p", 720 to "720p", 480 to "480p", 360 to "360p")
                                        .map { (h, l) -> Choice(h?.toString(), l, if (h == null) single?.height?.let { "Keep ${single.width}×$it" } ?: "Keep the size" else null) },
                                        height?.toString()) { height = it?.toInt() }
                                }
                                if (format != "gif") OptionRow(Icons.Outlined.DataUsage, "Quality and size",
                                    when (targetMb) { null -> "Normal"; 0 -> "High quality"; else -> "Under $targetMb MB" }) {
                                    choose("Quality and size", listOf(Choice(null, "Normal", "Good quality, sensible size"), Choice("0", "High quality", "Bigger file"),
                                        Choice("10", "Under 10 MB"), Choice("25", "Under 25 MB", "Email and most chat apps"), Choice("50", "Under 50 MB"),
                                        Choice("100", "Under 100 MB")).filter { it.value == null || it.value == "0" || dur != null || batch }, targetMb?.toString()) { targetMb = it?.toInt() }
                                }
                                Divider()
                                opt(Icons.Outlined.Crop, "Shape", "aspect", listOf(Choice("", "Original"), Choice("9:16", "Vertical 9:16", "Reels, TikTok, Shorts, Stories"),
                                    Choice("1:1", "Square 1:1", "Feed posts"), Choice("4:5", "Portrait 4:5", "Instagram feed"), Choice("16:9", "Wide 16:9", "YouTube, TV")))
                                opt(Icons.Outlined.RotateRight, "Turn", "rotate", listOf(Choice("", "No"), Choice("90", "Right ↻"), Choice("180", "Upside down"), Choice("270", "Left ↺")))
                                opt(Icons.Outlined.Flip, "Mirror", "flip", listOf(Choice("", "No"), Choice("h", "Left ↔ right"), Choice("v", "Top ↕ bottom")))
                                opt(Icons.Outlined.Speed, "Frame rate", "fps", listOf(Choice("", "Original"), Choice("60", "60 fps", "Smooth"), Choice("30", "30 fps"), Choice("24", "24 fps", "Film look")))
                            }
                            opt(Icons.Outlined.SlowMotionVideo, "Speed", "speed", listOf(Choice("0.5", "0.5×", "Slow motion"), Choice("0.75", "0.75×"), Choice("1", "Normal"),
                                Choice("1.25", "1.25×"), Choice("1.5", "1.5×"), Choice("2", "2×", "Twice as fast")), "1")
                            if (audioOut && format !in setOf("wav", "flac")) opt(Icons.Outlined.GraphicEq, "Sound quality", "abitrate", listOf(
                                Choice("", if (format == "mp3") "Automatic" else "Standard", "Best balance"), Choice("96", "96 kbps", "Speech"), Choice("128", "128 kbps"),
                                Choice("192", "192 kbps"), Choice("256", "256 kbps"), Choice("320", "320 kbps", "Highest")))
                            Divider()
                            if (!audioOut && format != "gif") SwitchRow(Icons.Outlined.VolumeOff, "Remove sound", removeAudio) { removeAudio = it }
                            if (!(removeAudio && !audioOut) && format != "gif") {
                                OptSwitch(Icons.Outlined.Equalizer, "Even out loudness", "loudnorm", opts)
                                OptSwitch(Icons.Outlined.NoiseAware, "Reduce background noise", "denoise", opts)
                            }
                            if (!audioOut) OptSwitch(Icons.Outlined.Vibration, "Steady a shaky video", "stabilize", opts)
                            if (dur != null && dur > 1) {
                                Divider()
                                SwitchRow(Icons.Outlined.ContentCut, "Trim", trim) { trim = it }
                                if (trim) Column(Modifier.padding(horizontal = 16.dp)) {
                                    RangeSlider(value = range, onValueChange = { range = it })
                                    Row {
                                        Text(fmtDuration(range.start * dur), style = MaterialTheme.typography.bodySmall)
                                        Text("${fmtDuration((range.endInclusive - range.start) * dur)} long", Modifier.weight(1f), textAlign = TextAlign.Center,
                                            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.primary)
                                        Text(fmtDuration(range.endInclusive * dur), style = MaterialTheme.typography.bodySmall)
                                    }
                                }
                            }
                        }
                        ConvertTools.JOIN -> InfoRow("Different shapes are fitted with black bars. The result is MP4 (or M4A for audio).")
                        ConvertTools.FRAMES -> {
                            formatRow()
                            opt(Icons.Outlined.Timer, "One picture every", "every", listOf(Choice("0.5", "½ second"), Choice("1", "1 second"), Choice("2", "2 seconds"),
                                Choice("5", "5 seconds"), Choice("10", "10 seconds"), Choice("0", "Every frame", "About 30 pictures per second of video")), "1")
                        }
                        ConvertTools.ADD_SUBS -> {
                            opt(Icons.Outlined.ClosedCaption, "How", "mode", listOf(Choice("burn", "Burned in", "Always visible, in every app. Takes longer"),
                                Choice("track", "As a track", "Fast; turn on and off in the player")), "burn")
                            if (opts["mode"] != "track") opt(Icons.Outlined.FormatSize, "Text size", "size",
                                listOf(Choice("small", "Small"), Choice("normal", "Normal"), Choice("large", "Large")), "normal")
                        }
                        ConvertTools.IMAGE -> {
                            formatRow()
                            OptionRow(Icons.Outlined.PhotoSizeSelectLarge, "Size", height?.let { "$it px" } ?: "Original") {
                                choose("Size (longest side)", listOf(null, 3840, 1920, 1280, 800, 400).map {
                                    Choice(it?.toString(), it?.let { v -> "$v px" } ?: "Original", when (it) { 3840 -> "4K"; 1920 -> "Full HD, sharp on phones"; 1280 -> "Web and chat"; 800 -> "Small"; 400 -> "Thumbnail"; else -> null })
                                }, height?.toString()) { height = it?.toInt() }
                            }
                            if (format in setOf("jpg", "webp", "avif")) opt(Icons.Outlined.HighQuality, "Quality", "quality",
                                listOf(Choice("95", "Best"), Choice("85", "High"), Choice("70", "Medium"), Choice("50", "Small file")), "85")
                            if (format in setOf("jpg", "webp")) opt(Icons.Outlined.DataUsage, "File size", "targetKb", listOf(Choice("", "Any"), Choice("100", "Under 100 KB"),
                                Choice("200", "Under 200 KB"), Choice("500", "Under 500 KB"), Choice("1000", "Under 1 MB")))
                            InfoRow("Location and camera info are removed. Transparent parts turn white in JPG and BMP.")
                        }
                        ConvertTools.IMAGES_PDF -> {
                            opt(Icons.Outlined.Description, "Page size", "page", listOf(Choice("a4", "A4"), Choice("letter", "Letter", "US"), Choice("fit", "Same as picture")), "a4")
                            opt(Icons.Outlined.HighQuality, "Quality", "quality", listOf(Choice("92", "High"), Choice("80", "Normal"), Choice("60", "Small file")), "80")
                            OptSwitch(Icons.Outlined.BorderOuter, "White border", "margin", opts)
                        }
                        ConvertTools.PDF_IMAGES -> {
                            formatRow()
                            opt(Icons.Outlined.HighQuality, "Sharpness", "dpi", listOf(Choice("72", "Screen", "Small files"), Choice("150", "Normal"), Choice("300", "Print", "Large files")), "150")
                        }
                        ConvertTools.PDF_MERGE -> InfoRow("Merged in the order shown above.")
                        ConvertTools.PDF_PAGES -> {
                            opt(Icons.Outlined.ContentCut, "What to do", "mode", listOf(Choice("pick", "Keep chosen pages", "One new PDF"),
                                Choice("split", "Every page on its own", "One PDF per page")), "pick")
                            TextOpt("Pages", "pages", opts, placeholder = "Tap pages above, or type 1-3, 5")
                        }
                        ConvertTools.PDF_COMPRESS -> {
                            opt(Icons.Outlined.Compress, "How much", "level", listOf(Choice("light", "A little", "Best quality"), Choice("medium", "Medium"),
                                Choice("strong", "A lot", "Smallest file")), "medium")
                            InfoRow("If nothing can shrink, you get the same file back.")
                        }
                        ConvertTools.PDF_ROTATE -> opt(Icons.Outlined.RotateRight, "Turn", "angle", listOf(Choice("90", "Right ↻"), Choice("180", "Upside down"), Choice("270", "Left ↺")), "90")
                        ConvertTools.PDF_PROTECT -> {
                            TextOpt("Password", "password", opts, password = true)
                            InfoRow("Don't forget it: nobody can open the file without it, not even AnySave.")
                        }
                        ConvertTools.PDF_UNLOCK -> TextOpt("Password", "password", opts, password = true)
                        ConvertTools.DOC -> {
                            formatRow("Convert to")
                            if (format == "pdf") {
                                opt(Icons.Outlined.Description, "Page size", "page", listOf(Choice("a4", "A4"), Choice("letter", "Letter", "US")), "a4")
                                opt(Icons.Outlined.FormatSize, "Text size", "font", listOf(Choice("small", "Small"), Choice("normal", "Normal"), Choice("large", "Large")), "normal")
                            }
                        }
                        ConvertTools.UNPACK -> {
                            if (inExt == "7z") TextOpt("Password (only if it has one)", "password", opts, password = true)
                            InfoRow("Several files are saved in a folder named after the archive.")
                        }
                        ConvertTools.PACK -> {
                            formatRow("Type")
                            TextOpt("Name (optional)", "name", opts)
                        }
                        ConvertTools.SUBS -> {
                            formatRow("Convert to")
                            opt(Icons.Outlined.Schedule, "Move all lines", "shift", listOf(Choice("", "Don't move"), Choice("-2", "2 s earlier"), Choice("-1", "1 s earlier"),
                                Choice("-0.5", "½ s earlier"), Choice("0.5", "½ s later"), Choice("1", "1 s later"), Choice("2", "2 s later")))
                        }
                        ConvertTools.DATA -> {
                            formatRow("Convert to")
                            if (inExt == "xlsx") InfoRow("Only the first sheet is converted.")
                        }
                    }
                }
            }

            if (converting.isNotEmpty()) {
                item { SectionTitle("Converting now") }
                items(converting, key = { it.id }) { DownloadRow(it) }
            }
        }
        item { MadeBy() }
    }
    }
    }

    sheet?.let { s ->
        ModalBottomSheet(onDismissRequest = { sheet = null }) {
            Text(s.title, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold, modifier = Modifier.padding(start = 24.dp, end = 24.dp, bottom = 8.dp))
            LazyColumn(Modifier.heightIn(max = 520.dp), contentPadding = PaddingValues(bottom = 24.dp)) {
                items(s.choices) { c ->
                    val sel = (c.value ?: "") == (s.selected ?: "")
                    Row(Modifier.fillMaxWidth().clickable { s.onPick(c.value); sheet = null }.padding(horizontal = 24.dp, vertical = 12.dp),
                        verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(c.label, fontWeight = if (sel) FontWeight.SemiBold else FontWeight.Normal,
                                color = if (sel) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurface)
                            c.desc?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                        }
                        if (sel) Icon(Icons.Outlined.Check, null, tint = MaterialTheme.colorScheme.primary)
                    }
                }
            }
        }
    }

    // Pick from finished downloads
    if (fromDownloads) {
        val done = jobs.filter { it.status == JobStatus.DONE && it.uri != null && it.fileName?.endsWith(" files)") != true }
        ModalBottomSheet(onDismissRequest = { fromDownloads = false }) {
            Column(Modifier.padding(horizontal = 16.dp)) {
                Text("Choose from your downloads", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                if (done.isEmpty()) Text("No finished downloads yet.", Modifier.padding(vertical = 24.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
                LazyColumn(Modifier.heightIn(max = 480.dp), contentPadding = PaddingValues(vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    items(done, key = { it.id }) { j ->
                        Surface(onClick = { pending = listOf(j.uri!!); fromDownloads = false }, shape = RoundedCornerShape(12.dp)) {
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

private fun readyToGo(tool: ConvertTool, spec: ConvertSpec, opts: Map<String, String>): Boolean = when (tool.id) {
    ConvertTools.PDF_PROTECT, ConvertTools.PDF_UNLOCK -> spec.opt("password") != null
    ConvertTools.PDF_PAGES -> opts["mode"] == "split" || spec.opt("pages") != null
    else -> true
}

// ---------- pieces ----------

@Composable
private fun SectionTitle(text: String) {
    Text(text, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
}

@Composable
private fun Hero(onPick: () -> Unit, onDownloads: () -> Unit) {
    Surface(shape = RoundedCornerShape(20.dp), color = MaterialTheme.colorScheme.surfaceContainerLowest,
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
        Column(Modifier.fillMaxWidth().padding(22.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            Box(Modifier.size(64.dp).clip(CircleShape).background(MaterialTheme.colorScheme.primaryContainer), contentAlignment = Alignment.Center) {
                Icon(Icons.Outlined.UploadFile, null, Modifier.size(32.dp), tint = MaterialTheme.colorScheme.primary)
            }
            Spacer(Modifier.height(14.dp))
            Text("Convert any file", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
            Text("See the result live before you convert. Everything stays on your phone.",
                style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center,
                modifier = Modifier.padding(top = 4.dp))
            Spacer(Modifier.height(18.dp))
            Button(onClick = onPick, modifier = Modifier.fillMaxWidth().height(50.dp), shape = RoundedCornerShape(14.dp)) {
                Icon(Icons.Outlined.FolderOpen, null, Modifier.size(20.dp)); Spacer(Modifier.width(8.dp))
                Text("Choose files", fontWeight = FontWeight.SemiBold)
            }
            Spacer(Modifier.height(4.dp))
            TextButton(onClick = onDownloads) { Text("Pick from my downloads") }
        }
    }
}

@Composable
private fun GroupTile(g: Group, modifier: Modifier, onClick: () -> Unit) {
    val color = kindColor(g.kind)
    Surface(onClick = onClick, modifier = modifier, shape = RoundedCornerShape(16.dp), color = MaterialTheme.colorScheme.surfaceContainerLowest,
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(38.dp).clip(RoundedCornerShape(11.dp)).background(color.copy(alpha = 0.14f)), contentAlignment = Alignment.Center) {
                Icon(kindIcon(g.kind), null, tint = color, modifier = Modifier.size(21.dp))
            }
            Spacer(Modifier.width(10.dp))
            Column {
                Text(g.title, fontWeight = FontWeight.SemiBold, style = MaterialTheme.typography.bodyMedium)
                Text(g.examples, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 2, lineHeight = 14.sp)
            }
        }
    }
}

@Composable
private fun FilePill(p: Picked, number: Int?, onLeft: (() -> Unit)?, onRight: (() -> Unit)?, onRemove: () -> Unit) {
    Surface(shape = RoundedCornerShape(14.dp), color = MaterialTheme.colorScheme.surfaceContainerLowest,
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
        Row(Modifier.padding(start = 8.dp, top = 8.dp, bottom = 8.dp, end = 2.dp), verticalAlignment = Alignment.CenterVertically) {
            if (number != null) IconButton(onClick = { onLeft?.invoke() }, enabled = onLeft != null, modifier = Modifier.size(28.dp)) {
                Icon(Icons.Outlined.ChevronLeft, "Move earlier", Modifier.size(18.dp))
            }
            Box(Modifier.size(36.dp).clip(RoundedCornerShape(9.dp)).background(kindColor(p.kind)), contentAlignment = Alignment.Center) {
                Text(number?.toString() ?: FileKinds.extOf(p.name).uppercase().take(4).ifEmpty { "FILE" }, color = Color.White,
                    fontWeight = FontWeight.Bold, fontSize = if (number != null) 14.sp else 10.sp, maxLines = 1)
            }
            Spacer(Modifier.width(8.dp))
            Column(Modifier.widthIn(max = 150.dp)) {
                Text(p.name, style = MaterialTheme.typography.bodySmall, fontWeight = FontWeight.Medium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(listOfNotNull(Engine.humanSize(p.size), p.durationSec?.let(::fmtDuration), p.height?.let { "${p.width}×$it" }).joinToString(" · "),
                    style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
            }
            if (number != null) IconButton(onClick = { onRight?.invoke() }, enabled = onRight != null, modifier = Modifier.size(28.dp)) {
                Icon(Icons.Outlined.ChevronRight, "Move later", Modifier.size(18.dp))
            }
            IconButton(onClick = onRemove, modifier = Modifier.size(32.dp)) { Icon(Icons.Outlined.Close, "Remove", Modifier.size(16.dp)) }
        }
    }
}

@Composable
private fun AddPill(onPick: () -> Unit, onDownloads: () -> Unit) {
    var menu by remember { mutableStateOf(false) }
    Box {
        Surface(onClick = { menu = true }, shape = RoundedCornerShape(14.dp), color = MaterialTheme.colorScheme.primaryContainer) {
            Row(Modifier.padding(horizontal = 14.dp, vertical = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.Add, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.primary)
                Spacer(Modifier.width(6.dp))
                Text("Add", color = MaterialTheme.colorScheme.primary, fontWeight = FontWeight.SemiBold)
            }
        }
        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
            DropdownMenuItem(text = { Text("From phone") }, leadingIcon = { Icon(Icons.Outlined.FolderOpen, null) }, onClick = { menu = false; onPick() })
            DropdownMenuItem(text = { Text("From downloads") }, leadingIcon = { Icon(Icons.Outlined.Download, null) }, onClick = { menu = false; onDownloads() })
        }
    }
}

@Composable
private fun ToolTile(t: ConvertTool, selected: Boolean, onClick: () -> Unit) {
    val look = toolLook(t.id)
    Surface(onClick = onClick, modifier = Modifier.width(96.dp), shape = RoundedCornerShape(16.dp),
        color = if (selected) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surfaceContainerLowest,
        border = BorderStroke(if (selected) 2.dp else 1.dp, if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.outlineVariant)) {
        Column(Modifier.padding(vertical = 12.dp, horizontal = 6.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            Box(Modifier.size(40.dp).clip(CircleShape).background(look.color.copy(alpha = 0.15f)), contentAlignment = Alignment.Center) {
                Icon(look.icon, null, tint = look.color, modifier = Modifier.size(22.dp))
            }
            Spacer(Modifier.height(8.dp))
            Text(look.tile, style = MaterialTheme.typography.labelMedium, fontWeight = if (selected) FontWeight.Bold else FontWeight.Medium,
                textAlign = TextAlign.Center, maxLines = 2, minLines = 2, lineHeight = 15.sp)
        }
    }
}

@Composable
private fun PresetCard(p: Preset, selected: Boolean, onClick: () -> Unit) {
    Surface(onClick = onClick, modifier = Modifier.width(136.dp), shape = RoundedCornerShape(16.dp),
        color = if (selected) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surfaceContainerLowest,
        border = BorderStroke(if (selected) 2.dp else 1.dp, if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.outlineVariant)) {
        Column(Modifier.padding(12.dp)) {
            Icon(p.icon, null, tint = if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.height(8.dp))
            Text(p.label, fontWeight = FontWeight.SemiBold, style = MaterialTheme.typography.bodyMedium, maxLines = 1)
            Text(p.sub, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
        }
    }
}

@Composable
private fun SettingsCard(content: @Composable ColumnScope.() -> Unit) {
    Column {
        SectionTitle("Settings")
        Spacer(Modifier.height(8.dp))
        Surface(shape = RoundedCornerShape(16.dp), color = MaterialTheme.colorScheme.surfaceContainerLowest,
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
            Column(Modifier.fillMaxWidth().padding(vertical = 4.dp), content = content)
        }
    }
}

@Composable
private fun Divider() = HorizontalDivider(Modifier.padding(horizontal = 16.dp, vertical = 2.dp), color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.6f))

@Composable
private fun OptionRow(icon: ImageVector, title: String, value: String, onClick: () -> Unit) {
    Row(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 16.dp, vertical = 13.dp), verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, Modifier.size(20.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.width(14.dp))
        Text(title, Modifier.weight(1f), style = MaterialTheme.typography.bodyLarge)
        Text(value, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.primary, fontWeight = FontWeight.Medium,
            maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.widthIn(max = 170.dp))
        Icon(Icons.Outlined.ChevronRight, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun SwitchRow(icon: ImageVector, label: String, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth().clickable { onChange(!checked) }.padding(horizontal = 16.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, Modifier.size(20.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.width(14.dp))
        Text(label, Modifier.weight(1f), style = MaterialTheme.typography.bodyLarge)
        Switch(checked = checked, onCheckedChange = onChange)
    }
}

@Composable
private fun OptSwitch(icon: ImageVector, label: String, key: String, opts: MutableMap<String, String>) =
    SwitchRow(icon, label, opts[key] == "1") { if (it) opts[key] = "1" else opts.remove(key) }

@Composable
private fun InfoRow(text: String) {
    Row(Modifier.padding(horizontal = 16.dp, vertical = 10.dp)) {
        Icon(Icons.Outlined.Info, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.width(12.dp))
        Text(text, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun TextOpt(label: String, key: String, opts: MutableMap<String, String>, placeholder: String? = null, password: Boolean = false) {
    var show by remember { mutableStateOf(false) }
    OutlinedTextField(
        value = opts[key].orEmpty(),
        onValueChange = { if (it.isEmpty()) opts.remove(key) else opts[key] = it },
        label = { Text(label) },
        placeholder = placeholder?.let { { Text(it) } },
        singleLine = true,
        visualTransformation = if (password && !show) PasswordVisualTransformation() else VisualTransformation.None,
        keyboardOptions = KeyboardOptions(keyboardType = if (password) KeyboardType.Password else KeyboardType.Text),
        trailingIcon = if (password) { {
            IconButton(onClick = { show = !show }) { Icon(if (show) Icons.Outlined.VisibilityOff else Icons.Outlined.Visibility, if (show) "Hide" else "Show") }
        } } else null,
        shape = RoundedCornerShape(12.dp),
        modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp),
    )
}

/** Always visible above the tab bar: what will come out, a size estimate, and the button. */
@Composable
private fun ActionBar(title: String, note: String, fast: Boolean, enabled: Boolean, onConvert: () -> Unit) {
    Surface(shadowElevation = 12.dp, color = MaterialTheme.colorScheme.surfaceContainerLowest) {
        Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(title, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Row(verticalAlignment = Alignment.CenterVertically) {
                    if (fast) { Icon(Icons.Outlined.Bolt, null, Modifier.size(14.dp), tint = AnySaveColors.status.ok); Spacer(Modifier.width(2.dp)) }
                    Text(note, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
            Spacer(Modifier.width(12.dp))
            Button(onClick = onConvert, enabled = enabled, shape = RoundedCornerShape(14.dp), contentPadding = PaddingValues(horizontal = 20.dp, vertical = 12.dp)) {
                Icon(Icons.Outlined.Transform, null, Modifier.size(18.dp)); Spacer(Modifier.width(8.dp))
                Text("Convert", fontWeight = FontWeight.SemiBold)
            }
        }
    }
}
