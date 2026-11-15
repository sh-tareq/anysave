package com.anysave.app

import android.content.Context
import android.net.Uri
import android.os.Handler
import android.os.HandlerThread
import androidx.media3.common.Effect
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.util.UnstableApi
import androidx.media3.effect.Presentation
import androidx.media3.effect.ScaleAndRotateTransformation
import androidx.media3.transformer.Composition
import androidx.media3.transformer.DefaultEncoderFactory
import androidx.media3.transformer.EditedMediaItem
import androidx.media3.transformer.Effects
import androidx.media3.transformer.ExportException
import androidx.media3.transformer.ExportResult
import androidx.media3.transformer.ProgressHolder
import androidx.media3.transformer.Transformer
import androidx.media3.transformer.VideoEncoderSettings
import java.io.File
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CountDownLatch

/**
 * Fast MP4 conversion on the phone's own video chip (Media3 Transformer): several times faster than
 * FFmpeg's software encoders and easier on the battery. Used when the chosen settings are ones it can
 * do; everything else goes through FFmpeg ([Converter]).
 */
@androidx.annotation.OptIn(UnstableApi::class)
object FastVideo {
    private class Running(val handler: Handler, val transformer: Transformer, val done: CountDownLatch)
    private val running = ConcurrentHashMap<String, Running>()
    private val cancelled = ConcurrentHashMap.newKeySet<String>()

    /** True when these settings can go through the hardware path. */
    fun canDo(spec: ConvertSpec): Boolean =
        spec.tool == ConvertTools.MEDIA && spec.format == "mp4" && (spec.opt("codec") ?: "h264") in setOf("h264", "h265") &&
            spec.opt("speed").let { it == null || it == "1" } && spec.opt("denoise") != "1" && spec.opt("loudnorm") != "1" &&
            spec.opt("stabilize") != "1" && spec.opt("fps") == null && spec.opt("engine") != "ffmpeg"

    /**
     * Converts and returns true, or returns false when the phone couldn't (the caller then uses FFmpeg).
     * Throws only when the user cancelled.
     */
    fun convert(ctx: Context, id: String, input: File, output: File, spec: ConvertSpec, onProgress: (Float) -> Unit): Boolean {
        cancelled.remove(id)
        val thread = HandlerThread("fastvideo-$id").apply { start() }
        val handler = Handler(thread.looper)
        val done = CountDownLatch(1)
        var ok = false
        var error: String? = null

        handler.post {
            try {
                val effects = mutableListOf<Effect>()
                when (spec.opt("aspect")) {
                    "9:16" -> 9f / 16f; "1:1" -> 1f; "4:5" -> 4f / 5f; "16:9" -> 16f / 9f; else -> null
                }?.let { effects += Presentation.createForAspectRatio(it, Presentation.LAYOUT_SCALE_TO_FIT_WITH_CROP) }
                // Media3 turns counter-clockwise; the app's "90" means a quarter turn clockwise, like FFmpeg's transpose=1.
                val rot = when (spec.opt("rotate")) { "90" -> 270f; "180" -> 180f; "270" -> 90f; else -> 0f }
                val flip = spec.opt("flip")
                if (rot != 0f || flip != null) {
                    effects += ScaleAndRotateTransformation.Builder()
                        .setRotationDegrees(rot)
                        .setScale(if (flip == "h") -1f else 1f, if (flip == "v") -1f else 1f)
                        .build()
                }
                spec.height?.let { effects += Presentation.createForHeight(it) }

                val clip = MediaItem.ClippingConfiguration.Builder().apply {
                    spec.startSec?.let { setStartPositionMs((it * 1000).toLong()) }
                    spec.endSec?.let { setEndPositionMs((it * 1000).toLong()) }
                }.build()
                val item = EditedMediaItem.Builder(MediaItem.Builder().setUri(Uri.fromFile(input)).setClippingConfiguration(clip).build())
                    .setRemoveAudio(spec.removeAudio)
                    .setEffects(Effects(emptyList(), effects))
                    .build()

                val seconds = (spec.endSec ?: Converter.durationSec(input) ?: 0.0) - (spec.startSec ?: 0.0)
                val bitrate = spec.targetMb?.takeIf { it > 0 && seconds > 0 }?.let { mb ->
                    val audio = if (spec.removeAudio) 0 else 128_000
                    ((mb * 0.96 * 8.0 * 1024 * 1024 / seconds) - audio).toInt().coerceAtLeast(150_000)
                } ?: run {
                    // Phone encoders pick very high bitrates on their own; choose one that suits the size.
                    val (sw, sh) = sourceSize(input) ?: return@run null
                    val (w, h) = MediaEstimate.outSize(sw, sh, spec)
                    MediaEstimate.videoBitrate(w, h, spec.opt("codec"), spec.targetMb == 0).toInt()
                }
                val settings = VideoEncoderSettings.Builder().apply { bitrate?.let { setBitrate(it) } }.build()
                val encoders = DefaultEncoderFactory.Builder(ctx.applicationContext)
                    .setRequestedVideoEncoderSettings(settings)
                    .setEnableFallback(true) // e.g. H.264 when this phone has no H.265 encoder
                    .build()

                val transformer = Transformer.Builder(ctx.applicationContext)
                    .setLooper(thread.looper)
                    .setVideoMimeType(if (spec.opt("codec") == "h265") MimeTypes.VIDEO_H265 else MimeTypes.VIDEO_H264)
                    .setEncoderFactory(encoders)
                    // Platform media metrics need Android 12; on older phones they crash the export.
                    .setUsePlatformDiagnostics(false)
                    .addListener(object : Transformer.Listener {
                        override fun onCompleted(composition: Composition, exportResult: ExportResult) { ok = true; done.countDown() }
                        override fun onError(composition: Composition, exportResult: ExportResult, exportException: ExportException) {
                            error = "${exportException.errorCodeName}: ${exportException.message}"
                            done.countDown()
                        }
                    })
                    .build()
                running[id] = Running(handler, transformer, done)
                transformer.start(item, output.absolutePath)

                val holder = ProgressHolder()
                handler.post(object : Runnable {
                    override fun run() {
                        if (done.count == 0L) return
                        if (transformer.getProgress(holder) == Transformer.PROGRESS_STATE_AVAILABLE) onProgress(holder.progress.toFloat().coerceAtMost(99f))
                        handler.postDelayed(this, 400)
                    }
                })
            } catch (e: Throwable) {
                android.util.Log.w("AnySave", "hardware conversion could not start", e)
                error = e.toString()
                done.countDown()
            }
        }
        done.await()
        running.remove(id)
        thread.quitSafely()
        if (cancelled.remove(id)) throw Exception("Cancelled")
        if (!ok) {
            android.util.Log.w("AnySave", "hardware conversion failed, using FFmpeg: $error")
            output.delete()
            return false
        }
        onProgress(100f)
        return true
    }

    /** Upright width and height of a video. */
    fun sourceSize(f: File): Pair<Int, Int>? = runCatching {
        val r = android.media.MediaMetadataRetriever()
        try {
            r.setDataSource(f.absolutePath)
            val w = r.extractMetadata(android.media.MediaMetadataRetriever.METADATA_KEY_VIDEO_WIDTH)!!.toInt()
            val h = r.extractMetadata(android.media.MediaMetadataRetriever.METADATA_KEY_VIDEO_HEIGHT)!!.toInt()
            val rot = r.extractMetadata(android.media.MediaMetadataRetriever.METADATA_KEY_VIDEO_ROTATION)?.toIntOrNull() ?: 0
            if (rot == 90 || rot == 270) h to w else w to h
        } finally { r.release() }
    }.getOrNull()

    fun cancel(id: String) {
        val r = running[id] ?: return
        cancelled += id
        r.handler.post {
            runCatching { r.transformer.cancel() }
            r.done.countDown() // cancel() calls no listener: wake up the waiting conversion.
        }
    }
}
