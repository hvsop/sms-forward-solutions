package com.kuaapi.sms.forwarder

import android.content.Context

/** Shared by foreground retries and background jobs; only one upload may drain the queue. */
internal class PendingUploader(
    private val applicationContext: Context,
    private val shouldStop: () -> Boolean = { false },
    private val loadBinding: () -> Binding? = { Vault(applicationContext).binding() },
    private val decode: (ByteArray) -> String = { String(Vault(applicationContext).decrypt(it), Charsets.UTF_8) },
    private val send: (Binding, String, String) -> Unit = { binding, body, id ->
        check(Api.request("/v1/ingest", "POST", binding.token, body, id, endpoint = binding.endpoint).optBoolean("ok"))
    }
) {
    private val prefs = applicationContext.getSharedPreferences("status", Context.MODE_PRIVATE)
    private data class Upload(val binding: Binding, val id: String, val body: String)
    enum class Outcome { COMPLETE, RETRY, PAUSED, AUTH_ERROR, ACCOUNT_CHANGED }
    fun upload(): Outcome = synchronized(transferLock) { uploadPending() }
    // Called on the activity executor: bypass WorkManager's existing backoff chain.
    fun retryNow(): Outcome = synchronized(transferLock) {
        synchronized(AccountGate.lock) {
            QueueStore(applicationContext).use { it.unblock() }
            check(prefs.edit().putBoolean("auth_error", false).remove("error").commit())
        }
        uploadPending()
    }
    private fun uploadPending(): Outcome {
        try {
            repeat(20) {
                if (shouldStop()) return Outcome.RETRY
                val upload = synchronized(AccountGate.lock) {
                    QueueStore(applicationContext).use { queue ->
                        if (queue.prune() > 0) prefs.edit().putString("notice", "部分待发送短信超过 7 天，已从本机队列删除。").apply()
                    }
                    if (!prefs.getBoolean("enabled", false)) return Outcome.PAUSED
                    if (prefs.getBoolean("auth_error", false)) return Outcome.AUTH_ERROR
                    val binding = loadBinding() ?: return Outcome.PAUSED
                    QueueStore(applicationContext).use { queue ->
                        if (queue.prune() > 0) prefs.edit().putString("notice", "部分待发送短信超过 7 天，已从本机队列删除。").apply()
                        val (id, payload) = queue.next() ?: return Outcome.COMPLETE
                        try { Upload(binding, id, decode(payload)) }
                        catch (_: Exception) {
                            queue.block(id)
                            prefs.edit().putString("error", "有一条待发送短信无法解密，已保留供检查。").apply()
                            null
                        }
                    }
                } ?: return@repeat
                // Network I/O must never block the SMS broadcast's short goAsync lifetime.
                val failure = try {
                    send(upload.binding, upload.body, upload.id)
                    null
                } catch (e: Exception) { e }
                synchronized(AccountGate.lock) {
                    if (loadBinding() != upload.binding) return Outcome.ACCOUNT_CHANGED
                    QueueStore(applicationContext).use { queue ->
                        if (failure == null) {
                            queue.delete(upload.id)
                            prefs.edit().putLong("last_success", System.currentTimeMillis())
                                .putLong("sent", prefs.getLong("sent", 0) + 1).remove("error").apply()
                        } else {
                            prefs.edit().putString("error", Api.errorMessage(failure)).apply()
                            if (failure is HttpFailure && failure.status in listOf(401, 403)) {
                                prefs.edit().putBoolean("auth_error", true).apply()
                                return Outcome.AUTH_ERROR
                            }
                            if (failure is HttpFailure && failure.status in listOf(400, 413, 415, 422)) queue.block(upload.id)
                            else return Outcome.RETRY
                        }
                    }
                }
            }
            return synchronized(AccountGate.lock) {
                QueueStore(applicationContext).use { if (it.next() != null) Outcome.RETRY else Outcome.COMPLETE }
            }
        } catch (e: Exception) {
            prefs.edit().putString("error", Api.errorMessage(e)).apply()
            return Outcome.RETRY
        }
    }
    companion object { private val transferLock = Any() }
}
