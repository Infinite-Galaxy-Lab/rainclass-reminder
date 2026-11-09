package lab.infinitegalaxy.rainclass.ui

import android.graphics.BitmapFactory
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import lab.infinitegalaxy.rainclass.BuildConfig

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun LoginScreen(state: LoginState, onBack: () -> Unit, onRetry: () -> Unit, onWeb: () -> Unit,
                         onCopyLink: () -> Unit = {}, onPublicAccount: (Boolean) -> Unit = {}) {
    var showLinkHelp by remember { mutableStateOf(false) }
    if (showLinkHelp && state.confirmationLink != null && BuildConfig.DEBUG) {
        AlertDialog(onDismissRequest = { showLinkHelp = false }, title = { Text("公众号链接登录实验") },
            text = { Text("复制后，在微信「文件传输助手」粘贴链接，发送给自己并点开。按微信页面提示操作，再返回本应用等待连接。\n\n此方式尚待真机验证。若仍要求摄像头扫码，请返回扫码登录。链接仅供本次登录，请勿发给他人。") },
            confirmButton = { TextButton(onClick = { showLinkHelp = false; onCopyLink() }) { Text("复制公众号链接") } },
            dismissButton = { TextButton(onClick = { showLinkHelp = false }) { Text("取消") } })
    }
    Scaffold(topBar = {
        TopAppBar(title = { Text("连接雨课堂") }, navigationIcon = {
            TextButton(onClick = onBack) { Text("返回") }
        })
    }) { insets ->
        Column(Modifier.fillMaxSize().padding(insets).verticalScroll(rememberScrollState())
            .padding(horizontal = 24.dp, vertical = 28.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(20.dp)) {
            Text(if (state.publicAccount) "公众号链接登录实验" else "微信扫码登录", style = MaterialTheme.typography.headlineSmall)
            Text(if (state.publicAccount) "尝试在微信中打开公众号场景链接。\n确认后返回，应用将继续等待登录结果。"
                else "请用微信扫描二维码。确认后返回应用，将自动连接你的课堂。\n微信可能禁止从相册识别登录码。",
                color = Muted, textAlign = TextAlign.Center, style = MaterialTheme.typography.bodyMedium)
            val bitmap = remember(state.image) {
                state.image?.let { BitmapFactory.decodeByteArray(it, 0, it.size)?.asImageBitmap() }
            }
            Box(Modifier.widthIn(max = 280.dp).fillMaxWidth().aspectRatio(1f)
                .background(Color.White, RoundedCornerShape(20.dp)).padding(12.dp), contentAlignment = Alignment.Center) {
                when {
                    bitmap != null -> Image(bitmap, contentDescription = "微信登录二维码", modifier = Modifier.fillMaxSize())
                    state.loading -> CircularProgressIndicator()
                    else -> Text(state.error ?: "准备连接…", textAlign = TextAlign.Center, color = Muted)
                }
            }
            if (state.image != null) Text("等待微信确认…", color = Muted)
            if (BuildConfig.DEBUG && state.confirmationLink != null) {
                Button(onClick = { showLinkHelp = true }) { Text("复制链接到微信") }
            }
            OutlinedButton(onClick = onRetry, enabled = !state.loading) { Text("获取新二维码") }
            if (BuildConfig.DEBUG) {
                TextButton(onClick = { showLinkHelp = false; onPublicAccount(!state.publicAccount) }) {
                    Text(if (state.publicAccount) "返回微信扫码登录" else "尝试公众号链接登录")
                }
            }
            TextButton(onClick = onWeb) { Text("使用官方网页登录") }
        }
    }
}
