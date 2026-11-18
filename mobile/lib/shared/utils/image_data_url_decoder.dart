import 'dart:convert';
import 'dart:typed_data';

/// 将 `data:image/...;base64,...` 形式的 Data URL 解码为原始图片字节。
Uint8List? decodeDataUrlImageBytes(String? dataUrl) {
  if (dataUrl == null || !dataUrl.startsWith('data:image/')) return null;
  final comma = dataUrl.indexOf(',');
  if (comma <= 0 || comma >= dataUrl.length - 1) return null;
  try {
    return base64Decode(dataUrl.substring(comma + 1));
  } catch (_) {
    return null;
  }
}
