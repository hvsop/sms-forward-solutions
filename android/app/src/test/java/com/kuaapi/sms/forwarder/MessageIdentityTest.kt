package com.kuaapi.sms.forwarder
import org.junit.Assert.*
import org.junit.Test
class MessageIdentityTest {
    @Test fun retryAndRepeatedBroadcastHaveSameIdentity() {
        assertEquals(MessageIdentity.id("银行", "你好 & 1234\n", "SIM 1", 1234), MessageIdentity.id("银行", "你好 & 1234\n", "SIM 1", 1234))
    }
    @Test fun differentSimOrTimeDoesNotDropNewMessage() {
        val id = MessageIdentity.id("bank", "same", "SIM 1", 1234)
        assertNotEquals(id, MessageIdentity.id("bank", "same", "SIM 2", 1234))
        assertNotEquals(id, MessageIdentity.id("bank", "same", "SIM 1", 1235))
        assertNotEquals(MessageIdentity.id("a", "b|c", "d", 1), MessageIdentity.id("a|b", "c", "d", 1))
    }
    @Test fun pairingNormalizesFullWidthAndCommonLookalikes() {
        assertEquals("MV4N3K3DO4GE3TF2", MessageIdentity.normalizeCode("ＭＶ４Ｎ-３Ｋ３Ｄ-０４ＧＥ-３ＴＦ２"))
        assertEquals("I", MessageIdentity.normalizeCode("1"))
        assertFalse(MessageIdentity.validCode(MessageIdentity.normalizeCode("ABCDEF!HIJKLMN23")))
    }
    @Test fun pairingAcceptsFormattingButRejectsInvalidCharacters() {
        val code = MessageIdentity.normalizeCode(" abcd-efgh ijkl-mn23\n")
        assertEquals("ABCDEFGHIJKLMN23", code)
        assertTrue(MessageIdentity.validCode(code))
        assertFalse(MessageIdentity.validCode("ABCDEFGHIJKLMN01"))
        assertFalse(MessageIdentity.validCode("ABC"))
    }
}
