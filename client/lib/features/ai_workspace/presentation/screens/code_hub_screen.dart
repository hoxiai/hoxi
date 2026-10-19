import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../models/ai_dev_session.dart';
import '../../models/code_task.dart';
import '../code_workspace_text.dart';
import '../controllers/ai_workspace_controller.dart';
import '../controllers/code_attention_controller.dart';
import '../widgets/code_attention_card.dart';
import '../widgets/code_hub_cards.dart';
import '../widgets/code_project_terminal_card.dart';
import 'code_attention_screen.dart';
import 'code_project_terminal_sheet.dart';
import 'code_session_sheet.dart';
import 'code_terminal_screen.dart';

class CodeHubScreen extends ConsumerStatefulWidget {
  const CodeHubScreen({super.key});

  @override
  ConsumerState<CodeHubScreen> createState() => _CodeHubScreenState();
}

class _CodeHubScreenState extends ConsumerState<CodeHubScreen> {
  Future<void> _refresh() async {
    await Future.wait([
      ref.read(aiWorkspaceControllerProvider.notifier).loadWorkspace(),
      ref.read(codeAttentionControllerProvider.notifier).load(),
    ]);
  }

  Future<void> _openAttention() async {
    await Navigator.of(
      context,
    ).push(MaterialPageRoute(builder: (_) => const CodeAttentionScreen()));
    if (mounted) {
      await ref.read(codeAttentionControllerProvider.notifier).load();
    }
  }

  Future<void> _openSessionCreator() async {
    final session = await showModalBottomSheet<AiDevSession>(
      context: context,
      isScrollControlled: true,
      showDragHandle: false,
      builder: (_) => const CodeSessionSheet(),
    );
    if (!mounted || session == null) return;
    await _openSession(session);
    if (mounted) await _refresh();
  }

  Future<void> _openProjectTerminal() async {
    final launch = await showModalBottomSheet<CodeProjectTerminalLaunch>(
      context: context,
      isScrollControlled: true,
      showDragHandle: false,
      builder: (_) => const CodeProjectTerminalSheet(),
    );
    if (!mounted || launch == null) return;
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => CodeTerminalScreen.project(
          projectTerminal: launch.terminal,
          terminalId: launch.terminal.id,
          projectName: launch.project.name,
        ),
      ),
    );
  }

  Future<void> _openSession(AiDevSession session) async {
    await ref
        .read(aiWorkspaceControllerProvider.notifier)
        .selectSession(session);
    if (!mounted) return;
    final workspace = ref.read(aiWorkspaceControllerProvider);
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => CodeTerminalScreen(
          session: workspace.currentSession ?? session,
          terminalId: session.id,
          task: workspace.currentTask,
          nativeProtocol: _usesNativeProtocol(
            workspace.executors,
            session.agentName,
          ),
          projectName: _projectName(workspace.projects, session.projectId),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final workspace = ref.watch(aiWorkspaceControllerProvider);
    final attention = ref.watch(codeAttentionControllerProvider);

    return Scaffold(
      appBar: AppBar(title: Text(CodeWorkspaceText.t(context, 'hub.title'))),
      body: RefreshIndicator(
        onRefresh: _refresh,
        child: ListView(
          physics: const AlwaysScrollableScrollPhysics(),
          padding: const EdgeInsets.fromLTRB(20, 4, 20, 28),
          children: [
            CodeHubHero(
              sessionCount: workspace.tasks.isNotEmpty
                  ? workspace.tasks.length
                  : workspace.sessions.length,
              activeCount: workspace.sessions.where(_isActive).length,
              onCreate: () {
                _openSessionCreator();
              },
            ),
            const SizedBox(height: 16),
            CodeAttentionCard(
              isLoading: attention.isLoading,
              errorMessage: attention.errorMessage,
              items: attention.items,
              onOpen: _openAttention,
              onRetry: ref.read(codeAttentionControllerProvider.notifier).load,
            ),
            const SizedBox(height: 16),
            CodeProjectTerminalCard(onTap: _openProjectTerminal),
            const SizedBox(height: 28),
            Row(
              children: [
                Text(
                  CodeWorkspaceText.t(context, 'hub.projectTasks'),
                  style: Theme.of(context).textTheme.titleMedium?.copyWith(
                    fontWeight: FontWeight.w800,
                  ),
                ),
                const Spacer(),
                Text(
                  CodeWorkspaceText.format(context, 'hub.sessionCount', {
                    'count': workspace.tasks.isNotEmpty
                        ? workspace.tasks.length
                        : workspace.sessions.length,
                  }),
                  style: Theme.of(context).textTheme.bodySmall,
                ),
              ],
            ),
            const SizedBox(height: 14),
            ..._buildTaskList(workspace),
          ],
        ),
      ),
    );
  }

  List<Widget> _buildTaskList(AiWorkspaceState state) {
    if (state.isLoading && state.sessions.isEmpty && state.tasks.isEmpty) {
      return const [
        SizedBox(
          height: 180,
          child: Center(child: CircularProgressIndicator()),
        ),
      ];
    }
    if (state.errorMessage != null &&
        state.sessions.isEmpty &&
        state.tasks.isEmpty) {
      return [
        CodeHubErrorCard(
          message: state.errorMessage!,
          onRetry: ref
              .read(aiWorkspaceControllerProvider.notifier)
              .loadWorkspace,
        ),
      ];
    }
    final sessionsById = {
      for (final session in state.sessions) session.id: session,
    };
    final taskItems = state.tasks
        .map((task) => (task: task, session: sessionsById[task.sessionId]))
        .where((item) => item.session != null)
        .toList();
    if (taskItems.isEmpty && state.sessions.isEmpty) {
      return [
        CodeHubEmptyCard(
          icon: Icons.terminal_rounded,
          title: CodeWorkspaceText.t(context, 'hub.noProjectTasks'),
          description: CodeWorkspaceText.t(context, 'hub.noProjectTasksHint'),
          actionLabel: CodeWorkspaceText.t(context, 'hub.create'),
          onAction: () {
            _openSessionCreator();
          },
        ),
      ];
    }

    if (taskItems.isNotEmpty) {
      final grouped = <int, List<({CodeTask task, AiDevSession? session})>>{};
      for (final item in taskItems) {
        grouped.putIfAbsent(item.task.projectId, () => []).add(item);
      }
      return state.projects.expand((project) {
        final projectItems = grouped[project.id] ?? const [];
        final projectName = project.name;
        return <Widget>[
          Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: Row(
              children: [
                const Icon(Icons.folder_outlined, size: 18),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    projectName,
                    style: Theme.of(context).textTheme.titleSmall?.copyWith(
                      fontWeight: FontWeight.w800,
                    ),
                  ),
                ),
                Text(
                  '${projectItems.length}',
                  style: Theme.of(context).textTheme.bodySmall,
                ),
              ],
            ),
          ),
          ...projectItems.map(
            (item) => Padding(
              padding: const EdgeInsets.only(bottom: 16),
              child: CodeSessionListCard(
                session: item.session!,
                task: item.task,
                projectName: projectName,
                onTap: () => _openSession(item.session!),
              ),
            ),
          ),
          if (projectItems.isEmpty)
            Padding(
              padding: const EdgeInsets.only(bottom: 16),
              child: Text(
                CodeWorkspaceText.t(context, 'hub.noProjectTasks'),
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ),
          const SizedBox(height: 4),
        ];
      }).toList();
    }

    return state.sessions
        .map(
          (session) => Padding(
            padding: const EdgeInsets.only(bottom: 16),
            child: CodeSessionListCard(
              session: session,
              projectName: _projectName(state.projects, session.projectId),
              onTap: () => _openSession(session),
            ),
          ),
        )
        .toList();
  }

  String _projectName(List<CodeProject> projects, int projectId) {
    for (final project in projects) {
      if (project.id == projectId) return project.name;
    }
    return '开发项目';
  }

  bool _usesNativeProtocol(List<CodeExecutor> executors, String executorId) {
    for (final executor in executors) {
      if (executor.id == executorId) return executor.nativeTerminal;
    }
    return executorId != 'terminal';
  }

  bool _isActive(AiDevSession session) {
    final stage = session.currentStage.toLowerCase();
    return stage == 'executing' ||
        stage == 'running' ||
        stage == 'instruction_queued' ||
        stage == 'awaiting_approval';
  }
}
