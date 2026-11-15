package com.anysave.app

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.pdf.PdfRenderer
import android.media.MediaMetadataRetriever
import android.net.Uri
import android.text.Html
import android.widget.TextView
import androidx.annotation.OptIn
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.C
import androidx.media3.common.Effect
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.PlaybackParameters
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.effect.Presentation
import androidx.media3.effect.ScaleAndRotateTransformation
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.PlayerView
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import kotlin.math.max

/** A file the preview works on: the user's content Uri plus what the screen already knows about it. */
data class PreviewFile(val uri: Uri, val name: String, val size: Long, val kind: InKind, val durationSec: Double?, val width: Int?, val height: Int?)

/** Local copy for previews that need a real file (picture encoding, documents, archives). Kept while the app runs. */
suspend fun previewCopy(ctx: Context, uri: Uri, name: String): File? = withContext(Dispatchers.IO) {
    runCatching {
        val dir = File(ctx.cacheDir, "preview").apply { mkdirs() }
        val f = File(dir, "${uri.toString().hashCode().toUInt()}-${ConvertTools.safe(name)}")
        if (!f.exists() || f.length() == 0L) ctx.contentResolver.openInputStream(uri)!!.use { i -> f.outputStream().use { i.copyTo(it) } }
        f
    }.getOrNull()
}

/** The live preview for the chosen tool. [onEstimate] reports a measured output size (bytes) when a sample was made. */
@Composable
fun ConvertPreview(
    tool: ConvertTool, files: List<PreviewFile>, spec: ConvertSpec, trim: ClosedFloatingPointRange<Float>?,
    onPagesChange: (String) -> Unit, onEstimate: (Long?) -> Unit, onLocked: () -> Unit = {},
) {
    val first = files.firstOrNull() ?: return
    when (tool.id) {
        ConvertTools.MEDIA -> MediaPreview(first, spec, trim, null, onEstimate)
        ConvertTools.ADD_SUBS -> {
            val video = files.firstOrNull { it.kind == InKind.VIDEO } ?: return
            MediaPreview(video, ConvertSpec(), null, files.firstOrNull { it.kind == InKind.SUBS }, onEstimate)
        }
        ConvertTools.JOIN, ConvertTools.FRAMES -> FramesPreview(tool, files, spec)
        ConvertTools.IMAGE -> ImagePreview(first, files.size, spec)
        ConvertTools.IMAGES_PDF -> PagesMockPreview(files, spec)
        ConvertTools.PDF_IMAGES, ConvertTools.PDF_PAGES, ConvertTools.PDF_ROTATE, ConvertTools.PDF_COMPRESS,
        ConvertTools.PDF_PROTECT, ConvertTools.PDF_UNLOCK -> PdfPreview(first, tool, spec, onPagesChange, onLocked)
        ConvertTools.PDF_MERGE -> PdfMergePreview(files)
        ConvertTools.DOC -> DocPreview(first, spec)
        ConvertTools.DATA -> DataPreview(first)
        ConvertTools.SUBS -> SubsPreview(first, spec)
        ConvertTools.UNPACK -> ArchivePreview(first)
        ConvertTools.PACK -> PackPreview(files, spec)
    }
}

@Composable
private fun PreviewBox(height: androidx.compose.ui.unit.Dp = 230.dp, dark: Boolean = false, content: @Composable BoxScope.() -> Unit) {
    Box(
        Modifier.fillMaxWidth().height(height).clip(RoundedCornerShape(16.dp))
            .background(if (dark) Color(0xFF0D0E10) else MaterialTheme.colorScheme.surfaceContainerHighest),
        contentAlignment = Alignment.Center, content = content,
    )
}

@Composable
private fun Caption(text: String, modifier: Modifier = Modifier) {
    Text(text, modifier.padding(top = 8.dp), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
}

@Composable
private fun Loading(label: String = "Making the preview…") {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        CircularProgressIndicator(Modifier.size(28.dp), strokeWidth = 3.dp)
        Spacer(Modifier.height(8.dp))
        Text(label, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

private fun mb(bytes: Long) = Engine.humanSize(bytes) ?: "?"

// ---------- video and audio ----------

/**
 * Plays the file with shape, turn, mirror, speed, trim and mute applied live (Media3 effects on the
 * phone's graphics chip). For what can't be shown live (codec, quality, noise, loudness) a real
 * 10-second sample is made with the same settings, which also measures the final size.
 */
@OptIn(UnstableApi::class)
@Composable
private fun MediaPreview(f: PreviewFile, spec: ConvertSpec, trim: ClosedFloatingPointRange<Float>?, subs: PreviewFile?, onEstimate: (Long?) -> Unit) {
    val ctx = LocalContext.current
    val player = remember(f.uri) {
        ExoPlayer.Builder(ctx).build().apply {
            repeatMode = Player.REPEAT_MODE_ALL
            // Added subtitle files usually have no language tag; show them anyway.
            trackSelectionParameters = trackSelectionParameters.buildUpon().setSelectUndeterminedTextLanguage(true)
                .setPreferredTextLanguage(null).setIgnoredTextSelectionFlags(0).build()
        }
    }
    DisposableEffect(player) { onDispose { player.release() } }
    val dur = f.durationSec
    val startMs = if (trim != null && dur != null) (trim.start * dur * 1000).toLong() else 0L
    val endMs = if (trim != null && dur != null && trim.endInclusive < 1f) (trim.endInclusive * dur * 1000).toLong() else C.TIME_END_OF_SOURCE

    var sample by remember(f.uri) { mutableStateOf<File?>(null) }
    var sampleBusy by remember { mutableStateOf(false) }
    var sampleNote by remember { mutableStateOf<String?>(null) }
    val sampleKey = spec.copy(startSec = null, endSec = null).toJson()
    // A sample stops matching as soon as a setting changes.
    LaunchedEffect(sampleKey) { sample = null; sampleNote = null; onEstimate(null) }

    val effects: List<Effect> = remember(spec.opt("aspect"), spec.opt("rotate"), spec.opt("flip"), sample) {
        if (sample != null) emptyList() else buildList {
            when (spec.opt("aspect")) { "9:16" -> 9f / 16f; "1:1" -> 1f; "4:5" -> 0.8f; "16:9" -> 16f / 9f; else -> null }
                ?.let { add(Presentation.createForAspectRatio(it, Presentation.LAYOUT_SCALE_TO_FIT_WITH_CROP)) }
            val rot = when (spec.opt("rotate")) { "90" -> 270f; "180" -> 180f; "270" -> 90f; else -> 0f }
            val flip = spec.opt("flip")
            if (rot != 0f || flip != null) add(ScaleAndRotateTransformation.Builder().setRotationDegrees(rot)
                .setScale(if (flip == "h") -1f else 1f, if (flip == "v") -1f else 1f).build())
        }
    }
    LaunchedEffect(player, effects, startMs, endMs, sample, subs?.uri) {
        val pos = player.currentPosition
        val s = sample
        val item = if (s != null) MediaItem.fromUri(Uri.fromFile(s)) else MediaItem.Builder().setUri(f.uri)
            .setClippingConfiguration(MediaItem.ClippingConfiguration.Builder().setStartPositionMs(startMs).setEndPositionMs(endMs).build())
            .apply {
                if (subs != null) setSubtitleConfigurations(listOf(MediaItem.SubtitleConfiguration.Builder(subs.uri)
                    .setMimeType(if (FileKinds.extOf(subs.name) == "vtt") MimeTypes.TEXT_VTT else if (FileKinds.extOf(subs.name) in setOf("ass", "ssa")) MimeTypes.TEXT_SSA else MimeTypes.APPLICATION_SUBRIP)
                    .setLanguage("und").setSelectionFlags(C.SELECTION_FLAG_DEFAULT or C.SELECTION_FLAG_FORCED).build()))
            }.build()
        runCatching { player.setVideoEffects(effects) }
        player.setMediaItem(item)
        player.prepare()
        if (s == null && pos > 0) player.seekTo(pos.coerceAtMost(max(0L, (if (endMs == C.TIME_END_OF_SOURCE) Long.MAX_VALUE else endMs - startMs) - 500)))
        player.play()
    }
    val speed = spec.opt("speed")?.toFloatOrNull() ?: 1f
    LaunchedEffect(speed, sample) { player.playbackParameters = PlaybackParameters(if (sample != null) 1f else speed) }
    LaunchedEffect(spec.removeAudio, sample) { player.volume = if (spec.removeAudio && sample == null) 0f else 1f }

    val audio = f.kind == InKind.AUDIO || spec.format in ConvertSpec.AUDIO_FORMATS
    PreviewBox(if (audio && f.kind == InKind.AUDIO) 140.dp else 230.dp, dark = true) {
        AndroidView(
            factory = { c -> PlayerView(c).apply {
                this.player = player
                setShowNextButton(false); setShowPreviousButton(false)
                controllerShowTimeoutMs = if (f.kind == InKind.AUDIO) 0 else 2500
                controllerHideOnTouch = f.kind != InKind.AUDIO
                setShutterBackgroundColor(android.graphics.Color.BLACK)
            } },
            update = { it.player = player },
            modifier = Modifier.fillMaxSize(),
        )
        if (f.kind == InKind.AUDIO) Row(Modifier.align(Alignment.TopStart).padding(start = 12.dp, top = 12.dp, end = 12.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(26.dp).clip(RoundedCornerShape(7.dp)).background(Color(0xFFF07A2C)), contentAlignment = Alignment.Center) {
                Icon(Icons.Outlined.MusicNote, null, Modifier.size(16.dp), tint = Color.White)
            }
            Spacer(Modifier.width(8.dp))
            Text(f.name, color = Color.White, fontSize = 13.sp, fontWeight = FontWeight.Medium, maxLines = 1, overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f, fill = false))
            if (sample != null) Text("  · real result", color = Color(0xFF6EE7A0), fontSize = 12.sp)
        }
        if (sample != null && f.kind != InKind.AUDIO) Surface(Modifier.align(Alignment.TopStart).padding(10.dp), shape = RoundedCornerShape(8.dp), color = Color(0xCC1D9A52)) {
            Text("Real result · 10 s sample", Modifier.padding(horizontal = 8.dp, vertical = 3.dp), color = Color.White, fontSize = 11.sp, fontWeight = FontWeight.SemiBold)
        } else if (f.kind == InKind.VIDEO) Surface(Modifier.align(Alignment.TopStart).padding(10.dp), shape = RoundedCornerShape(8.dp), color = Color(0x99000000)) {
            Text("Live preview", Modifier.padding(horizontal = 8.dp, vertical = 3.dp), color = Color.White, fontSize = 11.sp, fontWeight = FontWeight.SemiBold)
        }
    }
    if (subs != null) { Caption("Subtitles are shown as they will look. Burned-in text uses the app's own style."); return }

    // Make a real sample with the exact settings.
    val scope = rememberCoroutineScope()
    Row(Modifier.fillMaxWidth().padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(sampleNote ?: when {
            sample != null -> "Playing the real result."
            audio -> "Speed plays live. Make a sample to hear noise and loudness changes."
            else -> "Shape, turn, mirror, speed and trim show live. Make a sample to see the real quality."
        }, Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.width(8.dp))
        if (sample != null) TextButton(onClick = { sample = null }) { Text("Live") }
        else FilledTonalButton(enabled = !sampleBusy && dur != null, onClick = {
            sampleBusy = true; sampleNote = "Copying the file…"
            scope.launch(Dispatchers.IO) {
                val result = runCatching {
                    val input = previewCopy(ctx, f.uri, f.name) ?: throw ConvertError("Couldn't read the file.")
                    val total = dur ?: 0.0
                    val from = (trim?.start?.times(total) ?: 0.0).coerceAtLeast(0.0)
                    val until = trim?.endInclusive?.takeIf { it < 1f }?.times(total) ?: total
                    val start = if (until - from > 10) from + (until - from - 10) / 2 else from
                    val end = (start + 10).coerceAtMost(until)
                    val out = File(ctx.cacheDir, "preview/sample.${spec.format}").apply { delete() }
                    val s = spec.copy(startSec = start, endSec = end)
                    withContext(Dispatchers.Main) { sampleNote = "Making a 10-second sample…" }
                    val fast = FastVideo.canDo(s) && FastVideo.convert(ctx, "preview", input, out, s) {}
                    if (!fast) Converter.convert(ctx, "preview", input, out, s) {}
                    Triple(out, end - start, until - from)
                }
                withContext(Dispatchers.Main) {
                    sampleBusy = false
                    result.onSuccess { (out, secs, whole) ->
                        sample = out
                        val est = if (secs > 0) (out.length() * (whole / secs)).toLong() else null
                        sampleNote = "Sample: ${mb(out.length())} for ${secs.toInt()} s" + (est?.let { " · whole file about ${mb(it)}" } ?: "")
                        onEstimate(est)
                    }.onFailure { sampleNote = "Couldn't make a sample: ${it.message}" }
                }
            }
        }) {
            if (sampleBusy) CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
            else Icon(Icons.Outlined.PlayCircle, null, Modifier.size(18.dp))
            Spacer(Modifier.width(6.dp)); Text("10 s sample")
        }
    }
}

/** Frames from a video (Video to pictures) or the first frame of each clip (Join). */
@Composable
private fun FramesPreview(tool: ConvertTool, files: List<PreviewFile>, spec: ConvertSpec) {
    val ctx = LocalContext.current
    val every = spec.opt("every")?.toDoubleOrNull() ?: 1.0
    val key = if (tool.id == ConvertTools.FRAMES) "${files.first().uri}|$every" else files.joinToString { it.uri.toString() }
    var frames by remember(key) { mutableStateOf<List<Pair<Bitmap?, String>>?>(null) }
    LaunchedEffect(key) {
        frames = withContext(Dispatchers.IO) {
            if (tool.id == ConvertTools.FRAMES) {
                val f = files.first(); val dur = f.durationSec ?: 0.0
                val step = if (every <= 0) 1.0 / 30 else every
                val times = generateSequence(0.0) { it + step }.takeWhile { it < dur }.take(8).toList()
                frameAt(ctx, f.uri, times).mapIndexed { i, b -> b to fmtDuration(times[i]) }
            } else files.map { f -> (frameAt(ctx, f.uri, listOf(0.5)).firstOrNull()) to (f.durationSec?.let(::fmtDuration) ?: f.name) }
        }
    }
    val list = frames
    if (list == null) { PreviewBox(130.dp) { Loading() }; return }
    LazyRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        itemsIndexed(list) { i, (bmp, label) ->
            Column {
                Box(Modifier.size(width = 132.dp, height = 82.dp).clip(RoundedCornerShape(10.dp)).background(Color.Black), contentAlignment = Alignment.Center) {
                    if (bmp != null) Image(bmp.asImageBitmap(), null, Modifier.fillMaxSize(), contentScale = ContentScale.Fit)
                    else Icon(if (files.getOrNull(i)?.kind == InKind.AUDIO) Icons.Outlined.MusicNote else Icons.Outlined.Movie, null, tint = Color.White)
                    if (tool.id == ConvertTools.JOIN) Surface(Modifier.align(Alignment.TopStart).padding(4.dp), shape = CircleShape, color = Color(0xCC000000)) {
                        Text("${i + 1}", Modifier.padding(horizontal = 7.dp, vertical = 1.dp), color = Color.White, fontSize = 11.sp)
                    }
                }
                Text(label, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 3.dp))
            }
        }
    }
    if (tool.id == ConvertTools.FRAMES) {
        val dur = files.first().durationSec ?: 0.0
        val count = if (every <= 0) (dur * 30).toInt() else kotlin.math.round(dur / every).toInt().coerceAtLeast(1)
        Caption("About $count pictures.")
    } else Caption("Plays in this order. Total ${fmtDuration(files.sumOf { it.durationSec ?: 0.0 })}.")
}

private fun frameAt(ctx: Context, uri: Uri, secs: List<Double>): List<Bitmap?> = runCatching {
    val r = MediaMetadataRetriever()
    try {
        r.setDataSource(ctx, uri)
        secs.map { s ->
            val b = r.getFrameAtTime((s * 1_000_000).toLong(), MediaMetadataRetriever.OPTION_CLOSEST_SYNC)
            b?.let { if (it.width > 480) Bitmap.createScaledBitmap(it, 480, it.height * 480 / it.width, true) else it }
        }
    } finally { r.release() }
}.getOrDefault(secs.map { null })

// ---------- pictures ----------

/** Before/after with a slider: the right side is the real converted picture, with its real size. */
@Composable
private fun ImagePreview(f: PreviewFile, count: Int, spec: ConvertSpec) {
    val ctx = LocalContext.current
    data class R(val before: Bitmap, val after: Bitmap, val inW: Int, val inH: Int, val outW: Int, val outH: Int, val outBytes: Long)
    var r by remember(f.uri) { mutableStateOf<R?>(null) }
    val decoded = remember(f.uri) { HashMap<Int, Bitmap>() }
    var busy by remember { mutableStateOf(true) }
    var err by remember { mutableStateOf<String?>(null) }
    val key = "${spec.format}|${spec.height}|${spec.opt("quality")}|${spec.opt("targetKb")}"
    LaunchedEffect(f.uri, key) {
        busy = true
        delay(250) // wait for the user to stop tapping
        val res = withContext(Dispatchers.IO) {
            runCatching {
                val src = previewCopy(ctx, f.uri, f.name) ?: throw ConvertError("Couldn't read the picture.")
                // Decoding a big photo is slow (and HEIC grids need stitching), so keep what was read.
                val full = synchronized(decoded) { decoded[spec.height ?: 0] } ?: ImageTools.decode(ctx, "preview", src, spec.height).also { synchronized(decoded) { decoded[spec.height ?: 0] = it } }
                val inBounds = synchronized(decoded) { decoded[-1] } ?: (if (max(full.width, full.height) <= 1600) full else ImageTools.decode(ctx, "preview", src, 1600)).also { synchronized(decoded) { decoded[-1] = it } }
                val out = File(ctx.cacheDir, "preview/out.${spec.format}").apply { delete() }
                ImageTools.encode(ctx, "preview", full, out, spec.format, spec.opt("quality")?.toIntOrNull() ?: 85, spec.opt("targetKb")?.toIntOrNull())
                val after = ImageTools.decode(ctx, "preview", out, 1600)
                val o = BitmapFactory.Options().apply { inJustDecodeBounds = true }.also { BitmapFactory.decodeFile(src.absolutePath, it) }
                R(inBounds, after, if (o.outWidth > 0) o.outWidth else full.width, if (o.outHeight > 0) o.outHeight else full.height,
                    full.width, full.height, out.length())
            }
        }
        busy = false
        res.onSuccess { r = it; err = null }.onFailure { err = it.message }
    }
    var split by remember { mutableFloatStateOf(0.5f) }
    PreviewBox(230.dp, dark = true) {
        val cur = r
        if (cur != null) {
            Box(Modifier.fillMaxSize().pointerInput(Unit) {
                detectHorizontalDragGestures { change, _ -> split = (change.position.x / size.width).coerceIn(0f, 1f) }
            }) {
                Image(cur.after.asImageBitmap(), null, Modifier.fillMaxSize(), contentScale = ContentScale.Fit)
                Image(cur.before.asImageBitmap(), null, Modifier.fillMaxSize().drawWithContent {
                    clipRect(right = size.width * split) { this@drawWithContent.drawContent() }
                }, contentScale = ContentScale.Fit)
                BoxWithConstraints(Modifier.fillMaxSize()) {
                    val x = maxWidth * split
                    Box(Modifier.offset(x = x - 1.dp).width(2.dp).fillMaxHeight().background(Color.White))
                    Surface(Modifier.offset(x = x - 16.dp, y = maxHeight / 2 - 16.dp).size(32.dp), shape = CircleShape, color = Color.White, shadowElevation = 3.dp) {
                        Icon(Icons.Outlined.UnfoldMore, "Drag to compare", Modifier.padding(6.dp).graphicsLayer(rotationZ = 90f), tint = Color.Black)
                    }
                }
                Tag("Before", Modifier.align(Alignment.TopStart))
                Tag("After", Modifier.align(Alignment.TopEnd))
            }
        }
        if (busy) Box(Modifier.align(Alignment.BottomEnd).padding(10.dp)) { CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp, color = Color.White) }
        if (cur == null && !busy) Text(err ?: "No preview", color = Color.White, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(16.dp))
    }
    r?.let { cur ->
        val change = if (f.size > 0) ((cur.outBytes - f.size) * 100 / f.size) else 0
        Caption("${cur.inW}×${cur.inH} · ${mb(f.size)}  →  ${cur.outW}×${cur.outH} · ${mb(cur.outBytes)} (${if (change <= 0) "" else "+"}$change%)" +
            if (count > 1) " · first of $count pictures" else "")
    }
}

@Composable
private fun Tag(text: String, modifier: Modifier) {
    Surface(modifier.padding(10.dp), shape = RoundedCornerShape(8.dp), color = Color(0x99000000)) {
        Text(text, Modifier.padding(horizontal = 8.dp, vertical = 3.dp), color = Color.White, fontSize = 11.sp, fontWeight = FontWeight.SemiBold)
    }
}

/** Pictures to PDF: each picture placed on a page as it will be. */
@Composable
private fun PagesMockPreview(files: List<PreviewFile>, spec: ConvertSpec) {
    val ctx = LocalContext.current
    val page = spec.opt("page") ?: "a4"
    val margin = spec.opt("margin") == "1"
    var thumbs by remember(files.map { it.uri }) { mutableStateOf<List<Bitmap?>?>(null) }
    LaunchedEffect(files.map { it.uri }) {
        thumbs = withContext(Dispatchers.IO) {
            files.take(12).map { f -> previewCopy(ctx, f.uri, f.name)?.let { runCatching { ImageTools.decode(ctx, "preview", it, 500) }.getOrNull() } }
        }
    }
    val list = thumbs
    if (list == null) { PreviewBox(200.dp) { Loading() }; return }
    LazyRow(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
        itemsIndexed(list) { i, b ->
            val land = b != null && b.width > b.height
            val ratio = when (page) { "fit" -> if (b != null) b.width.toFloat() / b.height else 0.7f; "letter" -> if (land) 11f / 8.5f else 8.5f / 11f
                else -> if (land) 1.414f else 1 / 1.414f }
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Box(Modifier.height(170.dp).aspectRatio(ratio).clip(RoundedCornerShape(4.dp)).background(Color.White)
                    .border(1.dp, MaterialTheme.colorScheme.outlineVariant, RoundedCornerShape(4.dp)).padding(if (margin) 10.dp else 0.dp),
                    contentAlignment = Alignment.Center) {
                    if (b != null) Image(b.asImageBitmap(), null, Modifier.fillMaxSize(), contentScale = ContentScale.Fit)
                }
                Text("Page ${i + 1}", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 4.dp))
            }
        }
    }
    Caption("${files.size} ${if (files.size == 1) "page" else "pages"}${if (files.size > 12) " (first 12 shown)" else ""}.")
}

// ---------- PDF ----------

private fun renderPages(ctx: Context, uri: Uri, max: Int = 60, width: Int = 260): Pair<Int, List<Bitmap>> {
    ctx.contentResolver.openFileDescriptor(uri, "r")!!.use { pfd ->
        PdfRenderer(pfd).use { r ->
            val list = (0 until minOf(r.pageCount, max)).map { i ->
                r.openPage(i).use { p ->
                    val h = (width * p.height / p.width.toFloat()).toInt().coerceAtLeast(1)
                    Bitmap.createBitmap(width, h, Bitmap.Config.ARGB_8888).also {
                        it.eraseColor(android.graphics.Color.WHITE)
                        p.render(it, null, null, PdfRenderer.Page.RENDER_MODE_FOR_DISPLAY)
                    }
                }
            }
            return r.pageCount to list
        }
    }
}

/** Page thumbnails. Rotate turns them; Pick pages lets the user tap pages to keep. */
@Composable
private fun PdfPreview(f: PreviewFile, tool: ConvertTool, spec: ConvertSpec, onPagesChange: (String) -> Unit, onLocked: () -> Unit) {
    val ctx = LocalContext.current
    var pages by remember(f.uri) { mutableStateOf<Pair<Int, List<Bitmap>>?>(null) }
    var err by remember(f.uri) { mutableStateOf<String?>(null) }
    LaunchedEffect(f.uri) {
        withContext(Dispatchers.IO) { runCatching { renderPages(ctx, f.uri) } }
            .onSuccess { pages = it }
            .onFailure { err = if (it is SecurityException) "This PDF is locked. Type its password below to unlock it." .also { onLocked() } else "Couldn't show this PDF." }
    }
    val p = pages
    if (p == null) { PreviewBox(200.dp) { if (err != null) Text(err!!, Modifier.padding(16.dp), style = MaterialTheme.typography.bodySmall) else Loading() }; return }
    val (count, thumbs) = p
    val angle = if (tool.id == ConvertTools.PDF_ROTATE) (spec.opt("angle")?.toFloatOrNull() ?: 90f) else 0f
    val picking = tool.id == ConvertTools.PDF_PAGES
    val chosen = remember(spec.opt("pages"), count) {
        spec.opt("pages")?.let { runCatching { PdfTools.parseRanges(it, count).toSet() }.getOrNull() } ?: emptySet()
    }
    LazyRow(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
        itemsIndexed(thumbs) { i, b ->
            val n = i + 1
            val on = !picking || chosen.isEmpty() || n in chosen
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                // Show the page already turned, in a frame of the turned shape.
                val shown = remember(b, angle) {
                    if (angle % 360 == 0f) b else Bitmap.createBitmap(b, 0, 0, b.width, b.height, android.graphics.Matrix().apply { postRotate(angle) }, true)
                }
                Box(Modifier.height(170.dp).aspectRatio(shown.width.toFloat() / shown.height)
                    .clip(RoundedCornerShape(4.dp))
                    .border(if (picking && n in chosen) 3.dp else 1.dp, if (picking && n in chosen) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.outlineVariant, RoundedCornerShape(4.dp))
                    .then(if (picking) Modifier.clickable {
                        val next = if (n in chosen) chosen - n else chosen + n
                        onPagesChange(PdfTools.rangesText(next))
                    } else Modifier)
                    .graphicsLayer(alpha = if (on) 1f else 0.35f)) {
                    Image(shown.asImageBitmap(), "Page $n", Modifier.fillMaxSize())
                    if (picking && n in chosen) Icon(Icons.Outlined.CheckCircle, null, Modifier.align(Alignment.TopEnd).padding(4.dp), tint = MaterialTheme.colorScheme.primary)
                }
                Text("$n", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 4.dp))
            }
        }
    }
    Caption(when (tool.id) {
        ConvertTools.PDF_PAGES -> if (chosen.isEmpty()) "$count pages. Tap pages to choose them." else "${chosen.size} of $count pages chosen."
        ConvertTools.PDF_IMAGES -> "$count pages → $count pictures."
        else -> "$count pages · ${mb(f.size)}" + if (count > thumbs.size) " (first ${thumbs.size} shown)" else ""
    })
}

@Composable
private fun PdfMergePreview(files: List<PreviewFile>) {
    val ctx = LocalContext.current
    var covers by remember(files.map { it.uri }) { mutableStateOf<List<Pair<Bitmap?, Int>>?>(null) }
    LaunchedEffect(files.map { it.uri }) {
        covers = withContext(Dispatchers.IO) {
            files.map { f -> runCatching { renderPages(ctx, f.uri, max = 1).let { it.second.firstOrNull() to it.first } }.getOrDefault(null to 0) }
        }
    }
    val list = covers
    if (list == null) { PreviewBox(200.dp) { Loading() }; return }
    LazyRow(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
        itemsIndexed(list) { i, (b, n) ->
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Box(Modifier.height(170.dp).aspectRatio(b?.let { it.width.toFloat() / it.height } ?: 0.7f).clip(RoundedCornerShape(4.dp))
                    .background(Color.White).border(1.dp, MaterialTheme.colorScheme.outlineVariant, RoundedCornerShape(4.dp))) {
                    if (b != null) Image(b.asImageBitmap(), null, Modifier.fillMaxSize())
                    Surface(Modifier.align(Alignment.TopStart).padding(4.dp), shape = CircleShape, color = MaterialTheme.colorScheme.primary) {
                        Text("${i + 1}", Modifier.padding(horizontal = 7.dp, vertical = 1.dp), color = Color.White, fontSize = 11.sp)
                    }
                }
                Text(if (n > 0) "$n pages" else "locked", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 4.dp))
            }
        }
    }
    Caption("${list.sumOf { it.second }} pages in all, in this order.")
}

// ---------- documents, data, subtitles, archives ----------

@Composable
private fun DocPreview(f: PreviewFile, spec: ConvertSpec) {
    val ctx = LocalContext.current
    var html by remember(f.uri) { mutableStateOf<String?>(null) }
    var err by remember(f.uri) { mutableStateOf<String?>(null) }
    LaunchedEffect(f.uri) {
        withContext(Dispatchers.IO) {
            runCatching { PdfTools.setup(ctx); DocTools.toHtml(previewCopy(ctx, f.uri, f.name) ?: throw ConvertError("Couldn't read the file.")) }
        }.onSuccess { html = it.take(60_000) }.onFailure { err = it.message }
    }
    val h = html
    PreviewBox(260.dp) {
        when {
            h != null -> Box(Modifier.padding(14.dp).fillMaxHeight().widthIn(max = 420.dp).aspectRatio(if (spec.format == "pdf") 1 / 1.414f else 0.8f, matchHeightConstraintsFirst = true)
                .clip(RoundedCornerShape(4.dp)).background(Color.White)) {
                val plain = spec.format == "txt"
                AndroidView(factory = { c -> TextView(c).apply {
                    setTextColor(android.graphics.Color.rgb(23, 24, 28)); setPadding(28, 24, 28, 24)
                    textSize = if (plain) 8f else when (spec.opt("font")) { "small" -> 7f; "large" -> 10f; else -> 8.5f }
                    if (plain) typeface = android.graphics.Typeface.MONOSPACE
                } }, update = { tv ->
                    tv.text = if (plain) Html.fromHtml(h, Html.FROM_HTML_MODE_LEGACY).toString() else Html.fromHtml(h, Html.FROM_HTML_MODE_LEGACY)
                    tv.typeface = if (plain) android.graphics.Typeface.MONOSPACE else android.graphics.Typeface.DEFAULT
                }, modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()))
            }
            err != null -> Text(err!!, Modifier.padding(16.dp), style = MaterialTheme.typography.bodySmall)
            else -> Loading()
        }
    }
    Caption(if (spec.format == "pdf") "First page as it will look. Scroll to read more." else "How the text will come out. Scroll to read more.")
}

@Composable
private fun DataPreview(f: PreviewFile) {
    val ctx = LocalContext.current
    var table by remember(f.uri) { mutableStateOf<DataTools.Table?>(null) }
    var err by remember(f.uri) { mutableStateOf<String?>(null) }
    LaunchedEffect(f.uri) {
        withContext(Dispatchers.IO) {
            runCatching {
                val file = previewCopy(ctx, f.uri, f.name) ?: throw ConvertError("Couldn't read the file.")
                when (file.extension.lowercase().substringAfterLast('.')) {
                    "xlsx" -> DataTools.readXlsx(file); "json" -> DataTools.readJson(file.readText())
                    "tsv" -> DataTools.readCsv(file.readText(), '\t'); else -> DataTools.readCsv(file.readText(), null)
                }
            }
        }.onSuccess { table = it }.onFailure { err = it.message }
    }
    val t = table
    if (t == null) { PreviewBox(160.dp) { if (err != null) Text(err!!, Modifier.padding(16.dp)) else Loading() }; return }
    val cols = t.header.size.coerceAtMost(8)
    Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).border(1.dp, MaterialTheme.colorScheme.outlineVariant, RoundedCornerShape(12.dp))
        .horizontalScroll(rememberScrollState())) {
        (listOf(t.header) + t.rows.take(8)).forEachIndexed { ri, row ->
            Row(Modifier.background(if (ri == 0) MaterialTheme.colorScheme.surfaceContainerHighest else Color.Transparent)) {
                (0 until cols).forEach { c ->
                    Text(row.getOrElse(c) { "" }, Modifier.width(110.dp).padding(horizontal = 10.dp, vertical = 7.dp), maxLines = 1,
                        overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodySmall, fontWeight = if (ri == 0) FontWeight.SemiBold else FontWeight.Normal)
                }
            }
            if (ri == 0) HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
        }
    }
    Caption("${t.rows.size} rows · ${t.header.size} columns" + if (t.rows.size > 8) " (first 8 shown)" else "")
}

private data class Cue(val start: Double, val end: Double, val text: String)

private fun parseCues(text: String): List<Cue> {
    val time = Regex("""(?:(\d+):)?(\d{1,2}):(\d{2})[,.](\d{1,3})""")
    fun secs(m: MatchResult) = (m.groupValues[1].toIntOrNull() ?: 0) * 3600.0 + m.groupValues[2].toInt() * 60 + m.groupValues[3].toInt() +
        m.groupValues[4].padEnd(3, '0').toInt() / 1000.0
    return text.replace("\r", "").split(Regex("\n\\s*\n")).mapNotNull { block ->
        val lines = block.lines().filter { it.isNotBlank() }
        val i = lines.indexOfFirst { it.contains("-->") }
        if (i < 0) return@mapNotNull null
        val ms = time.findAll(lines[i]).toList()
        if (ms.size < 2) null else Cue(secs(ms[0]), secs(ms[1]), lines.drop(i + 1).joinToString(" ").replace(Regex("<[^>]+>|\\{[^}]+\\}"), ""))
    }
}

private fun cueTime(s: Double): String {
    val t = s.coerceAtLeast(0.0)
    return "%d:%02d.%d".format((t / 60).toInt(), (t % 60).toInt(), ((t * 10) % 10).toInt())
}

@Composable
private fun SubsPreview(f: PreviewFile, spec: ConvertSpec) {
    val ctx = LocalContext.current
    var cues by remember(f.uri) { mutableStateOf<List<Cue>?>(null) }
    LaunchedEffect(f.uri) {
        cues = withContext(Dispatchers.IO) {
            runCatching { parseCues(ctx.contentResolver.openInputStream(f.uri)!!.use { it.readBytes().decodeToString() }) }
                .onFailure { android.util.Log.w("AnySave", "subtitle preview", it) }.getOrDefault(emptyList())
        }
    }
    val list = cues ?: run { PreviewBox(120.dp) { Loading() }; return }
    val shift = spec.opt("shift")?.replace(',', '.')?.toDoubleOrNull() ?: 0.0
    Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).background(MaterialTheme.colorScheme.surfaceContainerHighest).padding(12.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp)) {
        if (list.isEmpty()) Text("No timed lines found (ASS styles are kept but not shown here).", style = MaterialTheme.typography.bodySmall)
        list.take(5).forEach { c ->
            Row {
                Text("${cueTime(c.start + shift)} → ${cueTime(c.end + shift)}", fontFamily = FontFamily.Monospace, fontSize = 12.sp,
                    color = if (shift != 0.0) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.width(140.dp))
                Text(c.text, style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
        }
    }
    Caption("${list.size} lines" + if (shift != 0.0) " · every line moved ${if (shift > 0) "later" else "earlier"} by ${kotlin.math.abs(shift)} s" else "")
}

@Composable
private fun ArchivePreview(f: PreviewFile) {
    val ctx = LocalContext.current
    var entries by remember(f.uri) { mutableStateOf<List<Pair<String, Long>>?>(null) }
    var err by remember(f.uri) { mutableStateOf<String?>(null) }
    LaunchedEffect(f.uri) {
        withContext(Dispatchers.IO) { runCatching { ArchiveTools.list(previewCopy(ctx, f.uri, f.name) ?: throw ConvertError("Couldn't read the file.")) } }
            .onSuccess { entries = it }.onFailure { err = it.message }
    }
    val list = entries
    if (list == null) { PreviewBox(120.dp) { if (err != null) Text(err!!, Modifier.padding(16.dp)) else Loading() }; return }
    FileList(list.map { it.first to it.second })
    Caption(if (list.isEmpty()) "Can't look inside this archive here." else "${list.size} ${if (list.size == 1) "file" else "files"} · ${mb(list.sumOf { it.second.coerceAtLeast(0) })} when unpacked")
}

@Composable
private fun PackPreview(files: List<PreviewFile>, spec: ConvertSpec) {
    FileList(files.map { it.name to it.size })
    Caption("${files.size} ${if (files.size == 1) "file" else "files"} · ${mb(files.sumOf { it.size })} before packing. Photos and videos hardly get smaller; documents do.")
}

@Composable
private fun FileList(items: List<Pair<String, Long>>) {
    Column(Modifier.fillMaxWidth().heightIn(max = 220.dp).clip(RoundedCornerShape(12.dp)).background(MaterialTheme.colorScheme.surfaceContainerHighest)
        .verticalScroll(rememberScrollState()).padding(vertical = 6.dp)) {
        items.take(200).forEach { (name, size) ->
            Row(Modifier.padding(horizontal = 12.dp, vertical = 5.dp), verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.InsertDriveFile, null, Modifier.size(16.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
                Spacer(Modifier.width(8.dp))
                Text(name, Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (size >= 0) Text(mb(size), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}
