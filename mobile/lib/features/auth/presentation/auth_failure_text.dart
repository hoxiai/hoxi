import '../../../core/l10n/l10n.dart';
import 'controllers/auth_controller.dart';

/// 将 [AuthState] 的失败信息本地化为界面文案。控制器不持上下文，只在
/// 这里（界面层）拼接，避免在控制器里硬编码中文。
String authFailureMessage(AppLocalizations l10n, AuthState state) {
  final detail = switch (state.failureDetail) {
    'timeout' => l10n.authOperationTimeout,
    'entranceInvalid' => l10n.authEntranceInvalid,
    _ => state.failureDetail ?? '',
  };
  return switch (state.failureKind) {
    AuthFailureKind.connect => l10n.authConnectFailed(detail),
    AuthFailureKind.qrVerify => l10n.authQrVerifyFailed(detail),
    AuthFailureKind.switchServer => l10n.authSwitchFailed(detail),
    null => '',
  };
}
