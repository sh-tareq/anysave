package com.vidgrab.app

import androidx.compose.animation.core.*
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

const val AUTHOR = "Shahidul H Tareq"

private val Brand = listOf(Color(0xFFE5484D), Color(0xFFFF7A59), Color(0xFF7B5CF5))

/**
 * "Made by Shahidul H Tareq" badge. Calm by design: a static gradient outline and photo ring,
 * and a soft light that glides across the name once every few seconds. No spinning or tilting.
 */
@Composable
fun MadeBy(modifier: Modifier = Modifier) {
    val t = rememberInfiniteTransition(label = "credit")
    // 0..1 = the shine passes over the name (1.5 s); 1..4 = rests (4.5 s)
    val p by t.animateFloat(0f, 4f, infiniteRepeatable(tween(6000, easing = LinearEasing)), label = "shine")
    val shine = p.coerceAtMost(1f)

    val shape = RoundedCornerShape(50)
    Box(modifier.fillMaxWidth().padding(vertical = 10.dp), contentAlignment = Alignment.Center) {
        Row(
            Modifier
                .clip(shape)
                .background(MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.55f))
                .border(1.2.dp, Brush.linearGradient(Brand), shape)
                .padding(start = 5.dp, end = 16.dp, top = 5.dp, bottom = 5.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Box(
                Modifier.size(34.dp).clip(CircleShape)
                    .background(Brush.linearGradient(Brand)).padding(1.5.dp).clip(CircleShape)
            ) { AuthorAvatar(Modifier.fillMaxSize()) }
            Spacer(Modifier.width(10.dp))
            Text("Made by ", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(
                AUTHOR,
                style = TextStyle(
                    fontSize = 15.sp, fontWeight = FontWeight.Bold,
                    // Brand gradient with a narrow highlight that sweeps across once per cycle
                    brush = Brush.linearGradient(
                        0f to Brand[0],
                        (shine - 0.15f).coerceIn(0.001f, 0.997f) to Brand[1],
                        shine.coerceIn(0.002f, 0.998f) to Color(0xFFFFD6C9),
                        (shine + 0.15f).coerceIn(0.003f, 0.999f) to Brand[1],
                        1f to Brand[2],
                        start = Offset.Zero, end = Offset(420f, 0f),
                    ),
                ),
            )
        }
    }
}

/** Author photo (res/drawable-nodpi/author_photo.jpg), cropped to fill a circle by the caller. */
@Composable
fun AuthorAvatar(modifier: Modifier = Modifier) {
    Image(painterResource(R.drawable.author_photo), contentDescription = AUTHOR,
        contentScale = ContentScale.Crop, modifier = modifier)
}
