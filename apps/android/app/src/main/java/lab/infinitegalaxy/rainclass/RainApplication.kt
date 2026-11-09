package lab.infinitegalaxy.rainclass

import android.app.Application
import lab.infinitegalaxy.rainclass.data.RainRepository

class RainApplication : Application() {
    val repository by lazy { RainRepository(this) }
}
