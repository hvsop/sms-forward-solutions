package com.kuaapi.sms.forwarder

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.AtomicFile
import org.json.JSONObject
import java.io.File
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

// Serializes account changes with queue mutations and uploads. Never wait on this from UI.
object AccountGate { val lock = Any() }

data class Binding(val token: String, val deviceId: String, val label: String, val endpoint: String = ServerEndpoint.DEFAULT) {
    companion object {
        fun fromJson(json: JSONObject) = Binding(json.getString("token"), json.getString("device_id"), json.optString("label"),
            ServerEndpoint.normalize(json.optString("endpoint", ServerEndpoint.DEFAULT)))
    }
}

class Vault(context: Context) {
    private val file = AtomicFile(File(context.noBackupFilesDir, "binding.enc"))
    private fun key(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey("private-sms-v1", null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").apply {
            init(KeyGenParameterSpec.Builder("private-sms-v1", KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build())
        }.generateKey()
    }
    fun encrypt(plain: ByteArray): ByteArray {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, key())
        cipher.updateAAD("private-sms/android/v1".toByteArray())
        return cipher.iv + cipher.doFinal(plain)
    }
    fun decrypt(data: ByteArray): ByteArray {
        require(data.size >= 28)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, data.copyOfRange(0, 12)))
        cipher.updateAAD("private-sms/android/v1".toByteArray())
        return cipher.doFinal(data.copyOfRange(12, data.size))
    }
    fun binding(): Binding? {
        if (!file.baseFile.exists()) return null
        val json = JSONObject(String(decrypt(file.readFully()), Charsets.UTF_8))
        return Binding.fromJson(json)
    }
    fun save(binding: Binding) {
        val bytes = encrypt(JSONObject().put("token", binding.token).put("device_id", binding.deviceId).put("label", binding.label)
            .put("endpoint", ServerEndpoint.normalize(binding.endpoint)).toString().toByteArray())
        val stream = file.startWrite()
        try { stream.write(bytes); file.finishWrite(stream) } catch (e: Exception) { file.failWrite(stream); throw e }
    }
    fun clear() = file.delete()
}
