package com.vidgrab.app

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.IOException
import java.io.RandomAccessFile
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLDecoder
import java.nio.ByteBuffer
import java.nio.channels.FileChannel
import java.security.MessageDigest
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicLong
import kotlin.math.max
import kotlin.math.min

/**
 * Multi-connection segmented HTTP(S) file download engine for Android.
 *
 * Features:
 * - Dynamic work-stealing segmentation (splits largest remaining segment at midpoint).
 * - Crash-safe resume via `state.json` + `download.part` in `workDir`.
 * - Refresh Expired Link support (updates `state.url` when resumed with a fresh link).
 * - ETag / Last-Modified `If-Range` validation with safe fallback on stale files.
 * - Adaptive 403/429 rate-limit backoff and single-stream fallback when Range is unsupported.
 * - Live global speed limiter via `Settings.speedLimitKbps`.
 */
object FastFileEngine {
    private const val MIN_SPLIT_SIZE = 256 * 1024L
    private const val DEFAULT_MAX_CONNECTIONS = 6
    private const val MAX_RETRIES = 6

    data class Segment(
        var id: Int,
        var start: Long,
        var current: Long,
        var end: Long, // exclusive; -1 if unknown
        var done: Boolean = false,
        var busy: Boolean = false,
        var inFlight: Long = 0L,
    )

    data class State(
        var url: String,
        var resolvedUrl: String,
        var filename: String,
        var totalBytes: Long,
        var acceptRanges: Boolean,
        var etag: String,
        var lastModified: String,
        val segments: MutableList<Segment>,
    )

    data class Progress(
        val doneBytes: Long,
        val totalBytes: Long,
        val percent: Float,
        val speedBps: Long,
        val etaSeconds: Long,
        val activeWorkers: Int,
        val segments: Int,
    )

    data class Result(
        val file: File,
        val filename: String,
        val totalBytes: Long,
        val sha256: String,
    )

    private class StaleResetException : IOException("Remote file changed on server")

    // Shared token bucket for global speed limiting across all active file downloads
    private val rateLock = Any()
    private var rateTokens: Double = 0.0
    private var rateLastNs: Long = 0L

    private suspend fun waitRateLimit(bytes: Int) {
        val kbps = Settings.speedLimitKbps.value
        if (kbps <= 0 || bytes <= 0) return
        val maxBps = kbps * 1024.0
        var waitMs = 0L
        synchronized(rateLock) {
            val now = System.nanoTime()
            if (rateLastNs == 0L) {
                rateLastNs = now
                rateTokens = maxBps
            } else {
                val elapsedSec = (now - rateLastNs) / 1_000_000_000.0
                if (elapsedSec > 0) {
                    rateTokens += elapsedSec * maxBps
                    if (rateTokens > maxBps) rateTokens = maxBps
                    rateLastNs = now
                }
            }
            rateTokens -= bytes.toDouble()
            if (rateTokens < 0) {
                waitMs = ((-rateTokens / maxBps) * 1000.0).toLong().coerceAtLeast(1L)
            }
        }
        if (waitMs > 0L) {
            delay(waitMs)
        }
    }

    suspend fun download(
        url: String,
        workDir: File,
        preferredFilename: String? = null,
        cookies: String? = null,
        userAgent: String = FileKinds.USER_AGENT,
        maxConnections: Int = DEFAULT_MAX_CONNECTIONS,
        paused: AtomicBoolean,
        onMeta: (filename: String, totalBytes: Long) -> Unit = { _, _ -> },
        onProgress: (Progress) -> Unit = {},
    ): Result = withContext(Dispatchers.IO) {
        workDir.mkdirs()
        val stateFile = File(workDir, "state.json")
        val partFile = File(workDir, "download.part")
        val lock = Any()

        var state = loadState(stateFile, partFile, url)
        if (state == null) {
            val probe = probeServer(url, preferredFilename, cookies, userAgent)
            val initSeg = Segment(
                id = 0,
                start = 0L,
                current = 0L,
                end = if (probe.totalBytes > 0L) probe.totalBytes else -1L,
            )
            state = State(
                url = url,
                resolvedUrl = probe.resolvedUrl,
                filename = probe.filename,
                totalBytes = probe.totalBytes,
                acceptRanges = probe.acceptRanges,
                etag = probe.etag,
                lastModified = probe.lastModified,
                segments = mutableListOf(initSeg),
            )
            RandomAccessFile(partFile, "rw").use { raf ->
                if (probe.totalBytes > 0L) {
                    raf.setLength(probe.totalBytes)
                } else {
                    raf.setLength(0L)
                }
            }
            saveState(stateFile, state, lock)
        }

        onMeta(state.filename, state.totalBytes)

        var raf = RandomAccessFile(partFile, "rw")
        var channel = raf.channel
        try {
            val canMulti = state.acceptRanges && state.totalBytes > MIN_SPLIT_SIZE
            if (!canMulti) {
                runSingleStream(
                    state = state,
                    stateFile = stateFile,
                    raf = raf,
                    channel = channel,
                    cookies = cookies,
                    userAgent = userAgent,
                    paused = paused,
                    lock = lock,
                    onProgress = onProgress,
                )
            } else {
                try {
                    runMultiSegment(
                        state = state,
                        stateFile = stateFile,
                        channel = channel,
                        cookies = cookies,
                        userAgent = userAgent,
                        maxConnections = maxConnections.coerceIn(1, 16),
                        paused = paused,
                        lock = lock,
                        onProgress = onProgress,
                    )
                } catch (_: StaleResetException) {
                    // Server file changed mid-download; restart cleanly as single stream from byte 0
                    synchronized(lock) {
                        state.acceptRanges = false
                        state.segments.clear()
                        state.segments.add(Segment(0, 0L, 0L, if (state.totalBytes > 0) state.totalBytes else -1L))
                    }
                    raf.setLength(0L)
                    saveState(stateFile, state, lock)
                    runSingleStream(
                        state = state,
                        stateFile = stateFile,
                        raf = raf,
                        channel = channel,
                        cookies = cookies,
                        userAgent = userAgent,
                        paused = paused,
                        lock = lock,
                        onProgress = onProgress,
                    )
                }
            }
        } finally {
            runCatching { channel.force(true) }
            runCatching { channel.close() }
            runCatching { raf.close() }
        }

        if (paused.get()) {
            saveState(stateFile, state, lock)
            throw CancellationException("Paused")
        }

        val actualSize = partFile.length()
        if (state.totalBytes > 0L && actualSize != state.totalBytes) {
            saveState(stateFile, state, lock)
            throw IOException("Incomplete download: got $actualSize of ${state.totalBytes} bytes")
        }

        val sha256 = computeSha256(partFile)
        val safeName = sanitizeFilename(state.filename)
        val destFile = File(workDir, safeName)
        if (destFile.exists() && destFile != partFile) destFile.delete()
        if (!partFile.renameTo(destFile)) {
            partFile.copyTo(destFile, overwrite = true)
            partFile.delete()
        }
        stateFile.delete()

        Result(
            file = destFile,
            filename = safeName,
            totalBytes = actualSize,
            sha256 = sha256,
        )
    }

    private suspend fun runMultiSegment(
        state: State,
        stateFile: File,
        channel: FileChannel,
        cookies: String?,
        userAgent: String,
        maxConnections: Int,
        paused: AtomicBoolean,
        lock: Any,
        onProgress: (Progress) -> Unit,
    ) = coroutineScope {
        val activeWorkers = AtomicInteger(0)
        val targetWorkers = AtomicInteger(min(2, maxConnections))
        val rateLimitedUntilMs = AtomicLong(0L)
        val workerJobs = mutableListOf<Job>()
        val workerScope = CoroutineScope(coroutineContext + SupervisorJob())
        var fatalError: Throwable? = null

        fun spawnWorker() {
            val job = workerScope.launch {
                activeWorkers.incrementAndGet()
                try {
                    while (isActive && !paused.get()) {
                        if (activeWorkers.get() > targetWorkers.get()) return@launch
                        val waitMs = rateLimitedUntilMs.get() - System.currentTimeMillis()
                        if (waitMs > 0) {
                            delay(min(waitMs, 500L))
                            continue
                        }
                        val seg = claimOrSplitSegment(state, lock)
                        if (seg == null) {
                            if (allSegmentsDone(state, lock)) return@launch
                            delay(80L)
                            continue
                        }
                        try {
                            downloadSegment(
                                state = state,
                                stateFile = stateFile,
                                seg = seg,
                                channel = channel,
                                cookies = cookies,
                                userAgent = userAgent,
                                paused = paused,
                                lock = lock,
                                onRateLimited = {
                                    rateLimitedUntilMs.set(System.currentTimeMillis() + 600L)
                                    val cur = targetWorkers.get()
                                    if (cur > 1) targetWorkers.compareAndSet(cur, cur - 1)
                                },
                            )
                        } catch (e: CancellationException) {
                            synchronized(lock) { seg.busy = false }
                            throw e
                        } catch (e: Throwable) {
                            synchronized(lock) { seg.busy = false }
                            if (fatalError == null) fatalError = e
                            return@launch
                        }
                    }
                } finally {
                    activeWorkers.decrementAndGet()
                }
            }
            synchronized(workerJobs) { workerJobs.add(job) }
        }

        val initial = targetWorkers.get()
        repeat(initial) { spawnWorker() }

        var lastBytes = currentDoneBytes(state, lock)
        var lastTickMs = System.currentTimeMillis()
        var ewmaSpeed = 0.0
        var scaleTicks = 0

        try {
            while (isActive && !paused.get()) {
                delay(250L)
                fatalError?.let { throw it }

                val nowMs = System.currentTimeMillis()
                val done = currentDoneBytes(state, lock)
                val dtSec = max((nowMs - lastTickMs) / 1000.0, 0.05)
                val instSpeed = max(0.0, (done - lastBytes) / dtSec)
                ewmaSpeed = if (ewmaSpeed == 0.0) instSpeed else 0.35 * instSpeed + 0.65 * ewmaSpeed
                lastBytes = done
                lastTickMs = nowMs

                val total = state.totalBytes
                val pct = if (total > 0L) (done * 100f / total).coerceIn(0f, 100f) else 0f
                val eta = if (total > done && ewmaSpeed > 1.0) ((total - done) / ewmaSpeed).toLong() else -1L
                val segCount = synchronized(lock) { state.segments.size }
                onProgress(
                    Progress(
                        doneBytes = done,
                        totalBytes = total,
                        percent = pct,
                        speedBps = ewmaSpeed.toLong(),
                        etaSeconds = eta,
                        activeWorkers = activeWorkers.get(),
                        segments = segCount,
                    )
                )
                saveState(stateFile, state, lock)

                if (allSegmentsDone(state, lock)) break

                // Scale up workers every ~500ms if not rate-limited and splittable work remains
                scaleTicks++
                if (scaleTicks % 2 == 0 && System.currentTimeMillis() >= rateLimitedUntilMs.get()) {
                    val curTarget = targetWorkers.get()
                    if (curTarget < maxConnections && hasSplittableWork(state, lock)) {
                        targetWorkers.incrementAndGet()
                        spawnWorker()
                    } else if (activeWorkers.get() == 0 && !allSegmentsDone(state, lock)) {
                        spawnWorker()
                    }
                }
            }
        } finally {
            val jobs = synchronized(workerJobs) { workerJobs.toList() }
            jobs.forEach { it.cancel() }
            jobs.forEach { runCatching { it. cancelAndJoin() } }
            saveState(stateFile, state, lock)
        }

        fatalError?.let { throw it }
    }

    private suspend fun downloadSegment(
        state: State,
        stateFile: File,
        seg: Segment,
        channel: FileChannel,
        cookies: String?,
        userAgent: String,
        paused: AtomicBoolean,
        lock: Any,
        onRateLimited: () -> Unit,
    ) {
        var attempt = 0
        val buf = ByteArray(32 * 1024)
        while (!paused.get()) {
            val (cur, end) = synchronized(lock) { seg.current to seg.end }
            if (end in 0..cur) {
                synchronized(lock) {
                    seg.done = true
                    seg.busy = false
                }
                saveState(stateFile, state, lock)
                return
            }

            var conn: HttpURLConnection? = null
            try {
                val targetUrl = synchronized(lock) { state.resolvedUrl.ifBlank { state.url } }
                conn = openRedirectingConnection(targetUrl, cookies, userAgent) { c ->
                    c.requestMethod = "GET"
                    if (end > 0L) {
                        c.setRequestProperty("Range", "bytes=$cur-${end - 1}")
                    } else if (cur > 0L) {
                        c.setRequestProperty("Range", "bytes=$cur-")
                    }
                    val validator = synchronized(lock) { state.etag.ifBlank { state.lastModified } }
                    if (validator.isNotBlank() && cur > 0L) {
                        c.setRequestProperty("If-Range", validator)
                    }
                }

                val code = conn.responseCode
                if (code == 403 || code == 429 || code == 503) {
                    onRateLimited()
                    attempt++
                    if (attempt > MAX_RETRIES) throw IOException("HTTP $code after $MAX_RETRIES retries")
                    delay(350L * attempt)
                    continue
                }
                if (code == 200 && cur > 0L) {
                    throw StaleResetException()
                }
                if (code != 200 && code != 206) {
                    throw IOException("HTTP $code")
                }

                conn.inputStream.use { input ->
                    while (!paused.get()) {
                        val n = input.read(buf)
                        if (n <= 0) break
                        waitRateLimit(n)

                        var writeOff = 0L
                        var toWrite = 0
                        var finishedNow = false
                        synchronized(lock) {
                            val dynamicEnd = seg.end
                            val remaining = if (dynamicEnd > 0L) dynamicEnd - seg.current else n.toLong()
                            if (remaining <= 0L) {
                                seg.done = true
                                seg.busy = false
                                finishedNow = true
                            } else {
                                toWrite = min(n.toLong(), remaining).toInt()
                                writeOff = seg.current
                                seg.inFlight = writeOff + toWrite
                            }
                        }
                        if (finishedNow) {
                            saveState(stateFile, state, lock)
                            return
                        }
                        if (toWrite > 0) {
                            writeAt(channel, buf, toWrite, writeOff)
                            val reachedEnd = synchronized(lock) {
                                seg.current = writeOff + toWrite
                                if (seg.end > 0L && seg.current >= seg.end) {
                                    seg.done = true
                                    seg.busy = false
                                    true
                                } else {
                                    false
                                }
                            }
                            if (reachedEnd) {
                                saveState(stateFile, state, lock)
                                return
                            }
                        }
                    }
                }

                if (paused.get()) {
                    synchronized(lock) { seg.busy = false }
                    return
                }

                val doneNow = synchronized(lock) {
                    if (seg.end <= 0L || seg.current >= seg.end) {
                        seg.done = true
                        seg.busy = false
                        true
                    } else {
                        false
                    }
                }
                if (doneNow) {
                    saveState(stateFile, state, lock)
                    return
                }
                // Premature EOF: retry remaining range
                attempt++
                if (attempt > MAX_RETRIES) {
                    throw IOException("Connection closed before segment finished")
                }
                delay(200L * attempt)
            } catch (e: StaleResetException) {
                synchronized(lock) { seg.busy = false }
                throw e
            } catch (e: CancellationException) {
                synchronized(lock) { seg.busy = false }
                throw e
            } catch (e: IOException) {
                attempt++
                if (attempt > MAX_RETRIES) {
                    synchronized(lock) { seg.busy = false }
                    throw e
                }
                delay(250L * attempt)
            } finally {
                conn?.disconnect()
            }
        }
        synchronized(lock) { seg.busy = false }
    }

    private suspend fun runSingleStream(
        state: State,
        stateFile: File,
        raf: RandomAccessFile,
        channel: FileChannel,
        cookies: String?,
        userAgent: String,
        paused: AtomicBoolean,
        lock: Any,
        onProgress: (Progress) -> Unit,
    ) = coroutineScope {
        val seg = synchronized(lock) {
            if (state.segments.isEmpty()) {
                val s = Segment(0, 0L, 0L, if (state.totalBytes > 0) state.totalBytes else -1L)
                state.segments.add(s)
                s
            } else {
                state.segments[0]
            }
        }

        var attempt = 0
        val buf = ByteArray(32 * 1024)
        var lastTickMs = System.currentTimeMillis()
        var lastBytes = synchronized(lock) { seg.current }
        var ewmaSpeed = 0.0

        while (isActive && !paused.get()) {
            val cur = synchronized(lock) { seg.current }
            if (state.totalBytes > 0L && cur >= state.totalBytes) {
                synchronized(lock) { seg.done = true }
                break
            }

            var conn: HttpURLConnection? = null
            try {
                val targetUrl = synchronized(lock) { state.resolvedUrl.ifBlank { state.url } }
                val canRange = synchronized(lock) { state.acceptRanges && cur > 0L }
                conn = openRedirectingConnection(targetUrl, cookies, userAgent) { c ->
                    c.requestMethod = "GET"
                    if (canRange) {
                        c.setRequestProperty("Range", "bytes=$cur-")
                        val validator = synchronized(lock) { state.etag.ifBlank { state.lastModified } }
                        if (validator.isNotBlank()) c.setRequestProperty("If-Range", validator)
                    }
                }
                val code = conn.responseCode
                if (code == 200 && cur > 0L) {
                    synchronized(lock) {
                        seg.current = 0L
                        seg.inFlight = 0L
                    }
                    raf.setLength(0L)
                } else if (code != 200 && code != 206) {
                    throw IOException("HTTP $code")
                }

                conn.inputStream.use { input ->
                    while (isActive && !paused.get()) {
                        ensureActive()
                        val n = input.read(buf)
                        if (n <= 0) break
                        waitRateLimit(n)

                        val writeOff = synchronized(lock) {
                            val off = seg.current
                            seg.inFlight = off + n
                            off
                        }
                        writeAt(channel, buf, n, writeOff)
                        val done = synchronized(lock) {
                            seg.current = writeOff + n
                            seg.current
                        }

                        val nowMs = System.currentTimeMillis()
                        if (nowMs - lastTickMs >= 250L) {
                            val dtSec = max((nowMs - lastTickMs) / 1000.0, 0.05)
                            val instSpeed = max(0.0, (done - lastBytes) / dtSec)
                            ewmaSpeed = if (ewmaSpeed == 0.0) instSpeed else 0.35 * instSpeed + 0.65 * ewmaSpeed
                            lastBytes = done
                            lastTickMs = nowMs
                            val total = state.totalBytes
                            val pct = if (total > 0L) (done * 100f / total).coerceIn(0f, 100f) else 0f
                            val eta = if (total > done && ewmaSpeed > 1.0) ((total - done) / ewmaSpeed).toLong() else -1L
                            onProgress(
                                Progress(
                                    doneBytes = done,
                                    totalBytes = total,
                                    percent = pct,
                                    speedBps = ewmaSpeed.toLong(),
                                    etaSeconds = eta,
                                    activeWorkers = 1,
                                    segments = 1,
                                )
                            )
                            saveState(stateFile, state, lock)
                        }
                    }
                }

                if (paused.get()) return@coroutineScope

                val finalDone = synchronized(lock) { seg.current }
                if (state.totalBytes <= 0L || finalDone >= state.totalBytes) {
                    synchronized(lock) {
                        seg.done = true
                        if (state.totalBytes <= 0L) state.totalBytes = finalDone
                    }
                    saveState(stateFile, state, lock)
                    return@coroutineScope
                }

                attempt++
                if (attempt > MAX_RETRIES) throw IOException("Stream ended early at $finalDone of ${state.totalBytes} bytes")
                delay(250L * attempt)
            } catch (e: CancellationException) {
                throw e
            } catch (e: IOException) {
                attempt++
                if (attempt > MAX_RETRIES) throw e
                delay(300L * attempt)
            } finally {
                conn?.disconnect()
            }
        }
    }

    private fun claimOrSplitSegment(state: State, lock: Any): Segment? = synchronized(lock) {
        for (seg in state.segments) {
            if (!seg.done && !seg.busy) {
                if (seg.end > 0L && seg.current >= seg.end) {
                    seg.done = true
                    continue
                }
                seg.busy = true
                return seg
            }
        }
        var best: Segment? = null
        var bestRem = 0L
        for (seg in state.segments) {
            if (!seg.done && seg.busy && seg.end > 0L) {
                val eff = max(seg.current, seg.inFlight)
                val rem = seg.end - eff
                if (rem > bestRem) {
                    bestRem = rem
                    best = seg
                }
            }
        }
        if (best == null || bestRem < 2 * MIN_SPLIT_SIZE) return null
        val eff = max(best.current, best.inFlight)
        val mid = eff + (best.end - eff) / 2
        if (mid <= eff || best.end - mid < MIN_SPLIT_SIZE) return null

        val oldEnd = best.end
        best.end = mid
        val newSeg = Segment(
            id = state.segments.size,
            start = mid,
            current = mid,
            end = oldEnd,
            done = false,
            busy = true,
            inFlight = mid,
        )
        state.segments.add(newSeg)
        newSeg
    }

    private fun hasSplittableWork(state: State, lock: Any): Boolean = synchronized(lock) {
        for (seg in state.segments) {
            if (!seg.done && !seg.busy) return true
            if (!seg.done && seg.end > 0L) {
                val eff = max(seg.current, seg.inFlight)
                if (seg.end - eff >= 2 * MIN_SPLIT_SIZE) return true
            }
        }
        false
    }

    private fun allSegmentsDone(state: State, lock: Any): Boolean = synchronized(lock) {
        state.segments.all { it.done || (it.end > 0L && it.current >= it.end) }
    }

    private fun currentDoneBytes(state: State, lock: Any): Long = synchronized(lock) {
        var sum = 0L
        for (seg in state.segments) {
            val done = (seg.current - seg.start).coerceAtLeast(0L)
            val cap = if (seg.end > 0L) (seg.end - seg.start).coerceAtLeast(0L) else done
            sum += min(done, cap)
        }
        sum
    }

    private data class ProbeResult(
        val resolvedUrl: String,
        val filename: String,
        val totalBytes: Long,
        val acceptRanges: Boolean,
        val etag: String,
        val lastModified: String,
    )

    private fun probeServer(
        url: String,
        preferredFilename: String?,
        cookies: String?,
        userAgent: String,
    ): ProbeResult {
        var resolvedUrl = url
        var totalBytes = -1L
        var acceptRanges = false
        var etag = ""
        var lastModified = ""
        var cdFilename: String? = null

        // Range 0-0 GET probe is the most reliable way to test real Range support + Content-Range total size
        var conn: HttpURLConnection? = null
        try {
            conn = openRedirectingConnection(url, cookies, userAgent) { c ->
                c.requestMethod = "GET"
                c.setRequestProperty("Range", "bytes=0-0")
            }
            val code = conn.responseCode
            resolvedUrl = conn.url.toString()
            etag = conn.getHeaderField("ETag").orEmpty()
            lastModified = conn.getHeaderField("Last-Modified").orEmpty()
            cdFilename = parseContentDisposition(conn.getHeaderField("Content-Disposition"))
            if (code == 206) {
                acceptRanges = true
                val cr = conn.getHeaderField("Content-Range").orEmpty()
                val slash = cr.lastIndexOf('/')
                if (slash >= 0) {
                    totalBytes = cr.substring(slash + 1).trim().toLongOrNull() ?: -1L
                }
            } else if (code == 200) {
                acceptRanges = conn.getHeaderField("Accept-Ranges")?.equals("bytes", ignoreCase = true) == true
                totalBytes = conn.contentLengthLong
            }
        } catch (_: Exception) {
        } finally {
            conn?.disconnect()
        }

        val finalName = sanitizeFilename(
            preferredFilename?.takeIf { it.isNotBlank() }
                ?: cdFilename?.takeIf { it.isNotBlank() }
                ?: filenameFromUrl(resolvedUrl)
        )
        return ProbeResult(
            resolvedUrl = resolvedUrl,
            filename = finalName,
            totalBytes = totalBytes,
            acceptRanges = acceptRanges,
            etag = etag,
            lastModified = lastModified,
        )
    }

    private fun openRedirectingConnection(
        startUrl: String,
        cookies: String?,
        userAgent: String,
        configure: (HttpURLConnection) -> Unit,
    ): HttpURLConnection {
        var current = startUrl
        repeat(10) {
            val conn = (URL(current).openConnection() as HttpURLConnection).apply {
                instanceFollowRedirects = false
                connectTimeout = 15_000
                readTimeout = 20_000
                setRequestProperty("User-Agent", userAgent)
                if (!cookies.isNullOrBlank()) setRequestProperty("Cookie", cookies)
                configure(this)
            }
            val code = conn.responseCode
            if (code in listOf(301, 302, 303, 307, 308)) {
                val loc = conn.getHeaderField("Location")
                conn.disconnect()
                if (loc.isNullOrBlank()) throw IOException("Redirect without Location header")
                current = URL(URL(current), loc).toString()
            } else {
                return conn
            }
        }
        throw IOException("Too many redirects")
    }

    private fun writeAt(ch: FileChannel, buf: ByteArray, len: Int, pos: Long) {
        val bb = ByteBuffer.wrap(buf, 0, len)
        var p = pos
        while (bb.hasRemaining()) {
            val n = ch.write(bb, p)
            if (n <= 0) break
            p += n
        }
    }

    private fun loadState(stateFile: File, partFile: File, currentUrl: String): State? {
        if (!stateFile.exists() || !partFile.exists()) return null
        return try {
            val o = JSONObject(stateFile.readText())
            val arr = o.optJSONArray("segments") ?: return null
            val segs = mutableListOf<Segment>()
            for (i in 0 until arr.length()) {
                val s = arr.getJSONObject(i)
                val cur = s.getLong("current")
                val end = s.getLong("end")
                segs.add(
                    Segment(
                        id = s.optInt("id", i),
                        start = s.getLong("start"),
                        current = cur,
                        end = end,
                        done = s.optBoolean("done", false) || (end > 0L && cur >= end),
                        busy = false,
                        inFlight = cur,
                    )
                )
            }
            if (segs.isEmpty()) return null
            val savedUrl = o.getString("url")
            val updatedUrl = if (currentUrl.isNotBlank() && currentUrl != savedUrl) currentUrl else savedUrl
            val resolved = if (updatedUrl != savedUrl) "" else o.optString("resolvedUrl", "")
            State(
                url = updatedUrl,
                resolvedUrl = resolved,
                filename = o.optString("filename", "download.bin"),
                totalBytes = o.optLong("totalBytes", -1L),
                acceptRanges = o.optBoolean("acceptRanges", false),
                etag = o.optString("etag", ""),
                lastModified = o.optString("lastModified", ""),
                segments = segs,
            )
        } catch (_: Exception) {
            null
        }
    }

    private fun saveState(stateFile: File, state: State, lock: Any) {
        val json = synchronized(lock) {
            val segs = JSONArray()
            for (s in state.segments) {
                segs.put(
                    JSONObject()
                        .put("id", s.id)
                        .put("start", s.start)
                        .put("current", s.current)
                        .put("end", s.end)
                        .put("done", s.done)
                )
            }
            JSONObject()
                .put("url", state.url)
                .put("resolvedUrl", state.resolvedUrl)
                .put("filename", state.filename)
                .put("totalBytes", state.totalBytes)
                .put("acceptRanges", state.acceptRanges)
                .put("etag", state.etag)
                .put("lastModified", state.lastModified)
                .put("segments", segs)
                .toString()
        }
        runCatching {
            val tmp = File(stateFile.parentFile, "${stateFile.name}.tmp")
            tmp.writeText(json)
            if (!tmp.renameTo(stateFile)) {
                stateFile.writeText(json)
                tmp.delete()
            }
        }
    }

    private fun computeSha256(file: File): String {
        val md = MessageDigest.getInstance("SHA-256")
        file.inputStream().use { input ->
            val buf = ByteArray(64 * 1024)
            while (true) {
                val n = input.read(buf)
                if (n <= 0) break
                md.update(buf, 0, n)
            }
        }
        return md.digest().joinToString("") { "%02x".format(it) }
    }

    fun parseContentDisposition(cd: String?): String? {
        if (cd.isNullOrBlank()) return null
        Regex("""filename\*\s*=\s*(?:UTF-8''|utf-8'')([^;]+)""").find(cd)?.groupValues?.get(1)?.let {
            return runCatching { URLDecoder.decode(it.trim('"', ' '), "UTF-8") }.getOrDefault(it)
        }
        Regex("""filename\s*=\s*"([^"]+)"""").find(cd)?.groupValues?.get(1)?.let { return it }
        Regex("""filename\s*=\s*([^;\s]+)""").find(cd)?.groupValues?.get(1)?.let { return it.trim('"') }
        return null
    }

    fun filenameFromUrl(url: String): String {
        val path = runCatching { URL(url).path }.getOrDefault("")
        val raw = path.substringAfterLast('/').takeIf { it.isNotBlank() } ?: "download.bin"
        val decoded = runCatching { URLDecoder.decode(raw, "UTF-8") }.getOrDefault(raw)
        return sanitizeFilename(decoded)
    }

    fun sanitizeFilename(name: String): String {
        val cleaned = name.replace(Regex("""[\\/:*?"<>|\u0000-\u001F]"""), "_").trim()
        return cleaned.ifBlank { "download.bin" }
    }
}
