package ai.hoxi.app

import android.app.Activity
import android.content.ClipData
import android.content.Intent
import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.provider.OpenableColumns
import android.webkit.MimeTypeMap
import androidx.core.content.FileProvider
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel
import java.io.ByteArrayOutputStream
import java.io.File
import java.util.UUID
import java.util.concurrent.Executors

private const val PICK_DOCUMENT_REQUEST = 4219

/**
 * Native Android document picker and system share bridge for `hoxi.ai/files`.
 */
class HoxiFilesPlugin(private val activity: Activity) {
    private var channel: MethodChannel? = null
    private var pendingPickResult: MethodChannel.Result? = null
    private var pendingMaxBytes: Int = 15 * 1024 * 1024
    private val ioExecutor = Executors.newSingleThreadExecutor()
    private val mainHandler = Handler(Looper.getMainLooper())

    fun attach(messenger: BinaryMessenger) {
        val ch = MethodChannel(messenger, "hoxi.ai/files")
        channel = ch
        ch.setMethodCallHandler { call, result -> handleCall(call, result) }
    }

    fun destroy() {
        pendingPickResult = null
        ioExecutor.shutdownNow()
    }

    private fun handleCall(call: MethodCall, result: MethodChannel.Result) {
        when (call.method) {
            "pickDocument" -> {
                val args = call.arguments as? Map<*, *>
                val maxBytes = (args?.get("maxBytes") as? Number)?.toInt() ?: (15 * 1024 * 1024)
                launchDocumentPicker(maxBytes.coerceAtLeast(1), result)
            }
            "shareFile" -> {
                val args = call.arguments as? Map<*, *>
                val filename = args?.get("filename") as? String
                val mime = (args?.get("mime") as? String)?.trim().orEmpty()
                val bytes = args?.get("bytes") as? ByteArray
                if (filename.isNullOrBlank() || bytes == null) {
                    result.error("invalid_args", "filename and bytes are required", null)
                    return
                }
                shareFile(filename, mime, bytes, result)
            }
            else -> result.notImplemented()
        }
    }

    private fun launchDocumentPicker(maxBytes: Int, result: MethodChannel.Result) {
        pendingPickResult?.success(null)
        pendingPickResult = result
        pendingMaxBytes = maxBytes
        val intent = Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
            addCategory(Intent.CATEGORY_OPENABLE)
            type = "*/*"
        }
        try {
            activity.startActivityForResult(intent, PICK_DOCUMENT_REQUEST)
        } catch (err: Exception) {
            pendingPickResult = null
            result.error("picker_unavailable", err.message ?: "document picker unavailable", null)
        }
    }

    fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?): Boolean {
        if (requestCode != PICK_DOCUMENT_REQUEST) return false
        val cb = pendingPickResult
        pendingPickResult = null
        if (cb == null) return true
        val uri: Uri? = if (resultCode == Activity.RESULT_OK) data?.data else null
        if (uri == null) {
            cb.success(null)
            return true
        }
        val maxBytes = pendingMaxBytes
        ioExecutor.execute {
            try {
                val meta = resolveMeta(uri)
                if (meta.size > 0 && meta.size > maxBytes.toLong()) {
                    mainHandler.post { cb.error("file_too_large", "File exceeds limit", meta.size) }
                    return@execute
                }
                val bytes = readBounded(uri, maxBytes)
                if (bytes == null) {
                    mainHandler.post { cb.error("file_too_large", "File exceeds limit", maxBytes + 1) }
                    return@execute
                }
                mainHandler.post {
                    cb.success(
                        mapOf(
                            "name" to meta.name,
                            "mime" to meta.mime,
                            "bytes" to bytes,
                        )
                    )
                }
            } catch (err: Exception) {
                mainHandler.post {
                    cb.error("read_failed", err.message ?: "failed to read document", null)
                }
            }
        }
        return true
    }

    private data class DocumentMeta(val name: String, val mime: String, val size: Long)

    private fun resolveMeta(uri: Uri): DocumentMeta {
        var displayName = ""
        var size = -1L
        activity.contentResolver.query(
            uri,
            arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE),
            null,
            null,
            null,
        )?.use { cursor ->
            if (cursor.moveToFirst()) {
                val nameIdx = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                if (nameIdx >= 0) displayName = cursor.getString(nameIdx)?.trim().orEmpty()
                val sizeIdx = cursor.getColumnIndex(OpenableColumns.SIZE)
                if (sizeIdx >= 0 && !cursor.isNull(sizeIdx)) size = cursor.getLong(sizeIdx)
            }
        }
        if (displayName.isBlank()) {
            displayName = uri.lastPathSegment?.substringAfterLast('/')?.trim().orEmpty()
        }
        if (displayName.isBlank()) displayName = "document"
        var mime = activity.contentResolver.getType(uri)?.trim().orEmpty()
        if (mime.isBlank() || mime == "application/octet-stream") {
            val ext = displayName.substringAfterLast('.', "").lowercase()
            if (ext.isNotEmpty()) {
                mime = MimeTypeMap.getSingleton().getMimeTypeFromExtension(ext).orEmpty()
            }
        }
        if (mime.isBlank()) mime = "application/octet-stream"
        return DocumentMeta(displayName, mime, size)
    }

    private fun readBounded(uri: Uri, maxBytes: Int): ByteArray? {
        val stream = activity.contentResolver.openInputStream(uri)
            ?: throw IllegalStateException("Unable to open selected document")
        stream.use { input ->
            val out = ByteArrayOutputStream()
            val buf = ByteArray(16 * 1024)
            var total = 0
            while (true) {
                val n = input.read(buf)
                if (n <= 0) break
                total += n
                if (total > maxBytes) return null
                out.write(buf, 0, n)
            }
            return out.toByteArray()
        }
    }

    private fun sanitizeFilename(raw: String): String {
        val base = raw.substringAfterLast('/').substringAfterLast('\\')
            .replace(Regex("[\\r\\n\\t]"), "_")
            .trim()
        return if (base.isEmpty() || base == "." || base == "..") "file.bin" else base
    }

    private fun shareFile(
        filename: String,
        mime: String,
        bytes: ByteArray,
        result: MethodChannel.Result,
    ) {
        val safeName = sanitizeFilename(filename)
        val effectiveMime = if (mime.isNotBlank()) mime else "application/octet-stream"
        ioExecutor.execute {
            try {
                val root = File(activity.cacheDir, "hoxi_share")
                val itemDir = File(root, UUID.randomUUID().toString())
                if (!itemDir.exists()) itemDir.mkdirs()
                val cutoff = System.currentTimeMillis() - 3600_000L
                root.listFiles()?.forEach { child ->
                    if (child != itemDir && child.lastModified() < cutoff) {
                        child.deleteRecursively()
                    }
                }
                val file = File(itemDir, safeName)
                file.writeBytes(bytes)
                val uri = FileProvider.getUriForFile(
                    activity,
                    "${activity.packageName}.fileprovider",
                    file,
                )
                val sendIntent = Intent(Intent.ACTION_SEND).apply {
                    type = effectiveMime
                    putExtra(Intent.EXTRA_STREAM, uri)
                    putExtra(Intent.EXTRA_TITLE, safeName)
                    clipData = ClipData.newRawUri(safeName, uri)
                    addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                }
                val chooser = Intent.createChooser(sendIntent, safeName)
                mainHandler.post {
                    try {
                        activity.startActivity(chooser)
                        result.success(true)
                    } catch (err: Exception) {
                        result.error("share_failed", err.message ?: "unable to share file", null)
                    }
                }
            } catch (err: Exception) {
                mainHandler.post {
                    result.error("write_failed", err.message ?: "failed to stage share file", null)
                }
            }
        }
    }
}
