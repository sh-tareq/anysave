package com.anysave.app

import android.content.Context
import android.os.Build
import java.io.File
import java.io.RandomAccessFile
import java.net.URL
import java.security.MessageDigest
import java.util.concurrent.ConcurrentHashMap

/**
 * Speech to text on the phone, offline: whisper.cpp's command-line program (libwhisper.so, built by
 * scripts/build-whisper-android.sh) with the same models as the desktop. The model is downloaded once
 * (checked against a fixed SHA-256) into the app's own storage.
 */
object Speech {
    data class Model(val name: String, val sha256: String, val size: Long, val label: String, val speed: Double)

    private const val BASE = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/"
    /** speed = minutes of work per minute of sound on a 2018 phone (measured on a Mi 8); updated from real runs. */
    val STANDARD = Model("ggml-small-q8_0.bin", "49c8fb02b65e6049d5fa6c04f81f53b867b5ec9540406812c643f177317f779f", 264464607, "standard speech model", 1.7)
    val BEST = Model("ggml-large-v3-turbo-q5_0.bin", "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2", 574041195, "accurate speech model", 8.0)

    val LANGS = listOf(
        "en" to "English", "bn" to "বাংলা (Bangla)", "hi" to "हिन्दी (Hindi)", "ur" to "اردو (Urdu)", "ar" to "العربية (Arabic)",
        "es" to "Español", "fr" to "Français", "de" to "Deutsch", "pt" to "Português", "ru" to "Русский", "tr" to "Türkçe",
        "id" to "Indonesia", "zh" to "中文 (Chinese)", "ja" to "日本語 (Japanese)", "ko" to "한국어 (Korean)", "auto" to "Auto-detect",
    )

    private val processes = ConcurrentHashMap<String, Process>()

    fun modelsDir(ctx: Context) = File(ctx.noBackupFilesDir, "models").apply { mkdirs() }
    fun modelFile(ctx: Context, m: Model) = File(modelsDir(ctx), m.name)
    fun has(ctx: Context, m: Model) = modelFile(ctx, m).length() == m.size

    /** The model for [lang]. Bangla needs the accurate one (the standard one can't do it at all). */
    fun modelFor(ctx: Context, lang: String, accurate: Boolean): Model = when {
        lang == "bn" || accurate -> BEST
        // The accurate model works for English too: don't download a second one if it's already here.
        !has(ctx, STANDARD) && has(ctx, BEST) -> BEST
        else -> STANDARD
    }

    private fun engineDir(ctx: Context) = File(ctx.applicationInfo.nativeLibraryDir)
    fun available(ctx: Context) = File(engineDir(ctx), "libwhisper.so").exists()

    /** The faster build needs the CPU's "dot product" instructions (newer phones). */
    private fun engine(ctx: Context): File {
        val dot = File(engineDir(ctx), "libwhisper_dot.so")
        val prefs = ctx.getSharedPreferences("speech", Context.MODE_PRIVATE)
        if (dot.exists() && !prefs.getBoolean("dotBroken", false) && cpuHas("asimddp")) return dot
        return File(engineDir(ctx), "libwhisper.so")
    }

    private fun cpuHas(flag: String) = runCatching {
        File("/proc/cpuinfo").readLines().any { it.startsWith("Features") && flag in it.substringAfter(':').split(' ') }
    }.getOrDefault(false)

    fun threads() = Runtime.getRuntime().availableProcessors().coerceIn(1, 4) // more made it slower in tests (big.LITTLE)

    /** Minutes of work per minute of sound for [m] on this phone (measured after each run). */
    fun speed(ctx: Context, m: Model): Double =
        ctx.getSharedPreferences("speech", Context.MODE_PRIVATE).getFloat("speed:${m.name}", m.speed.toFloat()).toDouble()

    private fun rememberSpeed(ctx: Context, m: Model, secs: Double, soundSecs: Double) {
        if (soundSecs < 20) return
        val prefs = ctx.getSharedPreferences("speech", Context.MODE_PRIVATE)
        val old = speed(ctx, m)
        val now = (secs / soundSecs).coerceIn(0.05, 50.0)
        prefs.edit().putFloat("speed:${m.name}", (old * 0.4 + now * 0.6).toFloat()).apply()
    }

    /** Downloads [m] (continuing a cut-off download) and checks it. */
    fun download(ctx: Context, id: String, m: Model, onProgress: (Float) -> Unit) {
        val out = modelFile(ctx, m)
        if (out.length() == m.size) return
        val part = File(out.path + ".part")
        var tries = 0
        while (part.length() < m.size) {
            ConvertTools.check(id)
            try {
                val have = part.length()
                val c = Net.open(URL(BASE + m.name))
                c.instanceFollowRedirects = true
                c.connectTimeout = 20000; c.readTimeout = 30000
                c.setRequestProperty("User-Agent", "AnySave")
                if (have > 0) c.setRequestProperty("Range", "bytes=$have-")
                val code = c.responseCode
                if (code !in 200..299) throw Exception("The model download failed (error $code).")
                val append = have > 0 && code == 206
                RandomAccessFile(part, "rw").use { f ->
                    if (!append) f.setLength(0)
                    f.seek(f.length())
                    val buf = ByteArray(1 shl 16)
                    c.inputStream.use { inp ->
                        var last = 0L
                        while (true) {
                            val n = inp.read(buf)
                            if (n < 0) break
                            f.write(buf, 0, n)
                            val done = f.length()
                            if (done - last > 1_000_000) { last = done; ConvertTools.check(id); onProgress(done * 100f / m.size) }
                        }
                    }
                }
                c.disconnect()
                if (part.length() > m.size) part.delete()
            } catch (e: Exception) {
                if (e.message == "Cancelled") throw e
                if (++tries >= 5) throw Exception("Couldn't download the speech model. Check the internet connection and try again.")
                Thread.sleep(3000L * tries)
            }
        }
        val md = MessageDigest.getInstance("SHA-256")
        part.inputStream().use { i -> val b = ByteArray(1 shl 16); while (true) { val n = i.read(b); if (n < 0) break; md.update(b, 0, n) } }
        val sum = md.digest().joinToString("") { "%02x".format(it) }
        if (sum != m.sha256) {
            part.delete()
            throw Exception("The downloaded speech model was damaged. Try again.")
        }
        if (!part.renameTo(out)) throw Exception("Couldn't save the speech model.")
    }

    /**
     * Turns a 16 kHz mono WAV into lines with word times. [soundSecs] is how long the sound is (for
     * the speed estimate). Writes whisper's JSON next to [wav].
     */
    fun transcribe(ctx: Context, id: String, wav: File, m: Model, lang: String, soundSecs: Double, onProgress: (Float) -> Unit): List<CapCue> {
        val outBase = File(wav.parentFile, "speech")
        fun args(exe: File) = listOf(exe.absolutePath, "-m", modelFile(ctx, m).absolutePath, "-l", lang, "-ojf", "-of", outBase.absolutePath,
            "-t", threads().toString(), "-pp", "-ml", "60", "-sow", "-f", wav.absolutePath)
        val started = System.currentTimeMillis()
        var exe = engine(ctx)
        var code = run(ctx, id, args(exe), onProgress)
        if (code == 132 && exe.name != "libwhisper.so") { // SIGILL: the CPU lacks an instruction after all
            ctx.getSharedPreferences("speech", Context.MODE_PRIVATE).edit().putBoolean("dotBroken", true).apply()
            exe = File(engineDir(ctx), "libwhisper.so")
            code = run(ctx, id, args(exe), onProgress)
        }
        ConvertTools.check(id)
        if (code == 137 || code == 143) throw Exception("Cancelled")
        val json = File(outBase.path + ".json")
        if (code != 0 || !json.exists()) throw Exception("The speech engine stopped (error $code). " +
            if (Build.SUPPORTED_64_BIT_ABIS.isEmpty()) "This phone may be too old for it." else "Try again, or try a shorter part.")
        rememberSpeed(ctx, m, (System.currentTimeMillis() - started) / 1000.0, soundSecs)
        return Captions.fromWhisperJson(json)
    }

    private fun run(ctx: Context, id: String, cmd: List<String>, onProgress: (Float) -> Unit): Int {
        val p = ProcessBuilder(cmd).redirectErrorStream(true).apply { environment()["TMPDIR"] = ctx.cacheDir.absolutePath }.start()
        processes[id] = p
        val re = Regex("""progress\s*=\s*(\d+)%""")
        val tail = ArrayDeque<String>()
        try {
            p.inputStream.bufferedReader().forEachLine { line ->
                re.find(line)?.let { onProgress(it.groupValues[1].toFloat()) }
                tail.addLast(line); if (tail.size > 12) tail.removeFirst()
            }
            val code = p.waitFor()
            if (code != 0) android.util.Log.w("AnySave", "whisper failed ($code): ${tail.joinToString(" | ")}")
            return code
        } finally {
            processes.remove(id)
        }
    }

    fun cancel(id: String) { processes.remove(id)?.destroy() }
}
