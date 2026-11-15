package com.anysave.app

import android.content.Context
import java.io.File
import java.util.concurrent.ConcurrentHashMap

/** What kind of file something is, as far as the converter cares. */
enum class InKind { VIDEO, AUDIO, IMAGE, PDF, DOC, ARCHIVE, SUBS, DATA, OTHER }

/** One thing the Convert screen can do. */
data class ConvertTool(
    val id: String,
    val title: String,
    val short: String,
    val desc: String,
    val accepts: Set<InKind>,
    val minInputs: Int = 1,
    val maxInputs: Int = Int.MAX_VALUE,
    /** Output formats the user picks from (empty = the tool decides). */
    val formats: List<String> = emptyList(),
)

/**
 * Every converter on the phone, and the code that runs them. Video and audio go through FFmpeg or the
 * phone's video chip; pictures through Android itself; PDFs through PDFBox; archives through Commons
 * Compress; documents, subtitles and spreadsheets through small converters written here.
 */
object ConvertTools {
    const val MEDIA = "media"
    const val JOIN = "join"
    const val FRAMES = "frames"
    const val ADD_SUBS = "addsubs"
    const val IMAGE = "image"
    const val IMAGES_PDF = "images-pdf"
    const val PDF_IMAGES = "pdf-images"
    const val PDF_MERGE = "pdf-merge"
    const val PDF_PAGES = "pdf-pages"
    const val PDF_COMPRESS = "pdf-compress"
    const val PDF_ROTATE = "pdf-rotate"
    const val PDF_PROTECT = "pdf-protect"
    const val PDF_UNLOCK = "pdf-unlock"
    const val DOC = "doc"
    const val UNPACK = "unpack"
    const val PACK = "pack"
    const val SUBS = "subs"
    const val DATA = "data"

    private val media = setOf(InKind.VIDEO, InKind.AUDIO)
    private val anyKind = InKind.values().toSet()

    val all = listOf(
        ConvertTool(MEDIA, "Convert video or audio", "Convert",
            "Change format, size, shape or speed. Clean up the sound.", media,
            formats = ConvertSpec.VIDEO_FORMATS + ConvertSpec.AUDIO_FORMATS),
        ConvertTool(JOIN, "Join into one file", "Join", "Put several clips or songs one after another.", media, minInputs = 2),
        ConvertTool(FRAMES, "Video to pictures", "Pictures", "Save pictures from a video.", setOf(InKind.VIDEO), maxInputs = 1,
            formats = listOf("jpg", "png")),
        ConvertTool(ADD_SUBS, "Add subtitles to a video", "Subtitles", "Choose one video and one subtitle file.",
            setOf(InKind.VIDEO, InKind.SUBS), minInputs = 2, maxInputs = 2),
        ConvertTool(IMAGE, "Convert pictures", "Pictures", "Change format, size or file size. Photo info like location is removed.",
            setOf(InKind.IMAGE), formats = listOf("jpg", "png", "webp", "avif", "gif", "bmp", "tiff", "ico")),
        ConvertTool(IMAGES_PDF, "Pictures to PDF", "To PDF", "One page per picture, in the order chosen.", setOf(InKind.IMAGE)),
        ConvertTool(PDF_IMAGES, "PDF to pictures", "To pictures", "One picture per page.", setOf(InKind.PDF), maxInputs = 1,
            formats = listOf("jpg", "png")),
        ConvertTool(PDF_MERGE, "Merge PDFs", "Merge", "Join PDFs in the order chosen.", setOf(InKind.PDF), minInputs = 2),
        ConvertTool(PDF_PAGES, "Pick or split pages", "Pages", "Keep some pages, or save every page on its own.", setOf(InKind.PDF), maxInputs = 1),
        ConvertTool(PDF_COMPRESS, "Make a PDF smaller", "Smaller", "Shrinks the pictures inside. Text stays sharp.", setOf(InKind.PDF), maxInputs = 1),
        ConvertTool(PDF_ROTATE, "Rotate pages", "Rotate", "Turn every page.", setOf(InKind.PDF), maxInputs = 1),
        ConvertTool(PDF_PROTECT, "Add a password", "Password", "Nobody can open it without the password.", setOf(InKind.PDF), maxInputs = 1),
        ConvertTool(PDF_UNLOCK, "Remove a password", "Unlock", "Needs the password once.", setOf(InKind.PDF), maxInputs = 1),
        ConvertTool(DOC, "Convert a document", "Document",
            "Word, ODT, ebook, Markdown, web page, text or PDF to PDF, text, web page or ebook. Layout is kept simple.",
            setOf(InKind.DOC, InKind.PDF), maxInputs = 1, formats = listOf("pdf", "txt", "html", "epub")),
        ConvertTool(UNPACK, "Unpack", "Unpack", "ZIP, 7z, TAR, GZ, BZ2 and XZ.", setOf(InKind.ARCHIVE), maxInputs = 1),
        ConvertTool(PACK, "Pack into an archive", "Pack", "Put the files in one ZIP, 7z or TAR.GZ.", anyKind,
            formats = listOf("zip", "7z", "tar.gz")),
        ConvertTool(SUBS, "Convert subtitles", "Subtitles", "SRT, VTT, ASS or LRC. Fix subtitles that come too early or late.",
            setOf(InKind.SUBS), maxInputs = 1, formats = listOf("srt", "vtt", "ass", "lrc")),
        ConvertTool(DATA, "Convert a spreadsheet", "Spreadsheet", "CSV, TSV, JSON and Excel (first sheet).",
            setOf(InKind.DATA), maxInputs = 1, formats = listOf("xlsx", "csv", "tsv", "json")),
    )

    fun byId(id: String) = all.firstOrNull { it.id == id }

    private val videoExt = setOf("mp4", "mkv", "webm", "mov", "avi", "m4v", "3gp", "flv", "wmv", "ts", "mts", "m2ts", "mpg", "mpeg", "ogv")
    private val audioExt = setOf("mp3", "m4a", "aac", "wav", "flac", "ogg", "oga", "opus", "wma", "amr", "aiff", "aif", "alac", "mka")
    private val imageExt = setOf("jpg", "jpeg", "png", "gif", "webp", "heic", "heif", "avif", "bmp", "tif", "tiff", "ico")
    private val docExt = setOf("docx", "odt", "epub", "md", "markdown", "txt", "html", "htm", "xhtml")
    private val archiveExt = setOf("zip", "7z", "tar", "gz", "tgz", "bz2", "tbz2", "xz", "txz", "rar", "cbz")
    private val subsExt = setOf("srt", "vtt", "ass", "ssa", "lrc")
    private val dataExt = setOf("csv", "tsv", "json", "xlsx")

    fun kindOf(name: String, mime: String?): InKind {
        val ext = FileKinds.extOf(name)
        val m = mime.orEmpty()
        return when {
            ext in subsExt -> InKind.SUBS
            ext in dataExt -> InKind.DATA
            ext == "pdf" || m == "application/pdf" -> InKind.PDF
            ext in imageExt || m.startsWith("image/") -> InKind.IMAGE
            ext in audioExt || m.startsWith("audio/") -> InKind.AUDIO
            ext in videoExt || m.startsWith("video/") -> InKind.VIDEO
            // EPUB's type is "application/epub+zip", so check documents before the "zip" test.
            ext in docExt || m.contains("epub") -> InKind.DOC
            ext in archiveExt || m.contains("zip") || m.contains("x-7z") || m.contains("x-tar") -> InKind.ARCHIVE
            m.startsWith("text/") -> InKind.DOC
            else -> InKind.OTHER
        }
    }

    /** Tools that fit the chosen files, best first. */
    fun toolsFor(kinds: List<InKind>): List<ConvertTool> {
        if (kinds.isEmpty()) return emptyList()
        return all.filter { t ->
            kinds.size >= t.minInputs && kinds.size <= t.maxInputs && kinds.all { it in t.accepts } &&
                (t.id != ADD_SUBS || (kinds.count { it == InKind.VIDEO } == 1 && kinds.count { it == InKind.SUBS } == 1)) &&
                (t.id != JOIN || kinds.toSet().size == 1) // all videos or all audio
        }.sortedBy { if (it.id == PACK) 1 else 0 } // packing works for anything, so it goes last
    }

    // ---------- running ----------

    private val cancelled = ConcurrentHashMap.newKeySet<String>()
    fun cancel(id: String) { cancelled += id }
    /** Long loops call this so Cancel stops them. */
    fun check(id: String) { if (id in cancelled) throw Exception("Cancelled") }

    /**
     * Runs [spec] on [inputs] (local copies, with their real names) inside [work].
     * Returns the result: a file, or a folder when there are several.
     */
    fun run(ctx: Context, id: String, spec: ConvertSpec, inputs: List<File>, work: File, onProgress: (Float) -> Unit): File {
        cancelled.remove(id)
        try {
            val first = inputs.first()
            val base = first.nameWithoutExtension.take(100)
            fun out(name: String) = File(work, "out").apply { mkdirs() }.let { File(it, name) }
            fun outDir(name: String) = File(work, "out").let { File(it, name) }.apply { mkdirs() }
            val fmt = spec.format
            return when (spec.tool) {
                JOIN -> {
                    val audio = inputs.none { Converter.hasVideo(it) }
                    out("$base and ${inputs.size - 1} more.${if (audio) "m4a" else "mp4"}").also { Converter.join(ctx, id, inputs, it, onProgress) }
                }
                FRAMES -> outDir("$base pictures").also {
                    Converter.frames(ctx, id, first, it, spec.opt("every")?.toDoubleOrNull() ?: 1.0, fmt, onProgress)
                }
                ADD_SUBS -> {
                    val video = inputs.first { kindOf(it.name, null) == InKind.VIDEO }
                    val subs = inputs.first { kindOf(it.name, null) == InKind.SUBS }
                    val burn = spec.opt("mode") != "track"
                    val ext = if (burn) "mp4" else if (video.extension == "mkv") "mkv" else "mp4"
                    out("${video.nameWithoutExtension} (subtitles).$ext").also {
                        Converter.addSubtitles(ctx, id, video, subs, it, burn, spec.opt("size"), onProgress)
                    }
                }
                IMAGE -> ImageTools.convert(ctx, id, inputs, work, spec, onProgress)
                IMAGES_PDF -> out("$base${if (inputs.size > 1) " and ${inputs.size - 1} more" else ""}.pdf").also {
                    ImageTools.toPdf(id, inputs, it, spec, onProgress)
                }
                PDF_IMAGES -> outDir("$base pages").also { PdfTools.toImages(id, first, it, spec, onProgress) }
                PDF_MERGE -> out("$base and ${inputs.size - 1} more.pdf").also { PdfTools.merge(ctx, id, inputs, it, onProgress) }
                PDF_PAGES -> PdfTools.pages(ctx, id, first, work, spec, onProgress)
                PDF_COMPRESS -> out("$base (smaller).pdf").also { PdfTools.compress(ctx, id, first, it, spec, onProgress) }
                PDF_ROTATE -> out("$base (rotated).pdf").also { PdfTools.rotate(ctx, first, it, spec) }
                PDF_PROTECT -> out("$base (locked).pdf").also { PdfTools.protect(ctx, first, it, spec) }
                PDF_UNLOCK -> out("$base (unlocked).pdf").also { PdfTools.unlock(ctx, first, it, spec) }
                DOC -> out("$base.$fmt").also { DocTools.convert(ctx, id, first, it, spec, onProgress) }
                UNPACK -> ArchiveTools.unpack(id, first, outDir(ArchiveTools.baseName(first.name)), spec, onProgress)
                PACK -> {
                    val name = spec.opt("name")?.let { safe(it) } ?: if (inputs.size == 1) base else "$base and ${inputs.size - 1} more"
                    out("$name.$fmt").also { ArchiveTools.pack(id, inputs, it, onProgress) }
                }
                SUBS -> out("$base.$fmt").also {
                    Converter.subtitles(ctx, id, first, it, spec.opt("shift")?.replace(',', '.')?.toDoubleOrNull() ?: 0.0, onProgress)
                }
                DATA -> out("$base.$fmt").also { DataTools.convert(first, it) }
                else -> throw ConvertError("This kind of conversion isn't available in this version.")
            }
        } finally {
            cancelled.remove(id)
        }
    }

    fun safe(s: String) = s.replace(Regex("""[\\/:*?"<>|\n\r\t]+"""), " ").replace(Regex("\\s+"), " ").trim().take(100).ifBlank { "AnySave" }
}
