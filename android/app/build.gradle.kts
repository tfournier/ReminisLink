import java.util.Properties

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

// Version (SemVer, CLAUDE.md rule 6): android/version.properties, written by
// "make android-vX.Y.Z"; the release tag is android-v<versionName>. The
// versionCode is derived from the versionName (same formula as
// scripts/android-version-code.sh): a mismatch fails the build.
val appVersion = Properties().apply {
    rootProject.file("version.properties").inputStream().use { load(it) }
}
val appVersionName: String = appVersion.getProperty("versionName")
val appVersionCode: Int = appVersion.getProperty("versionCode").toInt()

fun versionCodeOf(name: String): Int {
    val match = Regex("""^(\d+)\.(\d+)\.(\d+)(?:-(beta|rc)\.(\d+))?$""").matchEntire(name)
        ?: throw GradleException("versionName $name is not X.Y.Z, X.Y.Z-beta.N or X.Y.Z-rc.N")
    val (x, y, z, stage, n) = match.destructured
    if (y.toInt() > 99 || z.toInt() > 99) throw GradleException("versionName $name: minor and patch go up to 99")
    val suffix = when (stage) {
        "" -> 99
        "beta" -> n.toInt().takeIf { it in 1..49 } ?: throw GradleException("beta.N goes from 1 to 49")
        else -> n.toInt().takeIf { it in 1..48 }?.let { 50 + it } ?: throw GradleException("rc.N goes from 1 to 48")
    }
    return x.toInt() * 1_000_000 + y.toInt() * 10_000 + z.toInt() * 100 + suffix
}

if (versionCodeOf(appVersionName) != appVersionCode) {
    throw GradleException("version.properties: versionCode $appVersionCode does not match versionName $appVersionName (${versionCodeOf(appVersionName)})")
}

// Release signing (docs: android/README.md, "Release"): read from environment
// variables or Gradle properties (~/.gradle/gradle.properties), never from
// the repository. Without them the release APK is unsigned (F-Droid signs its
// own build); -Preminislink.signWithDebugKey=true signs it with the debug key
// for local tests and the CI artifact without secrets (labelled as such).
fun secret(env: String, property: String): String? =
    System.getenv(env)?.takeIf { it.isNotBlank() } ?: (findProperty(property) as String?)?.takeIf { it.isNotBlank() }

val releaseKeystore = secret("RL_ANDROID_KEYSTORE", "reminislink.keystore")
val releaseKeystorePassword = secret("RL_ANDROID_KEYSTORE_PASSWORD", "reminislink.keystorePassword")
val releaseKeyAlias = secret("RL_ANDROID_KEY_ALIAS", "reminislink.keyAlias")
val releaseKeyPassword = secret("RL_ANDROID_KEY_PASSWORD", "reminislink.keyPassword")
val hasReleaseKey = listOf(releaseKeystore, releaseKeystorePassword, releaseKeyAlias, releaseKeyPassword).all { it != null }
val signWithDebugKey = findProperty("reminislink.signWithDebugKey") == "true"

android {
    namespace = "link.reminis.app"
    // compileSdk and targetSdk 36 (Android 16): Google Play requires API 36 for
    // new apps since 31 August 2026. The app is edge-to-edge and uses
    // AndroidX's back dispatcher (predictive back) already.
    compileSdk = 36

    defaultConfig {
        applicationId = "link.reminis.app"
        minSdk = 26
        targetSdk = 36
        versionCode = appVersionCode
        versionName = appVersionName
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        // Integration test against a real pak (see docs/specs/10-app-android.md):
        // -Pandroid.testInstrumentationRunnerArguments.pairingLink=reminislink://pair?...
    }

    signingConfigs {
        if (hasReleaseKey) {
            create("release") {
                storeFile = file(releaseKeystore!!)
                storePassword = releaseKeystorePassword
                keyAlias = releaseKeyAlias
                keyPassword = releaseKeyPassword
                enableV1Signing = false
                enableV2Signing = true
                enableV3Signing = true
            }
        }
    }

    buildTypes {
        release {
            // R8: shrinking and optimization; the libraries ship their own
            // rules (kotlinx.serialization, OkHttp, coroutines), the app's
            // are in proguard-rules.pro.
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            signingConfig = when {
                hasReleaseKey -> signingConfigs.getByName("release")
                signWithDebugKey -> signingConfigs.getByName("debug")
                else -> null
            }
            // Reproducible builds (F-Droid): no VCS information in the APK.
            vcsInfo.include = false
        }
        // The release build (R8, same rules) signed with the debug key, for the
        // instrumented smoke test of the minified app (ReleaseSmokeTest):
        // ./gradlew connectedMinifiedAndroidTest -Preminislink.testBuildType=minified
        create("minified") {
            initWith(getByName("release"))
            signingConfig = signingConfigs.getByName("debug")
            matchingFallbacks += listOf("release")
            // The entry points the smoke test calls stay (everything else is shrunk as in release).
            proguardFiles("proguard-smoke.pro")
            testProguardFiles("proguard-test.pro")
        }
    }

    testBuildType = (findProperty("reminislink.testBuildType") as String?) ?: "debug"

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
    }

    packaging {
        resources.excludes += setOf("META-INF/AL2.0", "META-INF/LGPL2.1", "META-INF/versions/9/OSGI-INF/MANIFEST.MF")
    }

    // Reproducible builds for F-Droid: no dependency metadata blob signed by Google.
    dependenciesInfo {
        includeInApk = false
        includeInBundle = false
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

dependencies {
    implementation(project(":core"))
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.tink.android)
    implementation(libs.zxing.core)

    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.ui.tooling.preview)
    implementation(libs.compose.material3)
    implementation(libs.compose.material.icons.core)
    implementation(libs.activity.compose)
    implementation(libs.lifecycle.viewmodel.compose)
    implementation(libs.lifecycle.runtime.compose)
    implementation(libs.navigation.compose)
    implementation(libs.camerax.camera2)
    implementation(libs.camerax.lifecycle)
    implementation(libs.camerax.view)

    testImplementation(libs.junit)

    androidTestImplementation(platform(libs.compose.bom))
    androidTestImplementation(libs.compose.ui.test.junit4)
    androidTestImplementation(libs.androidx.test.ext.junit)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.okhttp.mockwebserver)
    androidTestImplementation(libs.okhttp.tls)
    debugImplementation(libs.compose.ui.test.manifest)
    "minifiedImplementation"(libs.compose.ui.test.manifest)
}
