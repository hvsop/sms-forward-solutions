package com.kuaapi.sms.forwarder

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class StorageTest {
    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    @Test fun encryptedOutboxSurvivesReopenAndRejectsTampering() = synchronized(AccountGate.lock) {
        val vault = Vault(context)
        val plain = "虚构测试消息 123456 & + 👋".toByteArray()
        val encrypted = vault.encrypt(plain)
        assertFalse(String(encrypted).contains("123456"))
        assertFalse(vault.encrypt(plain).contentEquals(encrypted))
        QueueStore(context).use { q -> q.clear(); assertTrue(q.add("fixture", encrypted)); assertTrue(q.add("fixture", encrypted)); assertEquals(1, q.counts().first) }
        QueueStore(context).use { q ->
            val row = q.next()!!
            assertArrayEquals(plain, vault.decrypt(row.second))
            val corrupted = row.second.copyOf(); corrupted[corrupted.lastIndex] = (corrupted.last().toInt() xor 1).toByte()
            try { vault.decrypt(corrupted); fail("tampered payload accepted") } catch (_: javax.crypto.AEADBadTagException) { }
            q.block(row.first); assertNull(q.next()); assertEquals(1, q.counts().second)
            q.unblock(); assertNotNull(q.next()); q.delete(row.first); assertEquals(0, q.counts().first)
        }
    }
    @Test fun bindingSurvivesReopenAndQueueIsBounded() = synchronized(AccountGate.lock) {
        Vault(context).save(Binding("f".repeat(64), "fixture-device", "fixture"))
        assertEquals("fixture-device", Vault(context).binding()!!.deviceId)
        QueueStore(context).use { q ->
            q.clear()
            val encrypted = Vault(context).encrypt("{}".toByteArray())
            repeat(1000) { assertTrue(q.add("fixture-$it", encrypted)) }
            assertFalse(q.add("overflow", encrypted))
            assertTrue(q.add("fixture-0", encrypted))
            q.clear()
        }
        Vault(context).clear(); assertNull(Vault(context).binding())
    }
}
