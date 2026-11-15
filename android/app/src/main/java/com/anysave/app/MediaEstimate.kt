package com.anysave.app

import kotlin.math.roundToInt

/** Output picture size and bitrates, shared by the hardware encoder and the size estimate on the Convert screen. */
object MediaEstimate {
    /** Width and height after shape, turn and resolution, from the source's (already upright) size. */
    fun outSize(srcW: Int, srcH: Int, spec: ConvertSpec): Pair<Int, Int> {
        var w = srcW.toDouble(); var h = srcH.toDouble()
        val r = when (spec.opt("aspect")) { "9:16" -> 9.0 / 16; "1:1" -> 1.0; "4:5" -> 0.8; "16:9" -> 16.0 / 9; else -> null }
        if (r != null) { if (w / h > r) w = h * r else h = w / r }
        if (spec.opt("rotate") == "90" || spec.opt("rotate") == "270") { val t = w; w = h; h = t }
        spec.height?.let { if (h > it) { w = w * it / h; h = it.toDouble() } }
        return ((w / 2).roundToInt() * 2).coerceAtLeast(2) to ((h / 2).roundToInt() * 2).coerceAtLeast(2)
    }

    /** A bitrate that looks good: bits per pixel per frame, less for H.265/AV1, more for "high quality". */
    fun videoBitrate(w: Int, h: Int, codec: String?, hq: Boolean, fps: Int = 30): Long {
        val bpp = when (codec) { "h265" -> 0.06; "av1" -> 0.045; else -> 0.1 } * (if (hq) 1.5 else 1.0)
        return (w.toLong() * h * fps * bpp).toLong().coerceIn(300_000, 60_000_000)
    }

    fun audioBitrate(spec: ConvertSpec): Long = when (spec.format) {
        "wav" -> 1_411_000; "flac" -> 750_000
        "mp3" -> (spec.opt("abitrate")?.toLong() ?: 190) * 1000
        "m4a" -> (spec.opt("abitrate")?.toLong() ?: 192) * 1000
        "ogg" -> (spec.opt("abitrate")?.toLong() ?: 160) * 1000
        "opus" -> (spec.opt("abitrate")?.toLong() ?: 128) * 1000
        else -> 128_000
    }

    /** Rough size of the result in bytes, or null when it can't be guessed. */
    fun bytes(srcW: Int?, srcH: Int?, seconds: Double?, spec: ConvertSpec): Long? {
        val secs = seconds?.takeIf { it > 0 } ?: return null
        val speed = spec.opt("speed")?.toDoubleOrNull() ?: 1.0
        val outSecs = secs / speed
        if (spec.format in ConvertSpec.AUDIO_FORMATS) return (audioBitrate(spec) * outSecs / 8).toLong()
        spec.targetMb?.takeIf { it > 0 }?.let { return it * 1024L * 1024 * 92 / 100 }
        if (srcW == null || srcH == null || srcW <= 0) return null
        if (spec.format == "gif") return null
        val (w, h) = outSize(srcW, srcH, spec)
        val fps = spec.opt("fps")?.toIntOrNull() ?: 30
        val v = videoBitrate(w, h, if (spec.format == "webm") "av1" else spec.opt("codec"), spec.targetMb == 0, fps)
        val a = if (spec.removeAudio) 0 else 128_000
        return ((v + a) * outSecs / 8).toLong()
    }
}
