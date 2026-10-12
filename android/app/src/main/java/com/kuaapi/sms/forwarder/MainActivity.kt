package com.kuaapi.sms.forwarder

import com.google.zxing.integration.android.IntentIntegrator
import android.Manifest
import android.app.Activity
import android.app.AlertDialog
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.net.Uri
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.PowerManager
import android.provider.Settings
import android.view.WindowManager
import android.widget.ScrollView
import org.json.JSONObject
import java.util.concurrent.Executors

class MainActivity : Activity() {
    private lateinit var scroll: ScrollView
    private lateinit var screens: Screens
    private val executor = Executors.newSingleThreadExecutor()
    private val handler = Handler(Looper.getMainLooper())
    private val draft = PairingDraft()
    private var state = HomeState()
    private var binding: Binding? = null
    private var busy: String? = null
    private var active = false
    private var refreshQueued = false
    private var pendingMessage: String? = null
    private val prefs by lazy { getSharedPreferences("status", MODE_PRIVATE) }
    private val refresh = object : Runnable {
        override fun run() {
            if (active) { loadStatus(); handler.postDelayed(this, 3000) }
        }
    }
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
        draft.code = savedInstanceState?.getString("pairing_draft").orEmpty()
        draft.consent = savedInstanceState?.getBoolean("pairing_consent") ?: false
        draft.endpoint = savedInstanceState?.getString("server_draft") ?: ServerEndpoint.DEFAULT
        screens = Screens(this, ScreenActions(::pair, ::toggle, ::requestSms, ::backgroundHelp, ::tools, ::privacy, ::disconnect, ::confirmReset, ::scan))
        scroll = ScrollView(this).apply { setBackgroundColor(Color.rgb(245, 247, 251)); isFillViewport = true }
        scroll.setOnApplyWindowInsetsListener { view, insets ->
            if (android.os.Build.VERSION.SDK_INT >= 30) {
                val bars = insets.getInsets(android.view.WindowInsets.Type.systemBars() or android.view.WindowInsets.Type.ime())
                view.setPadding(bars.left, bars.top, bars.right, bars.bottom)
            } else {
                @Suppress("DEPRECATION")
                view.setPadding(insets.systemWindowInsetLeft, insets.systemWindowInsetTop, insets.systemWindowInsetRight, insets.systemWindowInsetBottom)
            }
            insets
        }
        setContentView(scroll)
        busy = "正在读取设备状态…"
        render()
        busy = null
        loadStatus(force = true)
        UploadWorker.ensureRecovery(this)
        UploadWorker.schedule(this)
    }
    override fun onResume() {
        super.onResume(); active = true; loadStatus()
        handler.removeCallbacks(refresh); handler.postDelayed(refresh, 3000)
        pendingMessage?.let { pendingMessage = null; message(it) }
    }
    override fun onPause() { active = false; handler.removeCallbacks(refresh); super.onPause() }
    override fun onDestroy() { executor.shutdown(); super.onDestroy() }
    override fun onSaveInstanceState(outState: Bundle) {
        outState.putString("pairing_draft", draft.code)
        outState.putString("server_draft", draft.endpoint)
        outState.putBoolean("pairing_consent", draft.consent)
        super.onSaveInstanceState(outState)
    }
    private fun render() {
        if (isDestroyed) return
        val y = scroll.scrollY
        scroll.removeAllViews(); scroll.addView(screens.render(state, draft, busy))
        scroll.post { if (!isDestroyed) scroll.scrollTo(0, y) }
    }
    private fun message(value: String) {
        if (isDestroyed || isFinishing || value.isBlank()) return
        if (!active) { pendingMessage = value; return }
        AlertDialog.Builder(this).setMessage(value).setPositiveButton("知道了", null).show()
    }
    private fun operation(progress: String, action: () -> String) {
        if (busy != null) return
        busy = progress; render()
        executor.execute {
            val result = try { action() } catch (e: Exception) { Api.errorMessage(e) }
            runOnUiThread {
                busy = null
                if (!isDestroyed) { loadStatus(force = true); message(result) }
            }
        }
    }
    private fun loadStatus(force: Boolean = false) {
        if (busy != null || refreshQueued || executor.isShutdown) return
        refreshQueued = true
        executor.execute {
            var failed = false
            val snapshot = synchronized(AccountGate.lock) {
                val current = try { Vault(this).binding() } catch (_: Exception) { failed = true; null }
                val counts = try { QueueStore(this).use { it.counts() } } catch (_: Exception) { failed = true; 0 to 0 }
                val next = HomeState(
                    account = current?.label, endpoint = current?.endpoint ?: ServerEndpoint.DEFAULT, enabled = prefs.getBoolean("enabled", false),
                    smsAllowed = checkSelfPermission(Manifest.permission.RECEIVE_SMS) == PackageManager.PERMISSION_GRANTED,
                    batteryExempt = getSystemService(PowerManager::class.java).isIgnoringBatteryOptimizations(packageName),
                    queued = counts.first, blocked = counts.second, sent = prefs.getLong("sent", 0),
                    receivedAt = prefs.getLong("last_sms", 0), uploadedAt = prefs.getLong("last_success", 0),
                    authError = prefs.getBoolean("auth_error", false), storageError = failed,
                    error = prefs.getString("error", "").orEmpty(), notice = prefs.getString("notice", "").orEmpty()
                )
                current to next
            }
            runOnUiThread {
                refreshQueued = false
                if (isDestroyed || busy != null) return@runOnUiThread
                binding = snapshot.first
                val changed = state != snapshot.second
                state = snapshot.second
                if (force || changed) render()
            }
        }
    }
    private fun scan() {
        if (busy != null || binding != null) return
        IntentIntegrator(this).setCaptureActivity(PairingScanActivity::class.java)
            .setDesiredBarcodeFormats(IntentIntegrator.QR_CODE)
            .setPrompt("扫描 iPhone 上的 private-sms 配对二维码")
            .setBeepEnabled(false).setBarcodeImageEnabled(false).setOrientationLocked(true)
            .initiateScan()
    }
    @Deprecated("Activity result API used by embedded scanner")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        val result = IntentIntegrator.parseActivityResult(requestCode, resultCode, data)
        if (result == null) { super.onActivityResult(requestCode, resultCode, data); return }
        val raw = result.contents ?: return
        val code = PairingPayload.parse(raw)
        if (code == null) { message("这不是有效的 private-sms 配对二维码，请从 iPhone 的安卓设备页面重新生成。"); return }
        draft.code = code; draft.consent = false; render()
        message("配对码已填入。请阅读并同意短信上传说明，再点“配对这台手机”。")
    }
    private fun pair(code: String) = operation("正在安全连接…") {
        val endpoint = ServerEndpoint.normalize(draft.endpoint)
        val result = try { Api.request("/v1/android/pair", "POST", body = JSONObject().put("code", code).toString(), endpoint = endpoint) }
        catch (e: HttpFailure) {
            if (e.status == 401) return@operation "配对码无效、已使用或已过期。请生成新的配对码。"
            throw e
        }
        val token = result.getString("token")
        require(Regex("[a-f0-9]{64}").matches(token))
        val next = Binding(token, result.getString("device_id"), result.optString("label"), endpoint)
        synchronized(AccountGate.lock) {
            QueueStore(this).use { it.clear() }
            check(prefs.edit().clear().commit())
            Vault(this).save(next)
        }
        runOnUiThread { draft.code = ""; draft.consent = false }
        "配对成功。允许短信权限后，打开“短信转发”开关即可开始。"
    }
    private fun toggle(checked: Boolean) {
        if (checked && !state.smsAllowed) { render(); requestSms(); return }
        if (checked && state.authError) { render(); message("连接已失效，请解除配对后使用新配对码连接。"); return }
        operation(if (checked) "正在开启转发…" else "正在关闭转发…") {
            synchronized(AccountGate.lock) { check(prefs.edit().putBoolean("enabled", checked).commit()) }
            if (checked) UploadWorker.schedule(this)
            ""
        }
    }
    private fun requestSms() {
        if (state.smsAllowed) { message("短信权限已允许。"); return }
        val endpoint = binding?.endpoint ?: return
        AlertDialog.Builder(this).setTitle("允许短信同步？")
            .setMessage("private-sms-client 需要接收短信权限。只有配对并开启转发后，应用才会在后台接收所有 SIM 卡的新 SMS，将发件人、正文（可能含验证码）、接收时间和 SIM 标识通过 HTTPS 上传至：\n\n$endpoint\n\n用途仅为让你在已配对的接收端查看短信。服务器运营者能够解密内容，这不是端到端加密。我们不读取历史短信、通讯录，也不发送短信。你可随时关闭转发或解除配对；关闭期间的新短信不会补传。")
            .setNegativeButton("暂不允许", null)
            .setNeutralButton("隐私政策") { _, _ -> openPrivacyPolicy() }
            .setPositiveButton("同意并继续") { _, _ -> requestSystemSmsPermission() }.show()
    }
    private fun requestSystemSmsPermission() {
        if (prefs.getBoolean("permission_asked", false) && !shouldShowRequestPermissionRationale(Manifest.permission.RECEIVE_SMS)) {
            AlertDialog.Builder(this).setTitle("允许接收短信").setMessage("请到应用信息中允许短信权限。如果权限不可选，先从右上角菜单允许“受限设置”。")
                .setNegativeButton("取消", null).setPositiveButton("打开应用设置") { _, _ -> appSettings() }.show()
        } else requestPermissions(arrayOf(Manifest.permission.RECEIVE_SMS), 10)
    }
    private fun backgroundHelp() {
        AlertDialog.Builder(this).setTitle("允许后台运行")
            .setMessage("三星手机建议完成以下设置：\n\n1. 应用信息 → 电池 → 不受限制。\n2. 后台使用限制 → 从不自动休眠的应用，加入 private-sms-client。\n\n系统版本不同，名称可能略有差异。关机、强行停止应用或重启后尚未首次解锁时，转发可能暂停。")
            .setNegativeButton("关闭", null).setNeutralButton("应用设置") { _, _ -> appSettings() }
            .setPositiveButton("电池设置") { _, _ ->
                try { startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS)) } catch (_: Exception) { appSettings() }
            }.show()
    }
    private fun tools() {
        val current = binding
        if (current == null) {
            message("需要能够接收短信的安卓手机，以及你自己或可信管理员提供的兼容服务；安装 App 不会自动开通公共账户。\n\n先在 iPhone 的 private-sms-server 连接服务，再打开“设置 → 安卓设备与配对 → 添加安卓手机”，生成配对码或二维码。配对码有效期为 10 分钟，只能使用一次。\n\n服务器地址必须与 iPhone 一致。当前默认地址为 ${ServerEndpoint.DEFAULT}，可按管理员提供的信息修改。")
            return
        }
        AlertDialog.Builder(this).setTitle("连接检查与测试")
            .setItems(arrayOf("检查服务器连接", "发送测试消息", "重试待发送短信", "切换服务器（先解除配对）")) { _, which ->
                when (which) {
                    3 -> disconnect()
                    0 -> operation("正在检查连接…") {
                        Api.request("/v1/android/status", "GET", current.token, endpoint = current.endpoint)
                        "连接正常。\n\n这一步只检查服务器连接；真实短信接收和接收端通知需单独验证。"
                    }
                    1 -> sendTest(current)
                    2 -> operation("正在上传待发送短信…") {
                        val before = prefs.getLong("sent", 0)
                        val outcome = PendingUploader(applicationContext).retryNow()
                        if (outcome == PendingUploader.Outcome.RETRY) UploadWorker.schedule(this)
                        val (remaining, blocked) = synchronized(AccountGate.lock) { QueueStore(this).use { it.counts() } }
                        val uploaded = prefs.getLong("sent", 0) - before
                        when (outcome) {
                            PendingUploader.Outcome.PAUSED -> "上传已暂停，请确认配对有效并打开“短信转发”开关。"
                            PendingUploader.Outcome.ACCOUNT_CHANGED -> "配对已变化，本次上传已停止。"
                            else -> buildString {
                                append("已上传 $uploaded 条，待发送 ${remaining - blocked} 条，需处理 $blocked 条。")
                                val error = prefs.getString("error", "").orEmpty()
                                if (error.isNotBlank()) append("\n\n$error")
                                else if (outcome == PendingUploader.Outcome.RETRY) append("\n\n剩余短信已安排后台重试，也可以再次点击重试。")
                                else if (remaining == 0) append("\n\n服务器已接收，请到 iPhone 查看。")
                            }
                        }
                    }
                }
            }.setNegativeButton("关闭", null).show()
    }
    private fun sendTest(current: Binding) {
        AlertDialog.Builder(this).setTitle("发送测试消息")
            .setMessage("向已配对的收件箱上传一条虚构消息，可能触发接收端提醒。不会通过 SIM 卡发送短信，也不产生短信费用。")
            .setNegativeButton("取消", null).setPositiveButton("发送") { _, _ -> operation("正在发送测试消息…") {
                val body = JSONObject().put("sender", "安卓转发端测试")
                    .put("content", "这是一条 private-sms-client 测试消息，用于验证手机到收件箱的连接。")
                    .put("sim", "测试").put("source_time", java.time.Instant.now().toString()).toString()
                check(Api.request("/v1/ingest", "POST", current.token, body, java.util.UUID.randomUUID().toString(), endpoint = current.endpoint).optBoolean("ok"))
                "服务器已接收测试消息。请到接收端查看；它不计入真实短信上传数量。"
            } }.show()
    }
    private fun privacy() {
        AlertDialog.Builder(this).setTitle("隐私与帮助")
            .setMessage("private-sms-client ${BuildConfig.VERSION_NAME}\n\n转发范围\n只转发开启后新收到的 SMS，覆盖所有 SIM 卡。不读取历史短信和通讯录，不发送短信，不支持 RCS、彩信或电话。关闭开关后，新短信不会入队，已有队列暂停上传；已在途的一条可能已送达，重新开启不会补发关闭期间的短信。\n\n数据与安全\n短信通过 HTTPS 上传至你选择的服务器（当前：${binding?.endpoint ?: draft.endpoint}）。凭据和待发送内容在本机加密保存，成功后删除本地短信内容。队列最多 1,000 条，超过 7 天的条目在下次处理时清理。服务器默认保留 7 天。当前服务器运营者可解密短信，并非端到端加密。\n\n停止与删除\n随时关闭转发；解除配对会撤销这台手机的凭据并删除本地队列，服务器中的短信需在接收端删除。\n\n转发排查\n确认短信权限、后台运行和网络均正常；如果同时使用其他短信转发工具，请关闭它们的转发开关，避免重复收到。")
            .setNeutralButton("完整隐私政策") { _, _ -> openPrivacyPolicy() }
            .setPositiveButton("知道了", null).show()
    }
    private fun openPrivacyPolicy() {
        val url = "https://github.com/hvsop/sms-forward-solutions/blob/main/docs/android-privacy.md"
        try { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url))) }
        catch (_: android.content.ActivityNotFoundException) { message("请用浏览器打开：\n$url") }
    }
    private fun disconnect() {
        val current = binding ?: return
        AlertDialog.Builder(this).setTitle("解除这台手机的配对？")
            .setMessage("将停止转发、撤销设备凭据，并删除本地 ${state.queued} 条待发送短信。服务器已收到的短信不受影响。此操作需要网络连接。")
            .setNegativeButton("取消", null).setPositiveButton("解除配对") { _, _ -> operation("正在解除配对…") {
                try { Api.request("/v1/android/device", "DELETE", current.token, endpoint = current.endpoint) } catch (e: HttpFailure) { if (e.status != 401) throw e }
                resetLocal(); "已解除配对。"
            } }.show()
    }
    private fun appSettings() { startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:$packageName"))) }
    private fun resetLocal() = synchronized(AccountGate.lock) {
        check(prefs.edit().clear().commit()); QueueStore(this).use { it.clear() }; Vault(this).clear()
    }
    private fun confirmReset() {
        AlertDialog.Builder(this).setTitle("清除本地数据？")
            .setMessage("将删除本机凭据和所有待发送短信，无法恢复。旧服务器凭据需要通过重新配对替换，或由接收端撤销。")
            .setNegativeButton("取消", null).setPositiveButton("清除") { _, _ -> operation("正在清除…") { resetLocal(); "已清除，请重新配对。" } }.show()
    }
    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode == 10) {
            prefs.edit().putBoolean("permission_asked", true).apply()
            loadStatus(force = true)
            message(if (grantResults.firstOrNull() == PackageManager.PERMISSION_GRANTED) "已允许接收短信。打开“短信转发”开关开始同步。" else "未获得短信权限。请到应用信息检查“受限设置”和短信权限。")
        }
    }
}
