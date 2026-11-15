package com.anysave.app

import org.apache.commons.compress.archivers.ArchiveInputStream
import org.apache.commons.compress.archivers.sevenz.SevenZFile
import org.apache.commons.compress.archivers.sevenz.SevenZOutputFile
import org.apache.commons.compress.archivers.tar.TarArchiveEntry
import org.apache.commons.compress.archivers.tar.TarArchiveInputStream
import org.apache.commons.compress.archivers.tar.TarArchiveOutputStream
import org.apache.commons.compress.archivers.zip.ZipArchiveEntry
import org.apache.commons.compress.archivers.zip.ZipArchiveOutputStream
import org.apache.commons.compress.archivers.zip.ZipFile
import org.apache.commons.compress.compressors.bzip2.BZip2CompressorInputStream
import org.apache.commons.compress.compressors.gzip.GzipCompressorInputStream
import org.apache.commons.compress.compressors.gzip.GzipCompressorOutputStream
import org.apache.commons.compress.compressors.xz.XZCompressorInputStream
import java.io.BufferedInputStream
import java.io.File
import java.io.InputStream

/** Archives with Apache Commons Compress: unpack ZIP, 7z, TAR (+GZ/BZ2/XZ); pack ZIP, 7z, TAR.GZ. */
object ArchiveTools {
    private val doubleExt = listOf(".tar.gz", ".tar.bz2", ".tar.xz")

    fun baseName(name: String): String {
        val lower = name.lowercase()
        doubleExt.firstOrNull { lower.endsWith(it) }?.let { return name.dropLast(it.length) }
        return name.substringBeforeLast('.')
    }

    /** Where an entry goes, refusing names like "../../x" that would escape the folder. */
    private fun target(dir: File, entryName: String): File? {
        val clean = entryName.replace('\\', '/').trimStart('/')
        if (clean.isEmpty()) return null
        val f = File(dir, clean)
        return if (f.canonicalPath.startsWith(dir.canonicalPath + File.separator)) f else null
    }

    private fun write(dir: File, name: String, isDir: Boolean, data: InputStream?) {
        val f = target(dir, name) ?: return
        if (isDir) { f.mkdirs(); return }
        f.parentFile?.mkdirs()
        f.outputStream().use { out -> data?.copyTo(out) }
    }

    fun unpack(id: String, file: File, outDir: File, spec: ConvertSpec, onProgress: (Float) -> Unit): File {
        val name = file.name.lowercase()
        val total = file.length().coerceAtLeast(1)
        when {
            name.endsWith(".rar") -> throw ConvertError("RAR files can't be unpacked on the phone yet. ZIP, 7z and TAR work.")
            name.endsWith(".zip") || name.endsWith(".cbz") -> ZipFile.builder().setFile(file).get().use { zip ->
                val entries = zip.entries.toList()
                entries.forEachIndexed { i, e ->
                    ConvertTools.check(id)
                    if (!zip.canReadEntryData(e)) {
                        throw ConvertError(if (e.generalPurposeBit.usesEncryption()) "This ZIP is locked with a password. AnySave can't open locked ZIPs yet."
                            else "This ZIP uses a kind of packing AnySave can't read.")
                    }
                    if (e.isDirectory) write(outDir, e.name, true, null) else zip.getInputStream(e).use { write(outDir, e.name, false, it) }
                    onProgress((i + 1) * 100f / entries.size)
                }
            }
            name.endsWith(".7z") -> {
                val pw = spec.opt("password")?.toCharArray()
                val seven = try {
                    SevenZFile.builder().setFile(file).apply { if (pw != null) setPassword(pw) }.get()
                } catch (e: Exception) {
                    throw ConvertError(if (e.message?.contains("password", true) == true) "This 7z is locked. Type its password and try again." else
                        "Couldn't read this 7z file.")
                }
                seven.use { z ->
                    var done = 0L
                    while (true) {
                        ConvertTools.check(id)
                        val e = try { z.nextEntry } catch (ex: Exception) {
                            throw ConvertError(if (ex.message?.contains("password", true) == true) "Wrong password, or this 7z is damaged." else
                                "Couldn't read this 7z file: ${ex.message}")
                        } ?: break
                        if (e.isDirectory) write(outDir, e.name, true, null)
                        else write(outDir, e.name, false, object : InputStream() {
                            override fun read() = z.read()
                            override fun read(b: ByteArray, off: Int, len: Int) = z.read(b, off, len)
                        })
                        done += e.size
                        onProgress((done * 100f / (total * 3)).coerceAtMost(99f))
                    }
                }
            }
            else -> {
                val raw = BufferedInputStream(file.inputStream())
                val counting = CountingStream(raw)
                val stream: InputStream = when {
                    name.endsWith(".tar.gz") || name.endsWith(".tgz") || name.endsWith(".gz") -> GzipCompressorInputStream(counting, true)
                    name.endsWith(".tar.bz2") || name.endsWith(".tbz2") || name.endsWith(".bz2") -> BZip2CompressorInputStream(counting, true)
                    name.endsWith(".tar.xz") || name.endsWith(".txz") || name.endsWith(".xz") -> XZCompressorInputStream(counting)
                    else -> counting
                }
                val isTar = name.contains(".tar") || name.endsWith(".tgz") || name.endsWith(".tbz2") || name.endsWith(".txz")
                stream.use { s ->
                    if (!isTar) { // a single compressed file, e.g. log.txt.gz
                        write(outDir, baseName(file.name).ifBlank { "file" }, false, s)
                    } else {
                        val tar: ArchiveInputStream<TarArchiveEntry> = TarArchiveInputStream(s)
                        while (true) {
                            ConvertTools.check(id)
                            val e = tar.nextEntry ?: break
                            if (e.isSymbolicLink || e.isLink) continue
                            write(outDir, e.name, e.isDirectory, if (e.isDirectory) null else tar)
                            onProgress((counting.count * 100f / total).coerceAtMost(99f))
                        }
                    }
                }
            }
        }
        val files = outDir.walkTopDown().filter { it.isFile }.toList()
        if (files.isEmpty()) throw ConvertError("This archive is empty.")
        onProgress(100f)
        // A single file inside comes out as itself, not in a folder.
        return if (files.size == 1) files.first() else outDir
    }

    /** What's inside, for the preview: (name, size) of each file, at most [max]. RAR and locked files list what they can. */
    fun list(file: File, max: Int = 500): List<Pair<String, Long>> {
        val name = file.name.lowercase()
        val out = mutableListOf<Pair<String, Long>>()
        when {
            name.endsWith(".zip") || name.endsWith(".cbz") -> ZipFile.builder().setFile(file).get().use { z ->
                z.entries.asSequence().filter { !it.isDirectory }.take(max).forEach { out += it.name to it.size }
            }
            name.endsWith(".7z") -> runCatching {
                SevenZFile.builder().setFile(file).get().use { z -> z.entries.filter { !it.isDirectory }.take(max).forEach { out += it.name to it.size } }
            }
            name.endsWith(".rar") -> {}
            else -> {
                val raw = BufferedInputStream(file.inputStream())
                val stream: InputStream = when {
                    name.endsWith(".gz") || name.endsWith(".tgz") -> GzipCompressorInputStream(raw, true)
                    name.endsWith(".bz2") || name.endsWith(".tbz2") -> BZip2CompressorInputStream(raw, true)
                    name.endsWith(".xz") || name.endsWith(".txz") -> XZCompressorInputStream(raw)
                    else -> raw
                }
                stream.use { s ->
                    if (!(name.contains(".tar") || name.endsWith(".tgz") || name.endsWith(".tbz2") || name.endsWith(".txz"))) {
                        out += baseName(file.name) to -1L
                    } else {
                        val tar = TarArchiveInputStream(s)
                        while (out.size < max) { val e = tar.nextEntry ?: break; if (e.isFile) out += e.name to e.size }
                    }
                }
            }
        }
        return out
    }

    fun pack(id: String, inputs: List<File>, out: File, onProgress: (Float) -> Unit) {
        val total = inputs.sumOf { it.length() }.coerceAtLeast(1)
        var done = 0L
        val names = mutableSetOf<String>()
        fun nameOf(f: File): String {
            var n = f.name; var i = 1
            while (!names.add(n.lowercase())) n = "${f.nameWithoutExtension} (${i++})${if (f.extension.isNotEmpty()) "." + f.extension else ""}"
            return n
        }
        fun step(f: File) { done += f.length(); onProgress((done * 100f / total).coerceAtMost(99f)) }
        when {
            out.name.endsWith(".7z") -> SevenZOutputFile(out).use { z ->
                inputs.forEach { f ->
                    ConvertTools.check(id)
                    z.putArchiveEntry(z.createArchiveEntry(f, nameOf(f)))
                    f.inputStream().use { inp ->
                        val buf = ByteArray(64 * 1024)
                        while (true) { val r = inp.read(buf); if (r < 0) break; z.write(buf, 0, r) }
                    }
                    z.closeArchiveEntry()
                    step(f)
                }
                z.finish()
            }
            out.name.endsWith(".tar.gz") -> TarArchiveOutputStream(GzipCompressorOutputStream(out.outputStream().buffered())).use { t ->
                t.setLongFileMode(TarArchiveOutputStream.LONGFILE_POSIX)
                t.setBigNumberMode(TarArchiveOutputStream.BIGNUMBER_POSIX)
                inputs.forEach { f ->
                    ConvertTools.check(id)
                    t.putArchiveEntry(TarArchiveEntry(f, nameOf(f)))
                    f.inputStream().use { it.copyTo(t) }
                    t.closeArchiveEntry()
                    step(f)
                }
            }
            else -> ZipArchiveOutputStream(out).use { z ->
                z.setUseZip64(org.apache.commons.compress.archivers.zip.Zip64Mode.AsNeeded)
                inputs.forEach { f ->
                    ConvertTools.check(id)
                    z.putArchiveEntry(ZipArchiveEntry(f, nameOf(f)))
                    f.inputStream().use { it.copyTo(z) }
                    z.closeArchiveEntry()
                    step(f)
                }
            }
        }
        onProgress(100f)
    }

    private class CountingStream(private val inner: InputStream) : InputStream() {
        var count = 0L
        override fun read(): Int = inner.read().also { if (it >= 0) count++ }
        override fun read(b: ByteArray, off: Int, len: Int): Int = inner.read(b, off, len).also { if (it > 0) count += it }
        override fun close() = inner.close()
    }
}
