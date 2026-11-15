package com.anysave.app

import android.content.Context
import android.net.Uri
import java.io.File

/** Voice Lab's background jobs (run by the download service, like every conversion). */
object VoiceJobs {
    private val videoOut = setOf("mp4", "mov", "m4v", "mkv", "webm")
    private val audioOut = setOf("mp3", "m4a", "wav", "flac")

    fun run(ctx: Context, id: String, spec: ConvertSpec, input: File, work: File, onProgress: (Float) -> Unit): File {
        if (!Voice.available(ctx)) throw ConvertError("Voice Lab isn't available on this phone's type of processor.")
        val out = when (spec.tool) {
            ConvertTools.VOICE_SAY -> say(ctx, id, spec, input, work, onProgress)
            ConvertTools.VOICE_CLEAN -> clean(ctx, id, spec, input, work, onProgress)
            else -> copy(ctx, id, spec, input, work, onProgress)
        }
        // The text or recording the screen handed over isn't needed any more.
        spec.inputs.mapNotNull { Uri.parse(it).takeIf { u -> u.scheme == "file" }?.path?.let(::File) }
            .filter { it.parentFile == handoff(ctx) }.forEach { it.delete() }
        return out
    }

    /** Where the screen leaves typed text and recordings for a job; leftovers older than two days go. */
    private fun handoff(ctx: Context): File = File(ctx.filesDir, "voice-handoff").apply {
        mkdirs()
        listFiles()?.filter { it.lastModified() < System.currentTimeMillis() - 2 * 86_400_000L }?.forEach { it.delete() }
    }

    fun handoffFile(ctx: Context, ext: String): File = File(handoff(ctx), "${System.currentTimeMillis()}.$ext")

    /** Downloads what's missing as the first [share]% of the progress; returns where the rest starts. */
    private fun need(ctx: Context, id: String, packs: List<String>, share: Float, onProgress: (Float) -> Unit): Float {
        val missing = packs.filter { it.isNotEmpty() && !Voice.ready(ctx, it) }
        if (missing.isEmpty()) return 0f
        missing.forEachIndexed { i, p -> Voice.ensure(ctx, id, p) { onProgress((i + it / 100) * share / missing.size) } }
        return share
    }

    private fun say(ctx: Context, id: String, spec: ConvertSpec, input: File, work: File, onProgress: (Float) -> Unit): File {
        val text = input.readText()
        val v = Voice.voice(ctx, spec.opt("voice") ?: "") ?: throw ConvertError("Pick a voice first.")
        val p0 = need(ctx, id, listOf(v.pack), 30f, onProgress)
        val wav = Voice.speak(ctx, id, work, text, v, spec.opt("speed")?.toDoubleOrNull() ?: 1.0) { onProgress(p0 + it * (90 - p0) / 100) }
        val fmt = spec.format.takeIf { it in audioOut } ?: "mp3"
        val out = File(work, "out").apply { mkdirs() }.let { File(it, "${speakName(text)}.$fmt") }
        if (fmt == "wav") wav.renameTo(out)
        else Converter.ffmpeg(ctx, id, listOf("-i", wav.absolutePath) + Converter.audioCodec(ctx, fmt) + out.absolutePath, null) { onProgress(90 + it / 10) }
        return out
    }

    private fun clean(ctx: Context, id: String, spec: ConvertSpec, input: File, work: File, onProgress: (Float) -> Unit): File {
        if (!Converter.hasAudio(input)) throw ConvertError("${input.name} has no sound to clean.")
        val strength = spec.opt("strength") ?: "strong"
        val (pack, _) = Voice.cleanMode(strength)
        val p0 = need(ctx, id, listOf(pack), 15f, onProgress)
        val dur = Converter.durationSec(input)
        val clean = Voice.denoise(ctx, id, input, work, strength, 0.0, 0.0) { onProgress(p0 + it * (85 - p0) / 100) }
        val af = Voice.voiceFilters(strength, spec.opt("level") != "0")
        val ext = input.extension.lowercase()
        val video = Converter.hasVideo(input)
        val fmt = spec.format.takeIf { it in audioOut || it in videoOut } ?: when {
            video && ext in videoOut -> ext
            video -> "mp4"
            ext in audioOut -> ext
            else -> "m4a"
        }
        val out = File(work, "out").apply { mkdirs() }.let { File(it, "${ConvertTools.safe(input.nameWithoutExtension)} (clean).$fmt") }
        val a = if (video && fmt in videoOut) {
            // Same picture (copied as it is), cleaned sound.
            listOf("-i", input.absolutePath, "-i", clean.absolutePath, "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy",
                "-af", af.joinToString(",")) +
                (if (fmt == "webm") listOf("-c:a", "libopus", "-b:a", "128k") else listOf("-c:a", "aac", "-b:a", "192k")) +
                listOf("-ac", "2") + (if (fmt in setOf("mp4", "mov", "m4v")) listOf("-movflags", "+faststart") else emptyList())
        } else listOf("-i", clean.absolutePath, "-af", af.joinToString(",")) + Converter.audioCodec(ctx, fmt)
        Converter.ffmpeg(ctx, id, a + out.absolutePath, dur) { onProgress(85 + it * 0.15f) }
        return out
    }

    private fun copy(ctx: Context, id: String, spec: ConvertSpec, input: File, work: File, onProgress: (Float) -> Unit): File {
        if (spec.opt("consent") != "1") throw ConvertError("Tick the box to say it's your voice or you have permission.")
        val p0 = need(ctx, id, listOf("pocket"), 90f, onProgress)
        Voice.copyVoice(ctx, id, input, work, spec.opt("name") ?: "")
        onProgress(maxOf(p0, 99f))
        return File(work, ConvertTools.IN_APP).also { it.writeText("") }
    }

    /** A file name from the first words of the text. */
    fun speakName(text: String): String {
        val w = text.split(Regex("\\s+")).filter { it.isNotEmpty() }.take(6).joinToString(" ")
        return ConvertTools.safe(w.take(50).trimEnd('.', ',', ';', ':', '!', '?', '…').ifBlank { "Read aloud" })
    }

    // ---------- starting them ----------

    fun startSay(ctx: Context, text: String, voice: TtsVoice, speed: Double, format: String): String {
        val f = handoffFile(ctx, "txt")
        f.writeText(text)
        return DownloadService.startConvertId(ctx, listOf(Uri.fromFile(f)), speakName(text), ConvertSpec(tool = ConvertTools.VOICE_SAY, format = format,
            opts = mapOf("voice" to voice.id, "speed" to "%.2f".format(java.util.Locale.ROOT, speed))))
    }

    fun startClean(ctx: Context, uri: Uri, name: String, strength: String, level: Boolean, format: String): String =
        DownloadService.startConvertId(ctx, listOf(uri), name, ConvertSpec(tool = ConvertTools.VOICE_CLEAN, format = format,
            opts = mapOf("strength" to strength, "level" to if (level) "1" else "0")))

    fun startCopy(ctx: Context, uri: Uri, name: String): String =
        DownloadService.startConvertId(ctx, listOf(uri), name, ConvertSpec(tool = ConvertTools.VOICE_COPY, format = "wav",
            opts = mapOf("name" to name, "consent" to "1")))
}
