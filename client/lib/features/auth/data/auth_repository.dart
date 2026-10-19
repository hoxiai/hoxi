import 'dart:convert';

import 'package:dio/dio.dart';

import '../../../core/network/api_client.dart';

/// 认证仓库
/// 负责处理所有与登录、鉴权相关的网络请求，对接 GoPanel 后端真实接口
class AuthRepository {
  final ApiClient _apiClient;

  AuthRepository(this._apiClient);

  /// 登录接口 (对应 GoPanel: /api/auth/signin)
  /// GoPanel 的请求体支持 email 或 mobile+isdCode，以及 password
  /// 响应体中的 data 通常包含 { "xAuth": "xxx" }
  Future<String> login({
    required String serverUrl,
    required String entrance,
    required String name,
    required String password,
  }) async {
    final response = await _apiClient.post<Map<String, dynamic>>(
      '$serverUrl/api/auth/signin',
      data: {'email': name, 'password': password},
      options: buildConnectionOptions(entrance: entrance),
    );

    // 提取 Token
    final data = response.data;
    if (data != null && data.containsKey('xAuth')) {
      return data['xAuth'] as String;
    }

    throw Exception('登录成功，但未获取到 Token');
  }

  Future<void> validateToken({
    required String serverUrl,
    required String entrance,
    required String token,
  }) async {
    await _apiClient.post<Map<String, dynamic>>(
      '$serverUrl/api/user/info',
      options: buildConnectionOptions(entrance: entrance, token: token),
    );
  }

  /// 获取当前登录用户信息 (对应 GoPanel: POST /api/user/info)
  /// 可以用这个接口来“验证 Token 是否有效”
  Future<Map<String, dynamic>> getUserInfo() async {
    final response = await _apiClient.post<Map<String, dynamic>>(
      '/api/user/info',
    );
    return response.data ?? {};
  }

  /// 探测目标地址是否为 Hoxi 节点 (GET /api/hoxi/bootstrap)
  /// 返回 Hoxi bootstrap 信息（含 appVersion / rootDir / status）；若非 Hoxi 节点则返回 null
  Future<Map<String, dynamic>?> probeHoxiNode({
    required String serverUrl,
    String entrance = '',
    String token = '',
  }) async {
    try {
      final response = await _apiClient.get<Map<String, dynamic>>(
        '$serverUrl/api/hoxi/bootstrap',
        options: buildHoxiProbeOptions(entrance: entrance, token: token),
      );
      final data = response.data;
      if (data != null &&
          (data.containsKey('appVersion') ||
              data.containsKey('rootDir') ||
              data.containsKey('status'))) {
        return data;
      }
      return null;
    } catch (_) {
      return null;
    }
  }
}

Options buildConnectionOptions({required String entrance, String token = ''}) {
  final normalizedEntrance = entrance.trim();
  return Options(
    extra: const {'skipActiveCredentials': true},
    headers: {
      if (token.isNotEmpty) 'X-Auth': token,
      if (normalizedEntrance.isNotEmpty)
        'EntranceCode': base64Encode(utf8.encode(normalizedEntrance)),
    },
  );
}

Options buildHoxiProbeOptions({required String entrance, String token = ''}) {
  final normalizedEntrance = entrance.trim();
  final trimmedToken = token.trim();
  return Options(
    extra: const {'skipActiveCredentials': true, 'allowRawJson': true},
    headers: {
      if (trimmedToken.isNotEmpty) ...{
        if (trimmedToken.startsWith('Basic ') ||
            trimmedToken.startsWith('Bearer '))
          'Authorization': trimmedToken
        else ...{
          'X-Auth': trimmedToken,
          'Authorization': 'Bearer $trimmedToken',
        },
      },
      if (normalizedEntrance.isNotEmpty)
        'EntranceCode': base64Encode(utf8.encode(normalizedEntrance)),
    },
  );
}
