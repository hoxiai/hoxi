import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/theme/app_theme.dart';
import '../../models/code_session_recovery.dart';
import '../controllers/ai_workspace_controller.dart';
import '../controllers/code_session_recovery_controller.dart';
import '../widgets/code_recovery_cards.dart';
import 'code_execution_run_screen.dart';
import '../../../../core/l10n/l10n.dart';

class CodeSessionRecoveryScreen extends ConsumerStatefulWidget {
  const CodeSessionRecoveryScreen({
    super.key,
    required this.sessionId,
    required this.sessionTitle,
  });

  final int sessionId;
  final String sessionTitle;

  @override
  ConsumerState<CodeSessionRecoveryScreen> createState() =>
      _CodeSessionRecoveryScreenState();
}

class _CodeSessionRecoveryScreenState
    extends ConsumerState<CodeSessionRecoveryScreen> {
  late final CodeSessionRecoveryController _controller;

  @override
  void initState() {
    super.initState();
    _controller = CodeSessionRecoveryController(
      repository: ref.read(aiWorkspaceRepositoryProvider),
      sessionId: widget.sessionId,
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
            title: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(context.l10n.codeWsRecoveryTitle),
                Text(
                  widget.sessionTitle,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style:  TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 11,
                  ),
                ),
              ],
            ),
            actions: [
              IconButton(
                tooltip: context.l10n.codeWsActionRefresh,
                onPressed: state.isLoading ? null : _controller.load,
                icon: const Icon(Icons.refresh_rounded),
              ),
            ],
          ),
          body: RefreshIndicator(
            onRefresh: _controller.load,
            child: _buildBody(context, state),
          ),
        );
      },
    );
  }

  Widget _buildBody(BuildContext context, CodeSessionRecoveryState state) {
    if (state.isLoading && state.messages.isEmpty && state.runs.isEmpty) {
      return ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        children: const [
          SizedBox(height: 180),
          Center(child: CircularProgressIndicator()),
        ],
      );
    }
    if (state.errorMessage != null &&
        state.messages.isEmpty &&
        state.runs.isEmpty &&
        state.initialization == null) {
      return ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.all(24),
        children: [
          const SizedBox(height: 72),
          const Icon(Icons.history_toggle_off_rounded, size: 48),
          const SizedBox(height: 12),
          Text(
            context.l10n.codeWsRecoveryLoadFailed,
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

    return ListView(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.all(16),
      children: [
        if (state.isLoading) const LinearProgressIndicator(minHeight: 2),
        if (state.errorMessage != null) ...[
          _InlineError(message: state.errorMessage!),
          const SizedBox(height: 12),
        ],
        if (state.initialization != null) ...[
          CodeInitializationCard(
            initialization: state.initialization!,
            isRetrying: state.isRetryingInitialization,
            onRetry: _retryInitialization,
            isAborting: state.isAbortingInitialization,
            onAbort: _abortInitialization,
          ),
          const SizedBox(height: 16),
        ],
        CodeExecutionRunsCard(
          runs: state.runs,
          total: state.totalRuns,
          isLoadingMore: state.isLoadingMore,
          retryingInstructionId: state.retryingInstructionId,
          retriedInstructionIds: state.retriedInstructionIds,
          onRetry: _retryInstruction,
          onOpenDetail: _openRunDetail,
          onLoadMore: _controller.loadMore,
        ),
        const SizedBox(height: 16),
        CodeHistoryMessagesCard(messages: state.messages),
      ],
    );
  }

  Future<void> _retryInstruction(CodeExecutionRun run) async {
    final confirmed = await _confirm(
      title: context.l10n.codeWsRecoveryRetryInstruction,
      content: context.l10n.codeWsRecoveryRetryInstructionHint,
    );
    if (!confirmed || !mounted) return;
    final success = await _controller.retryInstruction(run);
    if (!mounted) return;
    if (success) {
      await ref
          .read(aiWorkspaceControllerProvider.notifier)
          .refreshCurrentSession(showLoading: true);
    }
    _showResult(success);
  }

  void _openRunDetail(CodeExecutionRun run) {
    Navigator.of(context).push(
      MaterialPageRoute(builder: (_) => CodeExecutionRunScreen(runId: run.id)),
    );
  }

  Future<void> _retryInitialization() async {
    final confirmed = await _confirm(
      title: context.l10n.codeWsRecoveryRetryInitialization,
      content: context.l10n.codeWsRecoveryRetryInitializationHint,
    );
    if (!confirmed || !mounted) return;
    final success = await _controller.retryInitialization();
    if (!mounted) return;
    _showResult(success);
  }

  Future<void> _abortInitialization() async {
    final confirmed = await _confirm(
      title: context.l10n.codeWsRecoveryAbortInitialization,
      content: context.l10n.codeWsRecoveryAbortInitializationHint,
    );
    if (!confirmed || !mounted) return;
    final success = await _controller.abortInitialization();
    if (!mounted) return;
    _showResult(success);
  }

  Future<bool> _confirm({
    required String title,
    required String content,
  }) async {
    return await showDialog<bool>(
          context: context,
          builder: (dialogContext) => AlertDialog(
            title: Text(title),
            content: Text(content),
            actions: [
              TextButton(
                onPressed: () => Navigator.of(dialogContext).pop(false),
                child: Text(
                  dialogContext.l10n.codeWsRecoveryCancel,
                ),
              ),
              FilledButton(
                onPressed: () => Navigator.of(dialogContext).pop(true),
                child: Text(
                  dialogContext.l10n.codeWsRecoveryConfirm,
                ),
              ),
            ],
          ),
        ) ??
        false;
  }

  void _showResult(bool success) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(
          success
              ? context.l10n.codeWsRecoveryActionSuccess
              : context.l10n.codeWsRecoveryActionFailed,
        ),
      ),
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
