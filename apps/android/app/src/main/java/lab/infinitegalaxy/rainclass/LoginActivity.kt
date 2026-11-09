package lab.infinitegalaxy.rainclass

import android.content.Intent
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
import kotlinx.coroutines.launch
import lab.infinitegalaxy.rainclass.ui.LoginScreen
import lab.infinitegalaxy.rainclass.ui.LoginViewModel
import lab.infinitegalaxy.rainclass.ui.RainTheme

class LoginActivity : ComponentActivity() {
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
                LoginScreen(state, onBack = { finish() }, onRetry = model::restart, onWeb = {
                    model.stop()
                    webLogin.launch(Intent(this, WebLoginActivity::class.java))
                })
            }
        }
        if (savedInstanceState == null) model.start()
        else if (model.state.value.image == null && !model.state.value.complete) model.start()
        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.STARTED) {
                model.state.collect { if (it.complete) complete() }
            }
        }
    }

    private fun complete() { setResult(RESULT_OK); finish() }
}
