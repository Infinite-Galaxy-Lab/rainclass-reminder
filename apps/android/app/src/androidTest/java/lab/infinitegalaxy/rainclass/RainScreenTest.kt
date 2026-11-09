package lab.infinitegalaxy.rainclass

import android.graphics.Bitmap
import android.content.pm.ActivityInfo
import android.content.res.Configuration
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.width
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasScrollToNodeAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.unit.dp
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import lab.infinitegalaxy.rainclass.data.Snapshot
import lab.infinitegalaxy.rainclass.data.Task
import lab.infinitegalaxy.rainclass.ui.RainScreen
import lab.infinitegalaxy.rainclass.ui.RainTheme
import lab.infinitegalaxy.rainclass.ui.ScreenState
import org.junit.Rule
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File

@RunWith(AndroidJUnit4::class)
class RainScreenTest {
    @get:Rule val compose = createAndroidComposeRule<MainActivity>()

    @Before fun portrait() {
        compose.activityRule.scenario.onActivity { it.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_PORTRAIT }
        compose.waitUntil(5000) { compose.activity.resources.configuration.orientation == Configuration.ORIENTATION_PORTRAIT }
        compose.waitForIdle()
    }

    @Test fun signedOutScreenAndSettings() {
        compose.onNodeWithText("登录雨课堂").assertIsDisplayed()
        screenshot("signed-out.png")
        compose.onNodeWithContentDescription("设置").performClick()
        compose.onNodeWithText("后台同步与提醒").assertIsDisplayed()
        compose.onNodeWithText("完成").performClick()
    }

    @Test fun narrowScreenShowsZeroScoreAndKeepsClassesSeparate() {
        val now = System.currentTimeMillis()
        val tasks = listOf(
            Task("synthetic-a", "homework", "作业", "合成作业 · 零分", "示例课程 A 班", now + 3600000,
                0, "pending", 0.0, 12.5, "https://changjiang.yuketang.cn/"),
            Task("synthetic-b", "homework", "作业", "合成作业 · 未提供成绩", "示例课程 B 班", now + 7200000,
                0, "pending", null, null, "https://changjiang.yuketang.cn/"),
        )
        compose.activityRule.scenario.onActivity { activity ->
            activity.setContent {
                RainTheme {
                    Box(Modifier.width(320.dp)) {
                        RainScreen(ScreenState(snapshot = Snapshot("ready", now, tasks)), true,
                            {}, {}, {}, {}, {}, {})
                    }
                }
            }
        }
        scrollTo("合成作业 · 零分")
        scrollTo("得分 0 / 12.5")
        screenshot("tasks-320dp.png")
        scrollTo("合成作业 · 未提供成绩")
        scrollTo("已完成")
        compose.onNodeWithText("已完成").performClick()
        scrollTo("这里暂时没有符合条件的任务")
    }

    private fun scrollTo(text: String) {
        compose.onNode(hasScrollToNodeAction()).performScrollToNode(hasText(text))
        compose.onNodeWithText(text).assertIsDisplayed()
    }

    private fun screenshot(name: String) {
        compose.waitForIdle()
        val bitmap = compose.onNode(hasScrollToNodeAction()).captureToImage().asAndroidBitmap()
        val directory = InstrumentationRegistry.getArguments().getString("additionalTestOutputDir")
            ?.let(::File) ?: File(compose.activity.externalMediaDirs.first(), "verification")
        val file = File(directory, name)
        file.parentFile!!.mkdirs()
        file.outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        bitmap.recycle()
    }
}
