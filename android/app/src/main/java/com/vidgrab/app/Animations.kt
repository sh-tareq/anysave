package com.vidgrab.app

import androidx.compose.animation.core.*
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowDownward
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp

/**
 * The AnySave logo, gently floating and turning in 3D, with a shadow on the
 * "floor" that shrinks as it rises.
 */
@Composable
fun AnimatedLogo(size: Dp, modifier: Modifier = Modifier) {
    val t = rememberInfiniteTransition(label = "logo")
    val tilt by t.animateFloat(
        -16f, 16f, infiniteRepeatable(tween(2600, easing = FastOutSlowInEasing), RepeatMode.Reverse), label = "tilt"
    )
    val lift by t.animateFloat(
        0f, 1f, infiniteRepeatable(tween(1800, easing = FastOutSlowInEasing), RepeatMode.Reverse), label = "lift"
    )
    Box(modifier.size(width = size, height = size * 1.08f), contentAlignment = Alignment.TopCenter) {
        Image(
            painterResource(R.drawable.vidgrab_logo), contentDescription = "AnySave",
            modifier = Modifier.size(size).graphicsLayer {
                rotationY = tilt
                rotationX = 6f * (lift - 0.5f)
                translationY = -size.toPx() * 0.07f * lift
                cameraDistance = 12f * density
            }
        )
    }
}

/** Icon that pops when it becomes selected (bottom navigation). */
@Composable
fun BouncyIcon(selected: Boolean, content: @Composable () -> Unit) {
    val scale by animateFloatAsState(
        if (selected) 1.15f else 1f, spring(dampingRatio = 0.35f, stiffness = 500f), label = "bounce"
    )
    Box(Modifier.graphicsLayer { scaleX = scale; scaleY = scale }) { content() }
}

/** Small red badge with an arrow dropping into it, shown on downloads in progress. */
@Composable
fun DownloadingBadge(modifier: Modifier = Modifier, size: Dp = 22.dp) {
    val t = rememberInfiniteTransition(label = "badge")
    val drop by t.animateFloat(
        -0.35f, 0.35f, infiniteRepeatable(tween(700, easing = LinearEasing), RepeatMode.Restart), label = "drop"
    )
    val fade by t.animateFloat(
        1f, 0f, infiniteRepeatable(tween(700, easing = LinearEasing), RepeatMode.Restart), label = "fade"
    )
    Box(
        modifier.size(size).clip(CircleShape).background(MaterialTheme.colorScheme.primary),
        contentAlignment = Alignment.Center
    ) {
        Icon(
            Icons.Filled.ArrowDownward, null, tint = Color.White,
            modifier = Modifier.size(size * 0.7f).graphicsLayer {
                translationY = size.toPx() * drop
                alpha = if (drop > 0.2f) fade * 2 else 1f
            }
        )
    }
}

/** Gentle pulse for "Getting ready…" and empty states. */
@Composable
fun Modifier.pulse(): Modifier {
    val t = rememberInfiniteTransition(label = "pulse")
    val s by t.animateFloat(0.96f, 1.04f, infiniteRepeatable(tween(1100), RepeatMode.Reverse), label = "s")
    return this.graphicsLayer { scaleX = s; scaleY = s }
}
