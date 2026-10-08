package com.kuaapi.sms.forwarder

import android.content.Context
import android.os.Build
import androidx.work.*
import java.util.concurrent.TimeUnit

class UploadWorker(context: Context, parameters: WorkerParameters) : Worker(context, parameters) {
    override fun doWork(): Result = when (PendingUploader(applicationContext, shouldStop = { isStopped }).upload()) {
        PendingUploader.Outcome.RETRY -> Result.retry()
        else -> Result.success()
    }
    companion object {
        private fun constraints() = Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()
        fun schedule(context: Context) {
            val builder = OneTimeWorkRequestBuilder<UploadWorker>().setConstraints(constraints())
                .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 30, TimeUnit.SECONDS)
            if (Build.VERSION.SDK_INT >= 31) builder.setExpedited(OutOfQuotaPolicy.RUN_AS_NON_EXPEDITED_WORK_REQUEST)
            // A fresh SMS or app launch must wake uploads even when the old job is in backoff.
            // The durable queue, shared upload lock and stable IDs make replacement safe.
            WorkManager.getInstance(context).enqueueUniqueWork("sms-upload", ExistingWorkPolicy.REPLACE, builder.build())
        }
        fun ensureRecovery(context: Context) {
            val request = PeriodicWorkRequestBuilder<UploadWorker>(15, TimeUnit.MINUTES).setConstraints(constraints()).build()
            WorkManager.getInstance(context).enqueueUniquePeriodicWork("sms-recovery", ExistingPeriodicWorkPolicy.KEEP, request)
        }
    }
}
