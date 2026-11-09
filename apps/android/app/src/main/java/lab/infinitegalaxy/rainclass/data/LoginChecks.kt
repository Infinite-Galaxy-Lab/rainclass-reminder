package lab.infinitegalaxy.rainclass.data

internal class LoginChecks {
    private var previous: String? = null
    private var attempts = 0
    private var retryAt = 0L

    fun resume() { previous = null; attempts = 0; retryAt = 0 }

    fun begin(cookie: String, automatic: Boolean, now: Long): Boolean {
        if (cookie != previous) { attempts = 0; retryAt = 0 }
        if (automatic && (attempts >= 3 || now < retryAt)) return false
        previous = cookie
        attempts++
        retryAt = now + 3_000
        return true
    }
}
