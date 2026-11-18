import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../storage/active_node_provider.dart';
import '../storage/storage_service.dart';
import 'api_client.dart';
import 'external_api_client.dart';

/// 节点与外部网关网络入口的唯一 provider 注入点。feature 层一律 `ref.watch` 这里，
/// 不要在仓库或控制器里直接 `ApiClient()` / `ExternalApiClient()`。
///
/// 受控节点（Hoxi 桌面 / GoPanel）：节点切换时按代次重建，请求保留代次。
final nodeApiClientProvider = Provider<ApiClient>((ref) {
  ref.watch(activeNodeGenerationProvider);
  return ApiClient.forNode(StorageService.activeNodeContext);
});

/// 第三方（模型网关 / Hoxi 网关）：调用方显式传完整 URL 与密钥，不落节点凭据。
final externalApiClientProvider = Provider<ExternalApiClient>(
  (_) => ExternalApiClient(),
);
