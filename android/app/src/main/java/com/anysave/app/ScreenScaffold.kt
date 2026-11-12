package com.anysave.app

import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

/**
 * Every tab uses this: a fixed, compact top bar. It never scrolls, collapses or resizes;
 * only the content below it scrolls.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ScreenScaffold(
    title: String,
    subtitle: String? = null,
    leading: (@Composable () -> Unit)? = null,
    actions: @Composable RowScope.() -> Unit = {},
    floatingActionButton: @Composable () -> Unit = {},
    content: @Composable (PaddingValues) -> Unit,
) {
    Scaffold(
        // The app shell around the tabs already handles the status and navigation bars.
        contentWindowInsets = WindowInsets(0),
        topBar = {
            Column {
                TopAppBar(
                    windowInsets = WindowInsets(0),
                    colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.surface),
                    title = {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            if (leading != null) { leading(); Spacer(Modifier.width(12.dp)) }
                            Column {
                                Text(title, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold,
                                    maxLines = 1, overflow = TextOverflow.Ellipsis)
                                if (subtitle != null) Text(
                                    subtitle, style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis,
                                )
                            }
                        }
                    },
                    actions = actions,
                )
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.5f))
            }
        },
        floatingActionButton = floatingActionButton,
        content = content,
    )
}
