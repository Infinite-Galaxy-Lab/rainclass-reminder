package lab.infinitegalaxy.rainclass.data

import org.junit.Assert.*
import org.junit.Test

class LoginChecksTest {
    @Test fun sameCookieRetriesWithDelayAndBoundedRequests() {
        val checks = LoginChecks()
        assertTrue(checks.begin("synthetic", true, 0))
        assertFalse(checks.begin("synthetic", true, 2999))
        assertTrue(checks.begin("synthetic", true, 3000))
        assertTrue(checks.begin("synthetic", true, 6000))
        assertFalse(checks.begin("synthetic", true, 9000))
    }

    @Test fun returningFromWechatChecksUnchangedCookieAgain() {
        val checks = LoginChecks()
        repeat(3) { assertTrue(checks.begin("synthetic", true, it * 3000L)) }
        checks.resume()
        assertTrue(checks.begin("synthetic", true, 6100))
    }

    @Test fun changedCookieAndManualActionCanCheckImmediately() {
        val checks = LoginChecks()
        assertTrue(checks.begin("first-synthetic", true, 0))
        assertTrue(checks.begin("new-synthetic", true, 1))
        assertTrue(checks.begin("new-synthetic", false, 2))
    }
}
