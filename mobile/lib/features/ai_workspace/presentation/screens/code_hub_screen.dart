import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/navigation/navigation_guard.dart';
import '../../../../core/theme/app_theme.dart';
import '../../models/ai_dev_session.dart';
import '../../models/code_task.dart';
import '../../data/task_ai_utils.dart';
import '../../../../shared/navigation/feature_route_registry.dart';
import '../controllers/ai_workspace_task_sync_bus.dart';
import '../controllers/ai_workspace_controller.dart';
import '../controllers/code_attention_controller.dart';
import '../widgets/code_attention_card.dart';
import '../widgets/code_hub_cards.dart';
import '../widgets/code_project_terminal_card.dart';
import 'ai_chat_screen.dart';
import 'code_attention_screen.dart';
import 'code_project_terminal_sheet.dart';
import 'code_session_sheet.dart';
import 'code_terminal_screen.dart';

enum CodeHubTab { recent, projects, apps }

class CodeHubScreen extends ConsumerStatefulWidget {
  final List<Widget> extraActions;
  final Future<void> Function()? onRefreshExtra;
  final bool showAppBar;
  final Widget? nodeDirectory;
  final String? nodeDirectoryLabel;
  final int? nodeDirectoryCount;
  final CodeHubTab initialTab;

  const CodeHubScreen({
    super.key,
    this.extraActions = const [],
    this.onRefreshExtra,
    this.showAppBar = true,
    this.nodeDirectory,
    this.nodeDirectoryLabel,
    this.nodeDirectoryCount,
    this.initialTab = CodeHubTab.recent,
  });

  @override
  ConsumerState<CodeHubScreen> createState() => _CodeHubScreenState();
}

class _CodeHubScreenState extends ConsumerState<CodeHubScreen> {
  static const String _collapsedPrefsKey =
      'hoxi.code_hub.collapsed_projects_v1';
  static const int defaultVisibleTasksPerProject = 3;

  final Map<int, bool> _projectCollapsedOverrides = {};
  final Set<int> _expandedMoreProjectIds = {};
  bool _flatSessionsExpanded = false;
  late CodeHubTab _selectedTab = widget.initialTab;

  @override
  void initState() {
    super.initState();
    unawaited(_restoreCollapsedState());
  }

  @override
  void didUpdateWidget(covariant CodeHubScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.nodeDirectory == null && _selectedTab == CodeHubTab.apps) {
      _selectedTab = CodeHubTab.recent;
    }
  }

  Future<void> _restoreCollapsedState() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final saved = prefs.getStringList(_collapsedPrefsKey);
      if (saved == null || !mounted) return;
      setState(() {
        for (final entry in saved) {
          final parts = entry.split(':');
          if (parts.length == 2) {
            final id = int.tryParse(parts[0]);
            if (id != null) {
              _projectCollapsedOverrides[id] = parts[1] == '1';
            }
          }
        }
      });
    } catch (_) {}
  }

  Future<void> _persistCollapsedState() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final encoded = _projectCollapsedOverrides.entries
          .map((e) => '${e.key}:${e.value ? 1 : 0}')
          .toList();
      await prefs.setStringList(_collapsedPrefsKey, encoded);
    } catch (_) {}
  }

  bool _isProjectCollapsed(
    CodeProject project,
    List<({CodeTask task, AiDevSession? session})> items,
  ) {
    final override = _projectCollapsedOverrides[project.id];
    if (override != null) return override;
    if (project.collapsed) return true;
    return false;
  }

  void _toggleProjectCollapsed(CodeProject project, bool currentlyCollapsed) {
    setState(() {
      _projectCollapsedOverrides[project.id] = !currentlyCollapsed;
    });
    unawaited(_persistCollapsedState());
  }

  Future<void> _refresh() async {
    await Future.wait([
      ref.read(aiWorkspaceControllerProvider.notifier).loadWorkspace(),
      ref.read(codeAttentionControllerProvider.notifier).load(),
      if (widget.onRefreshExtra != null) widget.onRefreshExtra!(),
    ]);
  }

  Future<void> _openAttention() async {
    await AppNavigationGuard.push(
      context,
      MaterialPageRoute(builder: (_) => const CodeAttentionScreen()),
    );
    if (mounted) {
      unawaited(ref.read(codeAttentionControllerProvider.notifier).reloadSilently());
      unawaited(
        ref.read(aiWorkspaceControllerProvider.notifier).refreshSessionsAndWorkspace(),
      );
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
    await _openSession(session, selectIfNeeded: true);
  }

  Future<void> _openProjectTerminal() async {
    final launch = await showModalBottomSheet<CodeProjectTerminalLaunch>(
      context: context,
      isScrollControlled: true,
      showDragHandle: false,
      builder: (_) => const CodeProjectTerminalSheet(),
    );
    if (!mounted || launch == null) return;
    await AppNavigationGuard.push(
      context,
      MaterialPageRoute(
        builder: (_) => CodeTerminalScreen.project(
          projectTerminal: launch.terminal,
          terminalId: launch.terminal.id,
          projectName: launch.project.name,
        ),
      ),
    );
  }

  Future<void> _openSession(
    AiDevSession session, {
    bool selectIfNeeded = false,
  }) async {
    final currentId = ref.read(aiWorkspaceControllerProvider).currentSession?.id;
    if (!selectIfNeeded || currentId != session.id) {
      unawaited(
        ref.read(aiWorkspaceControllerProvider.notifier).selectSession(session),
      );
    }
    if (!mounted) return;
    await AppNavigationGuard.push(
      context,
      MaterialPageRoute(builder: (_) => const AiChatScreen()),
    );
    if (mounted) {
      unawaited(
        ref.read(aiWorkspaceControllerProvider.notifier).refreshSessionsAndWorkspace(),
      );
      unawaited(ref.read(aiWorkspaceTaskSyncBusProvider).refreshRecentSilently());
      unawaited(ref.read(codeAttentionControllerProvider.notifier).reloadSilently());
    }
  }

  Future<void> _openTaskDetail(AiDevSession session, CodeTask? task) async {
    final resolvedProjectId = task?.projectId ?? session.projectId;
    final projectName = _projectName(
      ref.read(aiWorkspaceControllerProvider).projects,
      resolvedProjectId,
    );
    final entity = buildAiTaskEntity(
      taskId: task?.id ?? (session.lastTaskId > 0 ? session.lastTaskId : session.id),
      sessionId: session.id,
      projectId: resolvedProjectId,
      agentName: session.agentName.isNotEmpty ? session.agentName : (task?.agentName ?? 'opencode'),
      providerModel: session.providerModel.isNotEmpty ? session.providerModel : (task?.providerModel ?? ''),
      projectName: projectName,
      sourceWorkDir: session.sourceWorkDir,
      diffAdditions: session.diffAdditions > 0 ? session.diffAdditions : (task?.diffAdditions ?? 0),
      diffDeletions: session.diffDeletions > 0 ? session.diffDeletions : (task?.diffDeletions ?? 0),
      title: task?.title.isNotEmpty == true ? task!.title : session.title,
      workDir: session.workDir.isNotEmpty ? session.workDir : (task?.workDir ?? ''),
      status: task?.status.isNotEmpty == true ? task!.status : session.status,
      currentStage: session.currentStage,
      createdAt: task?.createdAt ?? session.createdAt,
      updatedAt: task?.updatedAt ?? session.updatedAt,
    );
    await FeatureRouteRegistry.openTaskDetail(context, entity);
    if (mounted) {
      unawaited(ref.read(aiWorkspaceControllerProvider.notifier).refreshSessionsAndWorkspace());
      unawaited(ref.read(aiWorkspaceTaskSyncBusProvider).refreshRecentSilently());
    }
  }

  Future<void> _openSessionById(int sessionId) async {
    AiDevSession? targetSession;
    await AppNavigationGuard.runAsync(context, () async {
      final sessions = ref.read(aiWorkspaceControllerProvider).sessions;
      AiDevSession? existing = sessions.where((s) => s.id == sessionId).firstOrNull;
      if (existing == null) {
        for (final root in sessions) {
          final child = root.subagents.where((s) => s.id == sessionId).firstOrNull;
          if (child != null) {
            existing = child;
            break;
          }
        }
      }
      if (existing != null) {
        targetSession = existing;
        return;
      }
      try {
        final sessionState = await ref.read(aiWorkspaceRepositoryProvider).getSessionState(sessionId);
        if (!mounted) return;
        targetSession = sessionState.session;
      } catch (error) {
        if (!mounted) return;
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('${context.l10n.codeWsAttentionOpenFailed}：$error')),
        );
      }
    });
    if (targetSession != null && mounted) {
      await _openSession(targetSession!);
    }
  }

  @override
  Widget build(BuildContext context) {
    final workspace = ref.watch(aiWorkspaceControllerProvider);
    final attention = ref.watch(codeAttentionControllerProvider);
    final hasDirectoryTab = widget.nodeDirectory != null;
    final effectiveTab = (!hasDirectoryTab && _selectedTab == CodeHubTab.apps)
        ? CodeHubTab.recent
        : _selectedTab;
    final int? tabCount = switch (effectiveTab) {
      CodeHubTab.projects => workspace.projects.length,
      CodeHubTab.recent =>
        workspace.tasks.isNotEmpty
            ? workspace.tasks.length
            : workspace.sessions.length,
      CodeHubTab.apps => widget.nodeDirectoryCount,
    };

    return Scaffold(
      appBar: widget.showAppBar
          ? AppBar(
              automaticallyImplyLeading: false,
              title: Text(context.l10n.codeWsHubTitle),
              actions: [
                ...widget.extraActions,
                IconButton(
                  tooltip: context.l10n.codeWsHubCreate,
                  onPressed: _openSessionCreator,
                  icon: const Icon(Icons.add_rounded),
                ),
                IconButton(
                  tooltip: context.l10n.codeWsActionRefresh,
                  onPressed: workspace.isLoading ? null : _refresh,
                  icon: const Icon(Icons.refresh_rounded),
                ),
              ],
            )
          : null,
      body: RefreshIndicator(
        onRefresh: _refresh,
        child: ListView(
          physics: const AlwaysScrollableScrollPhysics(),
          padding: EdgeInsets.fromLTRB(20, widget.showAppBar ? 4 : 14, 20, 28),
          children: [
            if (attention.isLoading ||
                attention.errorMessage != null ||
                attention.items.isNotEmpty) ...[
              CodeAttentionCard(
                isLoading: attention.isLoading,
                errorMessage: attention.errorMessage,
                items: attention.items,
                actionKey: attention.actionKey,
                onOpen: _openAttention,
                onRetry: ref.read(codeAttentionControllerProvider.notifier).load,
                onExecuteAction: ref.read(codeAttentionControllerProvider.notifier).execute,
                onOpenSession: _openSessionById,
              ),
              const SizedBox(height: 12),
            ],
            CodeProjectTerminalCard(onTap: _openProjectTerminal),
            const SizedBox(height: 18),
            Row(
              children: [
                Expanded(
                  child: SingleChildScrollView(
                    scrollDirection: Axis.horizontal,
                    child: Row(
                      children: [
                        _buildTabButton(
                          key: const ValueKey('code_hub_tab_recent'),
                          label: context.l10n.codeWsHubRecentTasks,
                          selected: effectiveTab == CodeHubTab.recent,
                          onTap: () =>
                              setState(() => _selectedTab = CodeHubTab.recent),
                        ),
                        const SizedBox(width: 8),
                        _buildTabButton(
                          key: const ValueKey('code_hub_tab_projects'),
                          label: context.l10n.codeWsHubProjectsTab,
                          selected: effectiveTab == CodeHubTab.projects,
                          onTap: () => setState(
                            () => _selectedTab = CodeHubTab.projects,
                          ),
                        ),
                        if (hasDirectoryTab) ...[
                          const SizedBox(width: 8),
                          _buildTabButton(
                            key: const ValueKey('code_hub_tab_apps'),
                            label: widget.nodeDirectoryLabel ?? context.l10n.codeWsHubProjectsTab,
                            selected: effectiveTab == CodeHubTab.apps,
                            onTap: () =>
                                setState(() => _selectedTab = CodeHubTab.apps),
                          ),
                        ],
                      ],
                    ),
                  ),
                ),
                if (tabCount != null) ...[
                  const SizedBox(width: 8),
                  Text(
                    context.l10n.codeWsHubSessionCount(tabCount),
                    style: Theme.of(context).textTheme.bodySmall?.copyWith(
                      color: AppTheme.textSecondary,
                    ),
                  ),
                ],
              ],
            ),
            const SizedBox(height: 12),
            if (effectiveTab == CodeHubTab.apps)
              widget.nodeDirectory!
            else
              ..._buildTaskList(workspace),
          ],
        ),
      ),
    );
  }

  Widget _buildTabButton({
    required Key key,
    required String label,
    required bool selected,
    required VoidCallback onTap,
  }) {
    return InkWell(
      key: key,
      borderRadius: BorderRadius.circular(10),
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        decoration: BoxDecoration(
          color: selected ? AppTheme.primaryBlueLight : AppTheme.surfaceMuted,
          borderRadius: BorderRadius.circular(10),
        ),
        child: Text(
          label,
          style: TextStyle(
            color: selected ? AppTheme.primaryBlue : AppTheme.textSecondary,
            fontSize: 13,
            fontWeight: selected ? FontWeight.w700 : FontWeight.w600,
          ),
        ),
      ),
    );
  }

  List<({CodeTask task, AiDevSession? session})> _sortProjectItems(
    List<({CodeTask task, AiDevSession? session})> items,
  ) {
    final sorted = List<({CodeTask task, AiDevSession? session})>.from(items);
    sorted.sort((a, b) {
      final aActive = a.session != null && _isActive(a.session!);
      final bActive = b.session != null && _isActive(b.session!);
      if (aActive != bActive) return aActive ? -1 : 1;
      final aTime =
          a.task.updatedAt ??
          a.session?.updatedAt ??
          a.task.createdAt ??
          a.session?.createdAt ??
          DateTime.fromMillisecondsSinceEpoch(0);
      final bTime =
          b.task.updatedAt ??
          b.session?.updatedAt ??
          b.task.createdAt ??
          b.session?.createdAt ??
          DateTime.fromMillisecondsSinceEpoch(0);
      return bTime.compareTo(aTime);
    });
    return sorted;
  }

  List<Widget> _buildTaskList(AiWorkspaceState state) {
    if (state.isLoading && state.sessions.isEmpty && state.tasks.isEmpty) {
      return const [
        SizedBox(height: 180, child: Center(child: CircularProgressIndicator())),
      ];
    }
    if (state.errorMessage != null && state.sessions.isEmpty && state.tasks.isEmpty) {
      return [
        CodeHubErrorCard(
          message: state.errorMessage!,
          onRetry: ref.read(aiWorkspaceControllerProvider.notifier).loadWorkspace,
        ),
      ];
    }
    final sessionsById = {for (final session in state.sessions) session.id: session};
    final taskItems = state.tasks
        .map((task) => (task: task, session: sessionsById[task.sessionId]))
        .where((item) => item.session != null)
        .toList();
    final isProjectsTab = _selectedTab == CodeHubTab.projects;
    if (taskItems.isEmpty &&
        state.sessions.isEmpty &&
        (!isProjectsTab || state.projects.isEmpty)) {
      return [
        CodeHubEmptyCard(
          icon: Icons.terminal_rounded,
          title: context.l10n.codeWsHubNoProjectTasks,
          description: context.l10n.codeWsHubNoProjectTasksHint,
          actionLabel: context.l10n.codeWsHubCreate,
          onAction: _openSessionCreator,
        ),
      ];
    }

    if (isProjectsTab && state.projects.isNotEmpty) {
      final grouped = <int, List<({CodeTask task, AiDevSession? session})>>{};
      for (final item in taskItems) {
        grouped.putIfAbsent(item.task.projectId, () => []).add(item);
      }
      if (taskItems.isEmpty && state.sessions.isNotEmpty) {
        for (final session in state.sessions) {
          grouped.putIfAbsent(session.projectId, () => []).add((
            task: CodeTask(
              id: session.lastTaskId > 0 ? session.lastTaskId : session.id,
              sessionId: session.id,
              projectId: session.projectId,
              title: session.currentTaskTitle.isNotEmpty ? session.currentTaskTitle : session.title,
              agentName: session.agentName,
              providerModel: session.providerModel,
              diffAdditions: session.diffAdditions,
              diffDeletions: session.diffDeletions,
              workDir: session.workDir,
              status: session.currentStage.isNotEmpty ? session.currentStage : session.status,
              createdAt: session.createdAt,
              updatedAt: session.updatedAt,
            ),
            session: session,
          ));
        }
      }
      final sortedProjects = List<CodeProject>.from(state.projects)
        ..sort((a, b) {
          final aItems = _sortProjectItems(grouped[a.id] ?? const []);
          final bItems = _sortProjectItems(grouped[b.id] ?? const []);
          final aActive = aItems.any((item) => item.session != null && _isActive(item.session!));
          final bActive = bItems.any((item) => item.session != null && _isActive(item.session!));
          if (aActive != bActive) return aActive ? -1 : 1;
          if (aItems.isNotEmpty != bItems.isNotEmpty) return aItems.isNotEmpty ? -1 : 1;
          if (aItems.isNotEmpty && bItems.isNotEmpty) {
            final aFirst = aItems.first, bFirst = bItems.first;
            final aTime = aFirst.task.updatedAt ?? aFirst.session?.updatedAt ?? aFirst.task.createdAt ?? aFirst.session?.createdAt ?? DateTime.fromMillisecondsSinceEpoch(0);
            final bTime = bFirst.task.updatedAt ?? bFirst.session?.updatedAt ?? bFirst.task.createdAt ?? bFirst.session?.createdAt ?? DateTime.fromMillisecondsSinceEpoch(0);
            final cmp = bTime.compareTo(aTime);
            if (cmp != 0) return cmp;
          }
          return 0;
        });
      return sortedProjects.expand((project) {
        final projectItems = _sortProjectItems(grouped[project.id] ?? const []);
        final projectName = project.name;
        final activeInProject = projectItems
            .where((item) => item.session != null && _isActive(item.session!))
            .length;
        if (projectItems.length <= 1) {
          final single = projectItems.firstOrNull;
          return <Widget>[
            Padding(
              padding: const EdgeInsets.only(bottom: 14),
              child: CodeSingleProjectCard(
                key: ValueKey('code_hub_project_card_${project.id}'),
                projectName: projectName,
                session: single?.session,
                task: single?.task,
                isActive: activeInProject > 0,
                onTap: single?.session == null ? null : () => _openSession(single!.session!),
                onOpenSubagent: _openSession,
                onOpenTaskDetail: single?.session == null
                    ? null
                    : () => _openTaskDetail(single!.session!, single.task),
              ),
            ),
          ];
        }

        final isCollapsed = _isProjectCollapsed(project, projectItems);
        final showAllInProject = _expandedMoreProjectIds.contains(project.id);
        final now = DateTime.now();
        final hotInProject = projectItems
            .where((item) => item.session != null && _isHotItem(item.session!, item.task, now))
            .length;
        final topLimit = hotInProject > defaultVisibleTasksPerProject
            ? hotInProject
            : defaultVisibleTasksPerProject;
        final hasMore = projectItems.length > topLimit;
        final hiddenCount = projectItems.length - topLimit;
        final initialItems = isCollapsed
            ? const <({CodeTask task, AiDevSession? session})>[]
            : projectItems.take(topLimit).toList();
        final remainingItems = isCollapsed || !showAllInProject
            ? const <({CodeTask task, AiDevSession? session})>[]
            : projectItems.skip(topLimit).toList();

        return <Widget>[
          Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: InkWell(
              key: ValueKey('code_hub_project_header_${project.id}'),
              borderRadius: BorderRadius.circular(12),
              onTap: () => _toggleProjectCollapsed(project, isCollapsed),
              child: Padding(
                padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 4),
                child: Row(
                  children: [
                    Icon(
                      isCollapsed ? Icons.keyboard_arrow_right_rounded : Icons.keyboard_arrow_down_rounded,
                      size: 20,
                      color: AppTheme.textSecondary,
                    ),
                    const SizedBox(width: 4),
                    Icon(
                      isCollapsed ? Icons.folder_outlined : Icons.folder_open_rounded,
                      size: 18,
                      color: AppTheme.primaryBlue,
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        projectName,
                        style: Theme.of(context).textTheme.titleSmall?.copyWith(fontWeight: FontWeight.w800),
                      ),
                    ),
                    if (activeInProject > 0) ...[
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                        decoration: BoxDecoration(
                          color: AppTheme.primaryBlueLight,
                          borderRadius: BorderRadius.circular(999),
                        ),
                        child: Text(
                          '$activeInProject 运行中',
                          style: const TextStyle(
                            color: AppTheme.primaryBlue,
                            fontSize: 11,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                      ),
                      const SizedBox(width: 8),
                    ],
                    Text('${projectItems.length}', style: Theme.of(context).textTheme.bodySmall),
                  ],
                ),
              ),
            ),
          ),
          if (!isCollapsed) ...[
            for (final item in initialItems)
              _buildSessionCard((task: item.task, session: item.session!)),
            if (hasMore)
              Padding(
                padding: const EdgeInsets.only(bottom: 14),
                child: Align(
                  alignment: Alignment.centerLeft,
                  child: TextButton.icon(
                    key: ValueKey('code_hub_project_more_${project.id}'),
                    onPressed: () {
                      final expanding = !showAllInProject;
                      setState(() {
                        if (showAllInProject) {
                          _expandedMoreProjectIds.remove(project.id);
                        } else {
                          _expandedMoreProjectIds.add(project.id);
                        }
                      });
                      if (expanding) {
                        unawaited(
                          ref.read(aiWorkspaceControllerProvider.notifier).refreshSessionsAndWorkspace(recentOnly: false),
                        );
                      }
                    },
                    icon: Icon(showAllInProject ? Icons.expand_less_rounded : Icons.expand_more_rounded, size: 18),
                    label: Text(showAllInProject ? '收起较早任务' : '展开其余 $hiddenCount 个任务'),
                  ),
                ),
              ),
            if (showAllInProject) ...[
              for (final item in remainingItems)
                _buildSessionCard((task: item.task, session: item.session!)),
              if (remainingItems.isNotEmpty)
                Padding(
                  padding: const EdgeInsets.only(bottom: 14),
                  child: Align(
                    alignment: Alignment.centerLeft,
                    child: TextButton.icon(
                      key: ValueKey('code_hub_project_collapse_bottom_${project.id}'),
                      onPressed: () => setState(() => _expandedMoreProjectIds.remove(project.id)),
                      icon: const Icon(Icons.expand_less_rounded, size: 18),
                      label: const Text('收起较早任务'),
                    ),
                  ),
                ),
            ],
          ],
          const SizedBox(height: 4),
        ];
      }).toList();
    }

    final recentItems = taskItems.isNotEmpty
        ? _sortProjectItems(taskItems).map((e) => (task: e.task as CodeTask?, session: e.session!)).toList()
        : (List<AiDevSession>.from(state.sessions)
                ..sort((a, b) {
                  final aAct = _isActive(a), bAct = _isActive(b);
                  if (aAct != bAct) return aAct ? -1 : 1;
                  final aTime = a.updatedAt ?? a.createdAt ?? DateTime.fromMillisecondsSinceEpoch(0);
                  final bTime = b.updatedAt ?? b.createdAt ?? DateTime.fromMillisecondsSinceEpoch(0);
                  return bTime.compareTo(aTime);
                }))
              .map((s) => (task: null as CodeTask?, session: s))
              .toList();
    final now = DateTime.now();
    final hotCount = recentItems.where((item) => _isHotItem(item.session, item.task, now)).length;
    final topLimit = hotCount > defaultVisibleTasksPerProject ? hotCount : defaultVisibleTasksPerProject;
    final hasMore = recentItems.length > topLimit;
    final hiddenSessions = recentItems.length - topLimit;
    final initialItems = recentItems.take(topLimit).toList();
    final remainingItems = _flatSessionsExpanded
        ? recentItems.skip(topLimit).toList()
        : const <({CodeTask? task, AiDevSession session})>[];

    return [
      for (final item in initialItems)
        _buildSessionCard(
          item,
          projectName: _projectName(state.projects, item.task?.projectId ?? item.session.projectId),
        ),
      if (hasMore)
        Padding(
          padding: const EdgeInsets.only(bottom: 14),
          child: Align(
            alignment: Alignment.centerLeft,
            child: TextButton.icon(
              key: const ValueKey('code_hub_flat_sessions_toggle_inline'),
              onPressed: () {
                final expanding = !_flatSessionsExpanded;
                setState(() => _flatSessionsExpanded = expanding);
                if (expanding) {
                  unawaited(
                    ref.read(aiWorkspaceControllerProvider.notifier).refreshSessionsAndWorkspace(recentOnly: false),
                  );
                }
              },
              icon: Icon(_flatSessionsExpanded ? Icons.expand_less_rounded : Icons.expand_more_rounded, size: 18),
              label: Text(_flatSessionsExpanded ? '收起较早任务' : '展开其余 $hiddenSessions 个任务'),
            ),
          ),
        ),
      if (_flatSessionsExpanded) ...[
        for (final item in remainingItems)
          _buildSessionCard(
            item,
            projectName: _projectName(state.projects, item.task?.projectId ?? item.session.projectId),
          ),
        if (remainingItems.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(bottom: 14),
            child: Align(
              alignment: Alignment.centerLeft,
              child: TextButton.icon(
                key: const ValueKey('code_hub_flat_sessions_toggle_bottom'),
                onPressed: () => setState(() => _flatSessionsExpanded = false),
                icon: const Icon(Icons.expand_less_rounded, size: 18),
                label: const Text('收起较早任务'),
              ),
            ),
          ),
      ],
    ];
  }

  Widget _buildSessionCard(
    ({CodeTask? task, AiDevSession session}) item, {
    String projectName = '',
  }) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 14),
      child: CodeSessionListCard(
        session: item.session,
        task: item.task,
        projectName: projectName,
        onTap: () => _openSession(item.session),
        onOpenSubagent: _openSession,
        onOpenTaskDetail: () => _openTaskDetail(item.session, item.task),
      ),
    );
  }

  String _projectName(List<CodeProject> projects, int projectId) {
    for (final project in projects) {
      if (project.id == projectId) return project.name;
    }
    return '';
  }

  bool _isActive(AiDevSession session) {
    final stage = session.currentStage.toLowerCase();
    final status = session.status.toLowerCase();
    return stage == 'executing' ||
        stage == 'running' ||
        stage == 'instruction_queued' ||
        stage == 'awaiting_approval' ||
        status == 'running' ||
        status == 'busy' ||
        session.runningSubagentsCount > 0;
  }

  bool _isHotItem(AiDevSession session, CodeTask? task, DateTime now) {
    if (_isActive(session) ||
        task?.status == 'running' ||
        task?.status == 'awaiting_approval') {
      return true;
    }
    final ts = task?.updatedAt ?? session.updatedAt ?? session.createdAt;
    return isRecentTimestamp(ts, now: now, window: defaultRecentTaskWindow);
  }
}
