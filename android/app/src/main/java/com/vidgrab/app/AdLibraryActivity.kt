package com.vidgrab.app

import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.view.ViewGroup
import android.webkit.CookieManager
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ArrowBack
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Videocam
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import coil.compose.SubcomposeAsyncImage
import kotlinx.coroutines.delay
import org.json.JSONArray

/** An ad video found on the page by adlib.js. */
data class AdItem(
    val src: String,
    val id: String?,
    val name: String?,
    val site: String, // "meta" or "tiktok"
    val poster: String?,
    val duration: Int?,
)

/**
 * Built-in browser for Meta Ad Library and TikTok Creative Center.
 * adlib.js records ad videos as they load or play; the floating button lists them for download.
 */
class AdLibraryActivity : ComponentActivity() {

    enum class Library(val label: String, val home: String) {
        META("Meta Ad Library",
            "https://www.facebook.com/ads/library/?active_status=active&ad_type=all&country=ALL&media_type=video"),
        TIKTOK("TikTok Creative Center",
            "https://ads.tiktok.com/business/creativecenter/inspiration/topads/mobile/en?period=30"),
    }

    @OptIn(ExperimentalMaterial3Api::class)
    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        val lib = Library.valueOf(intent.getStringExtra(EXTRA_LIBRARY) ?: Library.META.name)
        val startUrl = intent.getStringExtra(EXTRA_URL) ?: lib.home
        val script = assets.open("adlib.js").bufferedReader().use { it.readText() }

        setContent {
            VidGrabTheme {
                var web by remember { mutableStateOf<WebView?>(null) }
                var ads by remember { mutableStateOf<List<AdItem>>(emptyList()) }
                var showList by remember { mutableStateOf(false) }
                var loading by remember { mutableStateOf(true) }
                val started = remember { mutableStateListOf<String>() }

                // Re-inject (harmless if already there) and read the list every 1.5 s.
                LaunchedEffect(web) {
                    val w = web ?: return@LaunchedEffect
                    while (true) {
                        w.evaluateJavascript(script, null)
                        w.evaluateJavascript("window.__vgList ? window.__vgList() : '[]'") { raw ->
                            ads = parseAds(raw)
                        }
                        delay(1500)
                    }
                }

                BackHandler { if (web?.canGoBack() == true) web?.goBack() else finish() }

                Scaffold(
                    topBar = {
                        TopAppBar(
                            title = {
                                Column {
                                    Text(lib.label, maxLines = 1)
                                    Text(
                                        if (lib == Library.TIKTOK) "Tap play on the ads you want" else "Scroll to load more ads",
                                        style = MaterialTheme.typography.bodySmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant
                                    )
                                }
                            },
                            navigationIcon = { IconButton(onClick = { finish() }) { Icon(Icons.Outlined.Close, "Close") } },
                            actions = {
                                IconButton(onClick = { web?.goBack() }, enabled = web?.canGoBack() == true) {
                                    Icon(Icons.Outlined.ArrowBack, "Back")
                                }
                                IconButton(onClick = { web?.reload() }) { Icon(Icons.Outlined.Refresh, "Reload") }
                            }
                        )
                    },
                    floatingActionButton = {
                        ExtendedFloatingActionButton(
                            onClick = { showList = true },
                            icon = {
                                if (ads.isEmpty()) Icon(Icons.Outlined.Videocam, null)
                                else DownloadingBadge(size = 24.dp)
                            },
                            text = { Text(if (ads.isEmpty()) "No ads yet" else "Ads found: ${ads.size}") },
                            containerColor = if (ads.isEmpty()) MaterialTheme.colorScheme.surfaceVariant
                            else MaterialTheme.colorScheme.primary,
                            contentColor = if (ads.isEmpty()) MaterialTheme.colorScheme.onSurfaceVariant
                            else MaterialTheme.colorScheme.onPrimary,
                        )
                    }
                ) { pad ->
                    Box(Modifier.padding(pad).fillMaxSize()) {
                        AndroidView(factory = { ctx ->
                            WebView(ctx).apply {
                                layoutParams = ViewGroup.LayoutParams(-1, -1)
                                settings.javaScriptEnabled = true
                                settings.domStorageEnabled = true
                                settings.mediaPlaybackRequiresUserGesture = true
                                settings.userAgentString = settings.userAgentString.replace("; wv", "")
                                CookieManager.getInstance().setAcceptThirdPartyCookies(this, true)
                                webViewClient = object : WebViewClient() {
                                    override fun onPageFinished(view: WebView, url: String?) {
                                        loading = false
                                        view.evaluateJavascript(script, null)
                                    }
                                }
                                loadUrl(startUrl)
                                web = this
                            }
                        }, modifier = Modifier.fillMaxSize())
                        if (loading) LinearProgressIndicator(Modifier.fillMaxWidth().align(Alignment.TopCenter))
                    }
                }

                if (showList) ModalBottomSheet(onDismissRequest = { showList = false }) {
                    AdList(ads, lib, started) { item -> download(item, lib); started += item.src }
                }
            }
        }
    }

    private fun parseAds(raw: String?): List<AdItem> = runCatching {
        // evaluateJavascript returns the JSON string as a quoted JS string literal
        val json = org.json.JSONTokener(raw).nextValue() as? String ?: return emptyList()
        val arr = JSONArray(json)
        List(arr.length()) {
            val o = arr.getJSONObject(it)
            fun s(k: String) = if (o.isNull(k)) null else o.optString(k).ifBlank { null }
            AdItem(
                src = o.getString("src"), id = s("id"), name = s("name"),
                site = o.optString("site", "meta"), poster = s("poster"),
                duration = if (o.isNull("duration")) null else o.optInt("duration"),
            )
        }
    }.getOrDefault(emptyList())

    /** Meta: by Library ID through yt-dlp (HD). TikTok: the ad's video file directly. */
    private fun download(item: AdItem, lib: Library) {
        val title = when {
            item.site == "meta" -> listOfNotNull(item.name ?: "Meta ad", item.id?.let { "ad $it" }).joinToString(" - ")
            else -> listOfNotNull(item.name ?: "TikTok ad", item.id?.let { "ad $it" }).joinToString(" - ")
        }
        val metaById = item.site == "meta" && item.id != null
        DownloadService.start(
            this,
            Job(
                id = System.currentTimeMillis().toString() + (0..999).random(),
                url = if (metaById) "https://www.facebook.com/ads/library/?id=${item.id}" else item.src,
                title = title,
                format = if (metaById) "hd/b/bv*+ba" else "b/bv*+ba",
                sort = Engine.H264_SORT,
                audioOnly = false,
                qualityLabel = if (metaById) "HD" else "Original",
                site = if (lib == Library.META) "Facebook" else "TikTok",
                thumbnail = item.poster,
                fixedName = true,
            )
        )
        Toast.makeText(this, "Downloading: $title", Toast.LENGTH_SHORT).show()
    }

    companion object {
        const val EXTRA_LIBRARY = "library"
        const val EXTRA_URL = "url"

        fun intent(ctx: Context, lib: Library, url: String? = null) =
            Intent(ctx, AdLibraryActivity::class.java).putExtra(EXTRA_LIBRARY, lib.name).putExtra(EXTRA_URL, url)

        /** Which ad library a link belongs to, if any. */
        fun libraryOf(url: String): Library? {
            val u = runCatching { android.net.Uri.parse(url) }.getOrNull() ?: return null
            val host = u.host.orEmpty().lowercase()
            val path = u.path.orEmpty()
            return when {
                host.endsWith("facebook.com") && path.startsWith("/ads/library") -> Library.META
                host == "ads.tiktok.com" && path.contains("/creativecenter") -> Library.TIKTOK
                else -> null
            }
        }
    }
}

@Composable
private fun AdList(ads: List<AdItem>, lib: AdLibraryActivity.Library, started: List<String>, onDownload: (AdItem) -> Unit) {
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text("Ads on this page", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                Text(
                    if (lib == AdLibraryActivity.Library.TIKTOK) "Ads you've played. Play more to add them."
                    else "Downloaded in HD where available.",
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
            if (ads.size > 1) Button(onClick = { ads.filter { it.src !in started }.forEach(onDownload) }) {
                Text("Download all")
            }
        }
        if (ads.isEmpty()) {
            Text(
                if (lib == AdLibraryActivity.Library.TIKTOK) "No ads yet. Close this, tap play on an ad, then check again."
                else "No ad videos yet. Scroll the results so the ads load.",
                Modifier.padding(vertical = 32.dp), color = MaterialTheme.colorScheme.onSurfaceVariant
            )
        }
        LazyColumn(Modifier.heightIn(max = 520.dp), contentPadding = PaddingValues(vertical = 12.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp)) {
            items(ads, key = { it.src }) { ad ->
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Box(Modifier.size(64.dp).clip(RoundedCornerShape(10.dp))
                        .background(MaterialTheme.colorScheme.surfaceVariant), contentAlignment = Alignment.Center) {
                        val icon = @Composable { Icon(Icons.Outlined.Videocam, null, tint = MaterialTheme.colorScheme.outline) }
                        if (ad.poster != null) SubcomposeAsyncImage(model = ad.poster, contentDescription = null,
                            contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize(),
                            error = { icon() }, loading = { icon() })
                        else icon()
                    }
                    Spacer(Modifier.width(12.dp))
                    Column(Modifier.weight(1f)) {
                        Text(ad.name ?: "Ad", fontWeight = FontWeight.Medium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(
                            listOfNotNull(ad.id?.let { "ID $it" }, ad.duration?.let { fmtDuration(it.toDouble()) }).joinToString(" · "),
                            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }
                    val done = ad.src in started
                    FilledTonalButton(onClick = { onDownload(ad) }, enabled = !done) {
                        Icon(Icons.Outlined.Download, null, Modifier.size(18.dp))
                        Spacer(Modifier.width(4.dp))
                        Text(if (done) "Started" else "Get")
                    }
                }
            }
        }
        MadeBy()
        Spacer(Modifier.height(12.dp))
    }
}
