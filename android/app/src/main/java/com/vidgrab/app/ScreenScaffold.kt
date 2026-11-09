package com.vidgrab.app

import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

/**
 * Every tab uses this: a large title that shrinks into a compact bar as you scroll, but
 * never scrolls away, so the screen name and its actions (search, menu) are always reachable.
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
    val scroll = TopAppBarDefaults.exitUntilCollapsedScrollBehavior(rememberTopAppBarState())
    val expanded = scroll.state.collapsedFraction < 0.5f
    Scaffold(
        modifier = Modifier.nestedScroll(scroll.nestedScrollConnection),
        // The app shell around the tabs already handles the status and navigation bars.
        contentWindowInsets = WindowInsets(0),
        topBar = {
            MediumTopAppBar(
                windowInsets = WindowInsets(0),
                scrollBehavior = scroll,
                colors = TopAppBarDefaults.mediumTopAppBarColors(
                    containerColor = MaterialTheme.colorScheme.surface,
                    scrolledContainerColor = MaterialTheme.colorScheme.surfaceContainer,
                ),
                title = {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        if (leading != null) { leading(); Spacer(Modifier.width(12.dp)) }
                        Column {
                            Text(title, fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            if (subtitle != null && expanded) Text(
                                subtitle, style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis,
                            )
                        }
                    }
                },
                actions = actions,
            )
        },
        floatingActionButton = floatingActionButton,
        content = content,
    )
}
