package com.kuaapi.sms.forwarder

import java.net.URI
import java.util.Locale

object ServerEndpoint {
    const val DEFAULT = "https://sms.example.com"
    fun normalize(raw: String): String {
        val uri = URI(raw.trim())
        require(uri.scheme?.lowercase(Locale.ROOT) == "https")
        require(!uri.host.isNullOrBlank() && uri.rawUserInfo == null && uri.rawQuery == null && uri.rawFragment == null)
        require(uri.rawPath.isNullOrEmpty() || uri.rawPath == "/")
        require(uri.port == -1 || uri.port in 1..65535)
        val port = if (uri.port == -1 || uri.port == 443) "" else ":${uri.port}"
        return "https://${uri.host.lowercase(Locale.ROOT)}$port"
    }
}
