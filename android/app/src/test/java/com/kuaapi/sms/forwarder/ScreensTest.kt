package com.kuaapi.sms.forwarder

import android.app.Activity
import android.graphics.Bitmap
import android.graphics.Canvas
import android.view.View
import android.view.ViewGroup
import android.widget.CheckBox
import android.widget.EditText
import android.widget.Switch
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.File

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w360dp-h800dp-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class ScreensTest {
    private fun activity(): Activity {
        val controller = Robolectric.buildActivity(Activity::class.java)
        controller.get().setTheme(R.style.AppTheme)
        return controller.setup().get()
    }
    private fun actions(pair: (String) -> Unit = {}, toggle: (Boolean) -> Unit = {}) =
        ScreenActions(pair, toggle, {}, {}, {}, {}, {}, {})

    @Test fun pairingRequiresConsentAndKeepsDraftOnRebuild() {
        val activity = activity()
        var submitted: String? = null
        val screens = Screens(activity, actions(pair = { submitted = it }))
        val draft = PairingDraft()
        var root = screens.render(HomeState(), draft, null)
        root.findViewById<EditText>(R.id.pairing_code).setText("ｍｖ４ｎ-３ｋ３ｄ-０４ｇｅ-３ｔｆ２")
        root.findViewById<View>(R.id.pairing_submit).performClick()
        assertNull("Upload consent is required", submitted)
        root.findViewById<CheckBox>(R.id.pairing_consent).isChecked = true
        root = screens.render(HomeState(), draft, null)
        assertEquals(draft.code, root.findViewById<EditText>(R.id.pairing_code).text.toString())
        assertTrue(root.findViewById<CheckBox>(R.id.pairing_consent).isChecked)
        root.findViewById<View>(R.id.pairing_submit).performClick()
        assertEquals("MV4N3K3DO4GE3TF2", submitted)
    }
    @Test fun busyPairingPreventsDuplicateSubmissionsAndInvalidCodeShowsError() {
        val activity = activity()
        var calls = 0
        val screens = Screens(activity, actions(pair = { calls++ }))
        val draft = PairingDraft("ABC", true)
        val root = screens.render(HomeState(), draft, null)
        root.findViewById<View>(R.id.pairing_submit).performClick()
        assertNotNull(root.findViewById<EditText>(R.id.pairing_code).error)
        assertEquals(0, calls)
        val waiting = screens.render(HomeState(), draft, "正在安全连接…")
        assertFalse(waiting.findViewById<View>(R.id.pairing_submit).isEnabled)
        assertFalse(waiting.findViewById<View>(R.id.pairing_code).isEnabled)
    }
    @Test fun serverChangeRequiresNewConsentAndRejectsInvalidAddress() {
        val activity = activity()
        var calls = 0
        val draft = PairingDraft("ABCDEFGHIJKLMNOP", true)
        val root = Screens(activity, actions(pair = { calls++ })).render(HomeState(), draft, null)
        val server = root.findViewWithTag<EditText>("server_endpoint")
        server.setText("https://sms-alt.example.com")
        assertEquals("https://sms-alt.example.com", draft.endpoint)
        assertFalse(draft.consent)
        root.findViewById<View>(R.id.pairing_submit).performClick()
        assertEquals(0, calls)
        root.findViewById<CheckBox>(R.id.pairing_consent).isChecked = true
        root.findViewById<View>(R.id.pairing_submit).performClick()
        assertEquals(1, calls)
        server.setText("http://example.com")
        root.findViewById<CheckBox>(R.id.pairing_consent).isChecked = true
        root.findViewById<View>(R.id.pairing_submit).performClick()
        assertEquals(1, calls)
        assertNotNull(server.error)
    }
    @Test fun pairedPhoneRespectsExistingSwitchStateAndRequestsDisable() {
        val activity = activity()
        var requested: Boolean? = null
        val screens = Screens(activity, actions(toggle = { requested = it }))
        val root = screens.render(HomeState(account = "我的收件箱", enabled = true, smsAllowed = true), PairingDraft(), null)
        val toggle = root.findViewById<Switch>(R.id.forwarding_switch)
        assertTrue(toggle.isChecked)
        assertNull("Rendering must not change the saved state", requested)
        toggle.isChecked = false
        assertEquals(false, requested)
        val paused = screens.render(HomeState(account = "我的收件箱"), PairingDraft(), null)
        assertFalse(paused.findViewById<Switch>(R.id.forwarding_switch).isChecked)
    }
    @Test fun capturePairingAndHomeLayouts() {
        val activity = activity()
        val screens = Screens(activity, actions())
        screenshot("pairing-1.0.0", screens.render(HomeState(), PairingDraft(), null))
        screenshot("home-1.0.0", screens.render(HomeState(account = "我的收件箱", enabled = true, smsAllowed = true, batteryExempt = true,
            sent = 12, receivedAt = 1791090372000L, uploadedAt = 1791090373584L), PairingDraft(), null))
    }
    private fun screenshot(name: String, root: ViewGroup) {
        root.setBackgroundColor(android.graphics.Color.rgb(245, 247, 251))
        root.measure(View.MeasureSpec.makeMeasureSpec(720, View.MeasureSpec.EXACTLY), View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED))
        root.layout(0, 0, 720, root.measuredHeight)
        root.jumpDrawablesToCurrentState()
        assertTrue(root.measuredHeight > 0)
        val bitmap = Bitmap.createBitmap(720, root.measuredHeight, Bitmap.Config.ARGB_8888)
        root.draw(Canvas(bitmap))
        val file = File("build/outputs/ui/$name.png"); file.parentFile!!.mkdirs()
        file.outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        bitmap.recycle()
    }
}
