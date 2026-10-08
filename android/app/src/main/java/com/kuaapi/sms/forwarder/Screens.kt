package com.kuaapi.sms.forwarder

import android.content.Context
import android.content.res.ColorStateList
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.graphics.drawable.RippleDrawable
import android.text.Editable
import android.text.InputType
import android.text.TextWatcher
import android.view.Gravity
import android.view.View
import android.widget.*
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

internal data class HomeState(
    val account: String? = null,
    val enabled: Boolean = false,
    val smsAllowed: Boolean = false,
    val batteryExempt: Boolean = false,
    val queued: Int = 0,
    val blocked: Int = 0,
    val sent: Long = 0,
    val receivedAt: Long = 0,
    val uploadedAt: Long = 0,
    val authError: Boolean = false,
    val storageError: Boolean = false,
    val error: String = "",
    val notice: String = "",
    val endpoint: String = ServerEndpoint.DEFAULT
) {
    val headline: String get() = when {
        authError -> "需要重新配对"
        !enabled -> "转发已关闭"
        !smsAllowed -> "等待短信授权"
        else -> "转发已开启"
    }
    val summary: String get() = when {
        authError -> "当前连接已失效，请重新连接这台手机。"
        !enabled -> "打开开关，开始同步此后收到的新短信。"
        !smsAllowed -> "允许接收短信后，才能自动转发。"
        else -> "新收到的 SMS 将自动同步到你的收件箱。"
    }
}

internal class PairingDraft(var code: String = "", var consent: Boolean = false, var endpoint: String = ServerEndpoint.DEFAULT)
internal data class ScreenActions(
    val pair: (String) -> Unit,
    val toggle: (Boolean) -> Unit,
    val permission: () -> Unit,
    val background: () -> Unit,
    val tools: () -> Unit,
    val privacy: () -> Unit,
    val disconnect: () -> Unit,
    val reset: () -> Unit,
    val scan: () -> Unit = {}
)

/** UI only: never reads credentials, stores SMS, or performs network requests. */
internal class Screens(private val context: Context, private val actions: ScreenActions) {
    private val ink = Color.rgb(25, 37, 59)
    private val muted = Color.rgb(103, 115, 137)
    private val primary = Color.rgb(60, 83, 216)
    private val line = Color.rgb(232, 236, 243)
    private val surface = Color.rgb(245, 247, 251)
    private val green = Color.rgb(27, 118, 91)
    private fun dp(n: Int) = (context.resources.displayMetrics.density * n).toInt()
    private fun shape(color: Int, radius: Int = 20, stroke: Int? = null) = GradientDrawable().apply {
        setColor(color); cornerRadius = dp(radius).toFloat(); stroke?.let { setStroke(dp(1), it) }
    }
    private fun text(value: String, size: Float = 15f, color: Int = ink, bold: Boolean = false) = TextView(context).apply {
        this.text = value; textSize = size; setTextColor(color); includeFontPadding = false
        setLineSpacing(dp(4).toFloat(), 1f)
        if (bold) setTypeface(null, Typeface.BOLD)
    }
    private fun column() = LinearLayout(context).apply { orientation = LinearLayout.VERTICAL }
    private fun space(parent: LinearLayout, height: Int) { parent.addView(View(context), LinearLayout.LayoutParams(1, dp(height))) }
    private fun card(parent: LinearLayout): LinearLayout = column().apply {
        setPadding(dp(20), dp(22), dp(20), dp(22)); background = shape(Color.WHITE)
        parent.addView(this, LinearLayout.LayoutParams(-1, -2).apply { topMargin = dp(16) })
    }
    private fun button(parent: LinearLayout, label: String, filled: Boolean = false, enabled: Boolean = true, action: () -> Unit): Button = Button(context).apply {
        text = label; textSize = 15f; isAllCaps = false; minHeight = dp(52); minimumHeight = dp(52)
        setPadding(dp(16), dp(12), dp(16), dp(12)); stateListAnimator = null
        setTextColor(if (filled) Color.WHITE else primary)
        background = RippleDrawable(ColorStateList.valueOf(0x223C53D8), shape(if (filled) primary else surface, 14), null)
        isEnabled = enabled; alpha = if (enabled) 1f else .6f
        parent.addView(this, LinearLayout.LayoutParams(-1, -2).apply { topMargin = dp(14) })
        setOnClickListener { action() }
    }
    private fun link(parent: LinearLayout, label: String, action: () -> Unit) = button(parent, label, action = action)
    private fun header(root: LinearLayout) {
        val row = LinearLayout(context).apply { gravity = Gravity.CENTER_VERTICAL }
        row.addView(ImageView(context).apply { setImageResource(R.drawable.ic_app); importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO }, LinearLayout.LayoutParams(dp(32), dp(32)))
        row.addView(text("private-sms-client", 23f, bold = true), LinearLayout.LayoutParams(-2, -2).apply { marginStart = dp(10) })
        root.addView(row); space(root, 26)
    }
    fun render(state: HomeState, draft: PairingDraft, busy: String?): LinearLayout {
        val root = column().apply { setPadding(dp(22), dp(24), dp(22), dp(24)) }
        header(root)
        if (state.storageError) {
            root.addView(text("本地数据暂不可用", 26f, bold = true)); space(root, 12)
            root.addView(text("请先重启手机。如果仍无法恢复，可以清除本机数据并重新配对。", color = muted))
            button(root, "清除本地数据", enabled = busy == null, action = actions.reset)
        } else if (state.account == null) {
            pairing(root, draft, busy)
        } else {
            home(root, state, busy)
        }
        if (busy != null) {
            val row = LinearLayout(context).apply { gravity = Gravity.CENTER_VERTICAL; setPadding(0, dp(18), 0, 0) }
            row.addView(ProgressBar(context), LinearLayout.LayoutParams(dp(20), dp(20)))
            row.addView(text(busy, 14f, muted), LinearLayout.LayoutParams(-1, -2).apply { marginStart = dp(10) })
            root.addView(row)
        }
        space(root, 20)
        val footer = text("private-sms-client  ${BuildConfig.VERSION_NAME}  ·  隐私与帮助", 12f, muted).apply {
            gravity = Gravity.CENTER; minHeight = dp(48); setOnClickListener { actions.privacy() }
            isFocusable = true; contentDescription = "版本 ${BuildConfig.VERSION_NAME}，查看隐私与帮助"
        }
        root.addView(footer)
        return root
    }
    private fun pairing(root: LinearLayout, draft: PairingDraft, busy: String?) {
        root.addView(text("短信，随身可见。", 29f, bold = true)); space(root, 10)
        root.addView(text("连接备用手机，把新短信同步到你的收件箱。", color = muted))
        val card = card(root)
        card.addView(text("连接这台手机", 20f, bold = true)); space(card, 10)
        card.addView(text("在 iPhone 的“设置 → 安卓设备与配对”中生成配对码，有效期 10 分钟。", 14f, muted))
        space(card, 14)
        card.addView(text("服务器地址", 13f, muted))
        val server = EditText(context).apply {
            tag = "server_endpoint"
            contentDescription = "服务器地址"; hint = "https://sms.example.com"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI
            isSingleLine = true; textSize = 14f; setText(draft.endpoint); isEnabled = busy == null
            importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS
        }
        card.addView(server, LinearLayout.LayoutParams(-1, -2))
        card.addView(text("与 iPhone 连接的服务器保持一致。切换服务器需要重新配对。", 12f, muted))
        button(card, "扫描 iPhone 配对码", enabled = busy == null, action = actions.scan)
        space(card, 18)
        val input = EditText(context).apply {
            id = R.id.pairing_code
            hint = "XXXX-XXXX-XXXX-XXXX"; textSize = 17f; typeface = Typeface.MONOSPACE
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_CAP_CHARACTERS or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
            isSingleLine = true; setSelectAllOnFocus(false)
            setPadding(dp(12), dp(14), dp(12), dp(14)); background = shape(surface, 12, line)
            importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS
            contentDescription = "配对码"; setText(draft.code); setSelection(text.length); isEnabled = busy == null
            addTextChangedListener(object : TextWatcher {
                override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) = Unit
                override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) { draft.code = s.toString(); error = null }
                override fun afterTextChanged(s: Editable?) = Unit
            })
        }
        card.addView(input, LinearLayout.LayoutParams(-1, -2)); space(card, 8)
        card.addView(text("16 位字符，横线和空格可以省略。", 12f, muted)); space(card, 20)
        val uploadNotice = text("开启转发后，所有 SIM 卡的新 SMS（可能含验证码）会上传到 ${draft.endpoint}，供已配对的接收端查看。服务器运营者可解密短信；当前不是端到端加密。", 13f, muted)
        card.addView(uploadNotice)
        val consent = CheckBox(context).apply {
            id = R.id.pairing_consent; text = "我已了解并同意短信上传"; textSize = 13f; setTextColor(ink)
            minHeight = dp(48); isChecked = draft.consent; isEnabled = busy == null
            setOnCheckedChangeListener { _, checked -> draft.consent = checked; error = null }
        }
        card.addView(consent)
        server.addTextChangedListener(object : TextWatcher {
            override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) = Unit
            override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) {
                draft.endpoint = s.toString(); server.error = null
                consent.isChecked = false; draft.consent = false
                uploadNotice.text = "开启转发后，所有 SIM 卡的新 SMS（可能含验证码）会上传到 ${draft.endpoint}，供已配对的接收端查看。服务器运营者可解密短信；当前不是端到端加密。"
            }
            override fun afterTextChanged(s: Editable?) = Unit
        })
        button(card, if (busy == null) "配对这台手机" else "正在连接…", filled = true, enabled = busy == null) {
            val normalized = MessageIdentity.normalizeCode(input.text.toString())
            when {
                runCatching { ServerEndpoint.normalize(draft.endpoint) }.isFailure -> { server.error = "请输入 HTTPS 服务地址，不含路径、参数或密码"; server.requestFocus() }
                !MessageIdentity.validCode(normalized) -> { input.error = "请输入完整的 16 位配对码（字母或数字 2–7）"; input.requestFocus() }
                !consent.isChecked -> { consent.error = "请先同意短信上传说明"; consent.requestFocus() }
                else -> actions.pair(normalized)
            }
        }.id = R.id.pairing_submit
        space(root, 18)
        root.addView(text("配对后，由你决定何时开启转发。\n不读取历史短信，不发送短信。", 13f, muted))
        link(root, "如何获取配对码？") { actions.tools() }
    }
    private fun home(root: LinearLayout, state: HomeState, busy: String?) {
        root.addView(text("你的短信，在这里连接。", 15f, muted))
        space(root, 8)
        root.addView(text("服务器：${state.endpoint}", 12f, muted))
        val hero = card(root)
        val row = LinearLayout(context).apply { gravity = Gravity.CENTER_VERTICAL }
        val info = column()
        info.addView(text("短信转发", 21f, bold = true)); space(info, 8)
        val statusColor = if (state.authError || (state.enabled && !state.smsAllowed)) Color.rgb(156, 85, 20) else if (state.enabled) green else muted
        info.addView(text(state.headline, 13f, statusColor))
        row.addView(info, LinearLayout.LayoutParams(0, -2, 1f))
        row.addView(Switch(context).apply {
            id = R.id.forwarding_switch; contentDescription = "短信转发"
            isChecked = state.enabled; isEnabled = busy == null
            minHeight = dp(48); minWidth = dp(56)
            thumbTintList = ColorStateList(arrayOf(intArrayOf(-android.R.attr.state_enabled), intArrayOf(android.R.attr.state_checked), intArrayOf()), intArrayOf(Color.LTGRAY, primary, Color.rgb(154, 165, 184)))
            trackTintList = ColorStateList(arrayOf(intArrayOf(android.R.attr.state_checked), intArrayOf()), intArrayOf(Color.rgb(203, 210, 249), line))
            jumpDrawablesToCurrentState()
            setOnCheckedChangeListener { _, checked -> if (checked != state.enabled) actions.toggle(checked) }
        })
        hero.addView(row); space(hero, 20)
        hero.addView(text(state.summary, 14f, muted))
        if (!state.smsAllowed) button(hero, "允许接收短信", filled = true, enabled = busy == null, action = actions.permission)
        val stats = card(root)
        stats.addView(text("同步概况", 17f, bold = true)); space(stats, 20)
        val numbers = LinearLayout(context)
        fun metric(number: String, label: String, color: Int = ink) {
            val item = column(); item.addView(text(number, 28f, color, true)); space(item, 6); item.addView(text(label, 12f, muted))
            numbers.addView(item, LinearLayout.LayoutParams(0, -2, 1f))
        }
        metric((state.queued - state.blocked).toString(), "待发送")
        metric(state.sent.toString(), "已上传")
        metric(state.blocked.toString(), "需处理", if (state.blocked > 0) Color.rgb(156, 85, 20) else ink)
        stats.addView(numbers); space(stats, 20)
        stats.addView(View(context).apply { setBackgroundColor(line) }, LinearLayout.LayoutParams(-1, dp(1)))
        space(stats, 16)
        stats.addView(text("最近收到     ${time(state.receivedAt)}", 13f, muted)); space(stats, 10)
        stats.addView(text("最近上传     ${time(state.uploadedAt)}", 13f, muted)); space(stats, 14)
        stats.addView(text("上传成功表示服务器已接收，接收端提醒需另行确认。", 12f, muted))
        if (state.error.isNotBlank() || state.notice.isNotBlank()) {
            val issue = card(root); issue.addView(text("需要留意", 16f, bold = true)); space(issue, 10)
            issue.addView(text(listOf(state.error, state.notice).filter { it.isNotBlank() }.joinToString("\n\n"), 14f, muted))
        }
        val settings = card(root)
        settings.addView(text("设备与连接", 17f, bold = true)); space(settings, 8)
        settings.addView(text("已连接 · ${state.account?.ifBlank { "我的收件箱" }}", 13f, green))
        button(settings, "后台运行设置", enabled = busy == null, action = actions.background)
        button(settings, "连接检查与测试", enabled = busy == null, action = actions.tools)
        space(settings, 12)
        settings.addView(text(if (state.batteryExempt) "已获系统电池优化豁免，仍需留意厂商后台限制。" else "建议允许后台运行，让锁屏时的转发更稳定。", 12f, muted))
        button(root, "解除配对", enabled = busy == null, action = actions.disconnect)
    }
    private fun time(value: Long): String = if (value == 0L) "暂无记录" else SimpleDateFormat("MM-dd HH:mm:ss", Locale.getDefault()).format(Date(value))
}
