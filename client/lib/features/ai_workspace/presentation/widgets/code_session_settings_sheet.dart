import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/theme/app_theme.dart';
import '../../models/ai_dev_session.dart';
import '../code_workspace_text.dart';
import 'code_approval_reject_notice.dart';
import '../controllers/ai_workspace_controller.dart';
import '../screens/code_quality_checks_screen.dart';

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
    _titleController.text = session.title;
    _modelController.text = session.providerModel;
    _effortController.text = session.reasoningEffort;
    _policy = session.approvalPolicy;
  }

  Future<void> _save(AiDevSession session) async {
    final controller = ref.read(aiWorkspaceControllerProvider.notifier);
    final title = _titleController.text.trim();
    final model = _modelController.text.trim();
    final effort = _effortController.text.trim();
    var success = true;
    if (title.isNotEmpty && title != session.title) {
      success = await controller.updateSessionTitle(title) && success;
    }
    if (_policy != null && _policy != session.approvalPolicy) {
      success = await controller.updateApprovalPolicy(_policy!) && success;
    }
    if (model != session.providerModel || effort != session.reasoningEffort) {
      success =
          await controller.updateSessionModel(
            model: model == session.providerModel ? null : model,
            reasoningEffort: effort == session.reasoningEffort ? null : effort,
          ) &&
          success;
    }
    if (!mounted) return;
    if (success) {
      Navigator.of(context).pop();
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(CodeWorkspaceText.t(context, 'sessionSettings.saved')),
        ),
      );
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
          content: Text(CodeWorkspaceText.t(context, 'memory.extractSuccess')),
          backgroundColor: const Color(0xFF10B981),
        ),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            '${CodeWorkspaceText.t(context, 'memory.extractFailed')}: $e',
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
    final modelOptions = executor?.modelOptions ?? const <CodeExecutorOption>[];
    final effortOptions =
        executor?.reasoningEfforts ?? const <CodeExecutorOption>[];
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
                CodeWorkspaceText.t(context, 'sessionSettings.title'),
                style: const TextStyle(
                  fontSize: 18,
                  fontWeight: FontWeight.bold,
                ),
              ),
              const SizedBox(height: 16),
              TextField(
                controller: _titleController,
                decoration: InputDecoration(
                  labelText: CodeWorkspaceText.t(
                    context,
                    'sessionSettings.name',
                  ),
                  border: const OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 14),
              DropdownButtonFormField<String>(
                initialValue: _policy,
                decoration: InputDecoration(
                  labelText: CodeWorkspaceText.t(
                    context,
                    'sessionSettings.approval',
                  ),
                  border: const OutlineInputBorder(),
                ),
                items: [
                  for (final policy in policies)
                    DropdownMenuItem(
                      value: policy,
                      child: Text(
                        CodeWorkspaceText.t(
                          context,
                          'sessionSettings.policy_$policy',
                        ),
                      ),
                    ),
                ],
                onChanged: (value) => setState(() => _policy = value),
              ),
              CodeApprovalRejectNotice(executor: executor, policy: _policy),
              const SizedBox(height: 14),
              if (modelOptions.isEmpty)
                TextField(
                  controller: _modelController,
                  decoration: InputDecoration(
                    labelText: CodeWorkspaceText.t(
                      context,
                      'sessionSettings.model',
                    ),
                    hintText: CodeWorkspaceText.t(
                      context,
                      'sessionSettings.modelHint',
                    ),
                    border: const OutlineInputBorder(),
                  ),
                )
              else
                DropdownButtonFormField<String>(
                  initialValue: modelOptions.any(
                        (option) => option.value == _modelController.text,
                      )
                      ? _modelController.text
                      : '',
                  decoration: InputDecoration(
                    labelText: CodeWorkspaceText.t(
                      context,
                      'sessionSettings.model',
                    ),
                    border: const OutlineInputBorder(),
                  ),
                  items: [
                    DropdownMenuItem(
                      value: '',
                      child: Text(
                        CodeWorkspaceText.t(
                          context,
                          'sessionSettings.modelHint',
                        ),
                      ),
                    ),
                    for (final option in modelOptions)
                      DropdownMenuItem(
                        value: option.value,
                        child: Text(option.label),
                      ),
                  ],
                  onChanged: (value) {
                    _modelController.text = value ?? '';
                  },
                ),
              const SizedBox(height: 14),
              if (effortOptions.isEmpty)
                TextField(
                  controller: _effortController,
                  decoration: InputDecoration(
                    labelText: CodeWorkspaceText.t(
                      context,
                      'sessionSettings.effort',
                    ),
                    hintText: CodeWorkspaceText.t(
                      context,
                      'sessionSettings.effortHint',
                    ),
                    border: const OutlineInputBorder(),
                  ),
                )
              else
                DropdownButtonFormField<String>(
                  initialValue: effortOptions.any(
                        (option) => option.value == _effortController.text,
                      )
                      ? _effortController.text
                      : '',
                  decoration: InputDecoration(
                    labelText: CodeWorkspaceText.t(
                      context,
                      'sessionSettings.effort',
                    ),
                    border: const OutlineInputBorder(),
                  ),
                  items: [
                    DropdownMenuItem(
                      value: '',
                      child: Text(
                        CodeWorkspaceText.t(
                          context,
                          'sessionSettings.effortDefault',
                        ),
                      ),
                    ),
                    for (final option in effortOptions)
                      DropdownMenuItem(
                        value: option.value,
                        child: Text(option.label),
                      ),
                  ],
                  onChanged: (value) {
                    _effortController.text = value ?? '';
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
                    CodeWorkspaceText.t(context, 'sessionSettings.save'),
                  ),
                ),
              ),
              const SizedBox(height: 16),
              const Divider(color: Color(0xFFE2E8F0)),
              const SizedBox(height: 8),
              OutlinedButton.icon(
                onPressed: () {
                  Navigator.of(context).pop();
                  Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => CodeQualityChecksScreen(
                        sessionId: session.id,
                        sessionTitle: session.title.isEmpty
                            ? CodeWorkspaceText.format(
                                context,
                                'chat.sessionFallback',
                                {'id': session.id},
                              )
                            : session.title,
                      ),
                    ),
                  );
                },
                icon: const Icon(Icons.verified_outlined),
                label: Text(CodeWorkspaceText.t(context, 'quality.title')),
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
                      ? CodeWorkspaceText.t(context, 'memory.extracting')
                      : CodeWorkspaceText.t(context, 'memory.extract'),
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
