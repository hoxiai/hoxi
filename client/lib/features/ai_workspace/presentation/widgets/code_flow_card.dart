import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../models/code_session_flow.dart';
import '../code_flow_text.dart';
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
  String? _loadError;
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
      _loadError = null;
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
      setState(() {
        _flow = flow;
        _loadError = null;
      });
    } catch (error) {
      if (!mounted || request != _request) return;
      setState(() => _loadError = error.toString());
    }
    if (_flow?.run?.isActive == true || _flow?.autoStartPending == true) {
      _poll = Timer(const Duration(seconds: 5), _load);
    }
  }

  Future<void> _run(Future<void> Function() action, String successKey) async {
    setState(() => _busy = true);
    try {
      await action();
      if (mounted) _snack(CodeFlowText.t(context, successKey));
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
    if (!opened && mounted) _snack(CodeFlowText.t(context, 'openFailed'));
  }

  Future<void> _deploy(CodeFlowRun run, CodeFlowEnvironment environment) async {
    final params = {
      'version': run.version,
      'environment': CodeFlowText.t(context, 'environment.${environment.name}'),
      'website': environment.websiteName,
    };
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text(CodeFlowText.format(dialogContext, 'confirmTitle', params)),
        content: Text(
          CodeFlowText.format(
            dialogContext,
            environment.isProduction ? 'confirmProduction' : 'confirmPreview',
            params,
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: Text(CodeFlowText.t(dialogContext, 'cancel')),
          ),
          FilledButton(
            onPressed: () => Navigator.of(dialogContext).pop(true),
            child: Text(CodeFlowText.t(dialogContext, 'confirm')),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    await _run(
      () => ref
          .read(aiWorkspaceRepositoryProvider)
          .deployFlowRun(run.id, environment.id),
      'deployQueued',
    );
  }

  @override
  Widget build(BuildContext context) {
    final flow = _flow;
    if (flow == null && _loadError == null) return const SizedBox.shrink();
    if (flow != null && !flow.visible) return const SizedBox.shrink();
    final run = flow?.run;
    final accent = _accent(run);
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.fromLTRB(16, 0, 16, 12),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: const Color(0xFF111827),
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: accent.withValues(alpha: 0.24)),
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
                    CodeFlowText.t(context, 'title'),
                    if (flow != null) flow.flowName,
                  ].join(' · '),
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(
                    color: Colors.white,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
              if (run != null) _Badge(text: _statusText(run), color: accent),
            ],
          ),
          if (flow == null) ...[
            const SizedBox(height: 8),
            Text(
              _loadError ?? '',
              style: const TextStyle(color: Colors.redAccent),
            ),
            TextButton(
              onPressed: _load,
              child: const Icon(Icons.refresh_rounded),
            ),
          ] else ...[
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
                      CodeFlowText.t(context, 'autoStartPending'),
                      style: const TextStyle(
                        color: Colors.white70,
                        height: 1.45,
                      ),
                    ),
                  ),
                ],
              ),
            ] else if (flow.canStart) ...[
              const SizedBox(height: 10),
              Text(
                CodeFlowText.t(
                  context,
                  flow.autoStart ? 'autoStartHint' : 'startHint',
                ),
                style: const TextStyle(color: Colors.white70, height: 1.45),
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
                          'started',
                        ),
                  icon: const Icon(Icons.play_arrow_rounded),
                  label: Text(CodeFlowText.t(context, 'start')),
                ),
              ),
            ],
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
          CodeFlowText.format(context, 'version', {'version': run.version}),
          if (run.isActive && _hasText('stage.${run.currentStage}'))
            CodeFlowText.t(context, 'stage.${run.currentStage}'),
        ].join(' · '),
        style: const TextStyle(color: Colors.white70),
      ),
      if (run.status == 'failed' && run.errorSummary.isNotEmpty) ...[
        const SizedBox(height: 6),
        Text(
          run.errorSummary,
          maxLines: 3,
          overflow: TextOverflow.ellipsis,
          style: const TextStyle(color: Colors.redAccent, fontSize: 12),
        ),
      ],
      if (run.previewUrl.isNotEmpty) ...[
        const SizedBox(height: 10),
        OutlinedButton.icon(
          onPressed: () => _openPreview(run.previewUrl),
          icon: const Icon(Icons.open_in_new_rounded, size: 17),
          label: Text(CodeFlowText.t(context, 'openPreview')),
        ),
      ],
      if (run.deployBlocked.isNotEmpty) ...[
        const SizedBox(height: 8),
        Text(
          CodeFlowText.t(context, 'blocked.${run.deployBlocked}'),
          style: const TextStyle(
            color: Colors.amberAccent,
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
    final stateKey = deployed
        ? (environment.current ? 'state.current' : 'state.replaced')
        : 'state.${environment.state}';
    final actionKey = !deployed
        ? (environment.isProduction ? 'action.release' : 'action.deploy')
        : (environment.current ? 'action.redeploy' : 'action.switchBack');
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
                  CodeFlowText.t(context, 'environment.${environment.name}'),
                  style: const TextStyle(
                    color: Colors.white,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                Text(
                  [
                    CodeFlowText.t(context, stateKey),
                    environment.websiteName,
                  ].where((item) => item.isNotEmpty).join(' · '),
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(color: Colors.white54, fontSize: 12),
                ),
              ],
            ),
          ),
          if (environment.isProduction && !environment.current)
            FilledButton(
              onPressed: enabled ? () => _deploy(run, environment) : null,
              child: Text(CodeFlowText.t(context, actionKey)),
            )
          else
            TextButton(
              onPressed: enabled ? () => _deploy(run, environment) : null,
              child: Text(CodeFlowText.t(context, actionKey)),
            ),
        ],
      ),
    );
  }

  bool _hasText(String key) => CodeFlowText.t(context, key) != key;

  String _statusText(CodeFlowRun run) {
    final key = 'status.${run.status}';
    return _hasText(key) ? CodeFlowText.t(context, key) : run.status;
  }

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
