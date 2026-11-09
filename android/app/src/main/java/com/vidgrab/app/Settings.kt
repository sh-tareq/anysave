package com.vidgrab.app

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
    private val _autoClipboard = MutableStateFlow(true)
    val autoClipboard: StateFlow<Boolean> = _autoClipboard
    private val _wifiOnly = MutableStateFlow(false)
    val wifiOnly: StateFlow<Boolean> = _wifiOnly
    private val _maxParallel = MutableStateFlow(3)
    /** How many downloads run at the same time. */
    val maxParallel: StateFlow<Int> = _maxParallel
    private val _sortIntoFolders = MutableStateFlow(true)
    /** Save into Downloads/VidGrab/Videos, Music, Documents… instead of one folder. */
    val sortIntoFolders: StateFlow<Boolean> = _sortIntoFolders

    fun init(ctx: Context) {
        prefs = ctx.getSharedPreferences("settings", Context.MODE_PRIVATE)
        _quality.value = runCatching { Quality.valueOf(prefs.getString("quality", null)!!) }.getOrDefault(Quality.BEST)
        _autoClipboard.value = prefs.getBoolean("autoClipboard", true)
        _wifiOnly.value = prefs.getBoolean("wifiOnly", false)
        _maxParallel.value = prefs.getInt("maxParallel", 3)
        _sortIntoFolders.value = prefs.getBoolean("sortIntoFolders", true)
    }

    fun setQuality(q: Quality) { _quality.value = q; prefs.edit().putString("quality", q.name).apply() }
    fun setAutoClipboard(on: Boolean) { _autoClipboard.value = on; prefs.edit().putBoolean("autoClipboard", on).apply() }
    fun setWifiOnly(on: Boolean) { _wifiOnly.value = on; prefs.edit().putBoolean("wifiOnly", on).apply() }
    fun setMaxParallel(n: Int) { _maxParallel.value = n; prefs.edit().putInt("maxParallel", n).apply() }
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
