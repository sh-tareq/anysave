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
    /** A copied link we noticed while auto-download is off: offer it on Home. */
    var clipboardLink by mutableStateOf<String?>(null)
    val messages = MutableSharedFlow<String>(extraBufferCapacity = 4)

    private val ctx get() = getApplication<Application>()

    init {
        viewModelScope.launch {
            Engine.state.first { it == Engine.State.READY }
            ytdlpVersion = withContext(Dispatchers.IO) { YoutubeDL.getInstance().versionName(ctx) }
        }
    }

    fun lookUp(newUrl: String? = null) {
        newUrl?.let { url = it }
        val link = Engine.extractUrl(url) ?: run { error = "Paste a video link first."; return }
        url = link
        info = null; error = null; loading = true; tab = MainActivity.TAB_HOME
        viewModelScope.launch {
            try {
                Engine.state.first { it != Engine.State.STARTING } // wait for setup / update
                if (Engine.state.value == Engine.State.FAILED) throw Exception("Setup failed: ${Engine.startError}")
                val i = Engine.info(ctx, link)
                info = i
                // Pre-select the option matching the default quality
                val q = Settings.quality.value
                selected = when {
                    q == Quality.AUDIO -> i.options.indexOfFirst { it.audioOnly }
                    q.height != null -> i.options.indexOfFirst { it.label.startsWith("${q.height}p") }
                    else -> 0
                }.coerceAtLeast(0)
            } catch (e: Exception) {
                error = e.message ?: "Couldn't read this link"
            } finally {
                loading = false
            }
        }
    }

    fun download() {
        val i = info ?: return
        DownloadService.startFromInfo(ctx, i, i.options[selected])
        messages.tryEmit("Download started")
        info = null; url = ""
    }

    fun quickDownload(link: String) {
        if (DownloadService.startQuick(ctx, link)) messages.tryEmit("Downloading copied link (${Settings.quality.value.label})")
        clipboardLink = null
    }

    /** Called when the app window gets focus: Android only allows clipboard reads then. */
    fun checkClipboard(context: Context) {
        if (!Settings.acceptedTerms) return
        val clip = runCatching { context.getSystemService(ClipboardManager::class.java).primaryClip }.getOrNull()
        val text = clip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(context)?.toString()
        val link = Engine.extractUrl(text) ?: return
        if (Engine.siteOf(link) == null || link == Settings.lastClipboardUrl) return
        Settings.lastClipboardUrl = link
        if (Settings.autoClipboard.value) quickDownload(link) else clipboardLink = link
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
        setContent { VidGrabTheme { AppShell(vm) } }
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
                Engine.extractUrl(intent.getStringExtra(Intent.EXTRA_TEXT))?.let { vm.lookUp(it) }
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
        const val TAB_SETTINGS = 2
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
        title = { Text("Welcome to VidGrab", fontWeight = FontWeight.Bold) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(14.dp)) {
                Tip(Icons.Outlined.ContentCopy, "Copy a link, open VidGrab",
                    "VidGrab spots the copied video link and starts downloading by itself.")
                Tip(Icons.Outlined.Share, "Share → Download",
                    "In YouTube, Instagram, TikTok or Facebook tap Share, then VidGrab \"Download\". You stay in the app you were using.")
                Tip(Icons.Outlined.TouchApp, "Quick Settings tile",
                    "Add the VidGrab tile to your quick settings: copy a link, swipe down, tap it.")
                CopyrightNotice()
                Row(
                    Modifier.fillMaxWidth().clip(androidx.compose.foundation.shape.RoundedCornerShape(10.dp))
                        .clickable { agreed = !agreed },
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Checkbox(checked = agreed, onCheckedChange = { agreed = it })
                    Text("I'll only download videos I have the right to save, and I accept the legal notice.",
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
