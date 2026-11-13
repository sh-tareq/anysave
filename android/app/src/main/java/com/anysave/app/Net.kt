package com.anysave.app

import java.net.HttpURLConnection
import java.net.InetSocketAddress
import java.net.Proxy
import java.net.URI
import java.net.URL

/**
 * Proxy setting for every connection the app makes itself, and for yt-dlp.
 * Blank = this phone's own proxy settings; "direct" = never a proxy; otherwise
 * http://host:port or socks5://host:port (a bare host:port means http).
 */
object Net {
    private data class Parsed(val type: Proxy.Type, val host: String, val port: Int, val scheme: String)

    private fun parse(setting: String): Parsed? {
        val s = setting.trim()
        if (s.isEmpty() || s.equals("system", true) || s.equals("direct", true)) return null
        val uri = runCatching { URI(if (s.contains("://")) s else "http://$s") }.getOrNull() ?: return null
        val host = uri.host ?: return null
        if (uri.port !in 1..65535) return null
        return when (uri.scheme?.lowercase()) {
            "http", "https" -> Parsed(Proxy.Type.HTTP, host, uri.port, "http")
            "socks5", "socks5h" -> Parsed(Proxy.Type.SOCKS, host, uri.port, "socks5")
            else -> null
        }
    }

    /** True when the text is blank, "system", "direct" or a proxy address we can use. */
    fun valid(setting: String): Boolean {
        val s = setting.trim()
        return s.isEmpty() || s.equals("system", true) || s.equals("direct", true) || parse(s) != null
    }

    /** Opens a connection the way the setting says. */
    fun open(url: URL): HttpURLConnection {
        val setting = Settings.proxy.value.trim()
        val conn = when {
            setting.equals("direct", true) -> url.openConnection(Proxy.NO_PROXY)
            else -> parse(setting)?.let { url.openConnection(Proxy(it.type, InetSocketAddress.createUnresolved(it.host, it.port))) }
                ?: url.openConnection() // the phone's own proxy settings
        }
        return conn as HttpURLConnection
    }

    /** The value for yt-dlp's --proxy, or null to leave yt-dlp alone ("" means no proxy). */
    fun ytdlpProxy(): String? {
        val setting = Settings.proxy.value.trim()
        if (setting.equals("direct", true)) return ""
        return parse(setting)?.let { "${it.scheme}://${it.host}:${it.port}" }
    }
}
