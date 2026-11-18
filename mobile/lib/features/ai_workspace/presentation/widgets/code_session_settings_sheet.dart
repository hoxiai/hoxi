import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/navigation/navigation_guard.dart';
import '../../../../core/theme/app_theme.dart';
import '../../models/ai_dev_session.dart';
import '../../models/code_model_catalog.dart';
import 'code_approval_reject_notice.dart';
import 'code_model_selector_sheet.dart';
import '../controllers/ai_workspace_controller.dart';
import '../screens/code_quality_checks_screen.dart';
import '../../../../core/l10n/l10n.dart';

class CodeSessionSettingsSheet extends ConsumerStatefulWidget {
  const CodeSessionSettingsSheet({super.key});

  @override
  ConsumerState<CodeSessionSettingsSheet> createState() =>
      _CodeSessionSettingsSheetState();
}

class _CodeSessionSettingsSheetState
    extends ConsumerState<CodeSessionSettingsSheet> {
  final _titleController = TextEditingController();
  final _modelController = TextEditingController();
  final _effortController = TextEditingController();
  String? _selectedExecutorId;
  String? _policy;
  bool _initialized = false;

  @override
  void dispose() {
    _titleController.dispose();
    _modelController.dispose();
    _effortController.dispose();
    super.dispose();
  }

  void _initialize(AiDevSession session) {
    if (_initialized) return;
    _initialized = true;
    final state = ref.read(aiWorkspaceControllerProvider);
    _titleController.text = resolveSessionDisplayTitle(
      session: session,
      taskTitle: state.currentTask?.title,
      fallback: session.title,
    );
    _modelController.text = session.providerModel;
    _effortController.text = session.reasoningEffort;
    _selectedExecutorId = session.agentName;
    _policy = session.approvalPolicy;
    if (state.executors.isEmpty) {
      ref.read(aiWorkspaceControllerProvider.notifier).ensureExecutorsLoaded();
    }
  }

  Future<void> _save(AiDevSession session) async {
    final controller = ref.read(aiWorkspaceControllerProvider.notifier);
    final currentTaskTitle =
        ref.read(aiWorkspaceControllerProvider).currentTask?.title;
    final currentDisplayTitle = resolveSessionDisplayTitle(
      session: session,
      taskTitle: currentTaskTitle,
      fallback: session.title,
    ).trim();
    final title = _titleController.text.trim();
    final model = _modelController.text.trim();
    final effort = _effortController.text.trim();
    var success = true;
    if (title.isNotEmpty &&
        (title != session.title.trim() || title != currentDisplayTitle)) {
      success = await controller.updateSessionTitle(title) && success;
    }
    if (_policy != null && _policy != session.approvalPolicy) {
      success = await controller.updateApprovalPolicy(_policy!) && success;
    }
    final execChanged =
        _selectedExecutorId != null && _selectedExecutorId != session.agentName;
    if (model != session.providerModel ||
        effort != session.reasoningEffort ||
        execChanged) {
      final modelUpdated = await controller.updateSessionModel(
        model: (model == session.providerModel && !execChanged) ? null : model,
        executorId: _selectedExecutorId,
        reasoningEffort: effort == session.reasoningEffort ? null : effort,
      );
      if (modelUpdated && model.isNotEmpty) {
        unawaited(
          CodeModelCatalog.saveLastSelection(
            model: model,
            executorId: _selectedExecutorId ?? session.agentName,
          ),
        );
      }
      success = modelUpdated && success;
    }
    if (!mounted) return;
    if (success) {
      Navigator.of(context).pop();
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(context.l10n.codeWsSessionSettingsSaved),
        ),
      );
    } else {
      final err = ref.read(aiWorkspaceControllerProvider).errorMessage;
      if (err != null && err.trim().isNotEmpty) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(err),
            backgroundColor: AppTheme.error,
          ),
        );
      }
    }
  }

  bool _extractingMemory = false;

  Future<void> _extractMemory(AiDevSession session) async {
    setState(() => _extractingMemory = true);
    try {
      final repo = ref.read(aiWorkspaceRepositoryProvider);
      await repo.extractSessionMemory(session.id);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(context.l10n.codeWsMemoryExtractSuccess),
          backgroundColor: const Color(0xFF10B981),
        ),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            '${context.l10n.codeWsMemoryExtractFailed}: $e',
          ),
          backgroundColor: AppTheme.error,
        ),
      );
    } finally {
      if (mounted) setState(() => _extractingMemory = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final session = ref.watch(aiWorkspaceControllerProvider).currentSession;
    if (session == null) return const SizedBox.shrink();
    _initialize(session);
    final state = ref.watch(aiWorkspaceControllerProvider);
    final executor = state.executors.cast<CodeExecutor?>().firstWhere(
      (item) => item?.id == session.agentName,
      orElse: () => null,
    );
    final policies = <String>{
      ...?executor?.approvalPolicies,
      session.approvalPolicy,
    }.toList();
    final currentModelValue = _modelController.text.trim();
    return SafeArea(
      child: Padding(
        padding: EdgeInsets.fromLTRB(
          20,
          8,
          20,
          MediaQuery.viewInsetsOf(context).bottom + 20,
        ),
        child: SingleChildScrollView(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                context.l10n.codeWsSessionSettingsTitle,
                style: const TextStyle(
                  fontSize: 18,
                  fontWeight: FontWeight.bold,
                ),
              ),
              const SizedBox(height: 16),
              TextField(
                controller: _titleController,
                textInputAction: TextInputAction.done,
                onSubmitted: (_) {
                  if (!state.isActionLoading) _save(session);
                },
                decoration: InputDecoration(
                  labelText: context.l10n.codeWsSessionSettingsName,
                  border: const OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 14),
              DropdownButtonFormField<String>(
                initialValue: _policy,
                decoration: InputDecoration(
                  labelText: context.l10n.codeWsSessionSettingsApproval,
                  border: const OutlineInputBorder(),
                ),
                items: [
                  for (final policy in policies)
                    DropdownMenuItem(
                      value: policy,
                      child: Text(
                        switch (policy) {
                          'manual' =>
                            context.l10n.codeWsSessionSettingsPolicyManual,
                          'safe_auto' =>
                            context.l10n.codeWsSessionSettingsPolicySafeAuto,
                          'full_auto' =>
                            context.l10n.codeWsSessionSettingsPolicyFullAuto,
                          _ => policy,
                        },
                      ),
                    ),
                ],
                onChanged: (value) => setState(() => _policy = value),
              ),
              CodeApprovalRejectNotice(executor: executor, policy: _policy),
              const SizedBox(height: 14),
              CodeModelSelectorField(
                executors: state.executors,
                selectedExecutor: executor,
                selectedModel: currentModelValue,
                selectedEffort: _effortController.text.trim(),
                executorIdHint: session.agentName,
                allowCrossExecutorSelection: true,
                onSelected: (selection) {
                  setState(() {
                    _modelController.text = selection.model;
                    if (selection.executorId != null &&
                        selection.executorId!.isNotEmpty) {
                      _selectedExecutorId = selection.executorId;
                    }
                    if (selection.reasoningEffort != null) {
                      _effortController.text = selection.reasoningEffort!;
                    }
                  });
                },
              ),
              const SizedBox(height: 20),
              SizedBox(
                width: double.infinity,
                child: FilledButton.icon(
                  onPressed: state.isActionLoading ? null : () => _save(session),
                  icon: state.isActionLoading
                      ? const SizedBox.square(
                          dimension: 18,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.save_outlined),
                  label: Text(
                    context.l10n.codeWsSessionSettingsSave,
                  ),
                ),
              ),
              const SizedBox(height: 16),
              Divider(color: AppTheme.border),
              const SizedBox(height: 8),
              OutlinedButton.icon(
                onPressed: () {
                  if (!AppNavigationGuard.popIfCurrent(context)) return;
                  AppNavigationGuard.push(
                    context,
                    MaterialPageRoute(
                      builder: (_) => CodeQualityChecksScreen(
                        sessionId: session.id,
                        sessionTitle: session.title.isEmpty
                            ? context.l10n.codeWsChatSessionFallback(session.id)
                            : session.title,
                      ),
                    ),
                  );
                },
                icon: const Icon(Icons.verified_outlined),
                label: Text(context.l10n.codeWsQualityTitle),
                style: OutlinedButton.styleFrom(
                  minimumSize: const Size.fromHeight(42),
                ),
              ),
              const SizedBox(height: 10),
              OutlinedButton.icon(
                onPressed: _extractingMemory
                    ? null
                    : () => _extractMemory(session),
                icon: _extractingMemory
                    ? const SizedBox.square(
                        dimension: 16,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.psychology_outlined),
                label: Text(
                  _extractingMemory
                      ? context.l10n.codeWsMemoryExtracting
                      : context.l10n.codeWsMemoryExtract,
                ),
                style: OutlinedButton.styleFrom(
                  minimumSize: const Size.fromHeight(42),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
