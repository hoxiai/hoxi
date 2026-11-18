import 'package:web_socket_channel/web_socket_channel.dart';

import 'node_websocket_stub.dart'
    if (dart.library.io) 'node_websocket_io.dart'
    as platform;

/// 受控节点 WebSocket 的唯一入口。
///
/// 与三个 HTTP 客户端（[ApiClient] / [SiteApiClient] / [ExternalApiClient]）
/// 同属 `lib/core/network`：feature 层不得直接 `IOWebSocketChannel.connect` /
/// `WebSocketChannel.connect`。经 GoPanel 隧道中继只支持 HTTP 与 SSE，网关对
/// 终端 WS 返回 501，降级提示由调用方负责。
WebSocketChannel connectNodeWebSocket(
  Uri uri, {
  required Map<String, dynamic> headers,
}) {
  return platform.connectNodeWebSocket(uri, headers: headers);
}