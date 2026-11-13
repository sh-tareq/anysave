package com.anysave.app

import android.app.AlarmManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import java.util.Calendar

/**
 * "Only download between 01:00 and 07:00", for every download or for one named queue.
 * A download that has to wait is SCHEDULED. When the window opens everything scheduled starts;
 * when it closes, running downloads are paused and wait for the next window. "Start now" on a
 * row (ignoreSchedule) lets one download run anyway. An alarm wakes the app at each boundary.
 */
object Schedule {
    /** "07:30" -> 450, or null. */
    fun minutes(s: String): Int? {
        val m = Regex("""^(\d{1,2}):(\d{2})$""").find(s.trim()) ?: return null
        val h = m.groupValues[1].toInt()
        val min = m.groupValues[2].toInt()
        return if (h > 23 || min > 59) null else h * 60 + min
    }

    /** Inside start..stop (stop not included; the window may cross midnight). Unreadable or equal times never block. */
    fun inWindow(nowMinutes: Int, start: String, stop: String): Boolean {
        val s = minutes(start)
        val e = minutes(stop)
        if (s == null || e == null || s == e) return true
        return if (s < e) nowMinutes in s until e else nowMinutes >= s || nowMinutes < e
    }

    private fun nowMinutes() = Calendar.getInstance().let { it.get(Calendar.HOUR_OF_DAY) * 60 + it.get(Calendar.MINUTE) }

    /** Does the schedule make this download wait right now? */
    fun blocks(job: Job, nowMin: Int = nowMinutes()): Boolean {
        if (!Settings.scheduleOn.value || job.ignoreSchedule || job.kind == JobKind.CONVERT) return false
        val only = Settings.scheduleQueue.value
        if (only.isNotBlank() && only != job.queue) return false
        return !inWindow(nowMin, Settings.scheduleStart.value, Settings.scheduleStop.value)
    }

    /** Starts what may run now, pauses what may not, and sets the alarm for the next boundary. */
    fun tick(ctx: Context) {
        val app = ctx.applicationContext
        for (job in Downloads.jobs.value) {
            if (job.kind == JobKind.CONVERT) continue
            val blocked = blocks(job)
            when {
                job.status == JobStatus.SCHEDULED && !blocked -> DownloadService.release(app, job.id)
                blocked && (job.status == JobStatus.DOWNLOADING || job.status == JobStatus.QUEUED || job.status == JobStatus.WAITING_WIFI) ->
                    DownloadService.hold(app, job.id)
            }
        }
        arm(app)
    }

    private fun pending(ctx: Context) = PendingIntent.getBroadcast(
        ctx, 71, Intent(ctx, ScheduleReceiver::class.java),
        PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
    )

    /** Wake up at the next start or stop time, while the schedule is on and something is unfinished. */
    fun arm(ctx: Context) {
        val am = ctx.getSystemService(AlarmManager::class.java) ?: return
        val needed = Settings.scheduleOn.value && Downloads.jobs.value.any { !it.finished && it.kind != JobKind.CONVERT }
        val s = minutes(Settings.scheduleStart.value)
        val e = minutes(Settings.scheduleStop.value)
        if (!needed || s == null || e == null || s == e) { am.cancel(pending(ctx)); return }
        val now = Calendar.getInstance()
        val next = listOf(s, e).map { m ->
            (now.clone() as Calendar).apply {
                set(Calendar.HOUR_OF_DAY, m / 60); set(Calendar.MINUTE, m % 60)
                set(Calendar.SECOND, 2); set(Calendar.MILLISECOND, 0)
                if (!after(now)) add(Calendar.DAY_OF_YEAR, 1)
            }.timeInMillis
        }.min()
        runCatching { am.setAndAllowWhileIdle(AlarmManager.RTC_WAKEUP, next, pending(ctx)) }
    }

    /** The user's extra queues, in the order typed. */
    fun queueNames(): List<String> =
        Settings.queues.value.split(',').map { it.trim().take(24) }.filter { it.isNotEmpty() }.distinct()
}

class ScheduleReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent?) = Schedule.tick(context)
}
