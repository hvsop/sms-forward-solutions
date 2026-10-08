package com.kuaapi.sms.forwarder

import java.security.MessageDigest

object MessageIdentity {
    fun id(sender: String, content: String, sim: String, timestamp: Long): String {
        val digest = MessageDigest.getInstance("SHA-256")
        // Length framing prevents ambiguity when content itself contains separators.
        for (part in listOf(sender, content, sim, timestamp.toString())) {
            val bytes = part.toByteArray(Charsets.UTF_8)
            digest.update(java.nio.ByteBuffer.allocate(4).putInt(bytes.size).array())
            digest.update(bytes)
        }
        return digest.digest().joinToString("") { "%02x".format(it) }
    }
    fun normalizeCode(raw: String): String = java.text.Normalizer.normalize(raw, java.text.Normalizer.Form.NFKC)
        .uppercase(java.util.Locale.ROOT).filterNot { it == '-' || it.isWhitespace() }
        .replace('0', 'O').replace('1', 'I')
    fun validCode(code: String): Boolean = Regex("[A-Z2-7]{16}").matches(code)
}
