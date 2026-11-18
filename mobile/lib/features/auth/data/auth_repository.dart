import 'package:dio/dio.dart';

import '../../../core/network/api_client.dart';
import '../../../core/network/api_exception.dart';
import '../../../core/network/e2ee/x25519_kem.dart';
import '../../../core/network/hoxi_node_probe.dart';
import '../../../core/network/node_link_status.dart';
import '../../../core/storage/server_connection.dart';
import '../models/node_link_kind.dart';
import 'lan_node_scanner.dart';

export '../../../core/network/hoxi_node_probe.dart';

/// 认证仓库
/// 负责处理 Hoxi 桌面端与 GoPanel 远程面板的配对探测与认证网络请求
class AuthRepository {
  final ApiClient apiClient;

  AuthRepository(this.apiClient);

  /// 登录接口 (对应 GoPanel: /api/auth/signin)
  Future<String> login({
    required String serverUrl,
    required String entrance,
    required String name,
    required String password,
  }) async {
    final response = await apiClient.post<Map<String, dynamic>>(
      '$serverUrl/api/auth/signin',
      data: {'email': name, 'password': password},
      options: buildConnectionOptions(entrance: entrance),
    );

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
    try {
      await apiClient.post<Map<String, dynamic>>(
        '$serverUrl/api/user/info',
        options: buildConnectionOptions(entrance: entrance, token: token),
      );
    } catch (_) {
      await apiClient.get<Map<String, dynamic>>(
        '$serverUrl/api/mobile/app/overview',
        options: buildConnectionOptions(entrance: entrance, token: token),
      );
    }
  }

  static bool _isLoopbackServerUrl(String rawUrl) {
    final uri = Uri.tryParse(rawUrl.trim());
    if (uri == null) return false;
    final host = uri.host.toLowerCase().replaceAll(RegExp(r'^\[|\]$'), '');
    return host == 'localhost' || host == '127.0.0.1' || host == '::1';
  }

  /// GoPanel 手机管理配对码兑换或直接 Token 校验
  Future<({String url, String token, String entrance, String displayName})>
  resolveGoPanelQrToken({
    required String serverUrl,
    required String entrance,
    required String token,
    String relayUrl = '',
    List<String> peerUrls = const [],
  }) async {
    final seen = <String>{};
    final candidates = <String>[
      for (final raw in [serverUrl, ...peerUrls, relayUrl])
        if (raw.trim().isNotEmpty && seen.add(raw.trim())) raw.trim(),
    ];

    if (_isLoopbackServerUrl(serverUrl) && peerUrls.isEmpty) {
      final port = Uri.tryParse(serverUrl)?.port;
      final discovered = await LanNodeScanner.scan(
        filterKind: NodeLinkKind.gopanel,
        extraPorts: [if (port != null && port > 0) port],
      );
      for (final node in discovered) {
        if (seen.add(node.url)) {
          candidates.insert(
            candidates.length - (relayUrl.isNotEmpty ? 1 : 0),
            node.url,
          );
        }
      }
    }

    final hasMultiple = candidates.length > 1;
    Object? lastError;
    ApiException? firstApiError;

    for (final candidateUrl in candidates) {
      final isRelay = isHoxiRelayUrl(candidateUrl);
      final candidateTimeout = hasMultiple
          ? (isRelay
                ? const Duration(seconds: 7)
                : const Duration(milliseconds: 2500))
          : const Duration(seconds: 8);
      try {
        final result = await _resolveGoPanelQrOnUrl(
          serverUrl: candidateUrl,
          entrance: entrance,
          token: token,
          displayHostUrl: _isLoopbackServerUrl(serverUrl)
              ? candidateUrl
              : serverUrl,
          timeout: candidateTimeout,
        ).timeout(candidateTimeout);
        return (
          url: candidateUrl,
          token: result.token,
          entrance: result.entrance,
          displayName: result.displayName,
        );
      } on ApiException catch (e) {
        firstApiError ??= e;
        lastError = e;
        if (e.msg.contains('配对码')) {
          rethrow;
        }
      } catch (e) {
        lastError = e;
      }
    }

    if (firstApiError == null && token.trim().startsWith('gopanel_pair:')) {
      final port = Uri.tryParse(serverUrl)?.port;
      final discovered = await LanNodeScanner.scan(
        filterKind: NodeLinkKind.gopanel,
        extraPorts: [if (port != null && port > 0) port],
      );
      for (final node in discovered) {
        if (!seen.add(node.url)) continue;
        try {
          final result = await _resolveGoPanelQrOnUrl(
            serverUrl: node.url,
            entrance: entrance,
            token: token,
            displayHostUrl: node.url,
            timeout: const Duration(seconds: 4),
          ).timeout(const Duration(seconds: 4));
          return (
            url: node.url,
            token: result.token,
            entrance: result.entrance,
            displayName: result.displayName,
          );
        } on ApiException catch (e) {
          firstApiError ??= e;
          lastError = e;
          if (e.msg.contains('配对码')) rethrow;
        } catch (e) {
          lastError = e;
        }
      }
    }

    throw firstApiError ?? lastError ?? Exception('无法连接到 GoPanel 节点');
  }

  Future<({String token, String entrance, String displayName})>
  _resolveGoPanelQrOnUrl({
    required String serverUrl,
    required String entrance,
    required String token,
    required String displayHostUrl,
    Duration? timeout,
  }) async {
    final trimmed = token.trim();
    if (trimmed.startsWith('gopanel_pair:')) {
      final pairCode = trimmed.substring('gopanel_pair:'.length).trim();
      if (pairCode.isEmpty) {
        throw Exception('GoPanel 手机授权配对码为空');
      }
      final response = await apiClient.post<Map<String, dynamic>>(
        '$serverUrl/api/mobile/pair/exchange',
        data: {'code': pairCode, 'deviceName': 'Hoxi Mobile App'},
        options: buildConnectionOptions(
          entrance: entrance,
          connectTimeout: timeout,
          receiveTimeout: timeout,
        ),
      );
      final data = response.data ?? const <String, dynamic>{};
      var issuedToken = (data['token'] ?? data['xAuth'] ?? '')
          .toString()
          .trim();
      if (issuedToken.isEmpty) {
        for (final cookie in response.setCookies) {
          final match = RegExp(
            r'(?:^|;\s*)gopanel_mobile=([^;\s]+)',
          ).firstMatch(cookie);
          if (match != null) {
            issuedToken = Uri.decodeComponent(match.group(1)!.trim());
            if (issuedToken.isNotEmpty) break;
          }
        }
      }
      if (issuedToken.isEmpty) {
        throw Exception('GoPanel 配对成功，但服务端未下发手机访问令牌');
      }
      final serverEntrance = (data['entrance'] ?? '').toString().trim();
      final resolvedEntrance = serverEntrance.isNotEmpty
          ? serverEntrance
          : entrance;
      try {
        await validateToken(
          serverUrl: serverUrl,
          entrance: resolvedEntrance,
          token: issuedToken,
        );
      } catch (_) {}
      final host = Uri.tryParse(displayHostUrl)?.host ?? displayHostUrl;
      return (
        token: issuedToken,
        entrance: resolvedEntrance,
        displayName: 'GoPanel · $host',
      );
    }

    await validateToken(
      serverUrl: serverUrl,
      entrance: entrance,
      token: trimmed,
    );
    return (token: trimmed, entrance: entrance, displayName: 'QR 授权');
  }

  /// 获取当前登录用户信息 (对应 GoPanel: POST /api/user/info)
  Future<Map<String, dynamic>> getUserInfo() async {
    final response = await apiClient.post<Map<String, dynamic>>(
      '/api/user/info',
    );
    return response.data ?? {};
  }

  /// 扫描当前局域网内开放 Hoxi 配对端口的桌面端节点地址列表（如 `http://192.168.1.20:5417`）。
  Future<List<String>> discoverHoxiLanUrls({
    List<int> extraPorts = const [],
  }) async {
    final discovered = await LanNodeScanner.scan(
      filterKind: NodeLinkKind.hoxi,
      extraPorts: extraPorts,
    );
    return [for (final node in discovered) node.url];
  }

  /// 探测目标地址是否为 Hoxi 节点 (GET /api/hoxi/bootstrap)
  Future<Map<String, dynamic>?> probeHoxiNode({
    required String serverUrl,
    String entrance = '',
    String token = '',
    String e2eeKey = '',
  }) async {
    try {
      final trimmedToken = token.trim();
      final isRelay = isHoxiRelayUrl(serverUrl);
      final probeTimeout = isRelay
          ? const Duration(seconds: 6)
          : const Duration(seconds: 2);
      if (trimmedToken.isNotEmpty &&
          isCleartextLanUrl(serverUrl) &&
          !isRelay) {
        try {
          final preflight = await apiClient.get<Map<String, dynamic>>(
            '$serverUrl/api/hoxi/bootstrap',
            options: buildHoxiPreflightOptions(
              connectTimeout: probeTimeout,
              receiveTimeout: probeTimeout,
            ),
          );
          final preflightData = preflight.data;
          if (isValidHoxiBootstrap(preflightData)) {
            return preflightData;
          }
          return null;
        } on ApiException catch (error) {
          if (error.code != 401 ||
              error.msg.trim() != hoxiInvalidPairingTokenError) {
            return null;
          }
        } catch (_) {
          return null;
        }
      }

      final kexPair = e2eeKey.trim().isEmpty
          ? HoxiX25519KeyPair.generate()
          : null;
      final response = await apiClient.get<Map<String, dynamic>>(
        '$serverUrl/api/hoxi/bootstrap',
        options: buildHoxiProbeOptions(
          entrance: entrance,
          token: trimmedToken,
          e2eeKey: e2eeKey,
          kexPublicKey: kexPair?.publicKeyBase64Url ?? '',
          connectTimeout: probeTimeout,
          receiveTimeout: probeTimeout,
        ),
      );
      final data = response.data;
      if (isValidHoxiBootstrap(data)) {
        if (data != null &&
            kexPair != null &&
            (data['e2eeKey'] ?? '').toString().trim().isEmpty &&
            data['e2eeKex'] is Map) {
          final unwrapped = kexPair.tryOpenMasterKeyHex(
            data['e2eeKex'] as Map,
          );
          if (unwrapped != null && unwrapped.isNotEmpty) {
            return <String, dynamic>{...data, 'e2eeKey': unwrapped};
          }
        }
        return data;
      }
      return null;
    } on DioException catch (error) {
      if (NodeLinkStatus.isCredentialFailure(error)) rethrow;
      return null;
    } on ApiException catch (error) {
      if (NodeLinkStatus.isCredentialFailure(error)) rethrow;
      return null;
    } catch (_) {
      return null;
    }
  }
}
