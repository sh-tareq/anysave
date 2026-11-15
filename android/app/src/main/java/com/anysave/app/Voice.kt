package com.anysave.app

import android.content.Context
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import org.apache.commons.compress.archivers.tar.TarArchiveInputStream
import org.apache.commons.compress.compressors.bzip2.BZip2CompressorInputStream
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.RandomAccessFile
import java.net.URL
import java.security.MessageDigest
import java.util.concurrent.ConcurrentHashMap

/*
 * Voice Lab on the phone, offline: read text aloud, clean up recordings and copy a voice. The same
 * engine as the desktop (sherpa-onnx's programs, bundled as libsherpa_*.so by
 * scripts/fetch-sherpa-android.sh); models are downloaded once each, checked against fixed SHA-256s.
 * English uses Kitten TTS here (fast on phones) instead of the desktop's Kokoro (3–5× slower than
 * real time on a 2018 phone); Kokoro is still offered for the other languages.
 */

data class VoiceFile(val url: String, val sha256: String, val size: Long, val name: String? = null, val tar: Boolean = false,
                     val keep: (String) -> String? = { it })

data class VoicePack(val id: String, val label: String, val files: List<VoiceFile>, val after: ((File) -> Unit)? = null) {
    val size get() = files.sumOf { it.size }
}

data class TtsVoice(val id: String, val name: String, val group: String, val pack: String, val lang: String, val sid: Int = 0,
                    val code: String = "", val ref: File? = null, val gender: String = "")

object Voice {
    private const val REL = "https://github.com/k2-fsa/sherpa-onnx/releases/download/"
    private const val PIPER = "https://huggingface.co/rhasspy/piper-voices/resolve/6b9cfaacb4fb2e02a9621227e3c1a48bec9710c3/bn/bn_BD/google/medium/"

    private fun stripTop(e: String): String? = e.substringAfter('/', "").ifEmpty { null }

    val PACKS = listOf(
        VoicePack("clean-strong", "strong clean-up", listOf(VoiceFile(REL + "speech-enhancement-models/dpdfnet2_48khz_hr.onnx",
            "0b399f8a58dc4d70d8cd97541f5c39869406145193b957d00a03b66070944928", 10596848, "dpdfnet2_48khz_hr.onnx"))),
        VoicePack("clean-light", "light clean-up", listOf(VoiceFile(REL + "speech-enhancement-models/gtcrn_simple.onnx",
            "e77603ac0c23dac3227dd2d7135b3a585cbee2679048aecfa886657d3ae1b534", 535638, "gtcrn_simple.onnx"))),
        VoicePack("kitten", "English voices", listOf(VoiceFile(REL + "tts-models/kitten-micro-en-v0_8.tar.bz2",
            "85faaea7511ca9d1d2f251fed0a4553bdf0d1ee046102fa60ddd8046c751f76f", 44423643, tar = true, keep = ::stripTop))),
        VoicePack("bangla", "Bangla voices", listOf(
            VoiceFile(PIPER + "bn_BD-google-medium.onnx", "f2e7518ed5534a755024a48c71b80bf617efaf12570bbdf3ce255a9526a8afd3", 76782515, "bn.onnx"),
            VoiceFile(PIPER + "bn_BD-google-medium.onnx.json", "bc7e5e39e2a874bdad186620576ce18089b5a06c5645e258bcea3d56fdb11c0a", 5494, "bn.onnx.json"),
            VoiceFile(REL + "tts-models/espeak-ng-data.tar.bz2", "4135ccf82e1f40613491c0874d4945ae9e9c7840933d8e25a6f9e003d9ebf533", 7252012, tar = true),
        ), after = ::preparePiper),
        VoicePack("kokoro", "voices for 6 more languages", listOf(VoiceFile(REL + "tts-models/kokoro-int8-multi-lang-v1_0.tar.bz2",
            "72c347230107ef8a31098ec4d468a605244beef5f1ed00db97efa13518800653", 132353410, tar = true, keep = ::stripTop))),
        VoicePack("pocket", "voice copier", listOf(VoiceFile(REL + "tts-models/sherpa-onnx-pocket-tts-int8-2026-01-26.tar.bz2",
            "2f3b88823cbbb9bf0b2477ec8ae7b3fec417b3a87b6bb5f256dba66f2ad967cb", 98336520, tar = true,
            keep = { e -> stripTop(e)?.takeIf { !it.startsWith("test_wavs") && it != "README.md" } }))),
    )
    fun pack(id: String) = PACKS.first { it.id == id }

    // ---------- where things are ----------

    fun root(ctx: Context) = File(ctx.noBackupFilesDir, "voice").apply { mkdirs() }
    fun packDir(ctx: Context, id: String) = File(root(ctx), id)
    fun ready(ctx: Context, id: String) = File(packDir(ctx, id), ".ready").exists()
    private fun lib(ctx: Context, n: String) = File(ctx.applicationInfo.nativeLibraryDir, n)
    fun available(ctx: Context) = lib(ctx, "libsherpa_tts.so").exists()

    // ---------- the voices ----------

    private val kittenNames = listOf("Jasper" to "man", "Bella" to "woman", "Bruno" to "man", "Luna" to "woman",
        "Hugo" to "man", "Rosie" to "woman", "Leo" to "man", "Kiki" to "woman")
    private val kokoroNames = listOf("af_alloy", "af_aoede", "af_bella", "af_heart", "af_jessica", "af_kore", "af_nicole",
        "af_nova", "af_river", "af_sarah", "af_sky", "am_adam", "am_echo", "am_eric", "am_fenrir", "am_liam", "am_michael",
        "am_onyx", "am_puck", "am_santa", "bf_alice", "bf_emma", "bf_isabella", "bf_lily", "bm_daniel", "bm_fable",
        "bm_george", "bm_lewis", "ef_dora", "em_alex", "ff_siwis", "hf_alpha", "hf_beta", "hm_omega", "hm_psi", "if_sara",
        "im_nicola", "jf_alpha", "jf_gongitsune", "jf_nezumi", "jf_tebukuro", "jm_kumo", "pf_dora", "pm_alex", "pm_santa",
        "zf_xiaobei", "zf_xiaoni", "zf_xiaoxiao", "zf_xiaoyi", "zm_yunjian", "zm_yunxi", "zm_yunxia", "zm_yunyang", "em_santa")
    // English is Kitten's on the phone; Kokoro's other languages.
    private val kokoroLangs = mapOf('e' to ("Spanish" to "es"), 'f' to ("French" to "fr"), 'h' to ("Hindi" to "hi"),
        'i' to ("Italian" to "it"), 'p' to ("Portuguese (Brazil)" to "pt-br"), 'z' to ("Chinese" to "zh"))
    private val banglaSpeakers = listOf(0, 4, 15, 1, 2, 3, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14)

    fun voices(ctx: Context): List<TtsVoice> = buildList {
        kittenNames.forEachIndexed { i, (n, g) -> add(TtsVoice("t$i", n, "English", "kitten", "en", i, gender = g)) }
        banglaSpeakers.forEachIndexed { i, sid -> add(TtsVoice("bn$sid", "Voice ${i + 1}", "Bangla", "bangla", "bn", sid)) }
        addAll(myVoices(ctx))
        kokoroNames.forEachIndexed { sid, n ->
            val (label, code) = kokoroLangs[n[0]] ?: return@forEachIndexed
            add(TtsVoice("k$sid", n.substring(3).replaceFirstChar { it.uppercase() }, label, "kokoro", code.take(2), sid, code,
                gender = if (n[1] == 'm') "man" else "woman"))
        }
    }

    fun voice(ctx: Context, id: String) = voices(ctx).firstOrNull { it.id == id }

    // ---------- downloads ----------

    /** Packs being downloaded right now, and how far (0–100). */
    val downloading = MutableStateFlow<Map<String, Float>>(emptyMap())
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    val errors = MutableStateFlow<Map<String, String>>(emptyMap())
    /** Goes up whenever a pack or a copied voice arrives or goes, so screens look again. */
    val changed = MutableStateFlow(0)

    /** Starts downloading [id] in the background (for the screen's "Get" buttons). */
    fun startDownload(ctx: Context, id: String) {
        if (ready(ctx, id) || downloading.value.containsKey(id)) return
        val app = ctx.applicationContext
        downloading.update { it + (id to 0f) }
        errors.update { it - id }
        scope.launch {
            try { ensure(app, null, id) { p -> downloading.update { m -> m + (id to p) } } }
            catch (e: Throwable) { errors.update { it + (id to (e.message ?: "Download failed")) } }
            finally { downloading.update { it - id } }
        }
    }

    private val locks = ConcurrentHashMap<String, Any>()

    /** Makes sure pack [id] is here (downloading and unpacking it if needed). [jobId] lets Cancel stop it. */
    fun ensure(ctx: Context, jobId: String?, id: String, onProgress: (Float) -> Unit) {
        if (ready(ctx, id)) return
        synchronized(locks.getOrPut(id) { Any() }) {
            if (ready(ctx, id)) return
            val p = pack(id)
            val tmp = File(root(ctx), "$id.part").apply { deleteRecursively(); mkdirs() }
            var before = 0L
            for (f in p.files) {
                val dl = File(root(ctx), "$id-${f.sha256.take(12)}.dl")
                fetch(jobId, f, dl) { done -> onProgress(((before + done) * 100f / p.size).coerceAtMost(99f)) }
                if (f.tar) untar(dl, tmp, f.keep) else dl.copyTo(File(tmp, f.name!!), overwrite = true)
                dl.delete()
                before += f.size
            }
            p.after?.invoke(tmp)
            File(tmp, ".ready").writeText("1")
            val dir = packDir(ctx, id)
            dir.deleteRecursively()
            if (!tmp.renameTo(dir)) throw Exception("Couldn't save the ${p.label}.")
            changed.update { it + 1 }
            onProgress(100f)
        }
    }

    private fun fetch(jobId: String?, f: VoiceFile, out: File, onProgress: (Long) -> Unit) {
        if (out.length() == f.size && sha(out) == f.sha256) return
        var tries = 0
        while (out.length() < f.size) {
            jobId?.let { ConvertTools.check(it) }
            try {
                val have = out.length()
                val c = Net.open(URL(f.url))
                c.instanceFollowRedirects = true
                c.connectTimeout = 20000; c.readTimeout = 30000
                c.setRequestProperty("User-Agent", "AnySave")
                if (have > 0) c.setRequestProperty("Range", "bytes=$have-")
                val code = c.responseCode
                if (code !in 200..299) throw Exception("The download failed (error $code).")
                RandomAccessFile(out, "rw").use { raf ->
                    if (!(have > 0 && code == 206)) raf.setLength(0)
                    raf.seek(raf.length())
                    val buf = ByteArray(1 shl 16)
                    var last = 0L
                    c.inputStream.use { inp ->
                        while (true) {
                            val n = inp.read(buf)
                            if (n < 0) break
                            raf.write(buf, 0, n)
                            if (raf.length() - last > 500_000) { last = raf.length(); jobId?.let { ConvertTools.check(it) }; onProgress(last) }
                        }
                    }
                }
                c.disconnect()
                if (out.length() > f.size) out.delete()
            } catch (e: Exception) {
                if (e.message == "Cancelled") throw e
                if (++tries >= 5) throw Exception("Couldn't download the voice files. Check the internet connection and try again.")
                Thread.sleep(3000L * tries)
            }
        }
        if (sha(out) != f.sha256) { out.delete(); throw Exception("A downloaded voice file was damaged. Try again.") }
    }

    private fun sha(f: File): String {
        val md = MessageDigest.getInstance("SHA-256")
        f.inputStream().use { i -> val b = ByteArray(1 shl 16); while (true) { val n = i.read(b); if (n < 0) break; md.update(b, 0, n) } }
        return md.digest().joinToString("") { "%02x".format(it) }
    }

    private fun untar(tarBz2: File, dest: File, keep: (String) -> String?) {
        val root = dest.canonicalPath + File.separator
        TarArchiveInputStream(BZip2CompressorInputStream(tarBz2.inputStream().buffered(1 shl 16))).use { t ->
            while (true) {
                val e = t.nextEntry ?: break
                if (!e.isFile) continue
                val rel = keep(e.name.removePrefix("./")) ?: continue
                val out = File(dest, rel)
                if (!out.canonicalPath.startsWith(root)) continue // never outside the pack folder
                out.parentFile?.mkdirs()
                out.outputStream().use { t.copyTo(it) }
            }
        }
    }

    /** Piper's Bangla voice for sherpa-onnx: tokens.txt from its JSON, and the facts sherpa wants inside the model. */
    private fun preparePiper(dir: File) {
        val cfg = JSONObject(File(dir, "bn.onnx.json").readText())
        val map = cfg.getJSONObject("phoneme_id_map")
        val toks = map.keys().asSequence().mapNotNull { s ->
            val ids = map.getJSONArray(s)
            if (s.codePointCount(0, s.length) == 1 && ids.length() > 0) s to ids.getInt(0) else null
        }.sortedBy { it.second }.joinToString("") { "${it.first} ${it.second}\n" }
        File(dir, "tokens.txt").writeText(toks)
        val meta = listOf("model_type" to "vits", "comment" to "piper",
            "language" to cfg.getJSONObject("language").optString("name_english"),
            "voice" to cfg.getJSONObject("espeak").optString("voice"), "has_espeak" to "1",
            "n_speakers" to cfg.optInt("num_speakers").toString(), "sample_rate" to cfg.getJSONObject("audio").optInt("sample_rate").toString())
        // A protobuf message may be extended by appending fields: ModelProto.metadata_props is field 14.
        fun varint(v: Long, o: ByteArrayOutputStream) { var x = v; while (x >= 0x80) { o.write(((x and 0x7f) or 0x80).toInt()); x = x ushr 7 }; o.write(x.toInt()) }
        fun field(n: Int, v: ByteArray) = ByteArrayOutputStream().also { varint(((n shl 3) or 2).toLong(), it); varint(v.size.toLong(), it); it.write(v) }.toByteArray()
        val extra = ByteArrayOutputStream()
        for ((k, v) in meta) extra.write(field(14, field(1, k.toByteArray()) + field(2, v.toByteArray())))
        java.io.FileOutputStream(File(dir, "bn.onnx"), true).use { it.write(extra.toByteArray()) }
    }

    // ---------- running the engine ----------

    private val processes = ConcurrentHashMap<String, Process>()
    fun cancel(id: String) { processes.remove(id)?.destroy() }

    private fun run(ctx: Context, id: String, exe: String, args: List<String>) {
        val p = ProcessBuilder(listOf(lib(ctx, exe).absolutePath) + args).redirectErrorStream(true).apply {
            environment()["LD_LIBRARY_PATH"] = ctx.applicationInfo.nativeLibraryDir
            environment()["TMPDIR"] = ctx.cacheDir.absolutePath
        }.start()
        processes[id] = p
        val tail = ArrayDeque<String>()
        try {
            p.inputStream.bufferedReader().forEachLine { tail.addLast(it); if (tail.size > 10) tail.removeFirst() }
            val code = p.waitFor()
            if (code == 137 || code == 143) throw Exception("Cancelled")
            if (code != 0) {
                android.util.Log.w("AnySave", "voice: $exe failed ($code): ${tail.joinToString(" | ")}")
                throw Exception("The voice engine stopped with an error. Try again, or try another voice.")
            }
        } finally {
            processes.remove(id)
        }
    }

    fun threads() = Runtime.getRuntime().availableProcessors().coerceIn(1, 4)

    private fun ttsArgs(ctx: Context, v: TtsVoice, speed: Double): List<String> {
        val d = packDir(ctx, v.pack)
        fun k(n: String) = File(d, n).absolutePath
        val ls = "%.3f".format(java.util.Locale.ROOT, 1 / speed)
        val a = mutableListOf("--num-threads=${threads()}", "--sid=${v.sid}")
        when (v.pack) {
            "kitten" -> a += listOf("--kitten-model=${k("model.onnx")}", "--kitten-voices=${k("voices.bin")}", "--kitten-tokens=${k("tokens.txt")}",
                "--kitten-data-dir=${k("espeak-ng-data")}", "--kitten-length-scale=$ls")
            "kokoro" -> {
                a += listOf("--kokoro-model=${k("model.int8.onnx")}", "--kokoro-voices=${k("voices.bin")}", "--kokoro-tokens=${k("tokens.txt")}",
                    "--kokoro-data-dir=${k("espeak-ng-data")}", "--kokoro-length-scale=$ls")
                a += if (v.code == "zh") listOf("--kokoro-lexicon=${k("lexicon-us-en.txt")},${k("lexicon-zh.txt")}",
                    "--tts-rule-fsts=${k("date-zh.fst")},${k("phone-zh.fst")},${k("number-zh.fst")}") else listOf("--kokoro-lang=${v.code}")
            }
            "bangla" -> a += listOf("--vits-model=${k("bn.onnx")}", "--vits-tokens=${k("tokens.txt")}", "--vits-data-dir=${k("espeak-ng-data")}",
                "--vits-noise-scale=0.667", "--vits-noise-scale-w=0.8", "--vits-length-scale=$ls")
            "pocket" -> a += listOf("--pocket-lm-flow=${k("lm_flow.int8.onnx")}", "--pocket-lm-main=${k("lm_main.int8.onnx")}",
                "--pocket-encoder=${k("encoder.onnx")}", "--pocket-decoder=${k("decoder.int8.onnx")}",
                "--pocket-text-conditioner=${k("text_conditioner.onnx")}", "--pocket-vocab-json=${k("vocab.json")}",
                "--pocket-token-scores-json=${k("token_scores.json")}", "--reference-audio=${v.ref!!.absolutePath}",
                "--speed=${"%.3f".format(java.util.Locale.ROOT, speed)}")
            else -> throw Exception("Unknown voice.")
        }
        return a
    }

    data class Piece(val text: String, val pause: Double)

    private val sentenceEnd = Regex("""([.!?;:।॥。！？]+["'”’)\]]*)\s+|([。！？।])""")

    /** Text in pieces the engine reads one at a time, with the pause after each. */
    fun splitText(text: String, max: Int = 600): List<Piece> {
        val out = mutableListOf<Piece>()
        for (p in text.replace("\r\n", "\n").replace('\r', '\n').split(Regex("\n\\s*\n+"))) {
            val para = p.replace('\n', ' ').trim().replace(Regex("\\s+"), " ")
            if (para.isEmpty()) continue
            val sentences = mutableListOf<String>()
            var last = 0
            for (m in sentenceEnd.findAll(para)) { sentences += para.substring(last, m.range.last + 1).trim(); last = m.range.last + 1 }
            para.substring(last).trim().takeIf { it.isNotEmpty() }?.let { sentences += it }
            val cur = StringBuilder()
            fun flush(pause: Double) { cur.toString().trim().takeIf { it.isNotEmpty() }?.let { out += Piece(it, pause) }; cur.clear() }
            for (s0 in sentences) {
                var s = s0
                while (s.length > max) { // one very long "sentence": cut at a space or comma
                    var cut = s.substring(0, max).indexOfLast { it == ' ' || it == ',' || it == '،' || it == '、' || it == '，' }
                    cut = if (cut >= max / 3) cut + 1 else max
                    if (cur.isNotEmpty()) flush(0.25)
                    out += Piece(s.substring(0, cut).trim(), 0.15)
                    s = s.substring(cut).trim()
                }
                if (cur.isNotEmpty() && cur.length + s.length > max) flush(0.25)
                if (cur.isNotEmpty()) cur.append(' ')
                cur.append(s)
            }
            flush(0.6)
        }
        return out.filter { it.text.isNotBlank() }
    }

    /** Reads [text] aloud into a WAV (mono, 16-bit). */
    fun speak(ctx: Context, id: String, work: File, text: String, v: TtsVoice, speed: Double, onProgress: (Float) -> Unit): File {
        val pieces = splitText(text)
        if (pieces.isEmpty()) throw ConvertError("Type or paste some text first.")
        val sp = if (speed in 0.5..2.0) speed else 1.0
        val base = ttsArgs(ctx, v, sp)
        val out = File(work, "speech.wav")
        val w = WavWriter(out)
        val total = pieces.sumOf { it.text.length }.coerceAtLeast(1)
        var done = 0
        try {
            pieces.forEachIndexed { i, p ->
                ConvertTools.check(id)
                val t = p.text.trimStart('-', ' ')
                if (t.isEmpty()) return@forEachIndexed
                val piece = File(work, "piece$i.wav")
                // The voice copier tends to swallow its first word; a lead-in pause ("…") keeps it.
                val said = if (v.pack == "pocket") "... $t" else t
                run(ctx, id, "libsherpa_tts.so", base + listOf("--output-filename=${piece.absolutePath}", said))
                val (rate, pcm) = readWav(piece) ?: throw ConvertError("The voice engine made no sound for: ${t.take(40)}")
                piece.delete()
                w.add(rate, pcm)
                w.silence((p.pause * rate).toInt())
                done += p.text.length
                onProgress(done * 100f / total)
            }
        } finally { w.close() }
        if (w.rate == 0) throw ConvertError("There was nothing to read aloud.")
        return out
    }

    /** Clean-up strength → pack and sample rate ("" pack = FFmpeg only). */
    fun cleanMode(strength: String) = when (strength) { "light" -> "clean-light" to 16000; "basic" -> "" to 48000; else -> "clean-strong" to 48000 }

    /** FFmpeg filters after the noise is gone. Basic mode removes noise here too. */
    fun voiceFilters(strength: String, level: Boolean): List<String> = buildList {
        add("highpass=f=70")
        if (strength == "basic") add("afftdn=nr=20:nf=-30")
        if (level) { add("loudnorm=I=-16:TP=-1.5:LRA=11"); add("aresample=48000") }
    }

    /**
     * Removes the noise from [input]'s sound (from [start] for [length] seconds; 0 = all) into a mono
     * WAV. Long recordings go through in 5-minute pieces (the noise remover reads a whole file at once).
     */
    fun denoise(ctx: Context, id: String, input: File, work: File, strength: String, start: Double, length: Double, onProgress: (Float) -> Unit): File {
        val (pack, rate) = cleanMode(strength)
        val pieces = File(work, "denoise-pieces").apply { deleteRecursively(); mkdirs() }
        val out = File(work, "clean.wav")
        val a = mutableListOf<String>()
        if (start > 0) a += listOf("-ss", "%.3f".format(java.util.Locale.ROOT, start))
        a += listOf("-i", input.absolutePath)
        if (length > 0) a += listOf("-t", "%.3f".format(java.util.Locale.ROOT, length))
        a += listOf("-vn", "-map", "0:a:0", "-ac", "1", "-ar", "$rate", "-c:a", "pcm_s16le")
        if (pack.isEmpty()) {
            Converter.ffmpeg(ctx, id, a + out.absolutePath, length.takeIf { it > 0 } ?: Converter.durationSec(input)) { onProgress(it) }
            return out
        }
        Converter.ffmpeg(ctx, id, a + listOf("-f", "segment", "-segment_time", "300", File(pieces, "p%04d.wav").absolutePath),
            length.takeIf { it > 0 } ?: Converter.durationSec(input)) { onProgress(it * 0.1f) }
        val names = pieces.listFiles { f -> f.name.endsWith(".wav") }.orEmpty().sortedBy { it.name }
        if (names.isEmpty()) throw ConvertError("This file has no sound to clean.")
        val model = File(packDir(ctx, pack), pack(pack).files[0].name!!).absolutePath
        val flag = if (pack == "clean-light") "--speech-denoiser-gtcrn-model=" else "--speech-denoiser-dpdfnet-model="
        val w = WavWriter(out)
        try {
            names.forEachIndexed { i, p ->
                ConvertTools.check(id)
                val c = File(p.path + ".clean.wav")
                run(ctx, id, "libsherpa_denoise.so", listOf(flag + model, "--num-threads=2", "--input-wav=${p.absolutePath}", "--output-wav=${c.absolutePath}"))
                val (r, pcm) = readWav(c) ?: throw ConvertError("The noise remover made no sound.")
                w.add(r, pcm)
                p.delete(); c.delete()
                onProgress(10 + (i + 1) * 90f / names.size)
            }
        } finally { w.close() }
        return out
    }

    // ---------- copied voices ----------

    const val CONSENT = "This is my own voice, or I have the person's clear permission to copy it."
    private const val CLONE_MIN = 5.0
    private const val CLONE_MAX = 20.0

    private fun mineDir(ctx: Context) = File(root(ctx), "mine").apply { mkdirs() }

    fun myVoices(ctx: Context): List<TtsVoice> = mineDir(ctx).listFiles().orEmpty().mapNotNull { d ->
        val j = runCatching { JSONObject(File(d, "voice.json").readText()) }.getOrNull() ?: return@mapNotNull null
        if (!File(d, "voice.wav").exists()) return@mapNotNull null
        j.optString("made") to TtsVoice("my-${d.name}", j.optString("name", "My voice"), "Your voices", "pocket", "en", ref = File(d, "voice.wav"))
    }.sortedBy { it.first }.map { it.second }

    /** Makes a voice from about 10–20 seconds of one person talking. Returns its id. */
    fun copyVoice(ctx: Context, id: String, input: File, work: File, name0: String): String {
        var name = name0.trim().replace(Regex("\\s+"), " ").ifEmpty { "My voice" }.take(40)
        val taken = myVoices(ctx).map { it.name.lowercase() }.toSet()
        val base = name
        var n = 2
        while (name.lowercase() in taken) name = "$base ${n++}"
        if (!Converter.hasAudio(input)) throw ConvertError("${input.name} has no sound.")
        // Clean the sample when the strong clean-up is here: background noise gets copied too otherwise.
        var src = input
        if (ready(ctx, "clean-strong")) runCatching { src = denoise(ctx, id, input, work, "strong", 0.0, CLONE_MAX + 1) {} }
            .onFailure { if (it.message == "Cancelled") throw it }
        val vid = java.lang.Long.toHexString(System.nanoTime())
        val tmp = File(mineDir(ctx), "$vid.part").apply { mkdirs() }
        val wav = File(tmp, "voice.wav")
        try {
            Converter.ffmpeg(ctx, id, listOf("-i", src.absolutePath, "-vn", "-map", "0:a:0", "-ac", "1", "-ar", "24000",
                "-af", "silenceremove=start_periods=1:start_threshold=-45dB,highpass=f=70,atrim=0:${CLONE_MAX.toInt()},loudnorm=I=-18:TP=-2,aresample=24000",
                "-c:a", "pcm_s16le", wav.absolutePath), CLONE_MAX) {}
            val secs = readWav(wav)?.let { (r, pcm) -> pcm.size / 2.0 / r } ?: 0.0
            if (secs < CLONE_MIN) {
                val dur = Converter.durationSec(input) ?: 0.0
                throw ConvertError(if (dur >= CLONE_MIN)
                    "AnySave heard less than 5 seconds of clear talking in it. Record again somewhere quiet, close to the phone, and talk for 10–20 seconds."
                else "That's too short: AnySave needs at least 5 seconds of someone talking (10–20 is best).")
            }
            File(tmp, "voice.json").writeText(JSONObject().put("name", name).put("made", java.time.Instant.now().toString())
                .put("consent", CONSENT).put("secs", secs).toString(2))
            if (!tmp.renameTo(File(mineDir(ctx), vid))) throw ConvertError("Couldn't save the voice.")
            changed.update { it + 1 }
            return "my-$vid"
        } catch (e: Throwable) {
            tmp.deleteRecursively()
            throw e
        }
    }

    fun forgetVoice(ctx: Context, id: String) {
        val v = id.removePrefix("my-")
        if (v.isEmpty() || v.any { it == '/' || it == '\\' || it == '.' }) return
        File(mineDir(ctx), v).deleteRecursively()
        changed.update { it + 1 }
    }

    // ---------- WAV ----------

    /** A 16-bit mono WAV's sample rate and samples. */
    fun readWav(f: File): Pair<Int, ByteArray>? = runCatching {
        val b = f.readBytes()
        if (b.size < 12 || String(b, 0, 4) != "RIFF" || String(b, 8, 4) != "WAVE") return null
        fun u16(i: Int) = (b[i].toInt() and 0xff) or ((b[i + 1].toInt() and 0xff) shl 8)
        fun u32(i: Int) = u16(i) or (u16(i + 2) shl 16)
        var rate = 0; var bits = 0; var ch = 0
        var i = 12
        while (i + 8 <= b.size) {
            val id = String(b, i, 4)
            val n = u32(i + 4)
            if (id == "fmt ") { ch = u16(i + 10); rate = u32(i + 12); bits = u16(i + 22) }
            if (id == "data") {
                if (rate == 0 || bits != 16 || ch != 1) return null
                return rate to b.copyOfRange(i + 8, minOf(b.size, i + 8 + n))
            }
            i += 8 + n + (n and 1)
        }
        null
    }.getOrNull()

    /** Writes a mono 16-bit WAV piece by piece (long recordings never sit in memory whole). */
    class WavWriter(private val f: File) {
        private val raf = RandomAccessFile(f, "rw").apply { setLength(0); write(ByteArray(44)) }
        var rate = 0; private set
        private var bytes = 0L
        fun add(r: Int, pcm: ByteArray) {
            if (rate != 0 && r != rate) throw ConvertError("The voice engine changed its sound format.")
            rate = r; raf.write(pcm); bytes += pcm.size
        }
        fun silence(samples: Int) { if (samples > 0) { raf.write(ByteArray(samples * 2)); bytes += samples * 2L } }
        fun close() {
            val h = java.nio.ByteBuffer.allocate(44).order(java.nio.ByteOrder.LITTLE_ENDIAN)
            h.put("RIFF".toByteArray()).putInt((36 + bytes).toInt()).put("WAVEfmt ".toByteArray()).putInt(16).putShort(1).putShort(1)
                .putInt(rate).putInt(rate * 2).putShort(2).putShort(16).put("data".toByteArray()).putInt(bytes.toInt())
            raf.seek(0); raf.write(h.array()); raf.close()
        }
    }
}
