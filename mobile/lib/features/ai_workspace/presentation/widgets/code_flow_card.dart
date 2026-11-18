import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/theme/app_theme.dart';
import '../../models/code_session_flow.dart';
import '../controllers/ai_workspace_controller.dart';

/// 交付预览：Code 交付完成后在手机上看构建进展、打开预览、发布生产或切回旧版本。
/// 自门控——项目没有可接收 Code 交付的交付流程、也没有可生成的预览时不渲染。
class CodeFlowCard extends ConsumerStatefulWidget {
  const CodeFlowCard({
    super.key,
    required this.sessionId,
    required this.refreshKey,
  });

  final int sessionId;

  /// 交付状态变化时变化；交付完成那一刻需要重新拉取，自动开始的执行会在这时出现。
  final String refreshKey;

  @override
  ConsumerState<CodeFlowCard> createState() => _CodeFlowCardState();
}

class _CodeFlowCardState extends ConsumerState<CodeFlowCard> {
  CodeSessionFlow? _flow;
  bool _busy = false;
  Timer? _poll;
  int _request = 0;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void didUpdateWidget(covariant CodeFlowCard oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.sessionId != widget.sessionId) {
      _flow = null;
    }
    if (oldWidget.sessionId != widget.sessionId ||
        oldWidget.refreshKey != widget.refreshKey) {
      _load();
    }
  }

  @override
  void dispose() {
    _poll?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    final request = ++_request;
    _poll?.cancel();
    try {
      final flow = await ref
          .read(aiWorkspaceRepositoryProvider)
          .getSessionFlow(widget.sessionId);
      if (!mounted || request != _request) return;
      setState(() => _flow = flow);
    } catch (_) {
      if (!mounted || request != _request) return;
    }
    if (_flow?.run?.isActive == true || _flow?.autoStartPending == true) {
      _poll = Timer(const Duration(seconds: 5), _load);
    }
  }

  Future<void> _run(Future<void> Function() action, String successText) async {
    setState(() => _busy = true);
    try {
      await action();
      if (mounted) _snack(successText);
      await _load();
    } catch (error) {
      if (mounted) _snack(error.toString());
      // 操作失败多半是状态已变（例如预览已被自动生成），重新拉取，不停留在过期的按钮上
      await _load();
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _snack(String text) {
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(text)));
  }

  Future<void> _openPreview(String url) async {
    final uri = Uri.tryParse(url);
    final opened =
        uri != null &&
        await launchUrl(uri, mode: LaunchMode.externalApplication);
    if (!opened && mounted) _snack(context.l10n.codeFlowOpenFailed);
  }

  Future<void> _deploy(CodeFlowRun run, CodeFlowEnvironment environment) async {
    final version = run.version;
    final envLabel = _environmentName(environment.name);
    final website = environment.websiteName;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) {
        final l10n = dialogContext.l10n;
        return AlertDialog(
          title: Text(l10n.codeFlowConfirmTitle(envLabel)),
          content: Text(
            environment.isProduction
                ? l10n.codeFlowConfirmProduction(version, website)
                : l10n.codeFlowConfirmPreview(version, envLabel, website),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(dialogContext).pop(false),
              child: Text(l10n.codeFlowCancel),
            ),
            FilledButton(
              onPressed: () => Navigator.of(dialogContext).pop(true),
              child: Text(l10n.codeFlowConfirm),
            ),
          ],
        );
      },
    );
    if (confirmed != true || !mounted) return;
    await _run(
      () => ref
          .read(aiWorkspaceRepositoryProvider)
          .deployFlowRun(run.id, environment.id),
      context.l10n.codeFlowDeployQueued,
    );
  }

  @override
  Widget build(BuildContext context) {
    final flow = _flow;
    if (flow == null || !flow.visible) return const SizedBox.shrink();
    final run = flow.run;
    final accent = _accent(run);
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.fromLTRB(16, 0, 16, 12),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(18),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(Icons.public_rounded, color: accent, size: 19),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  [
                    context.l10n.codeFlowTitle,
                    flow.flowName,
                  ].join(' · '),
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
              if (run != null) _Badge(text: _statusText(run), color: accent),
            ],
          ),
          if (run != null) ..._runDetails(run),
          if (flow.autoStartPending) ...[
              const SizedBox(height: 10),
              Row(
                children: [
                  const SizedBox.square(
                    dimension: 14,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      context.l10n.codeFlowAutoStartPending,
                      style: TextStyle(
                        color: AppTheme.textSecondary,
                        height: 1.45,
                      ),
                    ),
                  ),
                ],
              ),
            ] else if (flow.canStart) ...[
              const SizedBox(height: 10),
              Text(
                flow.autoStart
                    ? context.l10n.codeFlowAutoStartHint
                    : context.l10n.codeFlowStartHint,
                style: TextStyle(color: AppTheme.textSecondary, height: 1.45),
              ),
              const SizedBox(height: 10),
              SizedBox(
                width: double.infinity,
                child: FilledButton.icon(
                  onPressed: _busy
                      ? null
                      : () => _run(
                          () => ref
                              .read(aiWorkspaceRepositoryProvider)
                              .startSessionFlowPreview(widget.sessionId),
                          context.l10n.codeFlowStarted,
                        ),
                  icon: const Icon(Icons.play_arrow_rounded),
                  label: Text(context.l10n.codeFlowStart),
                ),
              ),
            ],
        ],
      ),
    );
  }

  List<Widget> _runDetails(CodeFlowRun run) {
    return [
      const SizedBox(height: 8),
      Text(
        [
          context.l10n.codeFlowVersion(run.version),
          if (run.isActive && _stageText(run.currentStage) != null)
            _stageText(run.currentStage)!,
        ].join(' · '),
        style: TextStyle(color: AppTheme.textSecondary),
      ),
      if (run.status == 'failed' && run.errorSummary.isNotEmpty) ...[
        const SizedBox(height: 6),
        Text(
          run.errorSummary,
          maxLines: 3,
          overflow: TextOverflow.ellipsis,
          style: const TextStyle(color: AppTheme.error, fontSize: 12),
        ),
      ],
      if (run.previewUrl.isNotEmpty) ...[
        const SizedBox(height: 10),
        OutlinedButton.icon(
          onPressed: () => _openPreview(run.previewUrl),
          icon: const Icon(Icons.open_in_new_rounded, size: 17),
          label: Text(context.l10n.codeFlowOpenPreview),
        ),
      ],
      if (run.deployBlocked.isNotEmpty) ...[
        const SizedBox(height: 8),
        Text(
          _blockedText(run.deployBlocked),
          style: const TextStyle(
            color: AppTheme.warning,
            fontSize: 12,
            height: 1.45,
          ),
        ),
      ],
      for (final environment in run.environments)
        _environmentRow(run, environment),
    ];
  }

  Widget _environmentRow(CodeFlowRun run, CodeFlowEnvironment environment) {
    final deployed = environment.state == 'deployed';
    final stateText = deployed
        ? (environment.current
              ? context.l10n.codeFlowStateCurrent
              : context.l10n.codeFlowStateReplaced)
        : _stateText(environment.state);
    final actionText = !deployed
        ? (environment.isProduction
              ? context.l10n.codeFlowActionRelease
              : context.l10n.codeFlowActionDeploy)
        : (environment.current
              ? context.l10n.codeFlowActionRedeploy
              : context.l10n.codeFlowActionSwitchBack);
    final enabled =
        !_busy &&
        run.deployable &&
        !run.isActive &&
        environment.state != 'deploying';
    return Padding(
      padding: const EdgeInsets.only(top: 10),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  _environmentName(environment.name),
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                Text(
                  [
                    stateText,
                    environment.websiteName,
                  ].where((item) => item.isNotEmpty).join(' · '),
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(color: AppTheme.textSecondary, fontSize: 12),
                ),
              ],
            ),
          ),
          if (environment.isProduction && !environment.current)
            FilledButton(
              onPressed: enabled ? () => _deploy(run, environment) : null,
              child: Text(actionText),
            )
          else
            TextButton(
              onPressed: enabled ? () => _deploy(run, environment) : null,
              child: Text(actionText),
            ),
        ],
      ),
    );
  }

  String _environmentName(String name) => switch (name) {
    'preview' => context.l10n.codeFlowEnvironmentPreview,
    'production' => context.l10n.codeFlowEnvironmentProduction,
    _ => name,
  };

  String _stateText(String state) => switch (state) {
    'pending' => context.l10n.codeFlowStatePending,
    'deploying' => context.l10n.codeFlowStateDeploying,
    'failed' => context.l10n.codeFlowStateFailed,
    'current' => context.l10n.codeFlowStateCurrent,
    'replaced' => context.l10n.codeFlowStateReplaced,
    _ => state,
  };

  String _blockedText(String blocked) => switch (blocked) {
    'runner_target_stale' => context.l10n.codeFlowBlockedRunnerTargetStale,
    'runner_target_unavailable' =>
      context.l10n.codeFlowBlockedRunnerTargetUnavailable,
    'runner_fixed_port' => context.l10n.codeFlowBlockedRunnerFixedPort,
    _ => 'blocked.$blocked',
  };

  String? _stageText(String stage) => switch (stage) {
    'building' => context.l10n.codeFlowStageBuilding,
    'publishing' => context.l10n.codeFlowStagePublishing,
    'deploying' => context.l10n.codeFlowStageDeploying,
    _ => null,
  };

  String _statusText(CodeFlowRun run) => switch (run.status) {
    'queued' => context.l10n.codeFlowStatusQueued,
    'running' => context.l10n.codeFlowStatusRunning,
    'success' => context.l10n.codeFlowStatusSuccess,
    'failed' => context.l10n.codeFlowStatusFailed,
    'waiting_deployment' => context.l10n.codeFlowStatusWaitingDeployment,
    _ => run.status,
  };

  Color _accent(CodeFlowRun? run) {
    if (run == null) return Colors.lightBlueAccent;
    if (run.status == 'failed') return Colors.redAccent;
    if (run.status == 'success') return Colors.greenAccent;
    if (run.status == 'waiting_deployment') return Colors.amberAccent;
    return Colors.lightBlueAccent;
  }
}

class _Badge extends StatelessWidget {
  const _Badge({required this.text, required this.color});

  final String text;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.14),
        borderRadius: BorderRadius.circular(99),
      ),
      child: Text(text, style: TextStyle(color: color, fontSize: 12)),
    );
  }
}
