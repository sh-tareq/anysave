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
-keepclassmembers enum com.vidgrab.app.** { *; }

# Keep line numbers for readable crash reports
-keepattributes SourceFile,LineNumberTable
-renamesourcefileattribute SourceFile
