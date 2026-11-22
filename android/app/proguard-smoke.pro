# Extra rules of the "minified" build type only (never the release): the
# app's container, read by ReleaseSmokeTest, stays reachable. The protocol
# models, the client, Tink and OkHttp are shrunk and optimized exactly as in
# the release build, which is what the smoke test checks.
-keep class link.reminis.app.AppContainer { public *; }
-keep class link.reminis.app.ReminisLinkAppKt { public *; }
-keep class link.reminis.app.ConsoleEvents { public *; }
-keep class link.reminis.app.MainActivity { public *; }
-keep class link.reminis.core.demo.DemoConsole { public *; }
-keep class link.reminis.core.demo.FakePak { public *; }

# Shared with the test APK at run time (the runner traces with them; Compose test reads the composition).
-keep class androidx.tracing.** { *; }
-keep class androidx.compose.ui.** { *; }
-keep class androidx.compose.runtime.** { *; }
-keep class androidx.activity.ComponentActivity { *; }
-keep class androidx.lifecycle.** { *; }
-keep class kotlinx.coroutines.** { *; }
-keep class kotlin.** { *; }
-keep class androidx.test.** { *; }
-keep class androidx.core.** { *; }
