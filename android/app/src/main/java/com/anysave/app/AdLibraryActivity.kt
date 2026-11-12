package com.anysave.app

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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.DpSize
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
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
        /** Any website: open a page, play the video, AnySave lists what it finds. */
        WEB("Browser", "https://www.google.com"),
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
            AnySaveTheme {
                var web by remember { mutableStateOf<WebView?>(null) }
                var ads by remember { mutableStateOf<List<AdItem>>(emptyList()) }
                var showList by remember { mutableStateOf(false) }
                var loading by remember { mutableStateOf(true) }
                var pageUrl by remember { mutableStateOf(startUrl) }
                var address by remember { mutableStateOf(if (lib == Library.WEB) "" else startUrl) }
                // Ad src -> id of the job started for it, and the ids of the last "Download selected".
                val started = remember { mutableStateMapOf<String, String>() }
                var batch by remember { mutableStateOf<List<String>>(emptyList()) }
                val allJobs by Downloads.jobs.collectAsState()
                val jobIndex = remember(allJobs) { JobIndex(allJobs) }

                // Re-inject (harmless if already there) and read the list every 1.5 s.
                LaunchedEffect(web) {
                    val w = web ?: return@LaunchedEffect
                    while (true) {
                        w.evaluateJavascript(script, null)
                        w.evaluateJavascript("window.__anysaveList ? window.__anysaveList() : '[]'") { raw ->
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
                                        when (lib) {
                                            Library.TIKTOK -> "Tap play on the ads you want"
                                            Library.META -> "Scroll to load more ads"
                                            Library.WEB -> pageUrl.substringAfter("://").substringBefore('/').ifBlank { "Open any page with a video" }
                                        },
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
                            text = {
                                Text(when {
                                    lib == Library.WEB && ads.isEmpty() -> "Download"
                                    lib == Library.WEB -> plural(ads.size, "video") + " found"
                                    ads.isEmpty() -> "No ads yet"
                                    else -> plural(ads.size, "ad") + " found"
                                })
                            },
                            containerColor = if (ads.isEmpty() && lib != Library.WEB) MaterialTheme.colorScheme.surfaceVariant
                            else MaterialTheme.colorScheme.primary,
                            contentColor = if (ads.isEmpty() && lib != Library.WEB) MaterialTheme.colorScheme.onSurfaceVariant
                            else MaterialTheme.colorScheme.onPrimary,
                        )
                    }
                ) { pad ->
                    Column(Modifier.padding(pad).fillMaxSize()) {
                    if (lib == Library.WEB) {
                        OutlinedTextField(
                            value = address, onValueChange = { address = it },
                            placeholder = { Text("Search or type a web address") },
                            singleLine = true,
                            shape = RoundedCornerShape(24.dp),
                            modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp),
                            keyboardOptions = androidx.compose.foundation.text.KeyboardOptions(
                                keyboardType = androidx.compose.ui.text.input.KeyboardType.Uri,
                                imeAction = androidx.compose.ui.text.input.ImeAction.Go),
                            keyboardActions = androidx.compose.foundation.text.KeyboardActions(onGo = {
                                val t = address.trim()
                                val url = when {
                                    t.startsWith("http://") || t.startsWith("https://") -> t
                                    t.contains('.') && !t.contains(' ') -> "https://$t"
                                    else -> "https://www.google.com/search?q=" + java.net.URLEncoder.encode(t, "UTF-8")
                                }
                                web?.loadUrl(url)
                            }),
                        )
                    }
                    Box(Modifier.weight(1f).fillMaxWidth()) {
                        AndroidView(factory = { ctx ->
                            WebView(ctx).apply {
                                layoutParams = ViewGroup.LayoutParams(-1, -1)
                                settings.javaScriptEnabled = true
                                settings.domStorageEnabled = true
                                settings.mediaPlaybackRequiresUserGesture = true
                                settings.userAgentString = settings.userAgentString.replace("; wv", "")
                                CookieManager.getInstance().setAcceptThirdPartyCookies(this, true)
                                webViewClient = object : WebViewClient() {
                                    override fun onPageStarted(view: WebView, url: String?, favicon: android.graphics.Bitmap?) {
                                        loading = true
                                        url?.let { pageUrl = it }
                                    }
                                    override fun onPageFinished(view: WebView, url: String?) {
                                        loading = false
                                        url?.let { pageUrl = it }
                                        view.evaluateJavascript(script, null)
                                    }
                                    override fun doUpdateVisitedHistory(view: WebView, url: String?, isReload: Boolean) {
                                        url?.let { pageUrl = it } // single-page sites change URL without reloading
                                    }
                                }
                                loadUrl(startUrl)
                                web = this
                            }
                        }, modifier = Modifier.fillMaxSize())
                        if (loading) LinearProgressIndicator(Modifier.fillMaxWidth().align(Alignment.TopCenter))
                    }
                    }
                }

                // Fully open, so "Download selected" under the list is always in view.
                if (showList) ModalBottomSheet(onDismissRequest = { showList = false },
                    sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
                    AdList(ads, lib, jobIndex, started, batch,
                        onPage = { if (DownloadService.startQuick(this@AdLibraryActivity, pageUrl)) toastMsg("Downloading this page's video"); showList = false },
                        onDownloadMany = { list ->
                            val ids = list.map { it to download(it, lib, toast = false) }
                            ids.forEach { (ad, id) -> started[ad.src] = id }
                            batch = ids.map { it.second }
                        },
                    ) { item -> started[item.src] = download(item, lib, toast = false) }
                }
            }
        }
    }

    private fun toastMsg(m: String) = Toast.makeText(this, m, Toast.LENGTH_SHORT).show()

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
    /** Returns the new job's id, so the list can show its progress. */
    private fun download(item: AdItem, lib: Library, toast: Boolean = true): String {
        val title = when (item.site) {
            "meta" -> listOfNotNull(item.name ?: "Meta ad", item.id?.let { "ad $it" }).joinToString(" - ")
            "tiktok" -> listOfNotNull(item.name ?: "TikTok ad", item.id?.let { "ad $it" }).joinToString(" - ")
            else -> (item.name ?: "Video").take(80)
        }
        val metaById = item.site == "meta" && item.id != null
        val id = DownloadService.newId()
        DownloadService.start(
            this,
            Job(
                id = id,
                url = adJobUrl(item),
                title = title,
                format = if (metaById) "hd/b/bv*+ba" else "b/bv*+ba",
                sort = Engine.H264_SORT,
                audioOnly = false,
                qualityLabel = if (metaById) "HD" else "Best",
                site = when (lib) { Library.META -> "Facebook"; Library.TIKTOK -> "TikTok"; Library.WEB -> Engine.siteLabel(item.src) },
                thumbnail = item.poster,
                fixedName = true,
            )
        )
        if (toast) Toast.makeText(this, "Downloading: $title", Toast.LENGTH_SHORT).show()
        return id
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

/** The link a job for this ad downloads (Meta ads by Library ID, in HD), so rows match jobs by URL too. */
private fun adJobUrl(item: AdItem) =
    if (item.site == "meta" && item.id != null) "https://www.facebook.com/ads/library/?id=${item.id}" else item.src

@Composable
private fun AdList(
    ads: List<AdItem>, lib: AdLibraryActivity.Library, jobs: JobIndex, started: Map<String, String>, batch: List<String>,
    onPage: () -> Unit, onDownloadMany: (List<AdItem>) -> Unit, onDownload: (AdItem) -> Unit,
) {
    // Tick any number of ads (or Select all), then download just those.
    val selected = remember { mutableStateListOf<String>() }
    fun jobOf(ad: AdItem) = jobs.find(started[ad.src], adJobUrl(ad))
    val open = ads.filter { jobOf(it).isOpen() }
    LaunchedEffect(started.size) { selected.removeAll { it in started } }
    val noun = if (lib == AdLibraryActivity.Library.WEB) "video" else "ad"
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(if (lib == AdLibraryActivity.Library.WEB) "Videos on this page" else "Ads on this page",
                    style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                Text(
                    when (lib) {
                        AdLibraryActivity.Library.TIKTOK -> "Ads you've played. Play more to add them."
                        AdLibraryActivity.Library.META -> "Downloaded in HD where available."
                        AdLibraryActivity.Library.WEB -> "Play the video if it isn't listed yet."
                    },
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
            if (open.size > 1) SelectAllToggle(open.map { it.src }, selected)
        }
        if (lib == AdLibraryActivity.Library.WEB) {
            // Works on 1,000+ video sites (yt-dlp reads the page itself, in the best quality).
            OutlinedButton(onClick = onPage, modifier = Modifier.fillMaxWidth().padding(top = 12.dp)) {
                Icon(Icons.Outlined.Download, null, Modifier.size(18.dp)); Spacer(Modifier.width(8.dp))
                Text("Download this page's video", maxLines = 1)
            }
        }
        if (ads.isEmpty()) {
            Text(
                when (lib) {
                    AdLibraryActivity.Library.TIKTOK -> "No ads yet. Close this, tap play on an ad, then check again."
                    AdLibraryActivity.Library.META -> "No ad videos yet. Scroll the results so the ads load."
                    AdLibraryActivity.Library.WEB -> "No video files found yet. Play the video, or use the button above."
                },
                Modifier.padding(vertical = 24.dp), color = MaterialTheme.colorScheme.onSurfaceVariant
            )
        }
        // Rows keep their own rounded highlight, so the list itself is clipped to the same corners.
        LazyColumn(Modifier.heightIn(max = 440.dp).clip(RoundedCornerShape(12.dp)),
            contentPadding = PaddingValues(vertical = 12.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp)) {
            items(ads, key = { it.src }) { ad ->
                BatchRow(
                    title = ad.name ?: if (lib == AdLibraryActivity.Library.WEB) "Video" else "Ad",
                    subtitle = listOfNotNull(ad.duration?.let { fmtDuration(it.toDouble()) }, ad.id?.let { "ID $it" }).joinToString(" · "),
                    thumbnail = ad.poster, thumbSize = DpSize(64.dp, 64.dp),
                    job = jobOf(ad), selected = ad.src in selected,
                    onSelect = { if (it) selected += ad.src else selected -= ad.src },
                    noun = noun, onDownload = { onDownload(ad) },
                )
            }
        }
        if (ads.isNotEmpty()) {
            BatchButton(selected.size, noun, batch.mapNotNull { jobs[it] }, onDownloadSelected = {
                onDownloadMany(ads.filter { it.src in selected }); selected.clear()
            }, modifier = Modifier.padding(bottom = 12.dp))
        }
        MadeBy()
        Spacer(Modifier.height(12.dp))
    }
}
