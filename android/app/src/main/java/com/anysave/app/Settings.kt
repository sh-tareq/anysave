package com.anysave.app

import android.content.Context
import android.content.SharedPreferences
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

/** Quality used for one-tap downloads (copied link, Share → Download, Quick Settings tile). */
enum class Quality(val label: String, val height: Int?) {
    BEST("Best available", null),
    P1080("1080p", 1080),
    P720("720p", 720),
    P480("480p (saves data)", 480),
    P360("360p (smallest)", 360),
    AUDIO("Audio only (MP3)", null);

    fun toOption(): Engine.Option = when {
        this == AUDIO -> Engine.Option("Audio only (MP3)", "ba/b", audioOnly = true)
        height == null -> Engine.Option("Best quality (MP4)", "bv*+ba/b", Engine.H264_SORT)
        else -> Engine.Option("${height}p MP4", "bv*[height<=$height]+ba/b[height<=$height]", Engine.H264_SORT)
    }
}

object Settings {
    private lateinit var prefs: SharedPreferences

    private val _quality = MutableStateFlow(Quality.BEST)
    val quality: StateFlow<Quality> = _quality
    /** Off by default: a copied link is only offered on Home until the user opts in. */
    private val _autoClipboard = MutableStateFlow(false)
    val autoClipboard: StateFlow<Boolean> = _autoClipboard
    private val _wifiOnly = MutableStateFlow(false)
    val wifiOnly: StateFlow<Boolean> = _wifiOnly
    private val _maxParallel = MutableStateFlow(3)
    /** How many downloads run at the same time. */
    val maxParallel: StateFlow<Int> = _maxParallel
    private val _speedLimitKbps = MutableStateFlow(0)
    /** Global download speed cap in KB/s (0 = Unlimited). */
    val speedLimitKbps: StateFlow<Int> = _speedLimitKbps
    private val _sortIntoFolders = MutableStateFlow(true)
    /** Save into Downloads/AnySave/Videos, Music, Documents… instead of one folder. */
    val sortIntoFolders: StateFlow<Boolean> = _sortIntoFolders

    // Videos: subtitles (language codes, comma separated), chapter marks, cover art.
    private val _subLangs = MutableStateFlow("")
    val subLangs: StateFlow<String> = _subLangs
    private val _embedChapters = MutableStateFlow(true)
    val embedChapters: StateFlow<Boolean> = _embedChapters
    private val _embedCover = MutableStateFlow(false)
    val embedCover: StateFlow<Boolean> = _embedCover
    /** Blank = the phone's own proxy settings, "direct", or http://host:port / socks5://host:port. */
    private val _proxy = MutableStateFlow("")
    val proxy: StateFlow<String> = _proxy
    // Scheduler and queues (see Schedule).
    private val _scheduleOn = MutableStateFlow(false)
    val scheduleOn: StateFlow<Boolean> = _scheduleOn
    private val _scheduleStart = MutableStateFlow("01:00")
    val scheduleStart: StateFlow<String> = _scheduleStart
    private val _scheduleStop = MutableStateFlow("07:00")
    val scheduleStop: StateFlow<String> = _scheduleStop
    /** Blank = the schedule applies to every download, or the name of one queue. */
    private val _scheduleQueue = MutableStateFlow("")
    val scheduleQueue: StateFlow<String> = _scheduleQueue
    /** Names of the user's extra queues, comma separated. */
    private val _queues = MutableStateFlow("")
    val queues: StateFlow<String> = _queues
    /** The queue new downloads go into (blank = the main queue). */
    private val _newQueue = MutableStateFlow("")
    val newQueue: StateFlow<String> = _newQueue

    fun init(ctx: Context) {
        prefs = ctx.getSharedPreferences("settings", Context.MODE_PRIVATE)
        _quality.value = runCatching { Quality.valueOf(prefs.getString("quality", null)!!) }.getOrDefault(Quality.BEST)
        _autoClipboard.value = prefs.getBoolean("autoClipboard", false)
        _wifiOnly.value = prefs.getBoolean("wifiOnly", false)
        _maxParallel.value = prefs.getInt("maxParallel", 3)
        _speedLimitKbps.value = prefs.getInt("speedLimitKbps", 0)
        _sortIntoFolders.value = prefs.getBoolean("sortIntoFolders", true)
        _subLangs.value = prefs.getString("subLangs", "") ?: ""
        _embedChapters.value = prefs.getBoolean("embedChapters", true)
        _embedCover.value = prefs.getBoolean("embedCover", false)
        _proxy.value = prefs.getString("proxy", "") ?: ""
        _scheduleOn.value = prefs.getBoolean("scheduleOn", false)
        _scheduleStart.value = prefs.getString("scheduleStart", "01:00") ?: "01:00"
        _scheduleStop.value = prefs.getString("scheduleStop", "07:00") ?: "07:00"
        _scheduleQueue.value = prefs.getString("scheduleQueue", "") ?: ""
        _queues.value = prefs.getString("queues", "") ?: ""
        _newQueue.value = prefs.getString("newQueue", "") ?: ""
    }

    fun setSubLangs(v: String) { _subLangs.value = v; prefs.edit().putString("subLangs", v).apply() }
    fun setEmbedChapters(on: Boolean) { _embedChapters.value = on; prefs.edit().putBoolean("embedChapters", on).apply() }
    fun setEmbedCover(on: Boolean) { _embedCover.value = on; prefs.edit().putBoolean("embedCover", on).apply() }
    fun setProxy(v: String) { _proxy.value = v.trim(); prefs.edit().putString("proxy", v.trim()).apply() }
    fun setScheduleOn(on: Boolean) { _scheduleOn.value = on; prefs.edit().putBoolean("scheduleOn", on).apply() }
    fun setScheduleStart(v: String) { _scheduleStart.value = v; prefs.edit().putString("scheduleStart", v).apply() }
    fun setScheduleStop(v: String) { _scheduleStop.value = v; prefs.edit().putString("scheduleStop", v).apply() }
    fun setScheduleQueue(v: String) { _scheduleQueue.value = v; prefs.edit().putString("scheduleQueue", v).apply() }
    fun setNewQueue(v: String) { _newQueue.value = v; prefs.edit().putString("newQueue", v).apply() }
    /** Saves the queue names; a schedule or default for a queue that is gone falls back to "all" / main. */
    fun setQueues(v: String) {
        val clean = v.split(',').map { it.trim().take(24) }.filter { it.isNotEmpty() }.distinct()
        _queues.value = clean.joinToString(", ")
        prefs.edit().putString("queues", _queues.value).apply()
        if (_scheduleQueue.value !in clean) setScheduleQueue("")
        if (_newQueue.value !in clean) setNewQueue("")
    }

    fun setQuality(q: Quality) { _quality.value = q; prefs.edit().putString("quality", q.name).apply() }
    fun setAutoClipboard(on: Boolean) { _autoClipboard.value = on; prefs.edit().putBoolean("autoClipboard", on).apply() }
    fun setWifiOnly(on: Boolean) { _wifiOnly.value = on; prefs.edit().putBoolean("wifiOnly", on).apply() }
    fun setMaxParallel(n: Int) { _maxParallel.value = n; prefs.edit().putInt("maxParallel", n).apply() }
    fun setSpeedLimitKbps(kbps: Int) { _speedLimitKbps.value = kbps; prefs.edit().putInt("speedLimitKbps", kbps).apply() }
    fun setSortIntoFolders(on: Boolean) { _sortIntoFolders.value = on; prefs.edit().putBoolean("sortIntoFolders", on).apply() }

    /** The last copied link we already acted on, so reopening the app doesn't download it twice. */
    var lastClipboardUrl: String?
        get() = prefs.getString("lastClipboardUrl", null)
        set(v) = prefs.edit().putString("lastClipboardUrl", v).apply()

    var onboarded: Boolean
        get() = prefs.getBoolean("onboarded", false)
        set(v) = prefs.edit().putBoolean("onboarded", v).apply()

    /** The user ticked "I agree" to the current legal notice. Nothing downloads before that. */
    var acceptedTerms: Boolean
        get() = prefs.getInt("acceptedTermsVersion", 0) >= TERMS_VERSION
        set(v) = prefs.edit().putInt("acceptedTermsVersion", if (v) TERMS_VERSION else 0).apply()
}
