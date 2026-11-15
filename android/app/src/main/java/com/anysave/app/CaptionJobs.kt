package com.anysave.app

import android.content.Context
import android.net.Uri
import org.json.JSONObject
import java.io.File

/**
 * The Captions screen's background jobs. They run in the download service like every conversion,
 * so they keep going with the screen closed and show up in Downloads with progress and Cancel.
 */
object CaptionJobs {
    /**
     * Speech to text for one file: gets the model if needed, then listens. The lines go into the
     * project (for editing and styling); nothing is saved to Downloads until the user saves.
     */
    fun make(ctx: Context, id: String, spec: ConvertSpec, input: File, work: File, onProgress: (Float) -> Unit): File {
        val project = spec.opt("project") ?: throw ConvertError("This captions job has no project.")
        if (!Speech.available(ctx)) throw ConvertError("Captions aren't available on this phone's type of processor.")
        val lang = spec.opt("lang") ?: "auto"
        val model = if (spec.opt("model") == "best") Speech.BEST else Speech.STANDARD
        if (!Converter.hasAudio(input)) throw ConvertError("${input.name} has no sound.")
        val getModel = !Speech.has(ctx, model)
        val p0 = if (getModel) 35f else 0f
        if (getModel) Speech.download(ctx, id, model) { onProgress(it * p0 / 100) }
        ConvertTools.check(id)
        val wav = File(work, "speech.wav")
        Converter.speechWav(ctx, id, input, wav) { onProgress(p0 + it * 0.04f) }
        val secs = (wav.length() - 44) / 32000.0
        if (secs < 0.5) throw ConvertError("There's no sound to listen to in ${input.name}.")
        val cues = Speech.transcribe(ctx, id, wav, model, lang, secs) { onProgress(p0 + 4 + it * (95.5f - p0) / 100) }
        if (cues.isEmpty()) throw ConvertError("AnySave didn't hear any talking in ${input.name}.")
        val heard = runCatching {
            JSONObject(File(work, "speech.json").readText()).optJSONObject("result")?.optString("language").orEmpty()
        }.getOrDefault("")
        val p = CapProjects.get(ctx, project) ?: throw ConvertError("The captions were deleted while AnySave was listening.")
        val n = p.chunk.toIntOrNull()
        CapProjects.save(ctx, p.copy(cues = if (n != null) Captions.chunk(cues, n) else cues, orig = cues, jobId = null,
            lang = if (lang == "auto" && heard.isNotBlank()) heard else lang, edited = System.currentTimeMillis()))
        wav.delete()
        return File(work, ConvertTools.IN_APP).also { it.writeText("") }
    }

    /** Starts listening to [p]'s file. */
    fun startMake(ctx: Context, p: CapProject, accurate: Boolean): String {
        val model = Speech.modelFor(ctx, p.lang, accurate)
        val spec = ConvertSpec(tool = ConvertTools.CAPTIONS_MAKE, format = "srt",
            opts = mapOf("project" to p.id, "lang" to p.lang, "model" to if (model == Speech.BEST) "best" else "std"))
        val jobId = DownloadService.startConvertId(ctx, listOf(Uri.parse(p.source)), p.name, spec)
        CapProjects.save(ctx, p.copy(jobId = jobId, edited = System.currentTimeMillis()))
        return jobId
    }

    /** Saves the video with the captions drawn on. */
    fun startBurn(ctx: Context, p: CapProject): String {
        val f = File(ctx.filesDir, "captions/${p.id}.ass")
        f.writeText(Captions.toAss(p.cues, p.style, p.w, p.h, p.lang))
        val spec = ConvertSpec(tool = ConvertTools.CAPTIONS_BURN, format = "mp4", opts = mapOf("name" to p.name.substringBeforeLast('.')))
        return DownloadService.startConvertId(ctx, listOf(Uri.parse(p.source), Uri.fromFile(f)), p.name, spec)
    }

    /** Saves the captions as a subtitle or text file (srt, vtt, txt) in Downloads. */
    fun startFile(ctx: Context, p: CapProject, format: String): String {
        val text = when (format) { "vtt" -> Captions.toVtt(p.cues); "txt" -> Captions.toTxt(p.cues); else -> Captions.toSrt(p.cues) }
        val f = File(ctx.filesDir, "captions/${p.id}.$format")
        f.writeText(text)
        val spec = ConvertSpec(tool = ConvertTools.CAPTIONS_FILE, format = format, opts = mapOf("name" to p.name.substringBeforeLast('.')))
        return DownloadService.startConvertId(ctx, listOf(Uri.fromFile(f)), p.name, spec)
    }
}
