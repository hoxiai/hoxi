import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import '../../storage/storage_service.dart';
import '../../storage/active_node_context.dart';
import '../../storage/server_connection.dart';
import '../hoxi_node_probe.dart';
import '../hoxi_transport_failover.dart';

/// 全局认证与动态连接拦截器
/// 遵循 PROMPT.md：
/// 1. 不把 baseUrl 写死，移动端支持连接多台远程面板，URL 需要动态读取
/// 2. 不在页面里散落 Token 拼接逻辑，统一在拦截器处理鉴权头
class AuthInterceptor extends Interceptor {
  AuthInterceptor({HoxiTransportFailover? failover}) : _failover = failover;

  final HoxiTransportFailover? _failover;
  @override
  void onRequest(RequestOptions options, RequestInterceptorHandler handler) {
    final customConnectTimeout = options.extra['connectTimeout'];
    if (customConnectTimeout is Duration) {
      options.connectTimeout = customConnectTimeout;
    }
    if (options.extra['skipActiveCredentials'] == true) {
      super.onRequest(options, handler);
      return;
    }

    // 动态获取当前激活服务器的信息，而不是全局硬编码写死
    final node =
        options.extra['nodeContext'] as ActiveNodeContext? ??
        StorageService.activeNodeContext;
    options.extra['nodeContext'] = node;
    if (!StorageService.isCurrentNode(node)) {
      handler.reject(
        DioException(
          requestOptions: options,
          type: DioExceptionType.cancel,
          message: '节点已切换，请重新操作',
        ),
      );
      return;
    }
    final token = node.token;
    final baseUrl = node.url;
    // Explicit external URLs must opt out of active credentials.
    final absolute = Uri.tryParse(options.path);
    final base = Uri.tryParse(baseUrl ?? '');
    if (absolute != null &&
        absolute.hasScheme &&
        (base == null ||
            !base.hasAuthority ||
            absolute.origin != base.origin)) {
      handler.reject(
        DioException(requestOptions: options, message: '请求地址与受控节点不一致'),
      );
      return;
    }

    // 1. 如果请求的 URL 尚未补全域名（相对路径），且本地已有选中的服务端 URL，则覆盖 baseUrl
    if (!options.path.startsWith('http') &&
        baseUrl != null &&
        baseUrl.isNotEmpty) {
      options.baseUrl = baseUrl;
    }

    // 局域网直连 Hoxi 节点在脱离同一 Wi-Fi 后快速触发连接超时与中继切换，避免卡死 15 秒
    if (customConnectTimeout is! Duration &&
        node.isHoxi &&
        baseUrl != null &&
        isCleartextLanUrl(baseUrl) &&
        !isHoxiRelayUrl(baseUrl)) {
      options.connectTimeout = const Duration(seconds: 3);
    }

    // 2. 如果存在激活的 Token，统一追加认证请求头
    // GoPanel 使用 X-Auth（兼容手机配对令牌 X-Mobile-Token）；Hoxi 及支持 Bearer 的受控服务追加 Authorization
    if (token != null && token.isNotEmpty) {
      if (node.usesBearerAuth &&
          (token.startsWith('Basic ') || token.startsWith('Bearer '))) {
        options.headers['Authorization'] = token;
      } else {
        options.headers['X-Auth'] = token;
        if (node.usesBearerAuth) {
          options.headers['Authorization'] = 'Bearer $token';
        } else if (node.isGoPanel) {
          options.headers['X-Mobile-Token'] = token;
          options.headers['X-Mobile-Request'] = '1';
        }
      }
    }

    final entrance = node.entrance;
    if (entrance != null && entrance.isNotEmpty) {
      options.headers['EntranceCode'] = base64Encode(utf8.encode(entrance));
    }

    // 3. 如果存在激活的 Cookie (如 Entrance, session)，自动携带
    final cookie = node.cookie;
    if (cookie != null && cookie.isNotEmpty) {
      options.headers['Cookie'] = cookie;
    }

    super.onRequest(options, handler);
  }

  @override
  Future<void> onResponse(
    Response response,
    ResponseInterceptorHandler handler,
  ) async {
    if (response.requestOptions.extra['skipActiveCredentials'] == true) {
      super.onResponse(response, handler);
      return;
    }
    final node =
        response.requestOptions.extra['nodeContext'] as ActiveNodeContext?;
    if (node != null && !StorageService.isCurrentNode(node)) {
      handler.reject(
        DioException(
          requestOptions: response.requestOptions,
          type: DioExceptionType.cancel,
          message: '已丢弃旧节点响应',
        ),
      );
      return;
    }

    // 拦截并保存后端下发的 Set-Cookie (如 Entrance, gin_session 等)
    final setCookies = response.headers['set-cookie'];
    if (setCookies != null && setCookies.isNotEmpty) {
      final cookieStr = parseSetCookieHeaders(setCookies);
      if (cookieStr.isNotEmpty) {
        await StorageService.setActiveServerCookie(
          cookieStr,
          expectedNode: node,
          merge: true,
        );
      }
    }

    // 在这里可以统一处理特殊的响应业务状态码（比如 code != 200 抛异常）
    // 当前保留基础拦截流转，具体根据 ApiResponse 的 `code` 在 ApiClient 中处理
    super.onResponse(response, handler);
  }

  @override
  void onError(DioException err, ErrorInterceptorHandler handler) {
    final failover = _failover;
    if (failover != null &&
        err.requestOptions.extra['skipActiveCredentials'] != true &&
        HoxiTransportFailover.isConnectionFailure(err)) {
      // The failed request is not retried, but re-resolving the transport lets
      // the next request succeed (for example after leaving the LAN).
      unawaited(failover.handle(err));
    }

    // 统一处理 401，通常表示 Token 失效，可能需要跳转登录或清理本地连接状态
    if (err.response?.statusCode == 401) {
      // 可以在此处发送全局事件触发强制退出 / 返回服务器列表页
      // e.g. AppEventBus.fire(EventUserUnauthorized());
    }

    super.onError(err, handler);
  }
}
