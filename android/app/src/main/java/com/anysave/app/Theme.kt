package com.anysave.app

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color

/*
 * The same colours as the desktop manager page (extension/manager.css), so AnySave looks like one app
 * everywhere. Fixed colours, not the wallpaper's (Material You), which made every phone look different.
 */
private val Brand = Color(0xFFE5484D)

private val Light = lightColorScheme(
    primary = Brand, onPrimary = Color.White,
    primaryContainer = Color(0xFFFDEAEA), onPrimaryContainer = Color(0xFF7A1418),
    secondary = Color(0xFF2F6FED), onSecondary = Color.White,
    secondaryContainer = Color(0xFFE7EFFF), onSecondaryContainer = Color(0xFF173B85),
    tertiary = Color(0xFF1D9A52), onTertiary = Color.White,
    background = Color(0xFFF4F5F7), onBackground = Color(0xFF17181C),
    surface = Color(0xFFF4F5F7), onSurface = Color(0xFF17181C),
    surfaceVariant = Color(0xFFEEF0F3), onSurfaceVariant = Color(0xFF676A74),
    surfaceContainerLowest = Color.White, surfaceContainerLow = Color.White,
    surfaceContainer = Color.White, surfaceContainerHigh = Color(0xFFF2F4F7), surfaceContainerHighest = Color(0xFFE9EBEF),
    outline = Color(0xFF9A9DA6), outlineVariant = Color(0xFFE3E5EA),
    error = Color(0xFFD42F35), onError = Color.White,
    errorContainer = Color(0xFFFDECEC), onErrorContainer = Color(0xFF8C1B1F),
)

private val Dark = darkColorScheme(
    primary = Brand, onPrimary = Color.White,
    primaryContainer = Color(0xFF3A1D20), onPrimaryContainer = Color(0xFFFFB3B5),
    secondary = Color(0xFF5B8CFF), onSecondary = Color.White,
    secondaryContainer = Color(0xFF1B2742), onSecondaryContainer = Color(0xFFC9D8FF),
    tertiary = Color(0xFF3CCF7A), onTertiary = Color(0xFF0B2B18),
    background = Color(0xFF111215), onBackground = Color(0xFFECECF0),
    surface = Color(0xFF111215), onSurface = Color(0xFFECECF0),
    surfaceVariant = Color(0xFF202227), onSurfaceVariant = Color(0xFF9B9EA8),
    surfaceContainerLowest = Color(0xFF0D0E10), surfaceContainerLow = Color(0xFF18191D),
    surfaceContainer = Color(0xFF18191D), surfaceContainerHigh = Color(0xFF202227), surfaceContainerHighest = Color(0xFF2A2C32),
    outline = Color(0xFF6C6F78), outlineVariant = Color(0xFF2A2C32),
    error = Color(0xFFFF5C61), onError = Color(0xFF3A0B0D),
    errorContainer = Color(0xFF3A1D20), onErrorContainer = Color(0xFFFFB3B5),
)

/** Status colours used by progress bars and status words. */
@Immutable
data class StatusColors(
    val progress: Color, val progressTrack: Color,
    val ok: Color, val okSoft: Color,
    val warn: Color, val warnSoft: Color,
    val err: Color, val errSoft: Color,
    val selected: Color, val selectedLine: Color,
)

private val LightStatus = StatusColors(
    progress = Color(0xFF2F6FED), progressTrack = Color(0xFFDDE7FC),
    ok = Color(0xFF1D9A52), okSoft = Color(0xFFE3F5EA),
    warn = Color(0xFFB97206), warnSoft = Color(0xFFFBF0DC),
    err = Color(0xFFD42F35), errSoft = Color(0xFFFDECEC),
    selected = Color(0xFFE7EFFF), selectedLine = Color(0xFFB9CDFB),
)
private val DarkStatus = StatusColors(
    progress = Color(0xFF5B8CFF), progressTrack = Color(0xFF22304E),
    ok = Color(0xFF3CCF7A), okSoft = Color(0xFF173326),
    warn = Color(0xFFF0A73A), warnSoft = Color(0xFF3A2C14),
    err = Color(0xFFFF5C61), errSoft = Color(0xFF3A1D20),
    selected = Color(0xFF1B2742), selectedLine = Color(0xFF33508F),
)

val LocalStatusColors = staticCompositionLocalOf { LightStatus }

/** Shortcut: `AnySaveColors.status.progress`. */
object AnySaveColors {
    val status: StatusColors @Composable get() = LocalStatusColors.current

    /** File type badge colours, the same as the desktop's (MP4 purple, PDF blue, ZIP grey…). */
    fun category(c: FileKinds.Category): Color = when (c) {
        FileKinds.Category.VIDEOS -> Color(0xFF7C5CFF)
        FileKinds.Category.MUSIC -> Color(0xFFF07A2C)
        FileKinds.Category.DOCUMENTS -> Color(0xFF3D82F6)
        FileKinds.Category.PICTURES -> Color(0xFFD4489C)
        FileKinds.Category.ARCHIVES -> Color(0xFF7B818C)
        FileKinds.Category.APPS -> Color(0xFF12A07C)
        FileKinds.Category.CONVERTED -> Color(0xFF0EA5B7)
        FileKinds.Category.OTHER -> Color(0xFF8A8F99)
    }
}

@Composable
fun AnySaveTheme(content: @Composable () -> Unit) {
    val dark = isSystemInDarkTheme()
    CompositionLocalProvider(LocalStatusColors provides if (dark) DarkStatus else LightStatus) {
        MaterialTheme(colorScheme = if (dark) Dark else Light, content = content)
    }
}
