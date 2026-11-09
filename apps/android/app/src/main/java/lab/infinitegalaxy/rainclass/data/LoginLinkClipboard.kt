package lab.infinitegalaxy.rainclass.data

import android.content.ClipData
import android.content.ClipboardManager
import android.os.Build
import android.os.PersistableBundle

internal class LoginLinkClipboard(private val clipboard: ClipboardManager) {
    private var copied: String? = null

    fun copy(link: String) {
        val clip = ClipData.newPlainText("雨课堂登录确认", link)
        clip.description.extras = PersistableBundle().apply {
            putBoolean("android.content.extra.IS_SENSITIVE", true)
        }
        clipboard.setPrimaryClip(clip)
        copied = link
    }

    fun clear() {
        val link = copied ?: return
        copied = null
        // 切微信后用户可能复制了其他内容，只清理由本入口留下的链接。
        val clip = clipboard.primaryClip ?: return
        if (clip.itemCount != 1 || clip.getItemAt(0).text?.toString() != link) return
        if (Build.VERSION.SDK_INT >= 28) clipboard.clearPrimaryClip()
        else clipboard.setPrimaryClip(ClipData.newPlainText("", ""))
    }
}
