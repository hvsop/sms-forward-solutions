package com.kuaapi.sms.forwarder

import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.json.JSONObject

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ServerEndpointTest {
    @Test fun normalizesHttpsOrigins() {
        assertEquals("https://sms.example.com", ServerEndpoint.normalize(" https://SMS.example.com:443/ "))
        assertEquals("https://sms-alt.example.com", ServerEndpoint.normalize("https://sms-alt.example.com"))
        assertEquals("https://example.com:9443", ServerEndpoint.normalize("https://example.com:9443/"))
    }
    @Test fun rejectsCredentialRedirectAndPathConfusion() {
        listOf("http://example.com", "https://user:pass@example.com", "https://example.com/a", "https://example.com?token=x",
            "https://example.com#x", "https://example.com:70000", "https:///example.com", "https://example.com\\@evil.test", "").forEach { raw ->
            assertTrue("should reject $raw", runCatching { ServerEndpoint.normalize(raw) }.isFailure)
        }
    }
    @Test fun oldEncryptedBindingsKeepOriginalServerAfterUpgrade() {
        val original = JSONObject().put("token", "test").put("device_id", "device").put("label", "old")
        assertEquals(ServerEndpoint.DEFAULT, Binding.fromJson(original).endpoint)
        original.put("endpoint", "https://sms-alt.example.com/")
        assertEquals("https://sms-alt.example.com", Binding.fromJson(original).endpoint)
    }
}
