package link.reminis.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.util.Log
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.sample
import kotlinx.coroutines.launch
import link.reminis.core.TransferDirection
import link.reminis.core.TransferItem
import link.reminis.core.TransferPhase

/**
 * Keeps the process alive while transfers run, as a `dataSync` foreground
 * service with a progress notification (the Android counterpart of iOS's
 * background task, docs/specs/10-app-android.md). The transfers themselves
 * live in [link.reminis.core.TransferQueue], in memory: the service only
 * holds the process and shows where the queue stands, then stops itself
 * once nothing is left to do. If the system ends it (time limit of
 * `dataSync` services), the queue is paused: every transfer resumes later
 * at the offset confirmed by the console.
 */
class TransferService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private var watcher: Job? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val queue = container.transfers
        if (!startInForeground(notification(queue.items.value))) {
            stopSelf()
            return START_NOT_STICKY
        }
        if (watcher == null) {
            watcher = scope.launch {
                launch {
                    @OptIn(kotlinx.coroutines.FlowPreview::class)
                    queue.items.sample(1_000).collectLatest { items ->
                        getSystemService(NotificationManager::class.java)?.notify(NOTIFICATION_ID, notification(items))
                    }
                }
                queue.isBusy.collectLatest { busy ->
                    if (!busy) {
                        stopForeground(STOP_FOREGROUND_REMOVE)
                        stopSelf()
                    }
                }
            }
        }
        // Not restarted by the system: the queue died with the process.
        return START_NOT_STICKY
    }

    private fun startInForeground(notification: Notification): Boolean = try {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            startForeground(NOTIFICATION_ID, notification)
        }
        true
    } catch (e: Exception) {
        // E.g. not allowed from the background: the transfers go on while the process lives.
        Log.w(TAG, "foreground service refused: ${e.javaClass.simpleName}")
        false
    }

    /** The system ends a `dataSync` service after its daily time: pause, everything resumes later. */
    override fun onTimeout(startId: Int, fgsType: Int) {
        container.transfers.pauseAll()
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }

    private fun notification(items: List<TransferItem>): Notification {
        val manager = getSystemService(NotificationManager::class.java)
        if (manager?.getNotificationChannel(CHANNEL_ID) == null) {
            manager?.createNotificationChannel(
                NotificationChannel(CHANNEL_ID, getString(R.string.transfers_channel), NotificationManager.IMPORTANCE_LOW),
            )
        }
        val active = items.filter { it.phase.isActive }
        val current = active.firstOrNull { it.phase == TransferPhase.RUNNING || it.phase == TransferPhase.HASHING }
        val title = when {
            current == null -> getString(R.string.transfers_notification_waiting)
            current.direction == TransferDirection.UPLOAD -> getString(R.string.transfers_notification_upload, current.fileName)
            else -> getString(R.string.transfers_notification_download, current.fileName)
        }
        val open = PendingIntent.getActivity(
            this, 0,
            Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val builder = Notification.Builder(this, CHANNEL_ID)
            .setSmallIcon(android.R.drawable.stat_sys_upload)
            .setContentTitle(title)
            .setContentText(resources.getQuantityString(R.plurals.transfers_notification_count, active.size, active.size))
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setContentIntent(open)
        val fraction = current?.fraction
        if (current != null) builder.setProgress(1000, ((fraction ?: 0f) * 1000).toInt(), fraction == null)
        return builder.build()
    }

    companion object {
        private const val TAG = "ReminisLink"
        private const val CHANNEL_ID = "transfers"
        private const val NOTIFICATION_ID = 1

        /** Starts the service (from the foreground, when a transfer is added). */
        fun start(context: Context) {
            try {
                context.startForegroundService(Intent(context, TransferService::class.java))
            } catch (e: Exception) {
                // Background start refused (API 31+): the transfers go on while the process lives.
                Log.w(TAG, "transfer service not started: ${e.javaClass.simpleName}")
            }
        }
    }
}
