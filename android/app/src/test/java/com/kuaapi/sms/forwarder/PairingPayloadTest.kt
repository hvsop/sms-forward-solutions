package com.kuaapi.sms.forwarder

import org.junit.Assert.*
import org.junit.Test

class PairingPayloadTest {
    @Test fun acceptsOnlyVersionedPairingPayload() {
        assertEquals("ABCDEFGHIJKLMNOP", PairingPayload.parse("private-sms:pair:v1:ABCD-EFGH-IJKL-MNOP"))
        assertNull(PairingPayload.parse("https://evil.example/pair?token=ABCDEFGHIJKLMNOP"))
        assertNull(PairingPayload.parse("private-sms:pair:v2:ABCDEFGHIJKLMNOP"))
        assertNull(PairingPayload.parse("private-sms:pair:v1:ABCDEFGHIJKLMNOP&server=evil"))
        assertNull(PairingPayload.parse("private-sms:pair:v1:short"))
        assertNull(PairingPayload.parse("private-sms:pair:v1:" + "A".repeat(10000)))
    }
}
