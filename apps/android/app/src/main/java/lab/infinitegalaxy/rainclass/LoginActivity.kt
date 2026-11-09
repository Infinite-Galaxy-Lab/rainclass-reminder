package lab.infinitegalaxy.rainclass

import android.content.Intent
import android.content.ClipboardManager
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.viewModels
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.runtime.getValue
import android.widget.Toast
import kotlinx.coroutines.launch
import lab.infinitegalaxy.rainclass.ui.LoginScreen
import lab.infinitegalaxy.rainclass.ui.LoginViewModel
import lab.infinitegalaxy.rainclass.ui.RainTheme
import lab.infinitegalaxy.rainclass.data.LoginLinkClipboard

class LoginActivity : ComponentActivity() {
    private val loginClipboard by lazy { LoginLinkClipboard(getSystemService(ClipboardManager::class.java)) }
    private val model: LoginViewModel by viewModels {
        object : ViewModelProvider.Factory {
            @Suppress("UNCHECKED_CAST")
            override fun <T : ViewModel> create(modelClass: Class<T>): T =
                LoginViewModel((application as RainApplication).loginSessionFactory) as T
        }
    }
    private val webLogin = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        if (it.resultCode == RESULT_OK) complete() else model.restart()
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            val state by model.state.collectAsStateWithLifecycle()
            RainTheme {
                LoginScreen(state, onBack = { finish() }, onRetry = {
                    loginClipboard.clear()
                    model.restart()
                }, onWeb = {
                    loginClipboard.clear()
                    model.stop()
                    webLogin.launch(Intent(this, WebLoginActivity::class.java))
                }, onCopyLink = {
                    if (BuildConfig.DEBUG) model.state.value.confirmationLink?.let {
                        loginClipboard.copy(it)
                        Toast.makeText(this, "已复制，请切换微信打开链接", Toast.LENGTH_SHORT).show()
                    }
                })
            }
        }
        if (savedInstanceState == null) model.start()
        else if (model.state.value.image == null && !model.state.value.complete) model.start()
        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.STARTED) {
                model.state.collect {
                    if (it.confirmationLink == null) loginClipboard.clear()
                    if (it.complete) complete()
                }
            }
        }
    }

    override fun onDestroy() {
        loginClipboard.clear()
        super.onDestroy()
    }

    private fun complete() { loginClipboard.clear(); setResult(RESULT_OK); finish() }
}
