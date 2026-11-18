import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/navigation/main_scaffold_controller.dart';
import '../../../../core/storage/account_scope_binding.dart';
import '../../../../core/storage/storage_service.dart';
import '../../../../shared/navigation/feature_route_registry.dart';
import '../../models/node_link_kind.dart';
import '../auth_ui_extension_registry.dart';
import '../controllers/auth_controller.dart';
import '../qr_payload_connector.dart';

/// 扫码授权页面
class QRScannerScreen extends ConsumerStatefulWidget {
  const QRScannerScreen({
    super.key,
    this.kind,
    this.syncToAccount = false,
    this.replacingConnectionId,
  });

  final bool syncToAccount;
  final String? replacingConnectionId;

  /// 从「连接」进入时限定二维码类型，避免把面板码当成电脑码。
  final NodeLinkKind? kind;

  /// 统一打开扫码页：若当前页面处于 `GoRouter` 的声明式 `Page` 栈中则走 `/qr_scanner` 路由；
  /// 若当前处于命令式 `MaterialPageRoute` 或弹窗之上，则直接压入 `MaterialPageRoute`。
  static Future<void> open(
    BuildContext context, {
    NodeLinkKind? kind,
    bool syncToAccount = false,
    String? replacingConnectionId,
  }) {
    final route = ModalRoute.of(context);
    final isDeclarativePage = route == null || route.settings is Page<dynamic>;
    if (GoRouter.maybeOf(context) != null && isDeclarativePage) {
      final path = Uri(
        path: '/qr_scanner',
        queryParameters: {
          if (kind != null) 'kind': kind.id,
          if (syncToAccount) 'sync': '1',
          'connectionId': ?replacingConnectionId,
        },
      ).toString();
      return AppNavigationGuard.pushNamedLocation<void>(context, path);
    }
    return AppNavigationGuard.push<void>(
      context,
      MaterialPageRoute<void>(
        builder: (_) => QRScannerScreen(
          kind: kind,
          syncToAccount: syncToAccount,
          replacingConnectionId: replacingConnectionId,
        ),
      ),
    );
  }

  @override
  ConsumerState<QRScannerScreen> createState() => _QRScannerScreenState();
}

class _QRScannerScreenState extends ConsumerState<QRScannerScreen> {
  final MobileScannerController _scannerController = MobileScannerController(
    detectionSpeed: DetectionSpeed.normal,
    formats: const [BarcodeFormat.qrCode],
  );
  bool _isProcessing = false;
  bool _torchEnabled = false;
  late bool _syncToAccount = widget.syncToAccount;
  String? _syncScope = AccountScopeBinding.accountScope;

  String _resolveTitle(AppLocalizations l10n, bool isZh) {
    final kind = widget.kind;
    if (kind == null) return l10n.loginScanQr;
    if (kind == NodeLinkKind.hoxi) return l10n.loginScanHoxiQr;
    if (kind == NodeLinkKind.gopanel) return l10n.loginScanPanelQr;
    return AuthUiExtensionRegistry.qrTitleResolver?.call(kind, l10n, isZh) ??
        l10n.loginScanQr;
  }

  @override
  void dispose() {
    _scannerController.dispose();
    super.dispose();
  }

  Future<void> _handleBarcode(BarcodeCapture capture) async {
    if (_isProcessing) return;

    final List<Barcode> barcodes = capture.barcodes;
    if (barcodes.isEmpty) return;

    final String? codeValue = barcodes.first.rawValue;
    if (codeValue == null || codeValue.isEmpty) return;

    await _processRawPayload(codeValue);
  }

  Future<void> _handlePasteFromClipboard() async {
    if (_isProcessing) return;
    final data = await Clipboard.getData(Clipboard.kTextPlain);
    final raw = (data?.text ?? '').trim();
    if (!mounted) return;
    if (raw.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(context.l10n.qrErrorUnrecognized),
          backgroundColor: Colors.red,
        ),
      );
      return;
    }
    await _processRawPayload(raw);
  }

  Future<void> _processRawPayload(String codeValue) async {
    setState(() {
      _isProcessing = true;
    });
    _scannerController.stop();

    final error = await connectWithQrPayload(
      ref: ref,
      l10n: context.l10n,
      rawPayload: codeValue,
      expectedKind: widget.kind,
      replacingConnectionId: widget.replacingConnectionId,
    );

    if (!mounted) return;
    if (error == null) {
      final syncHook = FeatureRouteRegistry.onConnectionSavedSyncHook;
      if (syncHook != null) {
        await syncHook(
          ref: ref,
          context: context,
          activeServer: ref.read(authControllerProvider).activeServer,
          syncToAccount: _syncToAccount,
          syncScope: _syncScope,
          showFailureSnackBar: false,
        );
        if (!mounted) return;
      }
      if (StorageService.isActiveHoxi) {
        ref
            .read(mainScaffoldIndexProvider.notifier)
            .openEngineeringSegment(EngineeringSectionController.codeSegment);
      }
      context.go('/dashboard');
    } else {
      _showErrorAndResume(error);
    }
  }

  void _showErrorAndResume(String errorMsg) {
    if (!mounted) return;

    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(errorMsg), backgroundColor: Colors.red),
    );

    Future.delayed(const Duration(seconds: 2), () {
      if (mounted) {
        setState(() {
          _isProcessing = false;
        });
        _scannerController.start();
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final isLoading = ref.watch(authControllerProvider).isLoading;
    final isZh = Localizations.localeOf(context).languageCode.startsWith('zh');
    final syncToggle = FeatureRouteRegistry.buildConnectionSyncToggle(
      connectionId: widget.replacingConnectionId,
      value: _syncToAccount,
      onChanged: (value) => setState(() {
        _syncToAccount = value;
        _syncScope = AccountScopeBinding.accountScope;
      }),
    );

    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        backgroundColor: Colors.black,
        foregroundColor: Colors.white,
        surfaceTintColor: Colors.transparent,
        elevation: 0,
        title: Text(
          _resolveTitle(context.l10n, isZh),
          style: const TextStyle(
            color: Colors.white,
            fontSize: 16,
            fontWeight: FontWeight.w600,
          ),
        ),
        actions: [
          IconButton(
            tooltip: isZh ? '粘贴配对码' : 'Paste pairing code',
            icon: const Icon(Icons.assignment_outlined, size: 20),
            onPressed: _isProcessing ? null : _handlePasteFromClipboard,
          ),
          IconButton(
            tooltip: isZh ? '手电筒' : 'Flashlight',
            icon: Icon(
              _torchEnabled
                  ? Icons.flashlight_on_outlined
                  : Icons.flashlight_off_outlined,
              size: 20,
            ),
            onPressed: () {
              _scannerController.toggleTorch();
              setState(() => _torchEnabled = !_torchEnabled);
            },
          ),
          const SizedBox(width: 4),
        ],
      ),
      bottomNavigationBar: FeatureRouteRegistry.connectionSyncToggleBuilder == null
          ? null
          : SafeArea(
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 20),
                child: syncToggle,
              ),
            ),
      body: Stack(
        children: [
          MobileScanner(
            controller: _scannerController,
            onDetect: _handleBarcode,
          ),
          Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Container(
                  width: 236,
                  height: 236,
                  decoration: BoxDecoration(
                    border: Border.all(
                      color: Colors.white.withValues(alpha: 0.42),
                      width: 1.2,
                    ),
                    borderRadius: BorderRadius.circular(20),
                  ),
                ),
                const SizedBox(height: 24),
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 14,
                    vertical: 7,
                  ),
                  decoration: BoxDecoration(
                    color: Colors.black.withValues(alpha: 0.45),
                    borderRadius: BorderRadius.circular(999),
                  ),
                  child: Text(
                    context.l10n.qrAlignHint,
                    textAlign: TextAlign.center,
                    style: TextStyle(
                      color: Colors.white.withValues(alpha: 0.86),
                      fontSize: 13,
                      fontWeight: FontWeight.w500,
                    ),
                  ),
                ),
              ],
            ),
          ),
          if (isLoading || _isProcessing)
            Container(
              color: Colors.black.withValues(alpha: 0.6),
              child: Center(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const SizedBox(
                      width: 24,
                      height: 24,
                      child: CircularProgressIndicator(
                        color: Colors.white,
                        strokeWidth: 2.2,
                      ),
                    ),
                    const SizedBox(height: 14),
                    Text(
                      context.l10n.qrVerifying,
                      style: TextStyle(
                        color: Colors.white.withValues(alpha: 0.9),
                        fontSize: 14,
                      ),
                    ),
                  ],
                ),
              ),
            ),
        ],
      ),
    );
  }
}
