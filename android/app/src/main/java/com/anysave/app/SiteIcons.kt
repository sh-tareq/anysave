package com.anysave.app

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.MusicNote
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.outlined.CameraAlt
import androidx.compose.material.icons.outlined.Language
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

/**
 * Small icon for each supported site, in the site's colours.
 * These are simple symbols (play button, camera, note, "f"), not the companies' trademarked logos.
 */
@Composable
fun SiteIcon(site: String?, size: Dp = 20.dp, modifier: Modifier = Modifier) {
    val glyph = size * 0.68f
    when (site) {
        "YouTube" -> Box(
            modifier.size(size).background(Color(0xFFFF0033), RoundedCornerShape(size * 0.28f)),
            contentAlignment = Alignment.Center
        ) { Icon(Icons.Filled.PlayArrow, null, tint = Color.White, modifier = Modifier.size(glyph)) }

        "Instagram" -> Box(
            modifier.size(size).background(
                Brush.linearGradient(listOf(Color(0xFFFEDA75), Color(0xFFFA7E1E), Color(0xFFD62976), Color(0xFF962FBF), Color(0xFF4F5BD5))),
                RoundedCornerShape(size * 0.28f)
            ),
            contentAlignment = Alignment.Center
        ) { Icon(Icons.Outlined.CameraAlt, null, tint = Color.White, modifier = Modifier.size(glyph)) }

        "TikTok" -> Box(
            modifier.size(size).background(Color(0xFF111111), RoundedCornerShape(size * 0.28f)),
            contentAlignment = Alignment.Center
        ) {
            // Cyan and pink offset copies give the familiar "glitch" colouring
            val shift = size * 0.045f
            Icon(Icons.Filled.MusicNote, null, tint = Color(0xFF25F4EE), modifier = Modifier.size(glyph).offset(-shift, -shift))
            Icon(Icons.Filled.MusicNote, null, tint = Color(0xFFFE2C55), modifier = Modifier.size(glyph).offset(shift, shift))
            Icon(Icons.Filled.MusicNote, null, tint = Color.White, modifier = Modifier.size(glyph * 0.92f))
        }

        "Facebook" -> Box(
            modifier.size(size).background(Color(0xFF1877F2), CircleShape),
            contentAlignment = Alignment.Center
        ) { Text("f", color = Color.White, fontWeight = FontWeight.Black, fontSize = (size.value * 0.72f).sp) }

        "Vimeo" -> Box(
            modifier.size(size).background(Color(0xFF17D5FF), RoundedCornerShape(size * 0.28f)),
            contentAlignment = Alignment.Center
        ) { Text("v", color = Color.White, fontWeight = FontWeight.Black, fontSize = (size.value * 0.7f).sp) }

        else -> Box(
            modifier.size(size).background(Color(0xFF8A8A8A), CircleShape),
            contentAlignment = Alignment.Center
        ) { Icon(Icons.Outlined.Language, null, tint = Color.White, modifier = Modifier.size(glyph)) }
    }
}
