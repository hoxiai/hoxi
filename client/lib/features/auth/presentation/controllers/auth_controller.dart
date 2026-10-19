import 'dart:async';
import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/network/api_client.dart';
import '../../../../core/network/api_exception.dart';
import '../../../../core/storage/storage_service.dart';
import '../../../../shared/models/server_connection.dart';
import '../../data/auth_repository.dart';
import '../auth_text.dart';

/// 注入 AuthRepository 的 Provider
final authRepositoryProvider = Provider<AuthRepository>((ref) {
  return AuthRepository(ApiClient());
});

/// AuthController 的状态定义
class AuthState {
  final bool isLoading;
  final String? errorMessage;
  final bool isAuthenticated;

  const AuthState({
    this.isLoading = false,
    this.errorMessage,
    this.isAuthenticated = false,
  });

  AuthState copyWith({
    bool? isLoading,
    String? errorMessage,
    bool? isAuthenticated,
  }) {
    return AuthState(
      isLoading: isLoading ?? this.isLoading,
      errorMessage: errorMessage, // 故意不兜底，传 null 就能清空 error
      isAuthenticated: isAuthenticated ?? this.isAuthenticated,
    );
  }
}

/// 注入 AuthController 的 NotifierProvider
final authControllerProvider = NotifierProvider<AuthController, AuthState>(
  AuthController.new,
);

/// 处理整个连接和登录过程的控制器（同时兼容 GoPanel 节点与 Hoxi 节点）
class AuthController extends Notifier<AuthState> {
  static const _connectionTimeout = Duration(seconds: 20);
  static const _storageTimeout = Duration(seconds: 10);
  late AuthRepository _authRepository;

  @override
  AuthState build() {
    _authRepository = ref.watch(authRepositoryProvider);
    return const AuthState();
  }

  /// 建立连接并登录（自动识别 GoPanel 面板与 Hoxi 工作台节点）
  Future<bool> connectAndLogin({
    required String serverUrl,
    String entrance = '',
    required String username,
    required String password,
  }) async {
    state = state.copyWith(isLoading: true, errorMessage: null);

    try {
      final server = normalizeServerAddress(serverUrl, entrance);
      if (!isValidServerEntrance(server.entrance)) {
        throw const FormatException('entranceInvalid');
      }

      final trimmedUser = username.trim();
      final trimmedPass = password.trim();

      // 1. 若未填写账号密码，直接作为 Hoxi 节点探测连接
      if (trimmedUser.isEmpty && trimmedPass.isEmpty) {
        final bootstrap = await _authRepository
            .probeHoxiNode(serverUrl: server.baseUrl, entrance: server.entrance)
            .timeout(_connectionTimeout);
        if (bootstrap == null) {
          throw Exception('未检测到 Hoxi 节点，若连接 GoPanel 面板请填写账号与密码');
        }
        await _saveConnection(
          ServerConnection(
            id: DateTime.now().millisecondsSinceEpoch.toString(),
            name: _buildHoxiNodeName(bootstrap, server.baseUrl),
            url: server.baseUrl,
            entrance: server.entrance,
            token: '',
            nodeType: 'hoxi',
            lastConnectedAt: DateTime.now(),
          ),
        ).timeout(_storageTimeout);
        state = state.copyWith(isLoading: false, isAuthenticated: true);
        return true;
      }

      // 2. 填写了账号密码：优先按 GoPanel 标准登录，失败时自动探测是否为带认证的 Hoxi 节点
      try {
        final token = await _authRepository
            .login(
              serverUrl: server.baseUrl,
              entrance: server.entrance,
              name: trimmedUser,
              password: trimmedPass,
            )
            .timeout(_connectionTimeout);

        await _saveConnection(
          ServerConnection(
            id: DateTime.now().millisecondsSinceEpoch.toString(),
            name: trimmedUser,
            url: server.baseUrl,
            entrance: server.entrance,
            token: token,
            nodeType: 'gopanel',
            lastConnectedAt: DateTime.now(),
          ),
        ).timeout(_storageTimeout);

        state = state.copyWith(isLoading: false, isAuthenticated: true);
        return true;
      } catch (loginErr) {
        final basicToken =
            'Basic ${base64Encode(utf8.encode('$trimmedUser:$trimmedPass'))}';
        final bootstrap = await _authRepository
            .probeHoxiNode(
              serverUrl: server.baseUrl,
              entrance: server.entrance,
              token: basicToken,
            )
            .timeout(_connectionTimeout);
        if (bootstrap == null) {
          rethrow;
        }
        await _saveConnection(
          ServerConnection(
            id: DateTime.now().millisecondsSinceEpoch.toString(),
            name: _buildHoxiNodeName(bootstrap, server.baseUrl),
            url: server.baseUrl,
            entrance: server.entrance,
            token: basicToken,
            nodeType: 'hoxi',
            lastConnectedAt: DateTime.now(),
          ),
        ).timeout(_storageTimeout);
        state = state.copyWith(isLoading: false, isAuthenticated: true);
        return true;
      }
    } catch (e) {
      state = state.copyWith(
        isLoading: false,
        errorMessage: '连接或登录失败：${_errorMessage(e)}',
      );
      return false;
    }
  }

  /// 扫码授权登录：支持 GoPanel 二维码与 Hoxi 配对二维码
  Future<bool> connectWithQrToken({
    required String serverUrl,
    String entrance = '',
    required String token,
    String? nodeType,
  }) async {
    state = state.copyWith(isLoading: true, errorMessage: null);

    try {
      final server = normalizeServerAddress(serverUrl, entrance);
      if (!isValidServerEntrance(server.entrance)) {
        throw const FormatException('entranceInvalid');
      }

      final explicitHoxi =
          (nodeType ?? '').trim().toLowerCase() == 'hoxi' ||
          token.trim().isEmpty;

      if (explicitHoxi) {
        final bootstrap = await _authRepository
            .probeHoxiNode(
              serverUrl: server.baseUrl,
              entrance: server.entrance,
              token: token,
            )
            .timeout(_connectionTimeout);
        if (bootstrap == null) {
          throw Exception('无法连接到 Hoxi 节点');
        }
        await _saveConnection(
          ServerConnection(
            id: DateTime.now().millisecondsSinceEpoch.toString(),
            name: _buildHoxiNodeName(bootstrap, server.baseUrl),
            url: server.baseUrl,
            entrance: server.entrance,
            token: token,
            nodeType: 'hoxi',
            lastConnectedAt: DateTime.now(),
          ),
        ).timeout(_storageTimeout);
        state = state.copyWith(isLoading: false, isAuthenticated: true);
        return true;
      }

      try {
        await _authRepository
            .validateToken(
              serverUrl: server.baseUrl,
              entrance: server.entrance,
              token: token,
            )
            .timeout(_connectionTimeout);

        await _saveConnection(
          ServerConnection(
            id: DateTime.now().millisecondsSinceEpoch.toString(),
            name: 'QR 授权',
            url: server.baseUrl,
            entrance: server.entrance,
            token: token,
            nodeType: 'gopanel',
            lastConnectedAt: DateTime.now(),
          ),
        ).timeout(_storageTimeout);
      } catch (_) {
        final bootstrap = await _authRepository
            .probeHoxiNode(
              serverUrl: server.baseUrl,
              entrance: server.entrance,
              token: token,
            )
            .timeout(_connectionTimeout);
        if (bootstrap == null) rethrow;
        await _saveConnection(
          ServerConnection(
            id: DateTime.now().millisecondsSinceEpoch.toString(),
            name: _buildHoxiNodeName(bootstrap, server.baseUrl),
            url: server.baseUrl,
            entrance: server.entrance,
            token: token,
            nodeType: 'hoxi',
            lastConnectedAt: DateTime.now(),
          ),
        ).timeout(_storageTimeout);
      }

      state = state.copyWith(isLoading: false, isAuthenticated: true);
      return true;
    } catch (e) {
      state = state.copyWith(
        isLoading: false,
        errorMessage: '扫码授权验证失败：${_errorMessage(e)}',
      );
      return false;
    }
  }

  Future<bool> switchToSavedServer(ServerConnection server) async {
    state = state.copyWith(isLoading: true, errorMessage: null);
    try {
      if (server.isHoxi) {
        final bootstrap = await _authRepository
            .probeHoxiNode(
              serverUrl: server.url,
              entrance: server.entrance,
              token: server.token,
            )
            .timeout(_connectionTimeout);
        if (bootstrap == null) {
          throw Exception('Hoxi 节点不可达');
        }
      } else {
        await _authRepository
            .validateToken(
              serverUrl: server.url,
              entrance: server.entrance,
              token: server.token,
            )
            .timeout(_connectionTimeout);
      }
      await _saveConnection(server);
      state = state.copyWith(isLoading: false, isAuthenticated: true);
      return true;
    } catch (e) {
      state = state.copyWith(
        isLoading: false,
        errorMessage: '切换服务器失败：${_errorMessage(e)}',
      );
      return false;
    }
  }

  String _buildHoxiNodeName(Map<String, dynamic> bootstrap, String baseUrl) {
    final rootDir = (bootstrap['rootDir'] ?? '').toString().trim();
    if (rootDir.isNotEmpty) {
      final parts = rootDir.split(RegExp(r'[/\\]')).where((s) => s.isNotEmpty);
      if (parts.isNotEmpty) {
        return 'Hoxi · ${parts.last}';
      }
    }
    return 'Hoxi · ${Uri.tryParse(baseUrl)?.host ?? baseUrl}';
  }

  String _errorMessage(Object error) {
    if (error is ApiException) return error.msg;
    if (error is TimeoutException) return AuthText.current('operationTimeout');
    if (error is FormatException) return AuthText.current(error.message);
    return error.toString().replaceFirst('Exception: ', '');
  }

  Future<void> _saveConnection(ServerConnection server) async {
    await StorageService.clearActiveServerCookie();
    await StorageService.setActiveServerUrl(server.url);
    await StorageService.setActiveServerNodeType(server.nodeType);
    await StorageService.setActiveServerEntrance(server.entrance);
    await StorageService.setActiveServerToken(server.token);
    await StorageService.saveServerConnection(server);
  }

  /// 登出
  Future<void> logout() async {
    await StorageService.clearActiveServer();
    state = const AuthState(isAuthenticated: false);
  }
}
