package com.vidgrab.app

import android.content.ClipboardManager
import android.content.Intent
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity

/**
 * Invisible screen behind the one-tap paths:
 *  - Share → "Download" in YouTube / Instagram / TikTok / Facebook
 *  - the "AnySave" Quick Settings tile (downloads the copied link)
 * It starts the download with the default quality and closes, leaving the user where they were.
 */
class QuickDownloadActivity : ComponentActivity() {
    private var handled = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (intent?.action == Intent.ACTION_SEND) {
            handled = true
            download(Engine.extractUrls(intent.getStringExtra(Intent.EXTRA_TEXT)), fromClipboard = false)
        }
    }

    // Android 10+ only lets an app read the clipboard once its window has focus.
    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (!hasFocus || handled) return
        handled = true
        val clip = getSystemService(ClipboardManager::class.java).primaryClip
        val text = clip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(this)?.toString()
        download(Engine.extractUrls(text), fromClipboard = true)
    }

    private fun download(links: List<String>, fromClipboard: Boolean) {
        val url = links.firstOrNull()
        if (!Settings.acceptedTerms) {
            toast("Open AnySave once and accept the terms first")
            startActivity(Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            finish()
            return
        }
        // Ad library pages: open the built-in ad browser so the user can pick ads.
        val lib = url?.takeIf { links.size == 1 }?.let { AdLibraryActivity.libraryOf(it) }
        if (lib != null && !(lib == AdLibraryActivity.Library.META && url.contains("id="))) {
            startActivity(AdLibraryActivity.intent(this, lib, url).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            finish()
            return
        }
        // A playlist or channel: open AnySave, which lists its videos to pick from.
        if (url != null && links.size == 1 && Engine.youtubeList(url) != null && !Engine.isYoutubeVideo(url)) {
            startActivity(Intent(this, MainActivity::class.java).setAction(Intent.ACTION_SEND)
                .putExtra(Intent.EXTRA_TEXT, url).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            finish()
            return
        }
        when {
            // Several links at once: each like a single quick download.
            links.size > 1 -> {
                if (fromClipboard) Settings.lastClipboardUrl = links.joinToString(" ")
                val n = links.count { DownloadService.startLink(this, it) }
                if (n > 0) toast("AnySave: downloading ${plural(n, "link")}")
            }
            url == null -> toast(if (fromClipboard) "Copy a video or file link first" else "No link found in what was shared")
            // Direct file links (PDF, ZIP, APK, MP4 file...) download as files.
            FileKinds.looksLikeFile(url) && Engine.siteOf(url) == null -> {
                if (fromClipboard) Settings.lastClipboardUrl = url
                if (DownloadService.startFile(this, url)) toast("AnySave: downloading file")
            }
            // Everything else goes through yt-dlp, which knows 1,000+ video sites.
            else -> {
                if (fromClipboard) Settings.lastClipboardUrl = url
                if (DownloadService.startQuick(this, url)) {
                    toast("AnySave: downloading from ${Engine.siteLabel(url)} (${Settings.quality.value.label})")
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
