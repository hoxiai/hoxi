import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/navigation/main_scaffold_controller.dart';
import '../../../../core/navigation/navigation_guard.dart';
import '../../../../core/storage/account_scope_binding.dart';
import '../../../../core/storage/server_connection.dart';
import '../../../../core/storage/storage_service.dart';
import '../../../../core/theme/app_theme.dart';
import '../../../../shared/navigation/feature_route_registry.dart';
import '../../models/node_link_kind.dart';
import '../auth_failure_text.dart';
import '../auth_ui_extension_registry.dart';
import '../controllers/auth_controller.dart';
import '../qr_payload_connector.dart';
import '../widgets/lan_discovery_sheet.dart';
import 'qr_scanner_screen.dart';

part 'server_login_hoxi_section.dart';

/// 节点关联页：原生承载 Hoxi 桌面端与 GoPanel 远程面板关联，扩展服务经 [AuthUiExtensionRegistry] 挂载。
class ServerLoginScreen extends ConsumerStatefulWidget {
  const ServerLoginScreen({
    super.key,
    this.kind,
    this.initialUrl,
    this.replacingConnectionId,
    this.initialSelfHostedMode = false,
  });

  final NodeLinkKind? kind;
  final String? initialUrl;
  final String? replacingConnectionId;
  final bool initialSelfHostedMode;

  static Future<void> open(
    BuildContext context, {
    NodeLinkKind? kind,
    String? initialUrl,
    String? replacingConnectionId,
    bool initialSelfHostedMode = false,
  }) {
    final route = ModalRoute.of(context);
    final isDeclarativePage = route == null || route.settings is Page<dynamic>;
    if (GoRouter.maybeOf(context) != null && isDeclarativePage) {
      final params = <String, String>{
        if (kind != null) 'kind': kind.id,
        if (initialUrl != null && initialUrl.trim().isNotEmpty)
          'url': initialUrl.trim(),
        if (initialSelfHostedMode) 'mode': 'self_hosted',
        'connectionId': ?replacingConnectionId,
      };
      final uri = Uri(
        path: '/login',
        queryParameters: params.isEmpty ? null : params,
      );
      return AppNavigationGuard.pushNamedLocation<void>(
        context,
        uri.toString(),
      );
    }
    return AppNavigationGuard.push<void>(
      context,
      MaterialPageRoute<void>(
        builder: (_) => ServerLoginScreen(
          kind: kind,
          initialUrl: initialUrl,
          replacingConnectionId: replacingConnectionId,
          initialSelfHostedMode: initialSelfHostedMode,
        ),
      ),
    );
  }

  @override
  ConsumerState<ServerLoginScreen> createState() => _ServerLoginScreenState();
}

class _ServerLoginScreenState extends ConsumerState<ServerLoginScreen> {
  final _formKey = GlobalKey<FormState>();
  final _nameController = TextEditingController();
  final _urlController = TextEditingController();
  final _tokenController = TextEditingController();
  final _entranceController = TextEditingController();
  final _usernameController = TextEditingController();
  final _passwordController = TextEditingController();
  bool _syncToAccount = false;
  String? _syncScope;

  Future<void> _saveSyncPreference() async {
    final hook = FeatureRouteRegistry.onConnectionSavedSyncHook;
    if (hook == null) return;
    await hook(
      ref: ref,
      context: context,
      activeServer: ref.read(authControllerProvider).activeServer,
      syncToAccount: _syncToAccount,
      syncScope: _syncScope,
      showFailureSnackBar: true,
    );
  }

  NodeLinkKind? get _kind => widget.kind;
  bool get _isHoxi => _kind == NodeLinkKind.hoxi;
  bool get _isGoPanel => _kind == NodeLinkKind.gopanel;
  bool get _isAuxService => _kind != null && !_kind!.isDevNode;

  @override
  void initState() {
    super.initState();
    final initialUrl = widget.initialUrl?.trim() ?? '';
    if (initialUrl.isNotEmpty) {
      _urlController.text = initialUrl;
    }
  }

  @override
  void dispose() {
    _nameController.dispose();
    _urlController.dispose();
    _tokenController.dispose();
    _entranceController.dispose();
    _usernameController.dispose();
    _passwordController.dispose();
    super.dispose();
  }

  bool _isDirectQrOrTokenUrl(String input) {
    final trimmed = input.trim();
    if (trimmed.startsWith('{') && trimmed.endsWith('}')) return true;
    if (trimmed.startsWith('http://') || trimmed.startsWith('https://')) {
      return RegExp(r'[?&](?:token|code|mobilePairing)=').hasMatch(trimmed);
    }
    return false;
  }

  Future<void> _handlePasteAddressOrQr() async {
    final data = await Clipboard.getData(Clipboard.kTextPlain);
    final raw = (data?.text ?? '').trim();
    if (!mounted || raw.isEmpty) return;
    _urlController.text = raw;
    if (_isDirectQrOrTokenUrl(raw)) await _handleConnect();
  }

  Future<void> _handleScanLan() async {
    final picked = await showLanDiscoverySheet(
      context: context,
      filterKind: _kind,
    );
    if (!mounted || picked == null) return;
    _urlController.text = picked.url;
    if (_isHoxi) {
      if (!picked.requiresToken || _tokenController.text.trim().isNotEmpty) {
        await _handleConnect();
      } else {
        final isZh = Localizations.localeOf(
          context,
        ).languageCode.startsWith('zh');
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              isZh
                  ? '已发现并填入 ${picked.url}，请直接扫码或填入配对令牌完成连接'
                  : 'Filled ${picked.url}. Scan the QR code or enter the token to connect.',
            ),
            action: SnackBarAction(
              label: isZh ? '去扫码' : 'Scan QR',
              onPressed: () {
                if (mounted) QRScannerScreen.open(context, kind: _kind);
              },
            ),
          ),
        );
      }
    }
  }

  ServerConnection? _preferredSavedGoPanel() {
    final panels = StorageService.getServerList()
        .where((s) => s.isGoPanel)
        .toList();
    if (panels.isEmpty) return null;
    final activeUrl = StorageService.activeServerUrl;
    for (final panel in panels) {
      if (StorageService.isServerActive(panel, activeUrl)) return panel;
    }
    return panels.first;
  }

  Future<void> _handleConnectRelayFromGoPanel(ServerConnection gopanel) async {
    final connected = await showAndConnectHoxiRelayFromGoPanel(
      context: context,
      ref: ref,
      gopanel: gopanel,
    );
    if (connected) await _saveSyncPreference();
    if (connected && mounted) context.go('/dashboard');
  }

  Future<void> _handleConnect() async {
    final rawInput = _urlController.text.trim();
    final customName = _nameController.text.trim();
    if (_isDirectQrOrTokenUrl(rawInput)) {
      FocusScope.of(context).unfocus();
      final error = await connectWithQrPayload(
        ref: ref,
        l10n: context.l10n,
        rawPayload: rawInput,
        expectedKind: _kind,
        customName: customName,
        replacingConnectionId: widget.replacingConnectionId,
      );
      if (!mounted) return;
      if (error == null) {
        await _saveSyncPreference();
        if (!mounted) return;
        if (StorageService.isActiveHoxi) {
          ref
              .read(mainScaffoldIndexProvider.notifier)
              .openEngineeringSegment(EngineeringSectionController.codeSegment);
        }
        context.go('/dashboard');
      } else {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(error), backgroundColor: Colors.red),
        );
      }
      return;
    }

    if (!(_formKey.currentState?.validate() ?? false)) return;
    FocusScope.of(context).unfocus();
    final authController = ref.read(authControllerProvider.notifier);
    final noEntrance = _isHoxi || _isAuxService;
    final success = await authController.connectAndLogin(
      serverUrl: rawInput,
      entrance: noEntrance ? '' : _entranceController.text.trim(),
      username: _isHoxi ? '' : _usernameController.text.trim(),
      password: _isHoxi ? '' : _passwordController.text.trim(),
      token: noEntrance ? _tokenController.text.trim() : '',
      customName: customName,
      kind: _kind,
      replacingConnectionId: widget.replacingConnectionId,
    );

    if (!mounted) return;
    if (success) {
      await _saveSyncPreference();
      if (!mounted) return;
      if (StorageService.isActiveHoxi) {
        ref
            .read(mainScaffoldIndexProvider.notifier)
            .openEngineeringSegment(EngineeringSectionController.codeSegment);
      }
      context.go('/dashboard');
    } else {
      final failure = authFailureMessage(
        context.l10n,
        ref.read(authControllerProvider),
      );
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            failure.isEmpty ? context.l10n.loginConnectFailed : failure,
          ),
          backgroundColor: Colors.red,
        ),
      );
    }
  }

  String _qrRoute() => Uri(
    path: '/qr_scanner',
    queryParameters: {
      if (_kind != null) 'kind': _kind!.id,
      if (_syncToAccount) 'sync': '1',
      if (widget.replacingConnectionId != null)
        'connectionId': widget.replacingConnectionId!,
    },
  ).toString();

  Widget _buildCustomNameField(AppLocalizations l10n, bool isZh) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const SizedBox(height: 20),
        Text(
          isZh ? '自定义名称（可选）' : 'Custom Name (optional)',
          style: TextStyle(
            fontSize: 14,
            fontWeight: FontWeight.w600,
            color: AppTheme.textPrimary,
          ),
        ),
        const SizedBox(height: 8),
        TextFormField(
          key: const Key('server_login_custom_name_field'),
          controller: _nameController,
          decoration: InputDecoration(hintText: l10n.settingsDeviceRenameHint),
        ),
      ],
    );
  }

  String _appBarTitle(AppLocalizations l10n, bool isZh) {
    if (_isGoPanel) return l10n.loginGopanelTitle;
    if (_kind != null && !_isHoxi) {
      final resolved = AuthUiExtensionRegistry.loginTitleResolver?.call(
        _kind!,
        l10n,
        isZh,
      );
      if (resolved != null) return resolved;
    }
    return l10n.loginHoxiTitle;
  }

  String _headerSubtitle(AppLocalizations l10n, bool isZh) {
    if (_kind == null) return l10n.loginSubtitle;
    if (_isGoPanel) return l10n.loginGopanelSubtitle;
    if (!_isHoxi) {
      final resolved = AuthUiExtensionRegistry.loginSubtitleResolver?.call(
        _kind!,
        l10n,
        isZh,
      );
      if (resolved != null) return resolved;
    }
    return l10n.loginHoxiSubtitle;
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final isZh = Localizations.localeOf(context).languageCode.startsWith('zh');
    final isLoading = ref.watch(authControllerProvider).isLoading;
    final customField = _buildCustomNameField(l10n, isZh);
    final customSection = (_kind != null && _isAuxService)
        ? AuthUiExtensionRegistry.customLoginSectionBuilder?.call(
            CustomLoginSectionParams(
              kind: _kind!,
              formKey: _formKey,
              nameController: _nameController,
              urlController: _urlController,
              tokenController: _tokenController,
              usernameController: _usernameController,
              passwordController: _passwordController,
              customNameField: customField,
              isLoading: isLoading,
              initialSelfHostedMode: widget.initialSelfHostedMode,
              initialUrl: widget.initialUrl,
              replacingConnectionId: widget.replacingConnectionId,
              qrRoute: _qrRoute(),
              urlValidator: _validateUrl,
              onScanLan: _handleScanLan,
              onPasteAddressOrQr: _handlePasteAddressOrQr,
              onOpenQrScanner: () => QRScannerScreen.open(
                context,
                kind: _kind,
                syncToAccount: _syncToAccount,
                replacingConnectionId: widget.replacingConnectionId,
              ),
              onConnect: _handleConnect,
              onConnectedSync: _saveSyncPreference,
            ),
          )
        : null;

    return Scaffold(
      appBar: _kind == null ? null : AppBar(title: Text(_appBarTitle(l10n, isZh))),
      body: SafeArea(
        child: Center(
          child: SingleChildScrollView(
            padding: const EdgeInsets.symmetric(horizontal: 24),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                if (_kind == null) ...[
                  Center(
                    child: ClipRRect(
                      borderRadius: BorderRadius.circular(18),
                      child: Image.asset(
                        'web/icons/Icon-192.png',
                        width: 72,
                        height: 72,
                      ),
                    ),
                  ),
                  const SizedBox(height: 16),
                  Text(
                    l10n.appName,
                    textAlign: TextAlign.center,
                    style: const TextStyle(
                      fontSize: 28,
                      fontWeight: FontWeight.bold,
                      letterSpacing: 1.2,
                    ),
                  ),
                  const SizedBox(height: 8),
                ],
                Text(
                  _headerSubtitle(l10n, isZh),
                  textAlign: TextAlign.center,
                  style: TextStyle(fontSize: 15, color: AppTheme.textSecondary),
                ),
                if (customSection != null)
                  customSection
                else if (_isHoxi)
                  _HoxiNodeLoginSection(
                    formKey: _formKey,
                    urlController: _urlController,
                    tokenController: _tokenController,
                    customNameField: customField,
                    l10n: l10n,
                    isZh: isZh,
                    isLoading: isLoading,
                    urlValidator: _validateUrl,
                    savedGoPanel: _preferredSavedGoPanel(),
                    onScanQr: () => QRScannerScreen.open(
                      context,
                      kind: _kind,
                      syncToAccount: _syncToAccount,
                      replacingConnectionId: widget.replacingConnectionId,
                    ),
                    onScanLan: _handleScanLan,
                    onPasteAddressOrQr: _handlePasteAddressOrQr,
                    onConnect: _handleConnect,
                    onConnectRelayFromGoPanel: _handleConnectRelayFromGoPanel,
                  )
                else
                  _buildGoPanelOrGenericLoginCard(
                    context,
                    l10n,
                    isZh,
                    isLoading,
                    customField,
                  ),
                FeatureRouteRegistry.buildConnectionSyncToggle(
                  connectionId: widget.replacingConnectionId,
                  value: _syncToAccount,
                  onChanged: (value) => setState(() {
                    _syncToAccount = value;
                    _syncScope = AccountScopeBinding.accountScope;
                  }),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Future<void> _openGoPanelInstaller() async {
    final installed = await FeatureRouteRegistry.openGopanelInstaller(context);
    if (!mounted || installed == null) return;
    setState(() {
      _urlController.text = installed.panelUrl;
      _entranceController.text = installed.entrance;
      _usernameController.text = installed.adminEmail;
      _passwordController.text = installed.adminPassword;
      if (_nameController.text.trim().isEmpty) {
        _nameController.text = 'GoPanel (${installed.host})';
      }
    });
  }

  Widget _buildGoPanelOrGenericLoginCard(
    BuildContext context,
    AppLocalizations l10n,
    bool isZh,
    bool isLoading,
    Widget customField,
  ) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const SizedBox(height: 20),
        if (_isGoPanel && FeatureRouteRegistry.gopanelInstallerOpener != null) ...[
          Material(
            color: AppTheme.surface,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(16),
              side: BorderSide(color: AppTheme.primaryBlueBorder),
            ),
            child: InkWell(
              key: const ValueKey('gopanel_install_entry_button'),
              borderRadius: BorderRadius.circular(16),
              onTap: isLoading ? null : _openGoPanelInstaller,
              child: Padding(
                padding: const EdgeInsets.all(16),
                child: Row(
                  children: [
                    Container(
                      width: 40,
                      height: 40,
                      decoration: BoxDecoration(
                        color: AppTheme.primaryBlueLight,
                        borderRadius: BorderRadius.circular(12),
                      ),
                      child: const Icon(
                        Icons.terminal_rounded,
                        color: AppTheme.primaryBlue,
                        size: 20,
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(
                            isZh
                                ? '在一台新服务器上部署 GoPanel'
                                : 'Deploy GoPanel on a New Server',
                            style: TextStyle(
                              fontSize: 15,
                              fontWeight: FontWeight.w800,
                              color: AppTheme.textPrimary,
                            ),
                          ),
                          const SizedBox(height: 2),
                          Text(
                            isZh
                                ? '填写服务器 Root 密码或 SSH 证书，交互式运行 bash <(curl -fsSL https://gopanel.run)'
                                : 'Enter Root password or SSH key to interactively run bash <(curl -fsSL https://gopanel.run)',
                            style: TextStyle(
                              fontSize: 12,
                              color: AppTheme.textSecondary,
                            ),
                          ),
                        ],
                      ),
                    ),
                    const Icon(
                      Icons.chevron_right_rounded,
                      size: 20,
                      color: AppTheme.primaryBlue,
                    ),
                  ],
                ),
              ),
            ),
          ),
          const SizedBox(height: 12),
        ],
        Card(
          child: Padding(
            padding: const EdgeInsets.all(20),
            child: Form(
              key: _formKey,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    _isGoPanel ? l10n.loginPanelAddress : l10n.loginNodeAddress,
                    style: TextStyle(
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                      color: AppTheme.textPrimary,
                    ),
                  ),
                  const SizedBox(height: 8),
                  TextFormField(
                    controller: _urlController,
                    decoration: InputDecoration(
                      hintText: _isGoPanel
                          ? l10n.loginPanelAddressHint
                          : l10n.loginAddressHint,
                      suffixIcon: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          IconButton(
                            visualDensity: VisualDensity.compact,
                            icon: Icon(
                              Icons.radar_outlined,
                              size: 19,
                              color: AppTheme.textSecondary,
                            ),
                            tooltip: isZh ? '扫描局域网设备' : 'Scan LAN Devices',
                            onPressed: isLoading ? null : _handleScanLan,
                          ),
                          IconButton(
                            visualDensity: VisualDensity.compact,
                            icon: Icon(
                              Icons.assignment_outlined,
                              size: 19,
                              color: AppTheme.textSecondary,
                            ),
                            onPressed: isLoading
                                ? null
                                : _handlePasteAddressOrQr,
                          ),
                        ],
                      ),
                    ),
                    validator: _validateUrl,
                  ),
                  const SizedBox(height: 18),
                  Text(
                    l10n.loginEntranceLabel,
                    style: TextStyle(
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                      color: AppTheme.textPrimary,
                    ),
                  ),
                  const SizedBox(height: 8),
                  TextFormField(
                    controller: _entranceController,
                    decoration: InputDecoration(
                      hintText: l10n.loginEntranceHint,
                    ),
                    validator: (v) => isValidServerEntrance(v ?? '')
                        ? null
                        : l10n.loginEntranceInvalid,
                  ),
                  const SizedBox(height: 20),
                  Text(
                    _isGoPanel ? l10n.loginAdminEmail : l10n.loginUsername,
                    style: TextStyle(
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                      color: AppTheme.textPrimary,
                    ),
                  ),
                  const SizedBox(height: 8),
                  TextFormField(
                    controller: _usernameController,
                    keyboardType: TextInputType.emailAddress,
                    autofillHints: const [AutofillHints.username],
                    decoration: InputDecoration(
                      hintText: _isGoPanel
                          ? l10n.loginAdminEmailHint
                          : l10n.loginUsername,
                    ),
                    validator: (value) {
                      if (_isGoPanel) {
                        return (value ?? '').trim().isEmpty
                            ? l10n.loginAdminEmailHint
                            : null;
                      }
                      return null;
                    },
                  ),
                  const SizedBox(height: 20),
                  Text(
                    l10n.loginPassword,
                    style: TextStyle(
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                      color: AppTheme.textPrimary,
                    ),
                  ),
                  const SizedBox(height: 8),
                  TextFormField(
                    controller: _passwordController,
                    obscureText: true,
                    autofillHints: const [AutofillHints.password],
                    decoration: InputDecoration(
                      hintText: _isGoPanel
                          ? l10n.loginPasswordHintPanel
                          : l10n.loginPasswordHint,
                    ),
                    validator: (value) {
                      if (_isGoPanel) {
                        return (value ?? '').trim().isEmpty
                            ? l10n.loginPasswordRequired
                            : null;
                      }
                      final user = _usernameController.text.trim();
                      final pass = (value ?? '').trim();
                      if (user.isNotEmpty && pass.isEmpty) {
                        return l10n.loginPasswordRequired;
                      }
                      return null;
                    },
                  ),
                  customField,
                  const SizedBox(height: 32),
                  ElevatedButton(
                    onPressed: isLoading ? null : _handleConnect,
                    child: isLoading
                        ? const SizedBox(
                            width: 20,
                            height: 20,
                            child: CircularProgressIndicator(
                              color: Colors.white,
                              strokeWidth: 2,
                            ),
                          )
                        : Text(l10n.loginConnect),
                  ),
                  const SizedBox(height: 16),
                  OutlinedButton.icon(
                    onPressed: isLoading
                        ? null
                        : () => QRScannerScreen.open(context, kind: _kind),
                    icon: const Icon(Icons.crop_free_rounded),
                    label: Text(
                      _kind == null ? l10n.loginScanQr : l10n.loginScanPanelQr,
                    ),
                    style: OutlinedButton.styleFrom(
                      padding: const EdgeInsets.symmetric(vertical: 14),
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(12),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ],
    );
  }

  String? _validateUrl(String? value) {
    final l10n = context.l10n;
    final trimmed = (value ?? '').trim();
    if (trimmed.isEmpty) return l10n.loginAddressRequired;
    if (trimmed.startsWith('{') && trimmed.endsWith('}')) return null;
    if (!trimmed.startsWith('http')) return l10n.loginAddressScheme;
    return null;
  }
}
