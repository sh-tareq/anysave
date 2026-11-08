package com.vidgrab.app

import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.view.ViewGroup
import android.webkit.CookieManager
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.width
import androidx.compose.ui.unit.dp
import java.io.File

/**
 * Lets the user log in to Instagram / Facebook / TikTok / YouTube in a WebView,
 * then exports those cookies to cookies.txt for yt-dlp (needed for most Instagram posts).
 */
class LoginActivity : ComponentActivity() {

    @OptIn(ExperimentalMaterial3Api::class)
    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val site = Site.valueOf(intent.getStringExtra(EXTRA_SITE) ?: Site.INSTAGRAM.name)
        setContent {
            VidGrabTheme {
                Scaffold(topBar = {
                    TopAppBar(
                        title = { Text("Log in to ${site.label}") },
                        actions = {
                            Button(onClick = { saveAndFinish() }) { Text("Done") }
                            Spacer(Modifier.width(8.dp))
                        }
                    )
                }) { pad ->
                    Column(Modifier.padding(pad).fillMaxSize()) {
                        Text(
                            "Log in as usual, then tap Done. Your password stays in this page; VidGrab only keeps the login cookie on this phone.",
                            Modifier.padding(12.dp)
                        )
                        AndroidView(factory = { ctx ->
                            WebView(ctx).apply {
                                layoutParams = ViewGroup.LayoutParams(-1, -1)
                                settings.javaScriptEnabled = true
                                settings.domStorageEnabled = true
                                // Some sites refuse logins from embedded browsers; look like normal Chrome.
                                settings.userAgentString = settings.userAgentString.replace("; wv", "")
                                CookieManager.getInstance().setAcceptThirdPartyCookies(this, true)
                                webViewClient = WebViewClient()
                                loadUrl(site.loginUrl)
                            }
                        }, modifier = Modifier.fillMaxSize())
                    }
                }
            }
        }
    }

    private fun saveAndFinish() {
        exportCookies(this)
        setResult(RESULT_OK)
        finish()
    }

    enum class Site(val label: String, val loginUrl: String, val cookieUrl: String, val domain: String) {
        INSTAGRAM("Instagram", "https://www.instagram.com/accounts/login/", "https://www.instagram.com", ".instagram.com"),
        FACEBOOK("Facebook", "https://m.facebook.com/login/", "https://www.facebook.com", ".facebook.com"),
        TIKTOK("TikTok", "https://www.tiktok.com/login", "https://www.tiktok.com", ".tiktok.com"),
        YOUTUBE("YouTube", "https://accounts.google.com/ServiceLogin?service=youtube&continue=https://m.youtube.com", "https://www.youtube.com", ".youtube.com"),
    }

    companion object {
        const val EXTRA_SITE = "site"

        fun intent(ctx: Context, site: Site) =
            Intent(ctx, LoginActivity::class.java).putExtra(EXTRA_SITE, site.name)

        /** Writes the WebView's cookies for all supported sites as a Netscape cookies.txt. */
        fun exportCookies(ctx: Context) {
            val cm = CookieManager.getInstance()
            cm.flush()
            val lines = mutableListOf("# Netscape HTTP Cookie File")
            // WebView doesn't expose expiry dates; mark them valid for a year.
            val expiry = (System.currentTimeMillis() / 1000 + 365L * 24 * 3600).toString()
            for (site in Site.entries) {
                val raw = cm.getCookie(site.cookieUrl) ?: continue
                for (pair in raw.split(";")) {
                    val eq = pair.indexOf('=')
                    if (eq <= 0) continue
                    val name = pair.substring(0, eq).trim()
                    val value = pair.substring(eq + 1).trim()
                    lines += listOf(site.domain, "TRUE", "/", "TRUE", expiry, name, value).joinToString("\t")
                }
            }
            val f = Engine.cookieFile(ctx)
            if (lines.size > 1) f.writeText(lines.joinToString("\n") + "\n") else f.delete()
        }

        fun loggedInSites(ctx: Context): List<String> {
            val f: File = Engine.cookieFile(ctx)
            if (!f.exists()) return emptyList()
            val text = f.readText()
            return Site.entries.filter { text.contains(it.domain + "\t") }.map { it.label }
        }

        fun logOutAll(ctx: Context) {
            CookieManager.getInstance().removeAllCookies(null)
            CookieManager.getInstance().flush()
            Engine.cookieFile(ctx).delete()
        }
    }
}
