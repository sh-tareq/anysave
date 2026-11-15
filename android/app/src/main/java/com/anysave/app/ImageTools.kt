package com.anysave.app

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.ImageDecoder
import android.graphics.Matrix
import android.os.Build
import androidx.exifinterface.media.ExifInterface
import com.tom_roush.pdfbox.pdmodel.PDDocument
import com.tom_roush.pdfbox.pdmodel.PDPage
import com.tom_roush.pdfbox.pdmodel.PDPageContentStream
import com.tom_roush.pdfbox.pdmodel.common.PDRectangle
import com.tom_roush.pdfbox.pdmodel.graphics.image.JPEGFactory
import java.io.ByteArrayOutputStream
import java.io.File
import kotlin.math.max
import kotlin.math.roundToInt

/** Pictures: Android decodes and encodes JPG/PNG/WebP itself; FFmpeg does AVIF, GIF, BMP, TIFF and ICO. */
object ImageTools {
    /** Biggest side ever decoded, so a 200-megapixel photo can't run the phone out of memory. */
    private const val MAX_DECODE = 8192

    /** Opens a picture the right way up (camera rotation applied), no bigger than [maxSide]. */
    fun decode(ctx: Context, id: String, file: File, maxSide: Int?): Bitmap {
        val limit = (maxSide ?: MAX_DECODE).coerceAtMost(MAX_DECODE)
        val bmp = runCatching { platformDecode(file, limit) }.getOrNull()
            ?: run {
                // Android can't read it (TIFF, ICO, AVIF on older phones): let FFmpeg turn it into PNG first.
                val png = File(file.parentFile, "${file.nameWithoutExtension}.decoded.png")
                runCatching { Converter.image(ctx, id, file, png, 100) }
                runCatching { platformDecode(png, limit) }.getOrNull().also { png.delete() }
            }
            ?: throw ConvertError("Couldn't open ${file.name}. It may be damaged or a kind of picture AnySave can't read.")
        return bmp
    }

    private fun platformDecode(file: File, limit: Int): Bitmap? {
        if (Build.VERSION.SDK_INT >= 28) {
            return ImageDecoder.decodeBitmap(ImageDecoder.createSource(file)) { decoder, info, _ ->
                decoder.allocator = ImageDecoder.ALLOCATOR_SOFTWARE
                val w = info.size.width; val h = info.size.height
                val big = max(w, h)
                if (big > limit) {
                    val s = limit.toFloat() / big
                    decoder.setTargetSize((w * s).roundToInt().coerceAtLeast(1), (h * s).roundToInt().coerceAtLeast(1))
                }
            }
        }
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeFile(file.absolutePath, bounds)
        if (bounds.outWidth <= 0) return null
        var sample = 1
        while (max(bounds.outWidth, bounds.outHeight) / (sample * 2) >= limit) sample *= 2
        var bmp = BitmapFactory.decodeFile(file.absolutePath, BitmapFactory.Options().apply { inSampleSize = sample }) ?: return null
        val big = max(bmp.width, bmp.height)
        if (big > limit) bmp = Bitmap.createScaledBitmap(bmp, bmp.width * limit / big, bmp.height * limit / big, true)
        val deg = runCatching { ExifInterface(file.absolutePath).rotationDegrees }.getOrDefault(0)
        if (deg != 0) bmp = Bitmap.createBitmap(bmp, 0, 0, bmp.width, bmp.height, Matrix().apply { postRotate(deg.toFloat()) }, true)
        return bmp
    }

    private fun onWhite(bmp: Bitmap): Bitmap {
        if (!bmp.hasAlpha()) return bmp
        val out = Bitmap.createBitmap(bmp.width, bmp.height, Bitmap.Config.ARGB_8888)
        Canvas(out).apply { drawColor(Color.WHITE); drawBitmap(bmp, 0f, 0f, null) }
        return out
    }

    @Suppress("DEPRECATION")
    private fun compress(bmp: Bitmap, format: String, quality: Int): ByteArray {
        val bos = ByteArrayOutputStream()
        when (format) {
            "png" -> bmp.compress(Bitmap.CompressFormat.PNG, 100, bos)
            "webp" -> if (Build.VERSION.SDK_INT >= 30)
                bmp.compress(if (quality >= 100) Bitmap.CompressFormat.WEBP_LOSSLESS else Bitmap.CompressFormat.WEBP_LOSSY, quality, bos)
                else bmp.compress(Bitmap.CompressFormat.WEBP, quality, bos)
            else -> onWhite(bmp).compress(Bitmap.CompressFormat.JPEG, quality, bos)
        }
        return bos.toByteArray()
    }

    /** Writes [bmp] as [format]. With [targetKb], finds the best quality (and if needed a smaller size) that fits. */
    fun encode(ctx: Context, id: String, bmp: Bitmap, out: File, format: String, quality: Int, targetKb: Int?) {
        when (format) {
            "jpg", "webp" -> {
                if (targetKb == null) { out.writeBytes(compress(bmp, format, quality)); return }
                val limit = targetKb * 1024L
                var b = bmp
                repeat(6) {
                    var lo = 15; var hi = quality.coerceAtMost(95); var best: ByteArray? = null
                    while (lo <= hi) {
                        ConvertTools.check(id)
                        val q = (lo + hi) / 2
                        val bytes = compress(b, format, q)
                        if (bytes.size <= limit) { best = bytes; lo = q + 1 } else hi = q - 1
                    }
                    if (best != null) { out.writeBytes(best!!); return }
                    b = Bitmap.createScaledBitmap(b, (b.width * 0.8).toInt().coerceAtLeast(1), (b.height * 0.8).toInt().coerceAtLeast(1), true)
                }
                out.writeBytes(compress(b, format, 15))
            }
            "png" -> out.writeBytes(compress(bmp, "png", 100))
            else -> { // FFmpeg formats, from a lossless PNG
                val png = File(out.parentFile, "${out.nameWithoutExtension}.tmp.png")
                png.writeBytes(compress(if (format == "bmp") onWhite(bmp) else bmp, "png", 100))
                try { Converter.image(ctx, id, png, out, quality) } finally { png.delete() }
                if (!out.exists() || out.length() == 0L) throw ConvertError("Couldn't make a ${format.uppercase()} file.")
            }
        }
    }

    /** One picture becomes one file; several become a folder. */
    fun convert(ctx: Context, id: String, inputs: List<File>, work: File, spec: ConvertSpec, onProgress: (Float) -> Unit): File {
        val format = spec.format
        val ext = if (format == "tiff") "tif" else format
        val maxSide = spec.height
        val quality = spec.opt("quality")?.toIntOrNull() ?: 85
        val targetKb = spec.opt("targetKb")?.toIntOrNull()
        val outRoot = File(work, "out").apply { mkdirs() }
        val dir = if (inputs.size > 1) File(outRoot, "${inputs.first().nameWithoutExtension} and ${inputs.size - 1} more").apply { mkdirs() } else outRoot
        val used = mutableSetOf<String>()
        var last: File? = null
        inputs.forEachIndexed { i, f ->
            ConvertTools.check(id)
            val bmp = decode(ctx, id, f, maxSide)
            var name = "${f.nameWithoutExtension}.$ext"
            var n = 1
            while (!used.add(name.lowercase())) name = "${f.nameWithoutExtension} (${n++}).$ext"
            last = File(dir, name).also { encode(ctx, id, bmp, it, format, quality, targetKb) }
            bmp.recycle()
            onProgress((i + 1) * 100f / inputs.size)
        }
        return if (inputs.size > 1) dir else last!!
    }

    /** Pictures to a PDF with one page each. Photos are stored as JPEG, so the PDF stays a sensible size. */
    fun toPdf(id: String, inputs: List<File>, out: File, spec: ConvertSpec, onProgress: (Float) -> Unit) {
        PdfTools.init()
        val page = spec.opt("page") ?: "a4"
        val margin = if (spec.opt("margin") == "1") 28f else 0f
        val q = (spec.opt("quality")?.toIntOrNull() ?: 80) / 100f
        val maxSide = if (q >= 0.9f) 3508 else 2480 // A4 at 300 or ~210 dpi
        PDDocument().use { doc ->
            inputs.forEachIndexed { i, f ->
                ConvertTools.check(id)
                val bmp = onWhite(platformDecode(f, maxSide) ?: throw ConvertError("Couldn't open ${f.name}."))
                val img = JPEGFactory.createFromImage(doc, bmp, q)
                val landscape = bmp.width > bmp.height
                val box = when (page) {
                    "letter" -> if (landscape) PDRectangle(PDRectangle.LETTER.height, PDRectangle.LETTER.width) else PDRectangle.LETTER
                    "fit" -> PDRectangle(bmp.width * 72f / 150f + 2 * margin, bmp.height * 72f / 150f + 2 * margin)
                    else -> if (landscape) PDRectangle(PDRectangle.A4.height, PDRectangle.A4.width) else PDRectangle.A4
                }
                val p = PDPage(box)
                doc.addPage(p)
                val aw = box.width - 2 * margin; val ah = box.height - 2 * margin
                val s = minOf(aw / bmp.width, ah / bmp.height)
                val w = bmp.width * s; val h = bmp.height * s
                PDPageContentStream(doc, p).use { it.drawImage(img, margin + (aw - w) / 2, margin + (ah - h) / 2, w, h) }
                bmp.recycle()
                onProgress((i + 1) * 95f / inputs.size)
            }
            doc.save(out)
        }
        onProgress(100f)
    }
}
