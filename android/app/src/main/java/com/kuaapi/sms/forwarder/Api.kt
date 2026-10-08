package com.kuaapi.sms.forwarder

import org.json.JSONObject
import java.net.URL
import javax.net.ssl.HttpsURLConnection

class HttpFailure(val status: Int) : Exception("HTTP $status")

object Api {
    fun request(path: String, method: String, token: String? = null, body: String? = null, id: String? = null, endpoint: String): JSONObject {
        require(path.startsWith("/v1/"))
        val connection = URL(ServerEndpoint.normalize(endpoint) + path).openConnection() as HttpsURLConnection
        // Use platform TLS certificate and hostname verification. Never follow credential redirects.
        connection.instanceFollowRedirects = false
        connection.connectTimeout = 10000
        connection.readTimeout = 15000
        connection.requestMethod = method
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("User-Agent", "private-sms-Android/${BuildConfig.VERSION_NAME}")
        token?.let { connection.setRequestProperty("Authorization", "Bearer $it") }
        id?.let { connection.setRequestProperty("Idempotency-Key", it) }
        try {
            if (body != null) {
                val data = body.toByteArray(Charsets.UTF_8)
                connection.doOutput = true
                connection.setRequestProperty("Content-Type", "application/json; charset=utf-8")
                connection.setFixedLengthStreamingMode(data.size)
                connection.outputStream.use { it.write(data) }
            }
            val code = connection.responseCode
            if (code !in 200..299) throw HttpFailure(code)
            val bytes = connection.inputStream.use { it.readBytesLimited(32768) }
            return JSONObject(String(bytes, Charsets.UTF_8))
        } finally { connection.disconnect() }
    }
    private fun java.io.InputStream.readBytesLimited(limit: Int): ByteArray {
        val out = java.io.ByteArrayOutputStream()
        val buffer = ByteArray(4096)
        while (true) {
            val n = read(buffer)
            if (n < 0) break
            require(out.size() + n <= limit) { "Response too large" }
            out.write(buffer, 0, n)
        }
        return out.toByteArray()
    }
    fun errorMessage(e: Exception): String = when (e) {
        is HttpFailure -> when (e.status) {
            401, 403 -> "凭据无效或已被撤销，请重新配对。"
            429 -> "请求较多，稍后会自动重试。"
            507 -> "服务器收件箱已满，请在接收端删除旧短信。"
            in 500..599 -> "服务器暂不可用，稍后会自动重试。"
            else -> "服务器返回 ${e.status}，请检查服务版本。"
        }
        is javax.net.ssl.SSLException -> "HTTPS 证书校验失败，已停止此次连接。"
        is java.io.IOException -> "暂时无法连接服务器，网络恢复后重试。"
        else -> "本地存储或数据异常，请重新打开 App 检查。"
    }
}
