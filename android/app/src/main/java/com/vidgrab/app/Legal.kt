package com.vidgrab.app

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Gavel
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties

/** Bump when the legal text changes in a way users must agree to again. */
const val TERMS_VERSION = 2

/** Same content as LEGAL.md in the repository. */
val LegalSections = listOf(
    "What AnySave is for" to
        "AnySave is a personal tool for saving videos and files you are allowed to save, and for converting your own media: " +
        "content you made yourself, content whose owner has given you permission, content under a licence that allows " +
        "downloading (such as Creative Commons) or in the public domain, and uses your local law allows, such as personal " +
        "backup or fair use.",
    "Copyright warning" to
        "Most videos on YouTube, Facebook, Instagram and TikTok are protected by copyright and belong to the people who " +
        "made them. Downloading, copying, re-uploading, sharing, selling or publicly showing someone else's video without " +
        "permission can infringe copyright, and in many countries it can be a civil or criminal offence. It may also break " +
        "the site's terms of service and get your account suspended.\n\nCopyright laws differ by country. If you are not " +
        "sure whether you may download something, don't. Ask the owner, or ask a lawyer.",
    "Your responsibility" to
        "By using AnySave you agree that you are solely responsible for what you download and how you use it; that you will " +
        "only download content you have the right to download; that you will not use AnySave to infringe anyone's rights " +
        "or to get around paywalls, DRM or other access restrictions; and that you will follow each website's terms of service.",
    "No warranty, no liability" to
        "AnySave is provided \"as is\", free of charge, without any warranty. Websites change often and AnySave may stop " +
        "working at any time. To the fullest extent the law allows, the author ($AUTHOR) is not liable for any loss, " +
        "damage, claim or legal consequence arising from your use or misuse of AnySave or of any downloaded content.",
    "Not affiliated" to
        "AnySave is an independent project. It is not affiliated with, endorsed by or sponsored by YouTube or Google, " +
        "Meta (Facebook, Instagram), TikTok or ByteDance. Their names are used only to say which websites AnySave works " +
        "with. All trademarks belong to their owners.",
    "Privacy" to
        "No account, no analytics, no ads, no tracking. The author's servers receive nothing from you. Downloads go " +
        "straight from the website to your phone, and conversions happen on your phone. To tell a file from a web page, " +
        "AnySave may ask that website about the link first. If you log in to a site in AnySave, the login stays on this " +
        "phone and is sent only to that same website. AnySave contacts GitHub to update its downloader (yt-dlp).",
    "Copyright complaints" to
        "If you believe AnySave is being used to infringe your rights, contact the author. AnySave hosts no content: " +
        "downloaded copies exist only on the user's own device.",
    "Open-source software" to
        "AnySave is built on yt-dlp (Unlicense), youtubedl-android (GPL-3.0), FFmpeg (LGPL/GPL), Python (PSF License), " +
        "QuickJS (MIT), Jetpack Compose and AndroidX (Apache-2.0) and Coil (Apache-2.0).\n\nBecause it includes " +
        "youtubedl-android, the AnySave Android app is distributed under the GNU General Public License v3.0. You may get " +
        "its complete source code from the author, and you may use, study, change and share it under that licence.",
)

/** Full-screen legal notice, opened from Settings → About and from the first-run agreement. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LegalDialog(onClose: () -> Unit) {
    Dialog(onDismissRequest = onClose, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(Modifier.fillMaxSize()) {
            Column {
                TopAppBar(
                    title = { Text("Legal & disclaimer") },
                    navigationIcon = { IconButton(onClick = onClose) { Icon(Icons.Outlined.Close, "Close") } },
                )
                Column(
                    Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 20.dp, vertical = 8.dp),
                    verticalArrangement = Arrangement.spacedBy(18.dp)
                ) {
                    Text("Last updated 9 November 2026", style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant)
                    LegalSections.forEach { (title, body) ->
                        Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                            Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold,
                                color = if (title == "Copyright warning") MaterialTheme.colorScheme.error
                                else MaterialTheme.colorScheme.onSurface)
                            Text(body, style = MaterialTheme.typography.bodyMedium)
                        }
                    }
                    MadeBy()
                    Spacer(Modifier.height(24.dp))
                }
            }
        }
    }
}

/** Short warning card, used on the welcome screen and in Settings. */
@Composable
fun CopyrightNotice(modifier: Modifier = Modifier) {
    Card(modifier, colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.errorContainer)) {
        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.Top) {
            Icon(Icons.Outlined.Gavel, null, tint = MaterialTheme.colorScheme.onErrorContainer, modifier = Modifier.size(20.dp))
            Spacer(Modifier.width(10.dp))
            Text(
                "Only download videos and files you own or have permission to save. Downloading or reusing copyrighted " +
                    "content without permission can be illegal. You are responsible for how you use AnySave.",
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onErrorContainer
            )
        }
    }
}
