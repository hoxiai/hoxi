import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/network/short_code_pairing_client.dart';
import '../../models/node_link_kind.dart';
import '../qr_payload_connector.dart';

/// Bottom sheet for redeeming a 6-digit one-time pairing code from a pairing
/// relay and connecting to Hoxi desktop via zero-knowledge X25519 KEX.
Future<bool> showShortCodePairingSheet({
  required BuildContext context,
  required WidgetRef ref,
  NodeLinkKind? expectedKind,
  String? replacingConnectionId,
}) async {
  final result = await showModalBottomSheet<bool>(
    context: context,
    isScrollControlled: true,
    useSafeArea: true,
    builder: (_) => _ShortCodePairingSheet(
      parentRef: ref,
      expectedKind: expectedKind,
      replacingConnectionId: replacingConnectionId,
    ),
  );
  return result == true;
}

class _ShortCodePairingSheet extends StatefulWidget {
  const _ShortCodePairingSheet({
    required this.parentRef,
    this.expectedKind,
    this.replacingConnectionId,
  });

  final WidgetRef parentRef;
  final NodeLinkKind? expectedKind;
  final String? replacingConnectionId;

  @override
  State<_ShortCodePairingSheet> createState() => _ShortCodePairingSheetState();
}

class _ShortCodePairingSheetState extends State<_ShortCodePairingSheet> {
  final TextEditingController _relayController = TextEditingController();
  final TextEditingController _codeController = TextEditingController();
  final ShortCodePairingClient _client = ShortCodePairingClient();
  final ShortCodeClaim _claim = ShortCodeClaim();

  bool _submitting = false;
  String _error = '';

  @override
  void initState() {
    super.initState();
    _loadSavedRelayUrl();
  }

  Future<void> _loadSavedRelayUrl() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final saved = (prefs.getString(pairingRelayUrlPrefsKey) ?? '').trim();
      final fallback = defaultPairingRelayUrl.trim();
      final initial = saved.isNotEmpty ? saved : fallback;
      if (initial.isNotEmpty && mounted && _relayController.text.isEmpty) {
        _relayController.text = initial;
      }
    } catch (_) {
      final fallback = defaultPairingRelayUrl.trim();
      if (fallback.isNotEmpty && mounted && _relayController.text.isEmpty) {
        _relayController.text = fallback;
      }
    }
  }

  @override
  void dispose() {
    _relayController.dispose();
    _codeController.dispose();
    super.dispose();
  }

  String _localizeError(ShortCodePairingException err, bool isZh) {
    switch (err.kind) {
      case ShortCodePairingErrorKind.invalidInput:
        return isZh
            ? '请检查 HTTPS 配对中继地址与 6 位数字配对码'
            : 'Check the HTTPS pairing relay URL and 6-digit code';
      case ShortCodePairingErrorKind.unavailable:
        return isZh
            ? '配对码不存在、已过期或已被使用，请在电脑端刷新'
            : 'Code expired or already used — refresh it on your desktop';
      case ShortCodePairingErrorKind.rateLimited:
        return isZh ? '请求过于频繁，请稍后再试' : 'Too many attempts — try again shortly';
      case ShortCodePairingErrorKind.conflict:
        return isZh ? '配对请求冲突，请重新输入配对码' : 'Pairing request conflict — retry';
      case ShortCodePairingErrorKind.network:
        return isZh
            ? '无法连接配对中继：${err.message}'
            : 'Could not reach pairing relay: ${err.message}';
    }
  }

  Future<void> _submit() async {
    if (_submitting) return;
    final isZh = Localizations.localeOf(context).languageCode.startsWith('zh');
    final relayUrl = _relayController.text.trim();
    final pin = _codeController.text.replaceAll(RegExp(r'\s+'), '');

    if (relayUrl.isEmpty || !isValidShortCode(pin)) {
      setState(() {
        _error = isZh
            ? '请填写 HTTPS 配对中继地址与 6 位配对码'
            : 'Enter both the HTTPS relay URL and 6-digit code';
      });
      return;
    }

    setState(() {
      _submitting = true;
      _error = '';
    });

    try {
      final redeemed = await _client.redeem(
        relayOrigin: relayUrl,
        code: pin,
        claim: _claim,
      );
      try {
        final prefs = await SharedPreferences.getInstance();
        await prefs.setString(pairingRelayUrlPrefsKey, relayUrl);
      } catch (_) {}

      if (!mounted) return;
      final connectErr = await connectWithQrPayload(
        ref: widget.parentRef,
        l10n: context.l10n,
        rawPayload: redeemed.ticket,
        expectedKind: widget.expectedKind ?? NodeLinkKind.hoxi,
        replacingConnectionId: widget.replacingConnectionId,
      );
      if (!mounted) return;
      if (connectErr == null) {
        Navigator.of(context).pop(true);
      } else {
        setState(() {
          _submitting = false;
          _error = connectErr;
        });
      }
    } on ShortCodePairingException catch (err) {
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _error = _localizeError(err, isZh);
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _error = isZh ? '配对失败，请重试' : 'Pairing failed — please try again';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final isZh = Localizations.localeOf(context).languageCode.startsWith('zh');
    final bottomInset = MediaQuery.of(context).viewInsets.bottom;

    return Padding(
      padding: EdgeInsets.fromLTRB(20, 16, 20, 20 + bottomInset),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            isZh ? '输入 6 位一次性配对码' : 'Enter 6-digit pairing code',
            style: Theme.of(
              context,
            ).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w600),
          ),
          const SizedBox(height: 6),
          Text(
            isZh
                ? '中继仅转发 60 秒一次性连接票据，端到端密钥将在首次连接时经 X25519 直接与电脑协商'
                : 'The relay only forwards a 60s connection ticket; your E2EE key is negotiated directly over X25519',
            style: Theme.of(context).textTheme.bodySmall,
          ),
          const SizedBox(height: 14),
          TextField(
            controller: _relayController,
            enabled: !_submitting,
            keyboardType: TextInputType.url,
            autocorrect: false,
            decoration: InputDecoration(
              labelText: isZh ? '配对中继地址' : 'Pairing relay URL',
              hintText: 'https://link.example.com',
              border: const OutlineInputBorder(),
              isDense: true,
            ),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _codeController,
            enabled: !_submitting,
            keyboardType: TextInputType.number,
            maxLength: 6,
            textAlign: TextAlign.center,
            style: const TextStyle(
              fontFamily: 'monospace',
              fontSize: 24,
              fontWeight: FontWeight.w600,
              letterSpacing: 8,
            ),
            inputFormatters: [FilteringTextInputFormatter.digitsOnly],
            decoration: InputDecoration(
              labelText: isZh ? '6 位配对码' : '6-digit code',
              counterText: '',
              border: const OutlineInputBorder(),
            ),
            onSubmitted: (_) => _submit(),
          ),
          if (_error.isNotEmpty) ...[
            const SizedBox(height: 10),
            Text(
              _error,
              style: TextStyle(
                color: Theme.of(context).colorScheme.error,
                fontSize: 12,
              ),
            ),
          ],
          const SizedBox(height: 14),
          FilledButton(
            onPressed: _submitting ? null : _submit,
            child: _submitting
                ? const SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : Text(isZh ? '验证并连接电脑' : 'Verify & connect'),
          ),
        ],
      ),
    );
  }
}
