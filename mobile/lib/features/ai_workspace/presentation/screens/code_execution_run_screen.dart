import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/theme/app_theme.dart';
import '../../../../shared/widgets/panel/panel_card.dart';
import '../../models/code_execution_run_detail.dart';
import '../controllers/ai_workspace_controller.dart';
import '../controllers/code_execution_run_controller.dart';
import '../../../../core/l10n/l10n.dart';

class CodeExecutionRunScreen extends ConsumerStatefulWidget {
  const CodeExecutionRunScreen({super.key, required this.runId});

  final int runId;

  @override
  ConsumerState<CodeExecutionRunScreen> createState() =>
      _CodeExecutionRunScreenState();
}

class _CodeExecutionRunScreenState
    extends ConsumerState<CodeExecutionRunScreen> {
  late final CodeExecutionRunController _controller;

  @override
  void initState() {
    super.initState();
    _controller = CodeExecutionRunController(
      repository: ref.read(aiWorkspaceRepositoryProvider),
      runId: widget.runId,
    )..load();
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: _controller,
      builder: (context, _) {
        final state = _controller.state;
        return Scaffold(
          appBar: AppBar(
            title: Text(
              context.l10n.codeWsRunDetailTitle(widget.runId),
            ),
            actions: [
              IconButton(
                tooltip: context.l10n.codeWsRunDetailCopy,
                onPressed: state.run == null ? null : () => _copy(state.run!),
                icon: const Icon(Icons.content_copy_rounded),
              ),
              IconButton(
                tooltip: context.l10n.codeWsActionRefresh,
                onPressed: state.isLoading ? null : _controller.load,
                icon: const Icon(Icons.refresh_rounded),
              ),
            ],
          ),
          body: RefreshIndicator(
            onRefresh: _controller.load,
            child: _buildBody(state),
          ),
        );
      },
    );
  }

  Widget _buildBody(CodeExecutionRunState state) {
    if (state.isLoading && state.run == null) {
      return ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        children: const [
          SizedBox(height: 180),
          Center(child: CircularProgressIndicator()),
        ],
      );
    }
    if (state.errorMessage != null && state.run == null) {
      return ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.all(24),
        children: [
          const SizedBox(height: 72),
          const Icon(Icons.error_outline_rounded, size: 48),
          const SizedBox(height: 12),
          Text(
            context.l10n.codeWsRunDetailLoadFailed,
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 8),
          Text(
            state.errorMessage!,
            textAlign: TextAlign.center,
            style:  TextStyle(color: AppTheme.textSecondary),
          ),
          const SizedBox(height: 14),
          Center(
            child: OutlinedButton(
              onPressed: _controller.load,
              child: Text(context.l10n.codeWsActionRetry),
            ),
          ),
        ],
      );
    }
    final run = state.run;
    if (run == null) {
      return ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        children: [
          const SizedBox(height: 120),
          Center(child: Text(context.l10n.codeWsRunDetailEmpty)),
        ],
      );
    }
    return ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.all(16),
      children: [
        if (state.isLoading) const LinearProgressIndicator(minHeight: 2),
        if (state.errorMessage != null) ...[
          _InlineError(message: state.errorMessage!),
          const SizedBox(height: 12),
        ],
        CodeExecutionRunOverview(run: run),
        const SizedBox(height: 16),
        _ContentCard(
          title: context.l10n.codeWsRunDetailPrompt,
          content: run.prompt,
          emptyText: context.l10n.codeWsRunDetailNoPrompt,
        ),
        const SizedBox(height: 16),
        if (run.errorMessage.isNotEmpty) ...[
          _ContentCard(
            title: context.l10n.codeWsRunDetailError,
            content: run.errorMessage,
            error: true,
          ),
          const SizedBox(height: 16),
        ],
        _ContentCard(
          title: context.l10n.codeWsRunDetailOutput,
          content: run.output,
          emptyText: context.l10n.codeWsRunDetailNoOutput,
        ),
        const SizedBox(height: 16),
        _ContentCard(
          title: context.l10n.codeWsRunDetailRawOutput,
          content: run.rawOutput,
          emptyText: context.l10n.codeWsRunDetailNoRawOutput,
          code: true,
        ),
      ],
    );
  }

  Future<void> _copy(CodeExecutionRunDetail run) async {
    await Clipboard.setData(ClipboardData(text: run.diagnosticText));
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(context.l10n.codeWsRunDetailCopied)),
    );
  }
}

class CodeExecutionRunOverview extends StatelessWidget {
  const CodeExecutionRunOverview({super.key, required this.run});

  final CodeExecutionRunDetail run;

  @override
  Widget build(BuildContext context) {
    return PanelCard(
      title: Text(context.l10n.codeWsRunDetailOverview),
      child: Wrap(
        spacing: 10,
        runSpacing: 10,
        children: [
          _Meta(
            label: context.l10n.codeWsRunDetailStatus,
            value: _statusLabel(context, run.status),
          ),
          _Meta(
            label: context.l10n.codeWsRunDetailExecutor,
            value: run.executorId,
          ),
          if (run.model.isNotEmpty)
            _Meta(
              label: context.l10n.codeWsRunDetailModel,
              value: run.model,
            ),
          _Meta(
            label: context.l10n.codeWsRunDetailExitCode,
            value: '${run.exitCode}',
          ),
          _Meta(
            label: context.l10n.codeWsRunDetailDurationLabel,
            value: '${run.durationMs} ms',
          ),
          _Meta(
            label: context.l10n.codeWsRunDetailSession,
            value: '#${run.sessionId}',
          ),
          if (run.taskId > 0)
            _Meta(
              label: context.l10n.codeWsRunDetailTask,
              value: '#${run.taskId}',
            ),
          if (run.instructionId > 0)
            _Meta(
              label: context.l10n.codeWsRunDetailInstruction,
              value: '#${run.instructionId}',
            ),
          if (run.startedAt != null)
            _Meta(
              label: context.l10n.codeWsRunDetailStartedAt,
              value: _formatTime(run.startedAt!),
            ),
          if (run.completedAt != null)
            _Meta(
              label: context.l10n.codeWsRunDetailCompletedAt,
              value: _formatTime(run.completedAt!),
            ),
          if (run.hasTokenUsage) ...[
            _Meta(
              label: context.l10n.codeWsRunDetailInputTokens,
              value: '${run.inputTokens}',
            ),
            _Meta(
              label: context.l10n.codeWsRunDetailOutputTokens,
              value: '${run.outputTokens}',
            ),
            _Meta(
              label: context.l10n.codeWsRunDetailCachedTokens,
              value: '${run.cachedInputTokens}',
            ),
            _Meta(
              label: context.l10n.codeWsRunDetailReasoningTokens,
              value: '${run.reasoningTokens}',
            ),
            _Meta(
              label: context.l10n.codeWsRunDetailTotalTokens,
              value: '${run.totalTokens}',
            ),
          ],
        ],
      ),
    );
  }
}

class _ContentCard extends StatelessWidget {
  const _ContentCard({
    required this.title,
    required this.content,
    this.emptyText,
    this.error = false,
    this.code = false,
  });

  final String title;
  final String content;
  final String? emptyText;
  final bool error;
  final bool code;

  @override
  Widget build(BuildContext context) {
    final value = content.isEmpty && emptyText != null ? emptyText! : content;
    return PanelCard(
      title: Text(title),
      child: SelectableText(
        value,
        style: TextStyle(
          color: error ? AppTheme.error : AppTheme.textSecondary,
          height: 1.5,
          fontFamily: code ? 'monospace' : null,
          fontSize: code ? 12 : null,
        ),
      ),
    );
  }
}

class _Meta extends StatelessWidget {
  const _Meta({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
      decoration: BoxDecoration(
        color: AppTheme.surfaceMuted,
        borderRadius: BorderRadius.circular(10),
      ),
      child: Text('$label: ${value.isEmpty ? '-' : value}'),
    );
  }
}

class _InlineError extends StatelessWidget {
  const _InlineError({required this.message});

  final String message;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppTheme.errorSoft,
        borderRadius: BorderRadius.circular(12),
      ),
      child: Text(message, style: const TextStyle(color: AppTheme.error)),
    );
  }
}

String _statusLabel(BuildContext context, String status) {
  final l10n = context.l10n;
  return switch (status) {
    'completed' => l10n.codeWsRecoveryStatusCompleted,
    'failed' => l10n.codeWsRecoveryStatusFailed,
    'cancelled' || 'stopped' => l10n.codeWsRecoveryStatusStopped,
    'running' => l10n.codeWsRecoveryStatusRunning,
    'awaiting_approval' ||
    'pending_approval' => l10n.codeWsRecoveryStatusAwaitingApproval,
    'queued' => l10n.codeWsRecoveryStatusQueued,
    _ => l10n.codeWsRecoveryStatusUnknown,
  };
}

String _formatTime(DateTime value) {
  final local = value.toLocal();
  String two(int number) => number.toString().padLeft(2, '0');
  return '${local.year}-${two(local.month)}-${two(local.day)} '
      '${two(local.hour)}:${two(local.minute)}:${two(local.second)}';
}
