package com.kuaapi.sms.forwarder

object PairingPayload {
    fun parse(raw: String): String? {
        val prefix = "private-sms:pair:v1:"
        if (raw.length > 80 || !raw.startsWith(prefix)) return null
        val code = MessageIdentity.normalizeCode(raw.removePrefix(prefix))
        return code.takeIf { MessageIdentity.validCode(it) }
    }
}
