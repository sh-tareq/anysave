package com.anysave.app

import android.content.Context
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.pdf.PdfRenderer
import android.os.ParcelFileDescriptor
import com.tom_roush.pdfbox.android.PDFBoxResourceLoader
import com.tom_roush.pdfbox.cos.COSBase
import com.tom_roush.pdfbox.io.MemoryUsageSetting
import com.tom_roush.pdfbox.multipdf.PDFMergerUtility
import com.tom_roush.pdfbox.pdmodel.PDDocument
import com.tom_roush.pdfbox.pdmodel.encryption.AccessPermission
import com.tom_roush.pdfbox.pdmodel.encryption.InvalidPasswordException
import com.tom_roush.pdfbox.pdmodel.encryption.StandardProtectionPolicy
import com.tom_roush.pdfbox.pdmodel.graphics.image.JPEGFactory
import com.tom_roush.pdfbox.pdmodel.graphics.image.PDImageXObject
import com.tom_roush.pdfbox.text.PDFTextStripper
import java.io.File
import kotlin.math.max

/** PDF tools. Pages to pictures uses Android's own PDF renderer; the rest uses PDFBox (Apache 2.0). */
object PdfTools {
    @Volatile private var ready = false
    private lateinit var appCtx: Context

    fun setup(ctx: Context) { appCtx = ctx.applicationContext }
    fun init() {
        if (!ready) synchronized(this) { if (!ready) { PDFBoxResourceLoader.init(appCtx); ready = true } }
    }

    /** Opens a PDF. A locked one needs [password]; one that only limits printing/copying opens without it. */
    fun open(file: File, password: String? = null): PDDocument {
        init()
        return try {
            PDDocument.load(file, password ?: "", MemoryUsageSetting.setupTempFileOnly())
        } catch (e: InvalidPasswordException) {
            throw ConvertError(if (password.isNullOrEmpty()) "${file.name} is locked with a password. Use \"Remove a password\" first."
                else "That password is wrong.")
        } catch (e: java.io.IOException) {
            throw ConvertError("Couldn't read ${file.name}. It may be damaged or not really a PDF.")
        }.also { it.isAllSecurityToBeRemoved = true }
    }

    fun pageCount(file: File): Int? = runCatching { open(file).use { it.numberOfPages } }.getOrNull()

    fun toImages(id: String, file: File, outDir: File, spec: ConvertSpec, onProgress: (Float) -> Unit) {
        val dpi = spec.opt("dpi")?.toIntOrNull() ?: 150
        val format = spec.format
        val pfd = ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY)
        val renderer = try { PdfRenderer(pfd) } catch (e: SecurityException) {
            pfd.close(); throw ConvertError("This PDF is locked with a password. Use \"Remove a password\" first.")
        } catch (e: Exception) {
            pfd.close(); throw ConvertError("Couldn't read ${file.name}. It may be damaged or not really a PDF.")
        }
        renderer.use { r ->
            val digits = r.pageCount.toString().length.coerceAtLeast(2)
            for (i in 0 until r.pageCount) {
                ConvertTools.check(id)
                r.openPage(i).use { page ->
                    var w = page.width * dpi / 72; var h = page.height * dpi / 72
                    val big = max(w, h)
                    if (big > 6000) { w = w * 6000 / big; h = h * 6000 / big }
                    val bmp = Bitmap.createBitmap(w.coerceAtLeast(1), h.coerceAtLeast(1), Bitmap.Config.ARGB_8888)
                    Canvas(bmp).drawColor(Color.WHITE)
                    page.render(bmp, null, null, PdfRenderer.Page.RENDER_MODE_FOR_PRINT)
                    File(outDir, "${outDir.name.removeSuffix(" pages")} page ${(i + 1).toString().padStart(digits, '0')}.$format").outputStream().use {
                        bmp.compress(if (format == "png") Bitmap.CompressFormat.PNG else Bitmap.CompressFormat.JPEG, 90, it)
                    }
                    bmp.recycle()
                }
                onProgress((i + 1) * 100f / r.pageCount)
            }
        }
    }

    fun merge(ctx: Context, id: String, inputs: List<File>, out: File, onProgress: (Float) -> Unit) {
        init()
        PDDocument().use { result ->
            val merger = PDFMergerUtility()
            val opened = mutableListOf<PDDocument>()
            try {
                inputs.forEachIndexed { i, f ->
                    ConvertTools.check(id)
                    val d = open(f).also { opened += it }
                    merger.appendDocument(result, d)
                    onProgress((i + 1) * 90f / inputs.size)
                }
                result.save(out)
            } finally { opened.forEach { runCatching { it.close() } } }
        }
        onProgress(100f)
    }

    /** "1-3, 5, 8-" -> pages 1,2,3,5,8..last (1-based). */
    fun parseRanges(text: String, count: Int): List<Int> {
        val pages = mutableListOf<Int>()
        for (part in text.split(',', ';', ' ').map { it.trim() }.filter { it.isNotEmpty() }) {
            val m = Regex("""^(\d*)\s*[-–]\s*(\d*)$""").find(part)
            if (m != null) {
                val a = m.groupValues[1].toIntOrNull() ?: 1
                val b = m.groupValues[2].toIntOrNull() ?: count
                (a..b).forEach { if (it in 1..count) pages += it }
            } else part.toIntOrNull()?.let { if (it in 1..count) pages += it }
                ?: throw ConvertError("\"$part\" isn't a page number. Write pages like 1-3, 5, 8-")
        }
        if (pages.isEmpty()) throw ConvertError("Those pages aren't in this PDF (it has $count pages).")
        return pages.distinct()
    }

    /** [1,2,3,5,8,9] -> "1-3, 5, 8-9" */
    fun rangesText(pages: Collection<Int>): String {
        val sorted = pages.toSortedSet().toList()
        val parts = mutableListOf<String>()
        var i = 0
        while (i < sorted.size) {
            var j = i
            while (j + 1 < sorted.size && sorted[j + 1] == sorted[j] + 1) j++
            parts += if (j == i) "${sorted[i]}" else "${sorted[i]}-${sorted[j]}"
            i = j + 1
        }
        return parts.joinToString(", ")
    }

    /** Keeps the chosen pages in one PDF, or with mode "split" saves every page as its own PDF. */
    fun pages(ctx: Context, id: String, file: File, work: File, spec: ConvertSpec, onProgress: (Float) -> Unit): File {
        val outRoot = File(work, "out").apply { mkdirs() }
        open(file).use { src ->
            val n = src.numberOfPages
            val wanted = spec.opt("pages")?.let { parseRanges(it, n) } ?: (1..n).toList()
            if (spec.opt("mode") == "split") {
                val dir = File(outRoot, "${file.nameWithoutExtension} pages").apply { mkdirs() }
                val digits = n.toString().length.coerceAtLeast(2)
                wanted.forEachIndexed { i, p ->
                    ConvertTools.check(id)
                    PDDocument().use { d ->
                        d.importPage(src.getPage(p - 1))
                        d.save(File(dir, "${file.nameWithoutExtension} page ${p.toString().padStart(digits, '0')}.pdf"))
                    }
                    onProgress((i + 1) * 100f / wanted.size)
                }
                return dir
            }
            val out = File(outRoot, "${file.nameWithoutExtension} (pages ${spec.opt("pages") ?: "all"}).pdf".let { ConvertTools.safe(it) })
            PDDocument().use { d ->
                wanted.forEach { p -> d.importPage(src.getPage(p - 1)) }
                d.save(out)
            }
            onProgress(100f)
            return out
        }
    }

    /** Re-saves every picture inside as a smaller JPEG. Text and drawings are left alone. */
    fun compress(ctx: Context, id: String, file: File, out: File, spec: ConvertSpec, onProgress: (Float) -> Unit) {
        val (maxSide, q) = when (spec.opt("level")) { "strong" -> 1000 to 0.5f; "light" -> 2000 to 0.8f; else -> 1500 to 0.65f }
        open(file).use { doc ->
            val done = HashMap<COSBase, PDImageXObject>()
            val pages = doc.pages.toList()
            pages.forEachIndexed { i, page ->
                ConvertTools.check(id)
                val res = page.resources ?: return@forEachIndexed
                for (name in res.xObjectNames.toList()) {
                    val x = runCatching { res.getXObject(name) }.getOrNull() as? PDImageXObject ?: continue
                    val key = x.cosObject
                    done[key]?.let { res.put(name, it); continue }
                    if (x.softMask != null || x.isStencil) continue // transparency: leave as it is
                    val big = max(x.width, x.height)
                    if (big <= maxSide && x.suffix == "jpg" && spec.opt("level") == "light") continue
                    val bmp = runCatching { x.image }.getOrNull() ?: continue
                    val scaled = if (big > maxSide) Bitmap.createScaledBitmap(bmp, bmp.width * maxSide / big, bmp.height * maxSide / big, true) else bmp
                    val opaque = if (scaled.hasAlpha()) Bitmap.createBitmap(scaled.width, scaled.height, Bitmap.Config.ARGB_8888).also {
                        Canvas(it).apply { drawColor(Color.WHITE); drawBitmap(scaled, 0f, 0f, null) }
                    } else scaled
                    val small = JPEGFactory.createFromImage(doc, opaque, q)
                    res.put(name, small)
                    done[key] = small
                }
                onProgress((i + 1) * 90f / pages.size)
            }
            doc.save(out)
        }
        if (out.length() >= file.length()) {
            // Nothing to shrink (a text-only PDF, or already small): keep the original rather than a bigger copy.
            file.copyTo(out, overwrite = true)
        }
        onProgress(100f)
    }

    fun rotate(ctx: Context, file: File, out: File, spec: ConvertSpec) {
        val deg = spec.opt("angle")?.toIntOrNull() ?: 90
        open(file).use { doc ->
            doc.pages.forEach { it.rotation = ((it.rotation + deg) % 360 + 360) % 360 }
            doc.save(out)
        }
    }

    fun protect(ctx: Context, file: File, out: File, spec: ConvertSpec) {
        val pw = spec.opt("password") ?: throw ConvertError("Type a password first.")
        open(file).use { doc ->
            val policy = StandardProtectionPolicy(pw, pw, AccessPermission()).apply { encryptionKeyLength = 256 } // AES-256
            doc.protect(policy)
            doc.save(out)
        }
    }

    fun unlock(ctx: Context, file: File, out: File, spec: ConvertSpec) {
        open(file, spec.opt("password")).use { it.save(out) }
    }

    fun text(file: File): String = open(file).use { PDFTextStripper().getText(it) }
}
