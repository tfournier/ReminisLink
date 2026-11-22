# R8 rules of the release build (docs/specs/10-app-android.md, "Publication").
#
# The libraries ship their own consumer rules in their jars: OkHttp (TLS
# providers it may load), kotlinx.coroutines, kotlinx.serialization (the
# generated serializers of @Serializable classes) and AndroidX. What follows
# only covers this app's own code and the warnings of Tink's optional
# annotations.

# kotlinx.serialization: the serializers of the protocol models are looked up
# through their companion objects (ProtocolJson.decodeFromString<T>()).
-keepattributes RuntimeVisibleAnnotations,AnnotationDefault,InnerClasses,Signature
-keepclassmembers @kotlinx.serialization.Serializable class link.reminis.core.** {
    *** Companion;
    *** INSTANCE;
    kotlinx.serialization.KSerializer serializer(...);
}
-keepclasseswithmembers class link.reminis.core.** {
    kotlinx.serialization.KSerializer serializer(...);
}
-keep,includedescriptorclasses class link.reminis.core.**$$serializer { *; }

# Tink (Ed25519 of the "subtle" API, pure Java): annotations used at compile
# time only, not shipped.
-dontwarn com.google.errorprone.annotations.**
-dontwarn javax.annotation.**
-dontwarn com.google.api.client.**
-dontwarn org.joda.time.**

# OkHttp's optional TLS providers (never on Android's classpath).
-dontwarn org.bouncycastle.**
-dontwarn org.conscrypt.**
-dontwarn org.openjsse.**

# Stack traces in crash reports stay readable with the mapping file kept
# next to each release (app/build/outputs/mapping/release/mapping.txt).
-keepattributes SourceFile,LineNumberTable
-renamesourcefileattribute SourceFile
