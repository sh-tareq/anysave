# ---- youtubedl-android (yt-dlp + Python + ffmpeg wrapper) ----
# Maps yt-dlp JSON with Jackson (reflection) and unpacks bundled zips with commons-compress.
-keep class com.yausername.** { *; }
-keep class com.fasterxml.jackson.** { *; }
-keepattributes *Annotation*, Signature, InnerClasses, EnclosingMethod
-keep class org.apache.commons.compress.** { *; }
-keep class org.apache.commons.io.** { *; }
-dontwarn com.fasterxml.jackson.**
-dontwarn org.apache.commons.**
-dontwarn org.tukaani.xz.**
-dontwarn org.brotli.dec.**
-dontwarn com.github.luben.zstd.**
-dontwarn org.objectweb.asm.**
-dontwarn java.beans.**
-dontwarn javax.**

# ---- AnySave ----
# Services/activities/tile are referenced from the manifest (kept automatically).
# Enum names are stored in settings and history.json: keep them stable.
-keepclassmembers enum com.anysave.app.** { *; }

# Keep line numbers for readable crash reports
-keepattributes SourceFile,LineNumberTable
-renamesourcefileattribute SourceFile

# ---- Converter libraries ----
# PDFBox: JPEG 2000 support is an optional add-on we don't ship; security handlers are made by reflection.
-dontwarn com.gemalto.jp2.**
-keep class com.tom_roush.pdfbox.pdmodel.encryption.** { *; }
-keep class com.tom_roush.fontbox.** { *; }
# Media3 Transformer: R8's optimising breaks its Android 12-only code on older phones
# (NoClassDefFoundError: android.media.metrics.LogSessionId on Android 10). Keep it as Google built it.
-keep class androidx.media3.transformer.** { *; }
-keep class androidx.media3.exoplayer.** { *; }
-keep class androidx.media3.effect.** { *; }
-keep class androidx.media3.common.** { *; }
-keep class androidx.media3.ui.** { *; }
