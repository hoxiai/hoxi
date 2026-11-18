import 'dart:convert';

import 'package:image_picker/image_picker.dart';

enum ChatCameraFailure { cancelled, denied, unavailable }

class ChatCameraException implements Exception {
  final ChatCameraFailure failure;
  const ChatCameraException(this.failure);
}

/// 调用系统相机拍照或从相册选图并转成可随消息发送的 data URL。
/// 放在数据层，避免 Widget / 屏幕里直接拼相机与编码逻辑。
class ChatCameraCapture {
  const ChatCameraCapture({ImagePicker? picker}) : _picker = picker;

  final ImagePicker? _picker;

  /// 拍照成功返回 data URL。取消、拒绝权限、相机不可用都抛 [ChatCameraException]，不返回成功。
  Future<String?> captureDataUrl() =>
      pickImageDataUrl(source: ImageSource.camera);

  /// 从系统相册选择图片并返回 data URL。
  Future<String?> pickFromGalleryDataUrl() =>
      pickImageDataUrl(source: ImageSource.gallery);

  /// 按指定 [source]（相机或相册）获取图片并编码为 data URL。
  Future<String?> pickImageDataUrl({
    ImageSource source = ImageSource.camera,
  }) async {
    final picker = _picker ?? ImagePicker();
    final XFile? shot;
    try {
      shot = await picker.pickImage(
        source: source,
        maxWidth: 1600,
        maxHeight: 1600,
        imageQuality: 82,
      );
    } catch (error) {
      final text = error.toString().toLowerCase();
      if (text.contains('denied') || text.contains('permission')) {
        throw const ChatCameraException(ChatCameraFailure.denied);
      }
      throw const ChatCameraException(ChatCameraFailure.unavailable);
    }
    if (shot == null) {
      throw const ChatCameraException(ChatCameraFailure.cancelled);
    }
    final bytes = await shot.readAsBytes();
    if (bytes.isEmpty) return null;
    return 'data:${mimeOf(shot.path)};base64,${base64Encode(bytes)}';
  }

  static String mimeOf(String path) {
    final lower = path.toLowerCase();
    if (lower.endsWith('.png')) return 'image/png';
    if (lower.endsWith('.heic')) return 'image/heic';
    if (lower.endsWith('.webp')) return 'image/webp';
    return 'image/jpeg';
  }
}
