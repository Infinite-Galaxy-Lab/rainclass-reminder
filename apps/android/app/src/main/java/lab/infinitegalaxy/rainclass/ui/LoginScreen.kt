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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun LoginScreen(state: LoginState, onBack: () -> Unit, onRetry: () -> Unit, onWeb: () -> Unit) {
    Scaffold(topBar = {
        TopAppBar(title = { Text("连接雨课堂") }, navigationIcon = {
            TextButton(onClick = onBack) { Text("返回") }
        })
    }) { insets ->
        Column(Modifier.fillMaxSize().padding(insets).verticalScroll(rememberScrollState())
            .padding(horizontal = 24.dp, vertical = 28.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(20.dp)) {
            Text("微信扫码登录", style = MaterialTheme.typography.headlineSmall)
            Text("截图后，在微信「扫一扫 → 相册」识别。\n确认后返回应用，将自动连接你的课堂。",
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
            OutlinedButton(onClick = onRetry, enabled = !state.loading) { Text("获取新二维码") }
            TextButton(onClick = onWeb) { Text("使用官方网页登录") }
        }
    }
}
