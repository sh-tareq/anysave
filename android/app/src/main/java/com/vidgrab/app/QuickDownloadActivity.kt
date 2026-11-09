package com.vidgrab.app

import android.content.ClipboardManager
import android.content.Intent
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity

/**
 * Invisible screen behind the one-tap paths:
 *  - Share → "Download" in YouTube / Instagram / TikTok / Facebook
 *  - the "VidGrab" Quick Settings tile (downloads the copied link)
 * It starts the download with the default quality and closes, leaving the user where they were.
 */
class QuickDownloadActivity : ComponentActivity() {
    private var handled = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (intent?.action == Intent.ACTION_SEND) {
            handled = true
            download(Engine.extractUrl(intent.getStringExtra(Intent.EXTRA_TEXT)), fromClipboard = false)
        }
    }

    // Android 10+ only lets an app read the clipboard once its window has focus.
    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (!hasFocus || handled) return
        handled = true
        val clip = getSystemService(ClipboardManager::class.java).primaryClip
        val text = clip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(this)?.toString()
        download(Engine.extractUrl(text), fromClipboard = true)
    }

    private fun download(url: String?, fromClipboard: Boolean) {
        if (!Settings.acceptedTerms) {
            toast("Open VidGrab once and accept the terms first")
            startActivity(Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            finish()
            return
        }
        // Ad library pages: open the built-in ad browser so the user can pick ads.
        val lib = url?.let { AdLibraryActivity.libraryOf(it) }
        if (lib != null && !(lib == AdLibraryActivity.Library.META && url.contains("id="))) {
            startActivity(AdLibraryActivity.intent(this, lib, url).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            finish()
            return
        }
        when {
            url == null -> toast(if (fromClipboard) "Copy a video link first" else "No link found in what was shared")
            // Direct file links (PDF, ZIP, APK, MP4 file...) download as files.
            FileKinds.looksLikeFile(url) && Engine.siteOf(url) == null -> {
                if (fromClipboard) Settings.lastClipboardUrl = url
                if (DownloadService.startFile(this, url)) toast("VidGrab: downloading file")
            }
            // Everything else goes through yt-dlp, which knows 1,000+ video sites.
            else -> {
                if (fromClipboard) Settings.lastClipboardUrl = url
                if (DownloadService.startQuick(this, url)) {
                    toast("VidGrab: downloading from ${Engine.siteLabel(url)} (${Settings.quality.value.label})")
                }
            }
        }
        finish()
    }

    private fun toast(msg: String) = Toast.makeText(applicationContext, msg, Toast.LENGTH_SHORT).show()

    companion object {
        const val ACTION_CLIPBOARD = "com.vidgrab.app.DOWNLOAD_CLIPBOARD"
    }
}
