import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/theme/app_theme.dart';
import '../../models/ai_dev_session.dart';
import '../../models/code_model_catalog.dart';
import '../controllers/ai_workspace_controller.dart';
import '../widgets/code_approval_reject_notice.dart';
import '../widgets/code_model_selector_sheet.dart';
import '../widgets/code_project_selector_field.dart';
import '../widgets/code_worktree_capability_status.dart';

class CodeSessionSheet extends ConsumerStatefulWidget {
  const CodeSessionSheet({super.key});

  @override
  ConsumerState<CodeSessionSheet> createState() => _CodeSessionSheetState();
}

class _CodeSessionSheetState extends ConsumerState<CodeSessionSheet> {
  final _titleController = TextEditingController();
  String _selectedModel = '';
  int? _projectId;
  String? _executorId;
  String _approvalPolicy = 'safe_auto';
  String _reasoningEffort = '';
  bool _isolated = false;
  CodeWorktreeCapability? _worktreeCapability;
  bool _capabilityLoading = false;
  String? _capabilityError;
  int _capabilityRequest = 0;
  bool _userModifiedModel = false;
  String? _submitError;

  @override
  void initState() {
    super.initState();
    unawaited(_restoreRememberedModel());
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      final state = ref.read(aiWorkspaceControllerProvider);
      if (!state.isLoading &&
          (state.projects.isEmpty || state.executors.isEmpty)) {
        ref.read(aiWorkspaceControllerProvider.notifier).loadWorkspace();
      }
    });
  }

  Future<void> _restoreRememberedModel() async {
    final remembered = await CodeModelCatalog.loadLastSelection();
    if (!mounted || remembered == null || _userModifiedModel) return;
    setState(() {});
  }

  @override
  void dispose() {
    _capabilityRequest++;
    _titleController.dispose();
    super.dispose();
  }

  Future<void> _loadWorktreeCapability(int? projectId) async {
    final request = ++_capabilityRequest;
    if (projectId == null) {
      setState(() {
        _worktreeCapability = null;
        _capabilityError = null;
        _capabilityLoading = false;
      });
      return;
    }
    setState(() {
      _worktreeCapability = null;
      _capabilityError = null;
      _capabilityLoading = true;
    });
    try {
      final repository = ref.read(aiWorkspaceRepositoryProvider);
      final capability = await repository.getWorktreeCapability(projectId);
      if (!mounted || request != _capabilityRequest) return;
      setState(() {
        _worktreeCapability = capability;
        _capabilityLoading = false;
      });
    } catch (error) {
      if (!mounted || request != _capabilityRequest) return;
      setState(() {
        _capabilityError = error.toString();
        _capabilityLoading = false;
      });
    }
  }

  Future<void> _createSession(
    List<CodeProject> projects,
    List<CodeExecutor> executors,
  ) async {
    final projectId = _selectedProjectId(projects);
    final available = executors
        .where((item) => item.available && item.supportsAutomation)
        .toList();
    final state = ref.read(aiWorkspaceControllerProvider);
    final fallbackSession = CodeModelCatalog.latestSessionWithModel(
      state.currentSession,
      state.sessions,
    );
    final selectedExec = _selectedExecutor(
      executors,
      fallbackSession: fallbackSession,
    );
    final defaultModel = CodeModelCatalog.resolvePreferredModelForExecutors(
      available,
      preferredExecutor: selectedExec,
      fallbackSession: fallbackSession,
    );
    final effectiveModel = _effectiveModel(
      available,
      preferredExecutor: selectedExec,
      fallbackSession: fallbackSession,
    );
    final explicitExecutorId = _userModifiedModel
        ? _executorId
        : defaultModel?.executorId;
    final resolvedExecutorId = CodeModelCatalog.resolveRuntimeForModel(
      model: effectiveModel,
      explicitExecutorId: explicitExecutorId ?? defaultModel?.executorId,
      executors: available,
      fallbackRuntime: selectedExec?.id ?? 'opencode',
    );
    final executor = available.cast<CodeExecutor?>().firstWhere(
          (item) => item?.id == resolvedExecutorId,
          orElse: () => selectedExec,
        );
    if (projectId == null ||
        executor == null ||
        (_isolated && _worktreeCapability?.canCreate != true)) {
      return;
    }
    final policy = executor.approvalPolicies.contains(_approvalPolicy)
        ? _approvalPolicy
        : executor.approvalPolicies.firstOrNull ?? 'full_auto';
    if (_submitError != null) {
      setState(() => _submitError = null);
    }
    try {
      final controller = ref.read(aiWorkspaceControllerProvider.notifier);
      final session = await controller.createSession(
        projectId: projectId,
        executorId: resolvedExecutorId,
        approvalPolicy: policy,
        title: _titleController.text,
        isolated: _isolated,
        model: effectiveModel,
      );
      if (effectiveModel.isNotEmpty) {
        unawaited(
          CodeModelCatalog.saveLastSelection(
            model: effectiveModel,
            executorId: resolvedExecutorId,
          ),
        );
      }
      final effort = _reasoningEffort.trim();
      if (effort.isNotEmpty) {
        try {
          await controller.updateSessionModel(reasoningEffort: effort);
        } catch (_) {}
      }
      if (mounted) Navigator.of(context).pop(session);
    } catch (error) {
      if (!mounted) return;
      final message =
          ref.read(aiWorkspaceControllerProvider).errorMessage?.trim() ?? '';
      setState(() {
        _submitError = message.isNotEmpty ? message : '$error';
      });
    }
  }

  int? _selectedProjectId(List<CodeProject> projects) {
    if (projects.any((item) => item.id == _projectId)) return _projectId;
    return projects.firstOrNull?.id;
  }

  String _effectiveModel(
    List<CodeExecutor> availableExecutors, {
    AiDevSession? fallbackSession,
    CodeExecutor? preferredExecutor,
  }) {
    final explicit = _selectedModel.trim();
    if (_userModifiedModel && explicit.isNotEmpty) {
      return explicit;
    }
    final defaultModel = CodeModelCatalog.resolvePreferredModelForExecutors(
      availableExecutors,
      preferredExecutor: preferredExecutor,
      fallbackSession: fallbackSession,
    );
    if (defaultModel != null && defaultModel.value.trim().isNotEmpty) {
      return defaultModel.value.trim();
    }
    return explicit;
  }

  CodeExecutor? _selectedExecutor(
    List<CodeExecutor> executors, {
    AiDevSession? fallbackSession,
  }) {
    final available = executors
        .where((item) => item.available && item.supportsAutomation)
        .toList();
    if (_userModifiedModel && _executorId != null && _executorId!.isNotEmpty) {
      final matched = available.cast<CodeExecutor?>().firstWhere(
        (item) => item?.id == _executorId,
        orElse: () => null,
      );
      if (matched != null) return matched;
    }
    final currentModel = _selectedModel.trim();
    if (_userModifiedModel && currentModel.isNotEmpty) {
      final resolvedId = CodeModelCatalog.resolveRuntimeForModel(
        model: currentModel,
        executors: available,
      );
      final byModel = available.cast<CodeExecutor?>().firstWhere(
        (item) => item?.id == resolvedId,
        orElse: () => null,
      );
      if (byModel != null) return byModel;
    }
    final defaultModel = CodeModelCatalog.resolvePreferredModelForExecutors(
      available,
      fallbackSession: fallbackSession,
    );
    if (defaultModel != null) {
      final byDefault = available.cast<CodeExecutor?>().firstWhere(
        (item) => item?.id == defaultModel.executorId,
        orElse: () => null,
      );
      if (byDefault != null) return byDefault;
    }
    return available.firstOrNull;
  }

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(aiWorkspaceControllerProvider);
    final projects = state.projects;
    final availableExecutors = state.executors
        .where((item) => item.available && item.supportsAutomation)
        .toList();
    final fallbackSession = CodeModelCatalog.latestSessionWithModel(
      state.currentSession,
      state.sessions,
    );
    final selectedExecutor = _selectedExecutor(
      state.executors,
      fallbackSession: fallbackSession,
    );
    final policies = selectedExecutor?.approvalPolicies ?? const <String>[];
    final selectedPolicy = policies.contains(_approvalPolicy)
        ? _approvalPolicy
        : policies.firstOrNull;
    final selectedProjectId = _selectedProjectId(projects);
    final currentModelValue = _effectiveModel(
      availableExecutors,
      fallbackSession: fallbackSession,
    );
    final effortOptions = CodeModelCatalog.effectiveReasoningEfforts(
      selectedExecutor,
      currentEffort: _reasoningEffort,
    );
    final canSubmit = !state.isActionLoading &&
        selectedProjectId != null &&
        selectedExecutor != null &&
        (!_isolated ||
            (!_capabilityLoading && _worktreeCapability?.canCreate == true));

    return SafeArea(
      child: Padding(
        padding: EdgeInsets.only(
          left: 20,
          right: 20,
          top: 12,
          bottom: MediaQuery.viewInsetsOf(context).bottom + 20,
        ),
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Center(
                child: Container(
                  width: 40,
                  height: 4,
                  decoration: BoxDecoration(
                    color: AppTheme.border,
                    borderRadius: BorderRadius.circular(99),
                  ),
                ),
              ),
              const SizedBox(height: 18),
              Text(
                context.l10n.codeWsHubCreate,
                style: Theme.of(context).textTheme.titleLarge,
              ),
              const SizedBox(height: 12),
              if (state.isLoading &&
                  projects.isEmpty &&
                  availableExecutors.isEmpty)
                const Center(child: CircularProgressIndicator())
              else if (projects.isEmpty)
                _EmptyHint(text: context.l10n.codeWsTerminalNoProjects)
              else if (availableExecutors.isEmpty)
                _EmptyHint(text: context.l10n.codeWsSessionNoExecutors)
              else ...[
                CodeProjectSelectorField(
                  projects: projects,
                  selectedProjectId: selectedProjectId,
                  onChanged: (value) {
                    setState(() {
                      _projectId = value;
                      _worktreeCapability = null;
                      _capabilityError = null;
                      _capabilityLoading = false;
                    });
                    if (_isolated) {
                      _loadWorktreeCapability(value);
                    }
                  },
                ),
                const SizedBox(height: 16),
                Text(context.l10n.codeWsSessionIsolateLabel),
                const SizedBox(height: 8),
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    ChoiceChip(
                      key: const ValueKey('session_isolate_no'),
                      label: Text(context.l10n.codeWsSessionIsolateNo),
                      selected: !_isolated,
                      onSelected: (_) {
                        if (!_isolated) return;
                        setState(() {
                          _isolated = false;
                          _capabilityRequest++;
                          _capabilityLoading = false;
                          _capabilityError = null;
                        });
                      },
                    ),
                    ChoiceChip(
                      key: const ValueKey('session_isolate_yes'),
                      label: Text(context.l10n.codeWsSessionIsolateYes),
                      selected: _isolated,
                      onSelected: (_) {
                        if (_isolated) return;
                        setState(() => _isolated = true);
                        if (_worktreeCapability == null &&
                            !_capabilityLoading) {
                          _loadWorktreeCapability(selectedProjectId);
                        }
                      },
                    ),
                  ],
                ),
                if (_isolated) ...[
                  const SizedBox(height: 10),
                  CodeWorktreeCapabilityStatus(
                    capability: _worktreeCapability,
                    loading: _capabilityLoading,
                    error: _capabilityError,
                    onRetry: () => _loadWorktreeCapability(selectedProjectId),
                  ),
                ],
                const SizedBox(height: 12),
                CodeModelSelectorField(
                  executors: availableExecutors,
                  selectedExecutor: selectedExecutor,
                  selectedModel: currentModelValue,
                  selectedEffort: _reasoningEffort,
                  onSelected: (selection) {
                    setState(() {
                      _userModifiedModel = true;
                      _selectedModel = selection.model;
                      if (selection.reasoningEffort != null) {
                        _reasoningEffort = selection.reasoningEffort!;
                      }
                      final nextExecId = CodeModelCatalog.resolveRuntimeForModel(
                        model: selection.model,
                        explicitExecutorId: selection.executorId,
                        executors: availableExecutors,
                        fallbackRuntime: selectedExecutor?.id ?? 'opencode',
                      );
                      if (nextExecId.isNotEmpty &&
                          availableExecutors.any((e) => e.id == nextExecId)) {
                        _executorId = nextExecId;
                        final nextExec = availableExecutors.firstWhere(
                          (e) => e.id == nextExecId,
                        );
                        if (!nextExec.approvalPolicies.contains(
                          _approvalPolicy,
                        )) {
                          _approvalPolicy =
                              nextExec.approvalPolicies.firstOrNull ??
                              'full_auto';
                        }
                      }
                      if (selection.model.trim().isNotEmpty) {
                        unawaited(
                          CodeModelCatalog.saveLastSelection(
                            model: selection.model,
                            executorId: _executorId ?? selection.executorId,
                          ),
                        );
                      }
                    });
                  },
                ),
                if (effortOptions.isNotEmpty) ...[
                  const SizedBox(height: 12),
                  Text(context.l10n.codeWsSessionSettingsEffort),
                  const SizedBox(height: 8),
                  Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: [
                      ChoiceChip(
                        key: const ValueKey('session_effort_default'),
                        label: Text(
                          context.l10n.codeWsSessionSettingsEffortDefault,
                        ),
                        selected: _reasoningEffort.isEmpty,
                        onSelected: (_) {
                          setState(() => _reasoningEffort = '');
                        },
                      ),
                      for (final option in effortOptions)
                        ChoiceChip(
                          key: ValueKey('session_effort_${option.value}'),
                          label: Text(option.label),
                          selected: _reasoningEffort == option.value,
                          onSelected: (_) {
                            setState(() => _reasoningEffort = option.value);
                          },
                        ),
                    ],
                  ),
                ],
                const SizedBox(height: 12),
                TextField(
                  controller: _titleController,
                  decoration: InputDecoration(
                    labelText: context.l10n.codeWsSessionSettingsName,
                  ),
                ),
                const SizedBox(height: 16),
                Text(context.l10n.codeWsSessionSettingsApproval),
                const SizedBox(height: 8),
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: policies
                      .map(
                        (policy) => ChoiceChip(
                          label: Text(_policyLabel(context, policy)),
                          selected: policy == selectedPolicy,
                          onSelected: (_) {
                            setState(() => _approvalPolicy = policy);
                          },
                        ),
                      )
                      .toList(),
                ),
                if (selectedPolicy != null) ...[
                  const SizedBox(height: 8),
                  Text(
                    _policyDescription(context, selectedPolicy),
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                ],
                CodeApprovalRejectNotice(
                  executor: selectedExecutor,
                  policy: selectedPolicy,
                ),
                if (_submitError != null && _submitError!.isNotEmpty) ...[
                  const SizedBox(height: 8),
                  Text(
                    _submitError!,
                    key: const ValueKey('code_session_sheet_error'),
                    style: const TextStyle(color: AppTheme.error, fontSize: 12),
                  ),
                ],
                const SizedBox(height: 8),
                SizedBox(
                  width: double.infinity,
                  child: FilledButton.icon(
                    onPressed: canSubmit
                        ? () => _createSession(projects, state.executors)
                        : null,
                    icon: state.isActionLoading
                        ? const SizedBox.square(
                            dimension: 18,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.add_rounded),
                    label: Text(context.l10n.codeWsHubCreate),
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }

  String _policyLabel(BuildContext context, String policy) => switch (policy) {
    'manual' => context.l10n.codeWsSessionSettingsPolicyManual,
    'safe_auto' => context.l10n.codeWsSessionSettingsPolicySafeAuto,
    'full_auto' => context.l10n.codeWsSessionSettingsPolicyFullAuto,
    _ => policy,
  };

  String _policyDescription(BuildContext context, String policy) =>
      switch (policy) {
        'manual' => context.l10n.codeWsSessionSettingsPolicyManualDesc,
        'safe_auto' => context.l10n.codeWsSessionSettingsPolicySafeAutoDesc,
        'full_auto' => context.l10n.codeWsSessionSettingsPolicyFullAutoDesc,
        _ => '',
      };
}

class _EmptyHint extends StatelessWidget {
  const _EmptyHint({required this.text});

  final String text;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppTheme.surfaceMuted,
        borderRadius: BorderRadius.circular(12),
      ),
      child: Text(text),
    );
  }
}
