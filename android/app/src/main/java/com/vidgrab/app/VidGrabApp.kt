package com.vidgrab.app

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import coil.ImageLoader
import coil.ImageLoaderFactory
import coil.decode.VideoFrameDecoder
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch

class VidGrabApp : Application(), ImageLoaderFactory {
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    override fun onCreate() {
        super.onCreate()
        Settings.init(this)
        Downloads.init(this)
        getSystemService(NotificationManager::class.java).apply {
            createNotificationChannel(
                NotificationChannel(CH_PROGRESS, "Downloads in progress", NotificationManager.IMPORTANCE_LOW)
            )
            createNotificationChannel(
                NotificationChannel(CH_DONE, "Finished downloads", NotificationManager.IMPORTANCE_DEFAULT)
            )
        }
        scope.launch { Engine.start(this@VidGrabApp) }
    }

    /** Coil image loader that can also show a frame from downloaded videos. */
    override fun newImageLoader() = ImageLoader.Builder(this)
        .components { add(VideoFrameDecoder.Factory()) }
        .crossfade(true)
        .build()

    companion object {
        const val CH_PROGRESS = "progress"
        const val CH_DONE = "done"
    }
}
