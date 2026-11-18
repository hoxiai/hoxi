import 'package:web_socket_channel/web_socket_channel.dart';

/// 非 IO 平台（Web）的兜底：浏览器 WebSocket 不支持自定义请求头。
WebSocketChannel connectNodeWebSocket(
  Uri uri, {
  required Map<String, dynamic> headers,
}) {
  return WebSocketChannel.connect(uri);
}