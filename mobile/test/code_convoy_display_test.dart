import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/l10n/l10n.dart';
import 'package:hoxi/core/network/api_client.dart';
import 'package:hoxi/core/network/api_response.dart';
import 'package:hoxi/core/storage/storage_service.dart';
import 'package:hoxi/features/ai_workspace/data/hoxi_workspace_adapter.dart';
import 'package:hoxi/features/ai_workspace/models/ai_dev_session.dart';
import 'package:hoxi/features/ai_workspace/models/chat_message.dart';
import 'package:hoxi/features/ai_workspace/models/code_convoy_plan.dart';
import 'package:hoxi/features/ai_workspace/presentation/widgets/ai_chat_markdown.dart';
import 'package:hoxi/features/ai_workspace/presentation/widgets/ai_chat_message_item.dart';
import 'package:hoxi/features/ai_workspace/presentation/widgets/ai_chat_timeline_extras.dart';
import 'package:hoxi/features/ai_workspace/presentation/widgets/ai_convoy_widgets.dart';
import 'package:hoxi/features/ai_workspace/presentation/widgets/code_hub_cards.dart';

void main() {
  group('Convoy title, subtask block, and adapter aggregation', () {
    test('extracts convoy meta and strips convoy/subagent title wrappers', () {
      final meta = extractConvoyMetaFromTitle(
        'Convoy · plan_17440000_ab · api-auth',
      );
      expect(meta, isNotNull);
      expect(meta!.planId, 'plan_17440000_ab');
      expect(meta.taskId, 'api-auth');
      expect(
        stripSubagentTitleSuffix(
          'Convoy · plan_17440000_ab · Implement JWT (@general subagent)',
        ),
        'Implement JWT',
      );
    });

    test('parses inline hoxi-subtasks and hoxi-convoy markdown blocks and builds completion summary prompt', () {
      const markdown = '''
我将拆分为两个并行子任务执行：

```hoxi-subtasks
{
  "title": "前后端联调编队",
  "concurrency": 2,
  "tasks": [
    {"id": "backend-api", "prompt": "实现编队接口"},
    {"id": "client-ui", "prompt": "实现客户端编队卡片", "dependsOn": ["backend-api"]}
  ]
}
```

请确认。
''';
      final segments = splitMarkdownWithConvoyBlocks(markdown);
      expect(segments, hasLength(3));
      expect(segments[0].markdown, contains('我将拆分为两个并行子任务执行'));
      final plan = segments[1].convoyPlan;
      expect(plan, isNotNull);
      expect(plan!.title, '前后端联调编队');
      expect(plan.concurrency, 2);
      expect(plan.tasks, hasLength(2));
      expect(plan.tasks[0].id, 'backend-api');
      expect(plan.tasks[1].id, 'client-ui');
      expect(plan.tasks[1].dependsOn, equals(['backend-api']));

      final summaryPrompt = buildConvoyCompletionSummaryPrompt(
        const CodeConvoyPlan(
          id: 'plan_1',
          parentSessionId: 100,
          title: '前后端联调编队',
          status: 'succeeded',
          tasks: [
            CodeConvoyTaskItem(
              id: 'backend-api',
              prompt: '实现编队接口',
              status: 'succeeded',
              summary: '已完成 /api/hoxi/orchestration/plans 路由',
              diffAdditions: 80,
              diffDeletions: 4,
            ),
          ],
        ),
      );
      expect(summaryPrompt, contains('前后端联调编队'));
      expect(summaryPrompt, contains('backend-api'));
      expect(summaryPrompt, contains('已完成 /api/hoxi/orchestration/plans 路由'));
    });

    test(
      'HoxiWorkspaceAdapter groups child convoy sessions under root session and reconstructs convoy plans when orchestration API is unreachable',
      () async {
        final client = _StubHoxiApiClient(
          onGet: (path, query) async {
            if (path == '/api/hoxi/workspaces') {
              return const {
                'workspaces': [
                  {'name': 'hoxi', 'path': '/Users/me/hoxi'},
                ],
              };
            }
            if (path == '/api/hoxi/sessions') {
              return const [
                {
                  'id': 'ses_root',
                  'title': '升级编队显示',
                  'directory': '/Users/me/hoxi',
                  'time': {'created': 1700000000000, 'updated': 1700000005000},
                },
                {
                  'id': 'ses_convoy_1',
                  'parentID': 'ses_root',
                  'title': 'Convoy · plan_174400_xy · task-models',
                  'directory': '/Users/me/hoxi/.hoxi/worktrees/ses_convoy_1',
                  'sandboxMode': 'worktree',
                  'diffAdditions': 42,
                  'diffDeletions': 5,
                  'time': {'created': 1700000001000, 'updated': 1700000003000},
                },
                {
                  'id': 'ses_convoy_2',
                  'parentID': 'ses_root',
                  'title': 'Convoy · plan_174400_xy · task-ui',
                  'directory': '/Users/me/hoxi/.hoxi/worktrees/ses_convoy_2',
                  'sandboxMode': 'worktree',
                  'status': 'busy',
                  'diffAdditions': 88,
                  'diffDeletions': 12,
                  'time': {'created': 1700000002000, 'updated': 1700000004000},
                },
              ];
            }
            if (path == '/api/hoxi/sessions/status') {
              return const {
                'ses_convoy_2': {'type': 'busy'},
              };
            }
            if (path == '/api/hoxi/orchestration/plans') {
              throw DioException(
                requestOptions: RequestOptions(path: path),
                response: Response(
                  requestOptions: RequestOptions(path: path),
                  statusCode: 403,
                ),
              );
            }
            if (path == '/api/hoxi/sessions/ses_root/message') {
              return const [
                {
                  'info': {'id': 'u1', 'role': 'user'},
                  'parts': [
                    {'type': 'text', 'text': '帮我做显示编队'},
                  ],
                },
                {
                  'info': {'id': 'a1', 'role': 'assistant'},
                  'parts': [
                    {
                      'type': 'text',
                      'text': '''```hoxi-subtasks
{
  "title": "客户端编队显示",
  "tasks": [
    {"id": "task-models", "prompt": "补充数据模型与适配器"},
    {"id": "task-ui", "prompt": "补充编队概览卡片与子会话横幅", "dependsOn": ["task-models"]}
  ]
}
```''',
                    },
                  ],
                },
              ];
            }
            if (path == '/api/hoxi/sessions/ses_convoy_1/message') {
              return const [
                {
                  'info': {'id': 'u_c1', 'role': 'user'},
                  'parts': [
                    {'type': 'text', 'text': '补充数据模型与适配器'},
                  ],
                },
              ];
            }
            return const <String, dynamic>{};
          },
        );

        final adapter = HoxiWorkspaceAdapter(client);
        final rootSessions = await adapter.getSessions();
        expect(rootSessions, hasLength(1));

        final root = rootSessions.single;
        expect(root.title, '升级编队显示');
        expect(root.subagents, hasLength(2));
        expect(root.runningSubagentsCount, 1);
        expect(root.subagents[0].isChildSession, isTrue);
        expect(root.subagents[0].isConvoyTask, isTrue);
        expect(root.subagents[0].convoyPlanId, 'plan_174400_xy');
        expect(root.subagents[0].convoyTaskId, 'task-models');

        final rootState = await adapter.getSessionState(root.id);
        expect(rootState.convoyPlans, hasLength(1));
        final plan = rootState.convoyPlans.single;
        expect(plan.id, 'plan_174400_xy');
        expect(plan.title, '客户端编队显示');
        expect(plan.status, 'running');
        expect(plan.completedCount, 1);
        expect(plan.runningCount, 1);
        expect(plan.tasks, hasLength(2));
        expect(plan.tasks[0].id, 'task-models');
        expect(plan.tasks[0].status, 'succeeded');
        expect(plan.tasks[0].prompt, '补充数据模型与适配器');
        expect(plan.tasks[1].id, 'task-ui');
        expect(plan.tasks[1].status, 'running');
        expect(plan.tasks[1].dependsOn, equals(['task-models']));

        final childState = await adapter.getSessionState(
          root.subagents.first.id,
        );
        expect(childState.session.id, root.subagents.first.id);
        expect(childState.session.parentSessionId, root.id);
        expect(childState.convoyPlans, hasLength(1));
      },
    );
  });

  group('Convoy, Subagent, and PC-aligned Timeline UI widgets', () {
    testWidgets(
      'AiConvoyOverviewCard, AiSubagentSessionBanner, AiChatMarkdown (with code block copy/wrap & convoy actions), and CodeSessionListCard work end-to-end',
      (tester) async {
        tester.binding.platformDispatcher.localeTestValue = const Locale('zh');
        addTearDown(tester.binding.platformDispatcher.clearLocaleTestValue);

        int? openedSessionId;
        AiDevSession? openedSubagent;
        String? cancelledPlanId;
        CodeConvoyPlan? startedSpec;

        final child1 = AiDevSession.fromJson(const {
          'id': 201,
          'parentSessionId': 100,
          'convoyPlanId': 'plan_1',
          'convoyTaskId': 'task-api',
          'title': 'Convoy · plan_1 · task-api',
          'status': 'active',
          'currentStage': 'idle',
          'diffAdditions': 24,
          'diffDeletions': 3,
        });
        final child2 = AiDevSession.fromJson(const {
          'id': 202,
          'parentSessionId': 100,
          'convoyPlanId': 'plan_1',
          'convoyTaskId': 'task-ui',
          'title': 'Convoy · plan_1 · task-ui',
          'status': 'running',
          'currentStage': 'running',
          'diffAdditions': 56,
          'diffDeletions': 8,
        });
        final rootSession = AiDevSession.fromJson({
          'id': 100,
          'title': '主会话：编队重构',
          'status': 'running',
          'currentStage': 'running',
        }).copyWith(subagents: [child1, child2]);

        const plan = CodeConvoyPlan(
          id: 'plan_1',
          parentSessionId: 100,
          title: '并行重构编队',
          status: 'running',
          tasks: [
            CodeConvoyTaskItem(
              id: 'task-api',
              prompt: '改造接口适配器',
              status: 'succeeded',
              sessionId: 201,
              diffAdditions: 24,
              diffDeletions: 3,
            ),
            CodeConvoyTaskItem(
              id: 'task-ui',
              prompt: '增加编队面板组件',
              dependsOn: ['task-api'],
              status: 'running',
              sessionId: 202,
              diffAdditions: 56,
              diffDeletions: 8,
            ),
          ],
        );

        await tester.pumpWidget(
          MaterialApp(
            locale: const Locale('zh'),
            localizationsDelegates: AppLocalizations.localizationsDelegates,
            supportedLocales: AppLocalizations.supportedLocales,
            home: Scaffold(
              body: SingleChildScrollView(
                child: Column(
                  children: [
                    AiSubagentSessionBanner(
                      session: child2,
                      siblings: [child1, child2],
                      onOpenSession: (id) => openedSessionId = id,
                    ),
                    AiConvoyOverviewCard(
                      convoyPlans: const [plan],
                      subagents: const [],
                      sessionSubagents: [child1, child2],
                      onOpenSession: (id) => openedSessionId = id,
                      onCancelConvoyPlan: (id) {
                        cancelledPlanId = id;
                      },
                    ),
                    AiChatMarkdown(
                      text: '''```hoxi-convoy
{"title":"未启动编队","tasks":[{"id":"task-new","prompt":"新增模块"}]}
```

```dart
void main() => print('hello');
```''',
                      convoyPlans: const [plan],
                      onOpenSession: (id) => openedSessionId = id,
                      onStartConvoyPlan: (spec) {
                        startedSpec = spec;
                      },
                    ),
                    CodeSessionListCard(
                      session: rootSession,
                      onTap: () {},
                      onOpenSubagent: (sub) => openedSubagent = sub,
                    ),
                  ],
                ),
              ),
            ),
          ),
        );

        expect(find.byKey(const ValueKey('ai_subagent_session_banner')), findsOneWidget);
        expect(find.text('返回主会话'), findsOneWidget);
        expect(find.text('2/2'), findsOneWidget);

        await tester.tap(find.byKey(const ValueKey('ai_subagent_back_to_parent')));
        expect(openedSessionId, 100);

        expect(find.byKey(const ValueKey('ai_convoy_overview_card')), findsOneWidget);
        expect(find.text('多 Agent 编队'), findsOneWidget);
        expect(find.text('并行重构编队'), findsWidgets);
        expect(find.text('依赖: task-api'), findsWidgets);

        await tester.tap(find.byKey(const ValueKey('convoy_cancel_btn_plan_1')));
        await tester.pump();
        expect(cancelledPlanId, 'plan_1');

        await tester.tap(find.text('启动编队 (1)'));
        await tester.pump();
        expect(startedSpec?.title, '未启动编队');

        expect(find.text('dart'), findsOneWidget);
        expect(find.byKey(const ValueKey('ai_code_block_copy_dart')), findsOneWidget);

        await tester.tap(find.byKey(const ValueKey('convoy_task_row_task-api')).first);
        expect(openedSessionId, 201);

        expect(find.text('编队 · 2 个子任务'), findsOneWidget);
        final subagentItemFinder = find.byKey(
          const ValueKey('session_subagent_item_202'),
        );
        await tester.ensureVisible(subagentItemFinder);
        await tester.pump();
        await tester.tap(subagentItemFinder);
        expect(openedSubagent?.id, 202);
      },
    );

    testWidgets(
      'AiQueuedPromptsList, AiTranscriptSearchBar, AiScrollToBottomFab, and AiChatMessageItem (user edit & subagent tool step jump) work',
      (tester) async {
        tester.binding.platformDispatcher.localeTestValue = const Locale('zh');
        addTearDown(tester.binding.platformDispatcher.clearLocaleTestValue);

        String? sentNowId;
        String? editedId;
        String? deletedId;
        String? editedUserText;
        int? jumpedSubagentSessionId;
        bool scrolledToBottom = false;
        final searchController = TextEditingController(text: '编队');

        await tester.pumpWidget(
          MaterialApp(
            locale: const Locale('zh'),
            localizationsDelegates: AppLocalizations.localizationsDelegates,
            supportedLocales: AppLocalizations.supportedLocales,
            home: Scaffold(
              body: SingleChildScrollView(
                child: Column(
                  children: [
                    AiTranscriptSearchBar(
                      controller: searchController,
                      matchCount: 3,
                      activeMatchIndex: 1,
                      onChanged: (_) {},
                      onPrev: () {},
                      onNext: () {},
                      onClose: () {},
                    ),
                    AiQueuedPromptsList(
                      items: [
                        CodeQueuedPromptItem(
                          id: 'q1',
                          sessionId: 100,
                          text: '接着补齐单元测试',
                          createdAt: DateTime(2025, 1, 1),
                        ),
                      ],
                      onSendNow: (id) => sentNowId = id,
                      onEdit: (id) => editedId = id,
                      onDelete: (id) => deletedId = id,
                    ),
                    AiScrollToBottomFab(
                      isStreaming: true,
                      onTap: () => scrolledToBottom = true,
                    ),
                    AiChatMessageItem(
                      message: ChatMessage(
                        id: 'u1',
                        text: '请帮我对齐 PC 端对话功能',
                        isUser: true,
                        timestamp: DateTime(2025, 1, 1),
                      ),
                      onEditUserMessage: (text) => editedUserText = text,
                    ),
                    AiChatMessageItem(
                      message: ChatMessage(
                        id: 'a1',
                        text: '已委派子任务处理。',
                        isUser: false,
                        timestamp: DateTime(2025, 1, 1),
                        toolSteps: const ['task · 排查接口异常'],
                      ),
                      subagents: const [
                        CodeSubagentItem(
                          sessionId: 305,
                          hoxiSessionId: 'sub_1',
                          parentSessionId: 100,
                          title: '排查接口异常',
                          fullTitle: '排查接口异常',
                          agentType: 'general',
                          status: 'running',
                        ),
                      ],
                      onOpenSession: (id) => jumpedSubagentSessionId = id,
                    ),
                  ],
                ),
              ),
            ),
          ),
        );

        expect(find.byKey(const ValueKey('ai_transcript_search_bar')), findsOneWidget);
        expect(find.text('2/3'), findsOneWidget);

        expect(find.byKey(const ValueKey('ai_queued_prompts_list')), findsOneWidget);
        expect(find.text('接着补齐单元测试'), findsOneWidget);
        await tester.tap(find.byKey(const ValueKey('queued_prompt_send_now_q1')));
        expect(sentNowId, 'q1');
        await tester.tap(find.byKey(const ValueKey('queued_prompt_edit_q1')));
        expect(editedId, 'q1');
        await tester.tap(find.byKey(const ValueKey('queued_prompt_delete_q1')));
        expect(deletedId, 'q1');

        expect(find.text('正在输出 · 回到底部'), findsOneWidget);
        await tester.tap(find.byKey(const ValueKey('ai_scroll_to_bottom_fab')));
        expect(scrolledToBottom, isTrue);

        await tester.tap(find.byKey(const ValueKey('user_msg_edit_u1')));
        expect(editedUserText, '请帮我对齐 PC 端对话功能');

        // Expand collapsed 1-step tool summary if needed or tap directly
        await tester.tap(find.text('排查接口异常'));
        expect(jumpedSubagentSessionId, 305);
      },
    );
  });
}

class _StubHoxiApiClient extends ApiClient {
  _StubHoxiApiClient({required this.onGet})
    : super.forNode(StorageService.activeNodeContext);

  final Future<dynamic> Function(String path, Map<String, dynamic>? query) onGet;

  @override
  Future<ApiResponse<T>> get<T>(
    String path, {
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) async {
    final data = await onGet(path, queryParameters);
    return ApiResponse<T>(code: 200, msg: 'ok', data: data as T?);
  }
}
