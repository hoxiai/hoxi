import '../../../core/l10n/l10n.dart';
import '../../../core/network/api_client.dart';
import '../models/node_link_kind.dart';

typedef AuxLoginResult = ({String token, String displayName});

/// 外部扩展业务服务（非 Hoxi / GoPanel 基础节点）的可插拔认证端口。
///
/// 保持 `auth` 核心仓库与控制器仅聚焦 Hoxi 桌面与 GoPanel 远程面板协议；
/// 商业业务线认证由独立实现注册，开源薄壳无需携带。
abstract interface class AuxServiceAuthPort {
  bool supportsNodeType(String nodeType);

  Future<AuxLoginResult> loginWithCredentials({
    required NodeLinkKind kind,
    required String serverUrl,
    required String entrance,
    required String username,
    required String password,
    required String token,
  });

  Future<({String url, String token, String displayName})> validateQrOrSaved({
    required String nodeType,
    required String serverUrl,
    required String entrance,
    required String token,
    List<String> peerUrls = const [],
  });

  Future<Map<String, dynamic>> pollBrowserAuth({
    required String nodeType,
    required String serverUrl,
    required String state,
  });

  String formatAuthorizedDisplayName({
    required String nodeType,
    required Map<String, dynamic> payload,
  });

  String? inferQrUrlNodeType({required Uri uri, required String token});

  String? normalizeQrTokenNodeType({
    required String token,
    required String? fallbackNodeType,
  });

  bool? matchExpectedQrKind({
    required NodeLinkKind expectedKind,
    required String scannedType,
    required String token,
  });

  bool isKnownNonHoxiScannedType(String scannedType);

  String? customKindMismatchError({
    required String scannedType,
    required AppLocalizations l10n,
  });
}

abstract final class AuxServiceAuthRegistry {
  static AuxServiceAuthPort Function(ApiClient apiClient)? factory;

  static AuxServiceAuthPort? create(ApiClient apiClient) =>
      factory?.call(apiClient);
}
