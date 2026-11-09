package lab.infinitegalaxy.rainclass

import android.app.Application
import lab.infinitegalaxy.rainclass.data.RainRepository
import lab.infinitegalaxy.rainclass.data.LoginSession
import lab.infinitegalaxy.rainclass.data.NativeLoginSession

class RainApplication : Application() {
    val repository by lazy { RainRepository(this) }
    internal var loginSessionFactory: () -> LoginSession = { NativeLoginSession(repository) }
}
