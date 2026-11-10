package com.vidgrab.app

import android.Manifest
import android.app.Application
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.viewModels
import androidx.compose.foundation.layout.*
import androidx.compose.ui.draw.clip
import androidx.compose.foundation.clickable
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Transform
import androidx.compose.material.icons.outlined.Transform
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.Home
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material.icons.outlined.TouchApp
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.yausername.youtubedl_android.YoutubeDL
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

class MainViewModel(app: Application) : AndroidViewModel(app) {
    var tab by mutableIntStateOf(MainActivity.TAB_HOME)
    var url by mutableStateOf("")
    var loading by mutableStateOf(false)
    var error by mutableStateOf<String?>(null)
    var info by mutableStateOf<Engine.Info?>(null)
    var selected by mutableIntStateOf(0)
    var updating by mutableStateOf(false)
    var ytdlpVersion by mutableStateOf<String?>(null)
    /** Copied links we noticed while auto-download is off: offer them on Home. */
    var clipboardLinks by mutableStateOf<List<String>>(emptyList())
    /** Job started from the video card, so its button can show the progress. */
    var infoJobId by mutableStateOf<String?>(null)

    // Playlist or channel found by the lookup, its selection, and the jobs started from it.
    var playlist by mutableStateOf<Engine.Playlist?>(null)
    var playlistLoading by mutableStateOf(false)
    val playlistSelected = mutableStateListOf<String>()
    /** Entry link -> job id. */
    val playlistStarted = mutableStateMapOf<String, String>()
    var playlistBatch by mutableStateOf<List<String>>(emptyList())
    var playlistQuality by mutableIntStateOf(0)
    private var lookupJob: kotlinx.coroutines.Job? = null
    private var playlistJob: kotlinx.coroutines.Job? = null
    val messages = MutableSharedFlow<String>(extraBufferCapacity = 4)

    private val ctx get() = getApplication<Application>()

    init {
        viewModelScope.launch {
            Engine.state.first { it == Engine.State.READY }
            ytdlpVersion = withContext(Dispatchers.IO) { YoutubeDL.getInstance().versionName(ctx) }
        }
    }

    /** Set when a link belongs to an ad library; the screen opens the in-app ad browser for it. */
    var openAdLibrary by mutableStateOf<Pair<AdLibraryActivity.Library, String>?>(null)
    /** The failed link is a normal web page: offer to open it in the built-in browser. */
    var errorIsWebPage by mutableStateOf(false)

    fun lookUp(newUrl: String? = null) {
        newUrl?.let { url = it }
        val link = Engine.extractUrl(url) ?: run { error = "Paste a video or file link first."; return }
        url = link
        // Ad library pages list many ads: open them in the built-in ad browser instead.
        // (A single Meta ad link with ?id= still goes through the normal lookup, in HD.)
        AdLibraryActivity.libraryOf(link)?.let { lib ->
            if (!(lib == AdLibraryActivity.Library.META && link.contains("id="))) {
                openAdLibrary = lib to link; return
            }
        }
        // A direct file link (PDF, ZIP, APK, MP4 file...) downloads straight away.
        if (FileKinds.looksLikeFile(link) && Engine.siteOf(link) == null) {
            if (DownloadService.startFile(ctx, link)) messages.tryEmit("Downloading file")
            url = ""; tab = MainActivity.TAB_DOWNLOADS
            return
        }
        clearLookup()
        loading = true; tab = MainActivity.TAB_HOME
        // A YouTube playlist or channel: list its videos (a watch link with &list= shows both).
        val ytList = Engine.youtubeList(link)
        if (ytList != null && !Engine.isYoutubeVideo(link)) {
            loadPlaylist(ytList, onlyList = true)
            return
        }
        if (ytList != null) loadPlaylist(ytList, onlyList = false)
        lookupJob = viewModelScope.launch {
            try {
                Engine.state.first { it != Engine.State.STARTING } // wait for setup / update
                if (Engine.state.value == Engine.State.FAILED) throw Exception("Setup failed: ${Engine.startError}")
                // Unknown site: ask the server what the link is first (a quick HEAD request).
                // Files download directly; only web pages go to yt-dlp, which would otherwise
                // read a big file just to find out it isn't a page.
                val probe = if (Engine.siteOf(link) == null) withContext(Dispatchers.IO) { FileKinds.probe(link) } else null
                if (probe?.isFile == true) {
                    DownloadService.startFile(ctx, link, probe.name, probe.mime)
                    messages.tryEmit("Downloading ${probe.name ?: "file"}")
                    url = ""; tab = MainActivity.TAB_DOWNLOADS
                    return@launch
                }
                val i = try {
                    Engine.info(ctx, link)
                } catch (e: Exception) {
                    // Only an unsupported page goes to the built-in browser; a login error is shown as is.
                    val m = e.message.orEmpty()
                    errorIsWebPage = probe != null && !needsLogin(m)
                    throw e
                }
                info = i
                // Another site answered with a list: show the list instead of its first video.
                if (i.isPlaylist && ytList == null) loadPlaylist(link, onlyList = false, replacesInfo = true)
                // Pre-select the option matching the default quality
                val q = Settings.quality.value
                selected = when {
                    q == Quality.AUDIO -> i.options.indexOfFirst { it.audioOnly }
                    q.height != null -> i.options.indexOfFirst { it.label.startsWith("${q.height}p") }
                    else -> 0
                }.coerceAtLeast(0)
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                error = e.message ?: "Couldn't read this link"
            } finally {
                if (isActive) loading = false
            }
        }
    }

    /** Forget the last lookup (video card, playlist, error). */
    fun clearLookup() {
        lookupJob?.cancel(); lookupJob = null
        playlistJob?.cancel(); playlistJob = null
        info = null; error = null; errorIsWebPage = false; infoJobId = null; loading = false
        playlist = null; playlistLoading = false
        playlistSelected.clear(); playlistStarted.clear(); playlistBatch = emptyList()
    }

    /**
     * Lists a playlist's videos. [onlyList]: the link is just a list, so its errors are shown;
     * [replacesInfo]: a list on another site hides the single-video card once the list is in.
     */
    private fun loadPlaylist(listUrl: String, onlyList: Boolean, replacesInfo: Boolean = false) {
        playlistLoading = true
        val q = Settings.quality.value
        playlistQuality = when (q) {
            Quality.AUDIO -> Engine.batchOptions.indexOfFirst { it.audioOnly }
            Quality.P360 -> Engine.batchOptions.indexOfFirst { it.label.startsWith("480p") }
            else -> q.height?.let { h -> Engine.batchOptions.indexOfFirst { it.label.startsWith("${h}p") } } ?: 0
        }.coerceAtLeast(0)
        playlistJob?.cancel()
        playlistJob = viewModelScope.launch {
            try {
                Engine.state.first { it != Engine.State.STARTING }
                if (Engine.state.value == Engine.State.FAILED) throw Exception("Setup failed: ${Engine.startError}")
                val p = Engine.playlist(ctx, listUrl)
                when {
                    p.entries.size >= 2 || (onlyList && p.entries.isNotEmpty()) -> {
                        playlist = p
                        if (replacesInfo) info = null
                    }
                    onlyList -> error = "No videos found in this playlist or channel."
                }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                if (onlyList) error = e.message ?: "Couldn't read this link"
            } finally {
                // A newer lookup cancelled this one: leave its state alone.
                if (isActive) {
                    playlistLoading = false
                    if (onlyList) loading = false
                }
            }
        }
    }

    fun download() {
        val i = info ?: return
        infoJobId = DownloadService.startFromInfo(ctx, i, i.options[selected])
    }

    /** "Download N selected videos" under a playlist: every ticked video, in the chosen quality. */
    fun downloadPlaylist(entries: List<Engine.Entry>) {
        val opt = Engine.batchOptions[playlistQuality]
        val jobs = entries.map { DownloadService.entryJob(it, opt) }
        DownloadService.startMany(ctx, jobs)
        jobs.forEach { playlistStarted[it.url] = it.id }
        playlistBatch = jobs.map { it.id }
        playlistSelected.clear()
    }

    fun downloadEntry(entry: Engine.Entry) {
        val job = DownloadService.entryJob(entry, Engine.batchOptions[playlistQuality])
        DownloadService.start(ctx, job)
        playlistStarted[entry.url] = job.id
        playlistSelected.remove(entry.url)
    }

    /** Several links pasted, copied or shared at once: each one like a single quick download. */
    fun downloadAll(links: List<String>) {
        val started = links.count { DownloadService.startLink(ctx, it) }
        val skipped = links.size - started
        messages.tryEmit(when {
            started == 0 -> "Nothing new to download"
            skipped > 0 -> "Downloading ${plural(started, "link")} · ${plural(skipped, "link")} skipped"
            else -> "Downloading ${plural(started, "link")}"
        })
        clipboardLinks = emptyList()
        if (Engine.extractUrls(url).size > 1) url = ""
    }

    fun quickDownload(link: String) {
        if (FileKinds.looksLikeFile(link) && Engine.siteOf(link) == null) {
            if (DownloadService.startFile(ctx, link)) messages.tryEmit("Downloading file")
            clipboardLinks = emptyList(); return
        }
        AdLibraryActivity.libraryOf(link)?.let { lib ->
            if (!(lib == AdLibraryActivity.Library.META && link.contains("id="))) { openAdLibrary = lib to link; return }
        }
        // A playlist or channel: list its videos so the user picks which ones.
        if (Engine.youtubeList(link) != null && !Engine.isYoutubeVideo(link)) { clipboardLinks = emptyList(); lookUp(link); return }
        if (DownloadService.startQuick(ctx, link)) messages.tryEmit("Downloading copied link (${Settings.quality.value.label})")
        clipboardLinks = emptyList()
    }

    fun needsLogin(msg: String) = Regex("log(ged)?[ -]?in|sign in|cookies|private|account", RegexOption.IGNORE_CASE).containsMatchIn(msg)

    /** Called when the app window gets focus: Android only allows clipboard reads then. */
    fun checkClipboard(context: Context) {
        if (!Settings.acceptedTerms) return
        val clip = runCatching { context.getSystemService(ClipboardManager::class.java).primaryClip }.getOrNull()
        val text = clip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(context)?.toString()
        // Ordinary web links (articles, shops...) are ignored; videos and files are offered.
        val links = Engine.extractUrls(text).filter { Engine.siteOf(it) != null || FileKinds.looksLikeFile(it) }
        if (links.isEmpty()) return
        val key = links.joinToString(" ")
        if (key == Settings.lastClipboardUrl) return
        Settings.lastClipboardUrl = key
        val auto = Settings.autoClipboard.value
        when {
            links.size > 1 -> if (auto) downloadAll(links) else clipboardLinks = links
            Engine.siteOf(links[0]) != null && auto -> quickDownload(links[0])
            else -> clipboardLinks = links
        }
    }

    fun updateDownloader() {
        updating = true
        viewModelScope.launch {
            val msg = withContext(Dispatchers.IO) { Engine.updateNow(ctx) }
            ytdlpVersion = withContext(Dispatchers.IO) { YoutubeDL.getInstance().versionName(ctx) }
            updating = false
            messages.tryEmit(msg)
        }
    }
}

class MainActivity : ComponentActivity() {
    private val vm: MainViewModel by viewModels()
    private var skipClipboardOnce = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        if (savedInstanceState == null) handleIntent(intent)
        setContent { AnySaveTheme { AppShell(vm) } }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleIntent(intent)
    }

    private fun handleIntent(intent: Intent?) {
        when {
            intent?.action == Intent.ACTION_SEND -> {
                // Share → "Choose quality"
                skipClipboardOnce = true
                val links = Engine.extractUrls(intent.getStringExtra(Intent.EXTRA_TEXT))
                // Several links: put them in the field, which then offers "Download all N links".
                if (links.size > 1) { vm.clearLookup(); vm.url = links.joinToString(" "); vm.tab = TAB_HOME }
                else links.firstOrNull()?.let { vm.lookUp(it) }
            }
            intent?.hasExtra(EXTRA_TAB) == true -> vm.tab = intent.getIntExtra(EXTRA_TAB, TAB_HOME)
        }
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (!hasFocus) return
        if (skipClipboardOnce) { skipClipboardOnce = false; return }
        vm.checkClipboard(this)
    }

    companion object {
        const val EXTRA_TAB = "tab"
        const val TAB_HOME = 0
        const val TAB_DOWNLOADS = 1
        const val TAB_CONVERT = 2
        const val TAB_SETTINGS = 3
    }
}

private data class NavItem(val label: String, val icon: ImageVector, val selectedIcon: ImageVector)

@Composable
fun AppShell(vm: MainViewModel) {
    val ctx = LocalContext.current
    val jobs by Downloads.jobs.collectAsStateWithLifecycle()
    val snackbar = remember { SnackbarHostState() }
    val activeCount = jobs.count { it.active }
    var showWelcome by remember { mutableStateOf(!Settings.onboarded || !Settings.acceptedTerms) }

    LaunchedEffect(Unit) { vm.messages.collect { snackbar.showSnackbar(it) } }

    // Back on another tab goes to Home first instead of closing the app
    BackHandler(enabled = vm.tab != MainActivity.TAB_HOME) { vm.tab = MainActivity.TAB_HOME }

    // Open the built-in ad browser when a link belongs to an ad library
    LaunchedEffect(vm.openAdLibrary) {
        vm.openAdLibrary?.let { (lib, link) ->
            ctx.startActivity(AdLibraryActivity.intent(ctx, lib, link))
            vm.openAdLibrary = null; vm.url = ""
        }
    }

    val notifPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {}
    LaunchedEffect(Unit) {
        if (Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(ctx, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) notifPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
    }
    // Android 8/9 need storage permission to write into Downloads
    val storagePermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {}
    LaunchedEffect(Unit) {
        if (Build.VERSION.SDK_INT <= 28 &&
            ContextCompat.checkSelfPermission(ctx, Manifest.permission.WRITE_EXTERNAL_STORAGE) != PackageManager.PERMISSION_GRANTED
        ) storagePermission.launch(Manifest.permission.WRITE_EXTERNAL_STORAGE)
    }

    val items = listOf(
        NavItem("Home", Icons.Outlined.Home, Icons.Filled.Home),
        NavItem("Downloads", Icons.Outlined.Download, Icons.Filled.Download),
        NavItem("Convert", Icons.Outlined.Transform, Icons.Filled.Transform),
        NavItem("Settings", Icons.Outlined.Settings, Icons.Filled.Settings),
    )

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        bottomBar = {
            NavigationBar {
                items.forEachIndexed { i, item ->
                    NavigationBarItem(
                        selected = vm.tab == i,
                        onClick = { vm.tab = i },
                        label = { Text(item.label) },
                        icon = {
                            BadgedBox(badge = {
                                if (i == MainActivity.TAB_DOWNLOADS && activeCount > 0) Badge { Text("$activeCount") }
                            }) {
                                BouncyIcon(vm.tab == i) { Icon(if (vm.tab == i) item.selectedIcon else item.icon, item.label) }
                            }
                        }
                    )
                }
            }
        }
    ) { pad ->
        Box(Modifier.padding(pad).fillMaxSize()) {
            when (vm.tab) {
                MainActivity.TAB_HOME -> HomeScreen(vm, jobs)
                MainActivity.TAB_DOWNLOADS -> DownloadsScreen(jobs)
                MainActivity.TAB_CONVERT -> ConvertScreen(jobs) { vm.tab = MainActivity.TAB_DOWNLOADS }
                else -> SettingsScreen(vm)
            }
        }
    }

    if (showWelcome) WelcomeDialog {
        Settings.onboarded = true; Settings.acceptedTerms = true; showWelcome = false
    }
}

@Composable
private fun WelcomeDialog(onAccept: () -> Unit) {
    var agreed by remember { mutableStateOf(false) }
    var showLegal by remember { mutableStateOf(false) }
    AlertDialog(
        // Can't be dismissed without agreeing: nothing downloads before that.
        onDismissRequest = {},
        properties = androidx.compose.ui.window.DialogProperties(dismissOnClickOutside = false, dismissOnBackPress = false),
        confirmButton = { Button(onClick = onAccept, enabled = agreed) { Text("Agree & continue") } },
        icon = { AnimatedLogo(72.dp) },
        title = { Text("Welcome to AnySave", fontWeight = FontWeight.Bold) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(14.dp)) {
                Tip(Icons.Outlined.ContentCopy, "Copy a link, open AnySave",
                    "AnySave picks up a copied video or file link and starts downloading by itself.")
                Tip(Icons.Outlined.Share, "Share → Download",
                    "In any app or browser tap Share, then AnySave \"Download\". You stay in the app you were using.")
                Tip(Icons.Outlined.TouchApp, "Quick Settings tile",
                    "Add the AnySave tile to your quick settings: copy a link, swipe down, tap it.")
                CopyrightNotice()
                Row(
                    Modifier.fillMaxWidth().clip(androidx.compose.foundation.shape.RoundedCornerShape(10.dp))
                        .clickable { agreed = !agreed },
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Checkbox(checked = agreed, onCheckedChange = { agreed = it })
                    Text("I'll only download videos and files I have the right to save, and I accept the legal notice.",
                        style = MaterialTheme.typography.bodySmall)
                }
                TextButton(onClick = { showLegal = true }, contentPadding = PaddingValues(0.dp)) {
                    Text("Read the full legal notice")
                }
                MadeBy()
            }
        }
    )
    if (showLegal) LegalDialog { showLegal = false }
}

@Composable
fun Tip(icon: ImageVector, title: String, body: String) {
    Row(verticalAlignment = Alignment.Top) {
        Icon(icon, null, tint = MaterialTheme.colorScheme.primary, modifier = Modifier.padding(top = 2.dp).size(22.dp))
        Spacer(Modifier.width(12.dp))
        Column {
            Text(title, fontWeight = FontWeight.SemiBold)
            Text(body, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}
