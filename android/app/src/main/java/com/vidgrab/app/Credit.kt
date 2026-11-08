package com.vidgrab.app

import androidx.compose.animation.core.*
import androidx.compose.foundation.background
import androidx.compose.ui.draw.clip
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.AutoAwesome
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

const val AUTHOR = "Shahidul H Tareq"

private val Fire = listOf(Color(0xFFFF4D6D), Color(0xFFFF9F1C), Color(0xFFFFD60A), Color(0xFF7B2FF7), Color(0xFFFF4D6D))

/**
 * "Made by Shahidul H Tareq" badge: the name has a colour sweep running through it,
 * a glowing border rotates around the badge, a sparkle pulses, and the whole badge
 * tilts slowly in 3D.
 */
@Composable
fun MadeBy(modifier: Modifier = Modifier) {
    val t = rememberInfiniteTransition(label = "credit")
    val sweep by t.animateFloat(0f, 1f, infiniteRepeatable(tween(2400, easing = LinearEasing)), label = "sweep")
    val spin by t.animateFloat(0f, 360f, infiniteRepeatable(tween(4000, easing = LinearEasing)), label = "spin")
    val tiltY by t.animateFloat(-14f, 14f, infiniteRepeatable(tween(3200, easing = FastOutSlowInEasing), RepeatMode.Reverse), label = "tiltY")
    val tiltX by t.animateFloat(-6f, 6f, infiniteRepeatable(tween(2300, easing = FastOutSlowInEasing), RepeatMode.Reverse), label = "tiltX")
    val sparkle by t.animateFloat(0.75f, 1.2f, infiniteRepeatable(tween(650, easing = FastOutSlowInEasing), RepeatMode.Reverse), label = "sparkle")

    val shape = RoundedCornerShape(50)
    Box(modifier.fillMaxWidth().padding(vertical = 10.dp), contentAlignment = Alignment.Center) {
        // Outer layer: a spinning rainbow, clipped to the pill. The inner face sits 2.5dp
        // inside it, so only a glowing rotating rim shows.
        Box(
            Modifier
                .graphicsLayer {
                    rotationY = tiltY
                    rotationX = tiltX
                    cameraDistance = 14f * density
                }
                .shadow(12.dp, shape, ambientColor = Color(0xFFFF4D6D), spotColor = Color(0xFFFF4D6D))
                .clip(shape)
                .drawBehind {
                    rotate(spin) {
                        drawCircle(Brush.sweepGradient(Fire, center), radius = size.maxDimension)
                    }
                }
                .padding(2.5.dp)
        ) {
        Row(
            Modifier
                .background(MaterialTheme.colorScheme.surface, shape)
                .padding(start = 6.dp, end = 18.dp, top = 6.dp, bottom = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            // Round avatar with its own spinning ring, and a sparkle on its corner
            Box {
                Box(
                    Modifier.size(42.dp).clip(CircleShape)
                        .drawBehind { rotate(-spin) { drawCircle(Brush.sweepGradient(Fire, center), radius = size.maxDimension) } }
                        .padding(2.dp).clip(CircleShape)
                ) { AuthorAvatar(Modifier.fillMaxSize()) }
                Icon(
                    Icons.Filled.AutoAwesome, null, tint = Color(0xFFFFB703),
                    modifier = Modifier.align(Alignment.TopEnd).offset(x = 4.dp, y = (-4).dp).size(16.dp)
                        .graphicsLayer { scaleX = sparkle; scaleY = sparkle; rotationZ = spin / 4 }
                )
            }
            Spacer(Modifier.width(10.dp))
            Text("Made by ", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(
                AUTHOR,
                style = TextStyle(
                    fontSize = 16.sp, fontWeight = FontWeight.Black, letterSpacing = 0.3.sp,
                    // Colour sweep running through the name
                    brush = Brush.linearGradient(
                        Fire,
                        start = Offset(-300f + 900f * sweep, 0f),
                        end = Offset(0f + 900f * sweep, 60f),
                    ),
                ),
            )
        }
        }
    }
}

/** Author photo (res/drawable-nodpi/author_photo.jpg), cropped to fill a circle by the caller. */
@Composable
fun AuthorAvatar(modifier: Modifier = Modifier) {
    androidx.compose.foundation.Image(
        androidx.compose.ui.res.painterResource(R.drawable.author_photo), contentDescription = AUTHOR,
        contentScale = androidx.compose.ui.layout.ContentScale.Crop, modifier = modifier,
    )
}
