plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

android {
    namespace = "lab.infinitegalaxy.rainclass"
    compileSdk = 35
    buildToolsVersion = "35.0.0"
    ndkVersion = "28.2.13676358"

    defaultConfig {
        applicationId = "lab.infinitegalaxy.rainclass"
        minSdk = 26
        targetSdk = 35
        versionCode = 6
        versionName = "0.1.0-alpha.6"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        testInstrumentationRunnerArguments["additionalTestOutputDir"] =
            "/sdcard/Android/media/lab.infinitegalaxy.rainclass.verification/verification"
        ndk { abiFilters += listOf("arm64-v8a", "x86_64") }
    }
    buildFeatures { compose = true; buildConfig = true }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }

    val signingPath = providers.environmentVariable("YKT_ANDROID_KEYSTORE")
    if (signingPath.isPresent) {
        signingConfigs.create("distribution") {
            storeFile = file(signingPath.get())
            storePassword = providers.environmentVariable("YKT_ANDROID_STORE_PASSWORD").orNull
            keyAlias = providers.environmentVariable("YKT_ANDROID_KEY_ALIAS").orNull
            keyPassword = providers.environmentVariable("YKT_ANDROID_KEY_PASSWORD").orNull
        }
    }
    buildTypes {
        debug { applicationIdSuffix = ".debug"; versionNameSuffix = "-debug" }
        create("verification") {
            initWith(getByName("debug"))
            applicationIdSuffix = ".verification"
            matchingFallbacks += "debug"
        }
        release {
            isMinifyEnabled = false
            if (signingPath.isPresent) { signingConfig = signingConfigs.getByName("distribution") }
        }
    }
    testBuildType = "verification"
    packaging { jniLibs { useLegacyPackaging = false } }
}

dependencies {
    implementation(files("libs/raincore.aar"))
    val composeBom = platform("androidx.compose:compose-bom:2025.04.01")
    implementation(composeBom)
    androidTestImplementation(composeBom)
    implementation("androidx.activity:activity-compose:1.10.1")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.8.7")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.8.7")
    implementation("androidx.work:work-runtime-ktx:2.10.0")
    implementation("androidx.core:core-ktx:1.15.0")
    testImplementation("junit:junit:4.13.2")
    androidTestImplementation("androidx.test:runner:1.6.2")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")
    androidTestImplementation("androidx.test:core-ktx:1.6.1")
    androidTestImplementation("androidx.work:work-testing:2.10.0")
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")
    debugImplementation("androidx.compose.ui:ui-test-manifest")
    add("verificationImplementation", "androidx.compose.ui:ui-test-manifest")
}
