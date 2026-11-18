import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/l10n/l10n.dart';
import 'package:hoxi/core/storage/storage_service.dart';
import 'package:hoxi/features/ai_workspace/models/ai_dev_session.dart';
import 'package:hoxi/features/ai_workspace/models/code_task.dart';
import 'package:hoxi/shared/models/task_attention.dart';
import 'package:hoxi/features/ai_workspace/presentation/controllers/ai_workspace_controller.dart';
import 'package:hoxi/features/ai_workspace/presentation/controllers/code_attention_controller.dart';
import 'package:hoxi/features/ai_workspace/presentation/screens/code_hub_screen.dart';
import 'package:hoxi/features/ai_workspace/presentation/widgets/code_hub_cards.dart';
import 'package:shared_preferences/shared_preferences.dart';

AiDevSession _makeSession(int id, int projectId, String title) {
  return AiDevSession(
    id: id,
    userId: 1,
    projectId: projectId,
    title: title,
    agentName: 'opencode',
    workDir: '/path/to/project',
    sourceWorkDir: '/path/to/project',
    worktreeBranch: 'main',
    isolationMode: 'worktree',
    status: 'idle',
    currentStage: 'idle',
    approvalPolicy: 'manual',
    lastTaskId: id,
    currentTaskTitle: title,
    createdAt: DateTime.utc(2026, 1, 1),
    updatedAt: DateTime.utc(2026, 1, 1, 12, id),
    lastInstructionAt: DateTime.utc(2026, 1, 1, 12, id),
  );
}

CodeTask _makeTask(int id, int projectId, String title) {
  return CodeTask(
    id: id,
    sessionId: id,
    projectId: projectId,
    title: title,
    agentName: 'opencode',
    workDir: '/path/to/project',
    status: 'idle',
    createdAt: DateTime.utc(2026, 1, 1),
    updatedAt: DateTime.utc(2026, 1, 1, 12, id),
  );
}

class _StaticAiWorkspaceController extends AiWorkspaceController {
  final AiWorkspaceState _initial;

  _StaticAiWorkspaceController(this._initial);

  @override
  AiWorkspaceState build() => _initial;

  @override
  Future<void> loadWorkspace({bool refreshAttention = true}) async {}
}

class _FakeAttentionController extends CodeAttentionController {
  final List<TaskAttention> _initial;
  final List<String> executedTypes = [];

  _FakeAttentionController(this._initial);

  @override
  CodeAttentionState build() =>
      CodeAttentionState(isLoading: false, items: _initial);

  @override
  Future<void> load() async {}

  @override
  Future<bool> execute(
    TaskAttention attention,
    TaskAttentionAction action,
  ) async {
    executedTypes.add('${attention.id}:${action.type}');
    state = state.copyWith(
      items: state.items.where((i) => i.id != attention.id).toList(),
    );
    return true;
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() async {
    SharedPreferences.setMockInitialValues({});
    FlutterSecureStorage.setMockInitialValues({});
    await StorageService.init();
  });

  testWidgets(
    'expanding tasks keeps a collapse button in the original place and at the bottom, and collapsing restores state',
    (tester) async {
      tester.view.physicalSize = const Size(800, 2000);
      tester.view.devicePixelRatio = 1.0;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);

      const project = CodeProject(
        id: 1,
        name: '测试项目',
        description: '项目描述',
        workDir: '/path/to/project',
        sourceDirs: ['/path/to/project'],
      );

      final tasks = List.generate(
        6,
        (i) => _makeTask(i + 1, 1, '任务 ${i + 1}'),
      );
      final sessions = List.generate(
        6,
        (i) => _makeSession(i + 1, 1, '任务 ${i + 1}'),
      );

      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            aiWorkspaceControllerProvider.overrideWith(
              () => _StaticAiWorkspaceController(
                AiWorkspaceState(
                  projects: const [project],
                  tasks: tasks,
                  sessions: sessions,
                  isLoading: false,
                ),
              ),
            ),
          ],
          child: MaterialApp(
            locale: const Locale('zh'),
            localizationsDelegates: AppLocalizations.localizationsDelegates,
            supportedLocales: AppLocalizations.supportedLocales,
            home: const Scaffold(body: CodeHubScreen(showAppBar: false)),
          ),
        ),
      );
      await tester.pumpAndSettle();

      // 默认选中「最近任务」Tab，只展示前 3 个任务卡片，且卡片展示所属项目名
      expect(find.byType(CodeSessionListCard), findsNWidgets(3));
      expect(find.text('测试项目'), findsNWidgets(3));

      // 在「最近任务」Tab 下可通过 inline 按钮展开其余 3 个任务
      final flatExpandBtn = find.byKey(
        const ValueKey('code_hub_flat_sessions_toggle_inline'),
      );
      expect(flatExpandBtn, findsOneWidget);
      expect(find.text('展开其余 3 个任务'), findsOneWidget);

      // 切换到「项目」Tab，验证按项目分组与展开/收起逻辑
      final projectsTab = find.byKey(const ValueKey('code_hub_tab_projects'));
      expect(projectsTab, findsOneWidget);
      await tester.tap(projectsTab);
      await tester.pumpAndSettle();

      // 默认只展示前 3 个任务卡片
      expect(find.byType(CodeSessionListCard), findsNWidgets(3));

      // 原位置展示「展开其余 3 个任务」
      final expandBtn = find.byKey(const ValueKey('code_hub_project_more_1'));
      expect(expandBtn, findsOneWidget);
      expect(find.text('展开其余 3 个任务'), findsOneWidget);

      // 点击展开
      await tester.tap(expandBtn);
      await tester.pumpAndSettle();

      // 现在展开了所有 6 个任务
      expect(find.byType(CodeSessionListCard), findsNWidgets(6));

      // 在原地方（前 3 个卡片下方）应该有收起按钮！
      expect(find.text('收起较早任务'), findsNWidgets(2)); // 原地方 1 个 + 底部 1 个
      expect(expandBtn, findsOneWidget); // 原地方的按钮变成「收起较早任务」
      expect(
        find.byKey(const ValueKey('code_hub_project_collapse_bottom_1')),
        findsOneWidget,
      ); // 底部收起按钮

      // 点击原地方的收起按钮
      await tester.tap(expandBtn);
      await tester.pumpAndSettle();

      // 任务重新收起到 3 个，原地方按钮变回「展开其余 3 个任务」
      expect(find.byType(CodeSessionListCard), findsNWidgets(3));
      expect(find.text('展开其余 3 个任务'), findsOneWidget);
      expect(find.text('收起较早任务'), findsNothing);

      // 再次展开并点击底部的收起按钮
      await tester.tap(expandBtn);
      await tester.pumpAndSettle();
      expect(find.byType(CodeSessionListCard), findsNWidgets(6));

      final bottomCollapseBtn = find.byKey(
        const ValueKey('code_hub_project_collapse_bottom_1'),
      );
      await tester.tap(bottomCollapseBtn);
      await tester.pumpAndSettle();

      // 依然正常收起
      expect(find.byType(CodeSessionListCard), findsNWidgets(3));
      expect(find.text('展开其余 3 个任务'), findsOneWidget);
    },
  );

  testWidgets(
    'CodeSessionListCard renders clean layout without workDir or folder icon',
    (WidgetTester tester) async {
      final session = _makeSession(1, 1, '重构前端导航条');
      final task = _makeTask(1, 1, '重构前端导航条');

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: CodeSessionListCard(
              session: session,
              task: task,
              projectName: 'my-project',
              onTap: () {},
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();

      // Renders task title, projectName, branch, and agent
      expect(find.text('重构前端导航条'), findsOneWidget);
      expect(find.text('my-project'), findsOneWidget);
      expect(find.text('main'), findsOneWidget);
      expect(find.text('opencode'), findsOneWidget);

      // Does NOT render workDir or folder icon
      expect(find.text('/path/to/project'), findsNothing);
      expect(find.byIcon(Icons.folder_outlined), findsNothing);
    },
  );

  testWidgets(
    'CodeHubScreen recent tab sorts active and recent tasks across projects and switches cleanly to projects tab',
    (tester) async {
      tester.view.physicalSize = const Size(800, 2000);
      tester.view.devicePixelRatio = 1.0;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);

      const projectA = CodeProject(
        id: 1,
        name: '旧项目A',
        description: '',
        workDir: '/a',
        sourceDirs: ['/a'],
      );
      const projectB = CodeProject(
        id: 2,
        name: '活跃项目B',
        description: '',
        workDir: '/b',
        sourceDirs: ['/b'],
      );

      final oldSession = _makeSession(1, 1, '旧项目任务');
      final oldTask = _makeTask(1, 1, '旧项目任务');
      final runningSession = _makeSession(2, 2, '正在运行的跨项目任务').copyWith(
        status: 'running',
        currentStage: 'running',
      );
      final runningTask = _makeTask(2, 2, '正在运行的跨项目任务').copyWith(
        status: 'running',
      );

      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            aiWorkspaceControllerProvider.overrideWith(
              () => _StaticAiWorkspaceController(
                AiWorkspaceState(
                  projects: const [projectA, projectB],
                  tasks: [oldTask, runningTask],
                  sessions: [oldSession, runningSession],
                  isLoading: false,
                ),
              ),
            ),
          ],
          child: MaterialApp(
            locale: const Locale('zh'),
            localizationsDelegates: AppLocalizations.localizationsDelegates,
            supportedLocales: AppLocalizations.supportedLocales,
            home: const Scaffold(body: CodeHubScreen(showAppBar: false)),
          ),
        ),
      );
      await tester.pumpAndSettle();

      // 「最近任务」Tab 下，活跃项目B的运行中任务排在旧项目A任务上方
      final runningY = tester.getTopLeft(find.text('正在运行的跨项目任务')).dy;
      final oldY = tester.getTopLeft(find.text('旧项目任务')).dy;
      expect(runningY, lessThan(oldY));
      expect(find.text('活跃项目B'), findsOneWidget);
      expect(find.text('旧项目A'), findsOneWidget);

      // 切换到「项目」Tab，有运行中任务的「活跃项目B」卡片也排在「旧项目A」上方
      await tester.tap(find.byKey(const ValueKey('code_hub_tab_projects')));
      await tester.pumpAndSettle();

      final projectBY = tester
          .getTopLeft(find.byKey(const ValueKey('code_hub_project_card_2')))
          .dy;
      final projectAY = tester
          .getTopLeft(find.byKey(const ValueKey('code_hub_project_card_1')))
          .dy;
      expect(projectBY, lessThan(projectAY));
    },
  );

  testWidgets(
    'CodeHubScreen 待我处理卡片直接展示允许执行与拒绝按钮并可原地操作',
    (tester) async {
      final attentionController = _FakeAttentionController([
        const TaskAttention(
          id: 'hoxi_perm:1',
          type: 'approval_pending',
          severity: 'warning',
          title: '请求执行 Bash: git push',
          summary: '在项目目录执行推送',
          sessionId: 1,
          taskId: 1,
          approvalId: 1,
          updatedAt: null,
          actions: [
            TaskAttentionAction(
              type: 'approve',
              label: '允许执行',
              method: 'POST',
              path: '/api/hoxi/permission/1/reply',
              requiresConfirmation: false,
            ),
            TaskAttentionAction(
              type: 'reject',
              label: '拒绝',
              method: 'POST',
              path: '/api/hoxi/permission/1/reply?reject=1',
              requiresConfirmation: false,
            ),
            TaskAttentionAction(
              type: 'open_session',
              label: '打开会话',
              method: '',
              path: '',
              requiresConfirmation: false,
            ),
          ],
        ),
      ]);

      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            aiWorkspaceControllerProvider.overrideWith(
              () => _StaticAiWorkspaceController(
                AiWorkspaceState(
                  projects: const [],
                  tasks: [_makeTask(1, 1, '修复构建')],
                  sessions: [_makeSession(1, 1, '修复构建')],
                  isLoading: false,
                ),
              ),
            ),
            codeAttentionControllerProvider.overrideWith(
              () => attentionController,
            ),
          ],
          child: MaterialApp(
            locale: const Locale('zh'),
            localizationsDelegates: AppLocalizations.localizationsDelegates,
            supportedLocales: AppLocalizations.supportedLocales,
            home: const Scaffold(body: CodeHubScreen(showAppBar: false)),
          ),
        ),
      );
      await tester.pumpAndSettle();

      expect(find.text('待我处理'), findsOneWidget);
      expect(find.text('请求执行 Bash: git push'), findsOneWidget);
      final approveBtn = find.byKey(
        const ValueKey('code_attention_card_action_hoxi_perm:1_approve'),
      );
      final rejectBtn = find.byKey(
        const ValueKey('code_attention_card_action_hoxi_perm:1_reject'),
      );
      expect(approveBtn, findsOneWidget);
      expect(rejectBtn, findsOneWidget);

      await tester.tap(approveBtn);
      await tester.pumpAndSettle();

      expect(attentionController.executedTypes, ['hoxi_perm:1:approve']);
    },
  );

  test(
    'formatSessionModelDisplayLabel prefers model over agentName and enforces max length',
    () {
      expect(
        formatSessionModelDisplayLabel(
          providerModel: 'anthropic/claude-sonnet-4-5',
          agentName: 'opencode',
        ),
        'claude-sonnet-4-5',
      );
      final truncated = formatSessionModelDisplayLabel(
        providerModel: 'anthropic/claude-3-7-sonnet-20250219-thinking-preview',
        agentName: 'opencode',
      );
      expect(
        truncated.runes.length,
        lessThanOrEqualTo(kMaxSessionModelDisplayChars),
      );
      expect(truncated.endsWith('…'), isTrue);
      expect(
        formatSessionModelDisplayLabel(
          providerModel: '',
          agentName: 'antigravity',
        ),
        'antigravity',
      );

      final stats = extractSessionDiffStats({
        'summary': {'additions': 42, 'deletions': 15},
      });
      expect(stats.additions, 42);
      expect(stats.deletions, 15);
    },
  );

  testWidgets(
    'CodeSessionListCard and CodeSingleProjectCard display truncated model name and diff stats without overflow on narrow screens',
    (tester) async {
      tester.view.physicalSize = const Size(320, 720);
      tester.view.devicePixelRatio = 1.0;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);

      final session = _makeSession(10, 1, '实现代码变更行数与模型名展示').copyWith(
        agentName: 'opencode',
        providerModel: 'anthropic/claude-3-7-sonnet-20250219-thinking-preview',
        diffAdditions: 128,
        diffDeletions: 34,
        status: 'running',
        currentStage: 'running',
      );
      final task = _makeTask(10, 1, '实现代码变更行数与模型名展示').copyWith(
        status: 'running',
        providerModel: session.providerModel,
        diffAdditions: 128,
        diffDeletions: 34,
      );

      await tester.pumpWidget(
        MaterialApp(
          locale: const Locale('zh'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: Scaffold(
            body: Column(
              children: [
                CodeSessionListCard(
                  session: session,
                  task: task,
                  projectName: 'hoxi-super-long-project-name',
                  onTap: () {},
                ),
                CodeSingleProjectCard(
                  projectName: 'hoxi-super-long-project-name',
                  session: session,
                  task: task,
                  onTap: () {},
                ),
              ],
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();

      expect(tester.takeException(), isNull);
      expect(find.text('opencode'), findsNothing);
      expect(
        find.text(
          formatSessionModelDisplayLabel(
            providerModel: session.providerModel,
            agentName: session.agentName,
          ),
        ),
        findsNWidgets(2),
      );
      expect(find.text('+128'), findsNWidgets(2));
      expect(find.text('-34'), findsNWidgets(2));
      expect(find.text('刚刚'), findsNothing);
    },
  );

  test(
    'formatCodeHubTaskTime hides 刚刚 and active task timestamps, formats older completed timestamps',
    () {
      final now = DateTime.utc(2026, 11, 7, 12, 0, 0);
      expect(
        formatCodeHubTaskTime(
          now.subtract(const Duration(seconds: 30)),
          stage: 'completed',
          now: now,
        ),
        isEmpty,
      );
      expect(
        formatCodeHubTaskTime(
          now.subtract(const Duration(minutes: 25)),
          stage: 'running',
          now: now,
        ),
        isEmpty,
      );
      expect(
        formatCodeHubTaskTime(
          now.subtract(const Duration(minutes: 8)),
          stage: 'completed',
          now: now,
        ),
        '8 分钟前',
      );
    },
  );
}
