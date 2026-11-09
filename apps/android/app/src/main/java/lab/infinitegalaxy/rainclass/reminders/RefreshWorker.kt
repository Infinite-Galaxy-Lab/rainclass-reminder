package lab.infinitegalaxy.rainclass.reminders

import android.content.Context
import androidx.work.Constraints
import androidx.work.Worker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.Operation
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import lab.infinitegalaxy.rainclass.RainApplication
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.runBlocking

class RefreshWorker(context: Context, parameters: WorkerParameters) : Worker(context, parameters) {
    private val repository get() = (applicationContext as RainApplication).repository

    override fun doWork(): Result = runBlocking {
        try {
            if (isStopped) return@runBlocking Result.success()
            if (repository.snapshot().authState == "signed_out") return@runBlocking Result.success()
            if (isStopped) return@runBlocking Result.success()
            repository.refresh(id.toString()) { isStopped }
            Result.success()
        } catch (error: Exception) {
            if (error.message?.contains("auth_expired") == true ||
                error.message?.contains("session_required") == true) Result.success()
            else if (runAttemptCount < 2) Result.retry() else Result.failure()
        }
    }

    override fun onStopped() { repository.cancelWorker(id.toString()) }

    companion object {
        const val NAME = "rainclass-periodic-refresh"
        fun schedule(context: Context): Operation {
            val request = PeriodicWorkRequestBuilder<RefreshWorker>(15, TimeUnit.MINUTES)
                .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                .addTag(NAME).build()
            return WorkManager.getInstance(context).enqueueUniquePeriodicWork(NAME, ExistingPeriodicWorkPolicy.KEEP, request)
        }

        fun cancel(context: Context): Operation = WorkManager.getInstance(context).cancelUniqueWork(NAME)
    }
}
