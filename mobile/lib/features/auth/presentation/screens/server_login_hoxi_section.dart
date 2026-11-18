part of 'server_login_screen.dart';

class _HoxiNodeLoginSection extends StatelessWidget {
  final GlobalKey<FormState> formKey;
  final TextEditingController urlController;
  final TextEditingController tokenController;
  final Widget customNameField;
  final AppLocalizations l10n;
  final bool isZh;
  final bool isLoading;
  final FormFieldValidator<String> urlValidator;
  final ServerConnection? savedGoPanel;
  final VoidCallback onScanQr;
  final VoidCallback onScanLan;
  final VoidCallback onPasteAddressOrQr;
  final VoidCallback onConnect;
  final ValueChanged<ServerConnection> onConnectRelayFromGoPanel;

  const _HoxiNodeLoginSection({
    required this.formKey,
    required this.urlController,
    required this.tokenController,
    required this.customNameField,
    required this.l10n,
    required this.isZh,
    required this.isLoading,
    required this.urlValidator,
    required this.savedGoPanel,
    required this.onScanQr,
    required this.onScanLan,
    required this.onPasteAddressOrQr,
    required this.onConnect,
    required this.onConnectRelayFromGoPanel,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const SizedBox(height: 20),
        Material(
          color: AppTheme.surface,
          borderRadius: BorderRadius.circular(16),
          child: InkWell(
            borderRadius: BorderRadius.circular(16),
            onTap: isLoading ? null : onScanQr,
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Row(
                children: [
                  Container(
                    width: 40,
                    height: 40,
                    decoration: BoxDecoration(
                      color: AppTheme.surfaceMuted,
                      borderRadius: BorderRadius.circular(12),
                    ),
                    child: Icon(
                      Icons.crop_free_rounded,
                      color: AppTheme.textSecondary,
                      size: 20,
                    ),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          l10n.loginScanHoxiQr,
                          style: TextStyle(
                            fontSize: 15,
                            fontWeight: FontWeight.w800,
                            color: AppTheme.textPrimary,
                          ),
                        ),
                        const SizedBox(height: 2),
                        Text(
                          l10n.loginHoxiQrHint,
                          style: TextStyle(
                            fontSize: 12,
                            color: AppTheme.textSecondary,
                          ),
                        ),
                      ],
                    ),
                  ),
                  Icon(
                    Icons.chevron_right_rounded,
                    size: 20,
                    color: AppTheme.textSecondary,
                  ),
                ],
              ),
            ),
          ),
        ),
        const SizedBox(height: 12),
        Card(
          child: Padding(
            padding: const EdgeInsets.all(20.0),
            child: Form(
              key: formKey,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    l10n.loginHoxiAddress,
                    style: TextStyle(
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                      color: AppTheme.textPrimary,
                    ),
                  ),
                  const SizedBox(height: 8),
                  TextFormField(
                    controller: urlController,
                    decoration: InputDecoration(
                      hintText: l10n.loginHoxiAddressHint,
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
                            onPressed: isLoading ? null : onScanLan,
                          ),
                          IconButton(
                            visualDensity: VisualDensity.compact,
                            icon: Icon(
                              Icons.assignment_outlined,
                              size: 19,
                              color: AppTheme.textSecondary,
                            ),
                            onPressed: isLoading ? null : onPasteAddressOrQr,
                          ),
                        ],
                      ),
                    ),
                    validator: urlValidator,
                  ),
                  const SizedBox(height: 18),
                  Text(
                    l10n.loginHoxiToken,
                    style: TextStyle(
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                      color: AppTheme.textPrimary,
                    ),
                  ),
                  const SizedBox(height: 8),
                  TextFormField(
                    controller: tokenController,
                    decoration: InputDecoration(
                      hintText: l10n.loginHoxiTokenHint,
                    ),
                  ),
                  customNameField,
                  const SizedBox(height: 24),
                  ElevatedButton(
                    onPressed: isLoading ? null : onConnect,
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
                  if (savedGoPanel != null) ...[
                    const SizedBox(height: 12),
                    OutlinedButton.icon(
                      onPressed: isLoading
                          ? null
                          : () => onConnectRelayFromGoPanel(savedGoPanel!),
                      icon: const Icon(Icons.laptop_mac_outlined, size: 18),
                      label: Text(l10n.relayNodesTitle),
                      style: OutlinedButton.styleFrom(
                        padding: const EdgeInsets.symmetric(vertical: 14),
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(12),
                        ),
                      ),
                    ),
                  ],
                ],
              ),
            ),
          ),
        ),
      ],
    );
  }
}
