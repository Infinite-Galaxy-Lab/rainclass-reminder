package lab.infinitegalaxy.rainclass.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

internal val Ink = Color(0xFF192B45)
internal val Blue = Color(0xFF2F68C5)
internal val Muted = Color(0xFF69798C)
internal val Canvas = Color(0xFFF5F7FB)
internal val Amber = Color(0xFFAE5C1B)

@Composable
fun RainTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = lightColorScheme(primary = Blue, secondary = Blue,
        secondaryContainer = Color(0xFFE5EEFF), onSecondaryContainer = Ink,
        background = Canvas, surface = Color.White, onSurface = Ink), content = content)
}
