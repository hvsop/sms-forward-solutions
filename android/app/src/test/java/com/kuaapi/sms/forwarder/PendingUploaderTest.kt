package com.kuaapi.sms.forwarder

import android.content.Context
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config
import java.io.IOException

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class PendingUploaderTest {
    private val context: Context get() = RuntimeEnvironment.getApplication()
    private val prefs get() = context.getSharedPreferences("status", Context.MODE_PRIVATE)
    private var binding: Binding? = Binding("test-token", "test-device", "test")
    private val ids = mutableListOf<String>()

    @Before fun setup() {
        context.deleteDatabase("outbox.db")
        prefs.edit().clear().putBoolean("enabled", true).commit()
    }
    private fun enqueue(count: Int) = QueueStore(context).use { queue ->
        repeat(count) { assertTrue(queue.add("message-$it", "synthetic SMS $it".toByteArray())) }
    }
    private fun uploader(send: (Binding, String, String) -> Unit = { _, _, id -> ids.add(id); Unit }) =
        PendingUploader(context, loadBinding = { binding }, decode = { String(it) }, send = send)

    @Test fun manualRetryDrainsNineMessagesWithoutWaitingForBackgroundScheduler() {
        enqueue(9)
        prefs.edit().putLong("sent", 1).putString("error", "old network error").commit()
        assertEquals(PendingUploader.Outcome.COMPLETE, uploader().retryNow())
        assertEquals(9, ids.toSet().size)
        assertEquals(10, prefs.getLong("sent", 0))
        assertEquals(0 to 0, QueueStore(context).use { it.counts() })
        assertFalse(prefs.contains("error"))
    }
    @Test fun networkFailureKeepsMessageAndRetryUsesSameId() {
        enqueue(1)
        val uploader = uploader { _, _, id ->
            ids.add(id)
            if (ids.size == 1) throw IOException("simulated timeout")
        }
        assertEquals(PendingUploader.Outcome.RETRY, uploader.upload())
        assertEquals(1 to 0, QueueStore(context).use { it.counts() })
        assertEquals(0, prefs.getLong("sent", 0))
        assertEquals(PendingUploader.Outcome.COMPLETE, uploader.retryNow())
        assertEquals(listOf("message-0", "message-0"), ids)
        assertEquals(1, prefs.getLong("sent", 0))
    }
    @Test fun manualRetryRespectsForwardingSwitch() {
        enqueue(1)
        prefs.edit().putBoolean("enabled", false).commit()
        assertEquals(PendingUploader.Outcome.PAUSED, uploader().retryNow())
        assertTrue(ids.isEmpty())
        assertEquals(1 to 0, QueueStore(context).use { it.counts() })
    }
    @Test fun rejectedCredentialsKeepQueuedMessagesAndStopBackgroundRequests() {
        enqueue(2)
        val uploader = uploader { _, _, id -> ids.add(id); throw HttpFailure(401) }
        assertEquals(PendingUploader.Outcome.AUTH_ERROR, uploader.retryNow())
        assertEquals(PendingUploader.Outcome.AUTH_ERROR, uploader.upload())
        assertEquals(1, ids.size)
        assertEquals(2 to 0, QueueStore(context).use { it.counts() })
    }
    @Test fun invalidMessageDoesNotPreventOtherMessagesFromUploading() {
        enqueue(2)
        val uploader = uploader { _, _, id -> if (id == "message-0") throw HttpFailure(400) else ids.add(id) }
        assertEquals(PendingUploader.Outcome.COMPLETE, uploader.retryNow())
        assertEquals(listOf("message-1"), ids)
        assertEquals(1 to 1, QueueStore(context).use { it.counts() })
    }
    @Test fun replacedWorkerLeavesUnsentMessagesForItsSuccessor() {
        enqueue(3)
        var stopped = false
        val first = PendingUploader(context, shouldStop = { stopped }, loadBinding = { binding },
            decode = { String(it) }, send = { _, _, id -> ids.add(id); stopped = true })
        assertEquals(PendingUploader.Outcome.RETRY, first.upload())
        assertEquals(2 to 0, QueueStore(context).use { it.counts() })
        assertEquals(PendingUploader.Outcome.COMPLETE, uploader().upload())
        assertEquals(3, ids.size)
        assertEquals(3, ids.toSet().size)
        assertEquals(3, prefs.getLong("sent", 0))
        assertEquals(0 to 0, QueueStore(context).use { it.counts() })
    }
    @Test fun AccountChangeDuringRequestDoesNotMutateNewAccountsQueue() {
        enqueue(1)
        val uploader = uploader { _, _, _ -> binding = Binding("new-token", "new-device", "new") }
        assertEquals(PendingUploader.Outcome.ACCOUNT_CHANGED, uploader.retryNow())
        assertEquals(1 to 0, QueueStore(context).use { it.counts() })
        assertEquals(0, prefs.getLong("sent", 0))
    }
    @Test fun ServerChangeWithSameDeviceIdCannotAcknowledgeAnotherServersQueue() {
        enqueue(1)
        val uploader = uploader { old, _, _ -> binding = old.copy(endpoint = "https://sms-alt.example.com") }
        assertEquals(PendingUploader.Outcome.ACCOUNT_CHANGED, uploader.retryNow())
        assertEquals(1 to 0, QueueStore(context).use { it.counts() })
        assertEquals(0, prefs.getLong("sent", 0))
    }
}
