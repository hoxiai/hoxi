import 'dart:typed_data';

import 'package:flutter/material.dart';

import '../../core/l10n/l10n.dart';
import '../../core/storage/storage_service.dart';
import '../utils/image_data_url_decoder.dart';

/// 将附件图片字符串解析为可加载的完整网络 URL（若为 data URL 则返回空串）
String resolveChatAttachmentNetworkUrl(String rawUrl, {String? serverBaseUrl}) {
  final trimmed = rawUrl.trim();
  if (trimmed.isEmpty || trimmed.startsWith('data:')) return '';
  if (trimmed.startsWith('http://') || trimmed.startsWith('https://')) {
    return trimmed;
  }
  final base = (serverBaseUrl != null && serverBaseUrl.trim().isNotEmpty)
      ? serverBaseUrl.trim()
      : (StorageService.activeServerUrl?.trim() ?? '');
  if (base.isEmpty) return trimmed;
  final cleanBase = base.replaceAll(RegExp(r'/+$'), '');
  final cleanPath = trimmed.startsWith('/') ? trimmed : '/$trimmed';
  return '$cleanBase$cleanPath';
}

/// 会话消息与输入框中的可点击图片缩略图，支持 `data:image/...` 与网络/相对路径图片，点击后弹出全屏可缩放预览。
class ChatTappableImage extends StatelessWidget {
  const ChatTappableImage({
    super.key,
    required this.imageDataUrl,
    this.serverBaseUrl,
    this.width,
    this.height,
    this.fit = BoxFit.cover,
    this.borderRadius = const BorderRadius.all(Radius.circular(12)),
    this.errorBuilder,
  });

  final String imageDataUrl;
  final String? serverBaseUrl;
  final double? width;
  final double? height;
  final BoxFit fit;
  final BorderRadius borderRadius;
  final ImageErrorWidgetBuilder? errorBuilder;

  @override
  Widget build(BuildContext context) {
    final bytes = decodeDataUrlImageBytes(imageDataUrl);
    final networkUrl = bytes == null
        ? resolveChatAttachmentNetworkUrl(
            imageDataUrl,
            serverBaseUrl: serverBaseUrl,
          )
        : '';
    final canPreview = bytes != null || networkUrl.isNotEmpty;

    return MouseRegion(
      cursor: canPreview ? SystemMouseCursors.click : MouseCursor.defer,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: canPreview
            ? () => showChatImagePreview(
                context,
                imageDataUrl: imageDataUrl,
                serverBaseUrl: serverBaseUrl,
              )
            : null,
        child: ConstrainedBox(
          constraints: BoxConstraints(
            minWidth: width ?? 44,
            minHeight: height ?? 48,
          ),
          child: ClipRRect(
            borderRadius: borderRadius,
            child: bytes != null
                ? Image.memory(
                    bytes,
                    width: width,
                    height: height,
                    fit: fit,
                    errorBuilder:
                        errorBuilder ?? (_, _, _) => const SizedBox.shrink(),
                  )
                : (networkUrl.isNotEmpty
                      ? Image.network(
                          networkUrl,
                          width: width,
                          height: height,
                          fit: fit,
                          errorBuilder:
                              errorBuilder ??
                              (_, _, _) => Container(
                                width: width ?? 120,
                                height: height ?? 80,
                                color: Colors.black12,
                                alignment: Alignment.center,
                                child: const Icon(
                                  Icons.broken_image_outlined,
                                  size: 22,
                                ),
                              ),
                        )
                      : const SizedBox.shrink()),
          ),
        ),
      ),
    );
  }
}

/// 打开会话图片全屏预览弹窗（支持双指缩放、平移、一键放大还原与点击关闭）。
Future<void> showChatImagePreview(
  BuildContext context, {
  required String imageDataUrl,
  String? serverBaseUrl,
}) {
  final bytes = decodeDataUrlImageBytes(imageDataUrl);
  final networkUrl = bytes == null
      ? resolveChatAttachmentNetworkUrl(
          imageDataUrl,
          serverBaseUrl: serverBaseUrl,
        )
      : '';
  if (bytes == null && networkUrl.isEmpty) return Future<void>.value();
  return showDialog<void>(
    context: context,
    barrierColor: Colors.black.withValues(alpha: 0.92),
    useSafeArea: false,
    builder: (_) => ChatImagePreviewDialog(
      bytes: bytes ?? Uint8List(0),
      networkUrl: networkUrl.isNotEmpty ? networkUrl : null,
    ),
  );
}

class ChatImagePreviewDialog extends StatefulWidget {
  const ChatImagePreviewDialog({
    super.key,
    required this.bytes,
    this.networkUrl,
  });

  final Uint8List bytes;
  final String? networkUrl;

  @override
  State<ChatImagePreviewDialog> createState() => _ChatImagePreviewDialogState();
}

class _ChatImagePreviewDialogState extends State<ChatImagePreviewDialog> {
  late final TransformationController _transformationController;
  bool _isZoomed = false;

  @override
  void initState() {
    super.initState();
    _transformationController = TransformationController()
      ..addListener(_handleTransformChanged);
  }

  void _handleTransformChanged() {
    final scale = _transformationController.value.getMaxScaleOnAxis();
    final zoomed = scale > 1.05;
    if (zoomed != _isZoomed && mounted) {
      setState(() => _isZoomed = zoomed);
    }
  }

  void _toggleZoom() {
    if (_isZoomed) {
      _transformationController.value = Matrix4.identity();
    } else {
      _transformationController.value = Matrix4.diagonal3Values(2.5, 2.5, 1.0);
    }
  }

  @override
  void dispose() {
    _transformationController
      ..removeListener(_handleTransformChanged)
      ..dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Material(
      type: MaterialType.transparency,
      child: Stack(
        fit: StackFit.expand,
        children: [
          GestureDetector(
            key: const ValueKey('chat_image_preview_backdrop'),
            behavior: HitTestBehavior.opaque,
            onTap: () => Navigator.of(context).pop(),
            child: InteractiveViewer(
              transformationController: _transformationController,
              minScale: 0.5,
              maxScale: 4.0,
              child: Center(
                child: widget.bytes.isNotEmpty
                    ? Image.memory(
                        widget.bytes,
                        key: const ValueKey('chat_image_preview_image'),
                        fit: BoxFit.contain,
                        errorBuilder: (_, _, _) => const SizedBox.shrink(),
                      )
                    : (widget.networkUrl != null
                          ? Image.network(
                              widget.networkUrl!,
                              key: const ValueKey('chat_image_preview_image'),
                              fit: BoxFit.contain,
                              errorBuilder: (_, _, _) =>
                                  const SizedBox.shrink(),
                            )
                          : const SizedBox.shrink()),
              ),
            ),
          ),
          SafeArea(
            child: Align(
              alignment: Alignment.topRight,
              child: Padding(
                padding: const EdgeInsets.symmetric(
                  horizontal: 16,
                  vertical: 12,
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    _PreviewActionButton(
                      buttonKey: const ValueKey(
                        'chat_image_preview_zoom_toggle',
                      ),
                      icon: _isZoomed
                          ? Icons.zoom_out_rounded
                          : Icons.zoom_in_rounded,
                      onTap: _toggleZoom,
                    ),
                    const SizedBox(width: 10),
                    Tooltip(
                      message: context.l10n.commonClose,
                      child: _PreviewActionButton(
                        buttonKey: const ValueKey('chat_image_preview_close'),
                        icon: Icons.close_rounded,
                        onTap: () => Navigator.of(context).pop(),
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _PreviewActionButton extends StatelessWidget {
  const _PreviewActionButton({
    required this.buttonKey,
    required this.icon,
    required this.onTap,
  });

  final Key buttonKey;
  final IconData icon;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.white.withValues(alpha: 0.16),
      shape: const CircleBorder(),
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        key: buttonKey,
        onTap: onTap,
        customBorder: const CircleBorder(),
        child: Padding(
          padding: const EdgeInsets.all(10),
          child: Icon(icon, size: 20, color: Colors.white),
        ),
      ),
    );
  }
}
