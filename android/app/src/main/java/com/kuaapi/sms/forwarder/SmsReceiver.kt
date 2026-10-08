package com.kuaapi.sms.forwarder

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.provider.Telephony
import org.json.JSONObject
import java.time.Instant
import java.util.concurrent.Executors

class SmsReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Telephony.Sms.Intents.SMS_RECEIVED_ACTION) return
        val pending = goAsync()
        executor.execute {
            try {
                synchronized(AccountGate.lock) {
                    val prefs = context.getSharedPreferences("status", Context.MODE_PRIVATE)
                    if (!prefs.getBoolean("enabled", false)) return@synchronized
                    val vault = Vault(context)
                    if (vault.binding() == null) return@synchronized
                    val parts = Telephony.Sms.Intents.getMessagesFromIntent(intent)
                    if (parts.isNullOrEmpty()) return@synchronized
                    val sender = parts.first().originatingAddress?.takeIf { it.isNotBlank() } ?: "未知发送方"
                    val content = parts.joinToString("") { it.messageBody.orEmpty() }
                    if (content.isBlank()) return@synchronized
                    val slot = intent.getIntExtra("slot", intent.getIntExtra("slot_id", -1))
                    val subscription = intent.getIntExtra("subscription", -1)
                    val sim = if (slot >= 0) "SIM ${slot + 1}" else if (subscription >= 0) "订阅 $subscription" else "SIM"
                    val timestamp = parts.first().timestampMillis
                    val json = JSONObject().put("sender", sender).put("content", content).put("sim", sim)
                        .put("source_time", Instant.ofEpochMilli(timestamp).toString()).toString()
                    QueueStore(context).use { queue ->
                        val expired = queue.prune()
                        val saved = queue.add(MessageIdentity.id(sender, content, sim, timestamp), vault.encrypt(json.toByteArray()))
                        prefs.edit().putLong("last_sms", System.currentTimeMillis()).apply()
                        if (!saved) prefs.edit().putString("notice", "待发送队列已满，新短信未入队。请恢复连接后重试；原短信仍在系统短信 App。").apply()
                        else if (expired > 0) prefs.edit().putString("notice", "部分待发送短信超过 7 天，已从本机队列删除。").apply()
                    }
                    UploadWorker.schedule(context)
                }
            } catch (_: Exception) {
                context.getSharedPreferences("status", Context.MODE_PRIVATE).edit()
                    .putString("error", "短信未能写入待发送队列。请打开 App 检查存储；原短信仍在系统短信 App。").apply()
            } finally { pending.finish() }
        }
    }
    companion object { private val executor = Executors.newSingleThreadExecutor() }
}
