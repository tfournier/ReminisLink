// :core — pure Kotlin/JVM: protocol models, RL2-ED25519 signing, pairing v2,
// TLS pin, API client. No Android dependency: tested with plain JUnit.
plugins {
    alias(libs.plugins.kotlin.jvm)
    alias(libs.plugins.kotlin.serialization)
}

// Built by the running JDK (21), as Java 17 bytecode like the app.
java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

dependencies {
    api(libs.kotlinx.coroutines.core)
    api(libs.kotlinx.serialization.json)
    api(libs.okhttp)
    // Ed25519 (RFC 8032, deterministic) from Tink's pure Java "subtle" API.
    implementation(libs.tink.android)

    testImplementation(libs.junit)
    testImplementation(libs.okhttp.mockwebserver)
    testImplementation(libs.okhttp.tls)
}

tasks.test {
    // The shared test vectors are read in place from the repository
    // (protocol/test-vectors), never copied.
    val vectors = rootProject.layout.projectDirectory.dir("../protocol/test-vectors").asFile
    systemProperty("reminislink.vectors", vectors.absolutePath)
    inputs.dir(vectors).withPropertyName("testVectors")
    // AndroidApiTest: every JDK method this module calls must exist on Android 8 (API 26), the app's
    // minSdk, per the API database of the installed Android SDK (Android lint does not check a JVM module).
    val sdk = System.getenv("ANDROID_HOME") ?: System.getenv("ANDROID_SDK_ROOT")
        ?: rootProject.file("local.properties").takeIf { it.isFile }?.readLines()
            ?.firstOrNull { it.startsWith("sdk.dir=") }?.substringAfter("=")
        ?: "${System.getProperty("user.home")}/Library/Android/sdk"
    val apiDatabase = file("$sdk/platforms").listFiles().orEmpty()
        .mapNotNull { dir -> dir.name.removePrefix("android-").toIntOrNull()?.let { it to File(dir, "data/api-versions.xml") } }
        .filter { it.second.isFile }.maxByOrNull { it.first }?.second
    apiDatabase?.let {
        systemProperty("reminislink.apiVersions", it.absolutePath)
        inputs.file(it).withPropertyName("androidApiDatabase")
    }
    systemProperty("reminislink.coreClasses", sourceSets.main.get().output.classesDirs.asPath)
    systemProperty("reminislink.minSdk", "26")
    testLogging {
        events("failed")
        exceptionFormat = org.gradle.api.tasks.testing.logging.TestExceptionFormat.FULL
    }
}
