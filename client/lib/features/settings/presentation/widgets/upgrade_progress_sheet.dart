import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/theme/app_theme.dart';
import '../controllers/settings_controller.dart';
import '../settings_text.dart';

class UpgradeProgressSheet extends StatefulWidget {
  final String logName;
  final WidgetRef ref;
  final String currentVersion;
  final String targetVersion;

  const UpgradeProgressSheet({
    required this.logName,
    required this.ref,
    required this.currentVersion,
    required this.targetVersion,
    super.key,
  });

  @override
  State<UpgradeProgressSheet> createState() => _UpgradeProgressSheetState();
}

class _UpgradeProgressSheetState extends State<UpgradeProgressSheet> {
  final _logs = <String>[];
  late String _logName;
  bool _loading = true;
  bool _finished = false;
  bool _logFailed = false;
  bool _verified = false;
  bool _verifying = false;
  bool _verificationTimedOut = false;
  bool _retrying = false;
  int _progress = 0;
  String _phase = 'preparing';
  Timer? _timer;

  @override
  void initState() {
    super.initState();
    _logName = widget.logName;
    _startPolling();
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  void _startPolling() {
    _pollLogs();
    _timer = Timer.periodic(const Duration(seconds: 3), (_) => _pollLogs());
  }

  Future<void> _pollLogs() async {
    try {
      final controller = widget.ref.read(settingsControllerProvider.notifier);
      final lines = await controller.getUpgradeLogs(_logName);
      final status = await controller.getUpgradeStatus(_logName);
      if (!mounted) return;
      setState(() {
        _loading = false;
        for (final line in lines) {
          if (!_logs.contains(line)) _logs.add(line);
        }
        if (status != null) {
          _finished = status.status == 'success' || status.status == 'failed';
          _logFailed = status.status == 'failed';
          _progress = status.progress.clamp(0, 100).toInt();
          _phase = status.phase;
        } else {
          _finished = lines.any(
            (line) =>
                line.toLowerCase().contains('upload success') ||
                line.toLowerCase().contains('upload error'),
          );
          _logFailed = lines.any(
            (line) => line.toLowerCase().contains('upload error'),
          );
        }
      });
      if (_finished) {
        _timer?.cancel();
        if (!_logFailed && !_verified && !_verifying) await _verifyVersion();
      }
    } catch (_) {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _verifyVersion() async {
    if (_verifying) return;
    setState(() {
      _verifying = true;
      _verificationTimedOut = false;
    });
    for (var attempt = 0; attempt < 10; attempt++) {
      if (attempt > 0) await Future<void>.delayed(const Duration(seconds: 2));
      final version = await widget.ref
          .read(settingsControllerProvider.notifier)
          .loadVersion();
      if (!mounted) return;
      if (version?.versionName == widget.targetVersion) {
        setState(() {
          _verified = true;
          _verifying = false;
        });
        await widget.ref
            .read(settingsControllerProvider.notifier)
            .clearRecovery();
        return;
      }
    }
    if (mounted) {
      setState(() {
        _verifying = false;
        _verificationTimedOut = true;
      });
    }
  }

  Future<void> _retryUpgrade() async {
    if (_retrying) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text(SettingsText.t(context, 'retryConfirmTitle')),
        content: Text(
          SettingsText.format(context, 'retryConfirmContent', {
            'version': widget.targetVersion,
          }),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext, false),
            child: Text(SettingsText.t(context, 'cancel')),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(dialogContext, true),
            child: Text(SettingsText.t(context, 'confirmRetry')),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    setState(() => _retrying = true);
    try {
      final version = await widget.ref
          .read(settingsControllerProvider.notifier)
          .loadVersion();
      if (!mounted) return;
      if (version == null) {
        _showError(SettingsText.t(context, 'versionUnavailable'));
        return;
      }
      if (version.versionName == widget.targetVersion) {
        setState(() => _verified = true);
        await widget.ref
            .read(settingsControllerProvider.notifier)
            .clearRecovery();
        return;
      }
      final logName = await widget.ref
          .read(settingsControllerProvider.notifier)
          .startUpgrade(
            currentVersion: version.versionName,
            targetVersion: widget.targetVersion,
          );
      if (!mounted) return;
      _timer?.cancel();
      setState(() {
        _logName = logName;
        _logs.clear();
        _loading = true;
        _finished = false;
        _logFailed = false;
        _verified = false;
        _verificationTimedOut = false;
        _progress = 0;
        _phase = 'preparing';
      });
      _startPolling();
    } catch (_) {
      if (mounted) _showError(SettingsText.t(context, 'retryFailed'));
    } finally {
      if (mounted) setState(() => _retrying = false);
    }
  }

  Future<void> _clearTask() async {
    try {
      await widget.ref
          .read(settingsControllerProvider.notifier)
          .clearRecovery();
      if (mounted) Navigator.pop(context);
    } catch (_) {
      if (mounted) _showError(SettingsText.t(context, 'clearFailed'));
    }
  }

  void _showError(String message) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(message), backgroundColor: AppTheme.error),
    );
  }

  String _statusText() {
    if (_logFailed) return SettingsText.t(context, 'upgradeFailed');
    if (_verified) return SettingsText.t(context, 'upgradeVerified');
    if (_verificationTimedOut) {
      return SettingsText.t(context, 'verifyTimeout');
    }
    if (_finished) return SettingsText.t(context, 'upgradeFinished');
    return switch (_phase) {
      'preparing' => SettingsText.t(context, 'upgradePreparing'),
      'downloading' => SettingsText.t(context, 'upgradeDownloading'),
      'verifying' => SettingsText.t(context, 'upgradeVerifying'),
      'installing' => SettingsText.t(context, 'upgradeInstalling'),
      'restarting' => SettingsText.t(context, 'upgradeRestarting'),
      _ => SettingsText.t(context, 'upgradeRunning'),
    };
  }

  @override
  Widget build(BuildContext context) {
    final showFailureActions = _logFailed || _verificationTimedOut;
    return SafeArea(
      child: SizedBox(
        height: MediaQuery.sizeOf(context).height * 0.72,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 0, 20, 20),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                SettingsText.t(context, 'upgradeTitle'),
                style: const TextStyle(
                  fontSize: 20,
                  fontWeight: FontWeight.w800,
                ),
              ),
              const SizedBox(height: 8),
              Text(
                _statusText(),
                style: const TextStyle(color: AppTheme.textSecondary),
              ),
              const SizedBox(height: 14),
              LinearProgressIndicator(
                value: _finished ? 1 : (_progress > 0 ? _progress / 100 : null),
                color: _logFailed ? AppTheme.error : null,
              ),
              const SizedBox(height: 14),
              Expanded(
                child: DecoratedBox(
                  decoration: BoxDecoration(
                    color: const Color(0xFF0B1020),
                    borderRadius: BorderRadius.circular(16),
                  ),
                  child: ListView.builder(
                    padding: const EdgeInsets.all(14),
                    itemCount: _logs.length,
                    itemBuilder: (_, index) => Padding(
                      padding: const EdgeInsets.only(bottom: 5),
                      child: Text(
                        _logs[index],
                        style: const TextStyle(
                          color: Color(0xFFE2E8F0),
                          fontSize: 12,
                          height: 1.35,
                        ),
                      ),
                    ),
                  ),
                ),
              ),
              if (_loading && _logs.isEmpty) ...[
                const SizedBox(height: 12),
                Text(
                  SettingsText.t(context, 'waitingLogs'),
                  style: const TextStyle(color: AppTheme.textSecondary),
                ),
              ],
              if (showFailureActions) ...[
                const SizedBox(height: 14),
                Row(
                  children: [
                    TextButton(
                      onPressed: _retrying ? null : _clearTask,
                      child: Text(SettingsText.t(context, 'clearTask')),
                    ),
                    const Spacer(),
                    if (_verificationTimedOut)
                      OutlinedButton(
                        onPressed: _verifying ? null : _verifyVersion,
                        child: Text(SettingsText.t(context, 'verifyAgain')),
                      ),
                    if (_verificationTimedOut) const SizedBox(width: 10),
                    FilledButton(
                      onPressed: _retrying ? null : _retryUpgrade,
                      child: Text(SettingsText.t(context, 'retryUpgrade')),
                    ),
                  ],
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}
