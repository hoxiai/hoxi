import 'package:flutter/material.dart';

import '../../../core/l10n/l10n.dart';
import '../models/node_link_kind.dart';

class CustomLoginSectionParams {
  final NodeLinkKind kind;
  final GlobalKey<FormState> formKey;
  final TextEditingController nameController;
  final TextEditingController urlController;
  final TextEditingController tokenController;
  final TextEditingController usernameController;
  final TextEditingController passwordController;
  final Widget customNameField;
  final bool isLoading;
  final bool initialSelfHostedMode;
  final String? initialUrl;
  final String? replacingConnectionId;
  final String qrRoute;
  final String? Function(String?) urlValidator;
  final VoidCallback onScanLan;
  final VoidCallback onPasteAddressOrQr;
  final VoidCallback onOpenQrScanner;
  final Future<void> Function() onConnect;
  final Future<void> Function() onConnectedSync;

  const CustomLoginSectionParams({
    required this.kind,
    required this.formKey,
    required this.nameController,
    required this.urlController,
    required this.tokenController,
    required this.usernameController,
    required this.passwordController,
    required this.customNameField,
    required this.isLoading,
    required this.initialSelfHostedMode,
    required this.initialUrl,
    required this.replacingConnectionId,
    required this.qrRoute,
    required this.urlValidator,
    required this.onScanLan,
    required this.onPasteAddressOrQr,
    required this.onOpenQrScanner,
    required this.onConnect,
    required this.onConnectedSync,
  });
}

/// 认证界面扩展注册表：
/// 让 `ServerLoginScreen`、`QRScannerScreen`、`NodeLinkKindScreen`、`LanDiscoverySheet`
/// 仅依赖基础 `Hoxi` / `GoPanel` 协议，商业扩展界面的标题、图标与登录表单通过注册表挂载。
abstract final class AuthUiExtensionRegistry {
  static String? Function(NodeLinkKind kind, AppLocalizations l10n)?
  pickerTitleResolver;
  static String? Function(NodeLinkKind kind, AppLocalizations l10n)?
  pickerSubtitleResolver;
  static IconData? Function(NodeLinkKind kind)? kindIconResolver;
  static String? Function(NodeLinkKind kind, AppLocalizations l10n, bool isZh)?
  loginTitleResolver;
  static String? Function(NodeLinkKind kind, AppLocalizations l10n, bool isZh)?
  loginSubtitleResolver;
  static String? Function(NodeLinkKind kind, AppLocalizations l10n, bool isZh)?
  qrTitleResolver;
  static Widget? Function(CustomLoginSectionParams params)?
  customLoginSectionBuilder;
}
