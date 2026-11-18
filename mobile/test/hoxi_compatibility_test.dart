import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/l10n/l10n.dart';
import 'package:hoxi/core/network/api_client.dart';
import 'package:hoxi/core/network/api_response.dart';
import 'package:hoxi/core/storage/storage_service.dart';
import 'package:hoxi/features/ai_workspace/data/hoxi_workspace_adapter.dart';
import 'package:hoxi/features/ai_workspace/models/ai_dev_session.dart';
import 'package:hoxi/features/ai_workspace/models/code_conversation_stream.dart';
import 'package:hoxi/features/ai_workspace/models/code_workspace_file.dart';
import 'package:hoxi/features/ai_workspace/presentation/widgets/code_delivery_card.dart';
import 'package:hoxi/features/auth/data/auth_repository.dart';
import 'package:hoxi/core/storage/server_connection.dart';

void main() {
  group('ServerConnection nodeType compatibility', () {
    test('defaults to gopanel when nodeType is omitted in legacy JSON', () {
      final conn = ServerConnection.fromJson({
        'id': 'node_1',
        'name': 'Prod Panel',
        'url': 'https://panel.example.com',
        'entrance': 'admin',
        'token': 'jwt_token',
        'lastConnectedAt': '2026-10-19T00:00:00.000',
      });
      expect(conn.nodeType, 'gopanel');
      expect(conn.isGoPanel, isTrue);
      expect(conn.isHoxi, isFalse);
    });

    test('persists and restores hoxi nodeType', () {
      final conn = ServerConnection(
        id: 'hoxi_1',
        name: 'Hoxi Mac Studio',
        url: 'http://192.168.1.50:4096',
        entrance: '',
        token: '',
        nodeType: 'hoxi',
        lastConnectedAt: DateTime(2026, 10, 19),
      );
      final restored = ServerConnection.fromJson(conn.toJson());
      expect(restored.nodeType, 'hoxi');
      expect(restored.isHoxi, isTrue);
      expect(restored.isGoPanel, isFalse);
    });
  });

  group('Hoxi SSE frame parsing', () {
    test('maps Hoxi message.part.delta and session.status to stream events', () {
      final parser = CodeConversationStreamParser();
      final events = parser.add(
        'data: {"type":"message.part.delta","properties":{"field":"text","delta":"Hello Hoxi"}}\n\n'
        'data: {"type":"message.part.updated","properties":{"part":{"type":"tool","tool":"bash","state":{"status":"running","title":"Run tests"}}}}\n\n'
        'data: {"type":"permission.asked","properties":{"id":"perm_1","title":"Allow rm -rf?"}}\n\n'
        'data: {"type":"permission.replied","properties":{"id":"perm_1","reply":"once"}}\n\n'
        'data: {"type":"session.status","properties":{"status":{"type":"idle"}}}\n\n',
      );
      expect(events, hasLength(5));
      expect(events[0].type, 'delta');
      expect(events[0].content, 'Hello Hoxi');
      expect(events[1].type, 'activity');
      expect(events[1].activity, '正在执行 bash');
      expect(events[2].type, 'status');
      expect(events[2].status, 'awaiting_approval');
      expect(events[2].activityKind, 'approval_waiting');
      expect(events[3].type, 'status');
      expect(events[3].status, 'running');
      expect(events[3].activityKind, 'approval_resolved');
      expect(events[4].type, 'done');
      expect(events[4].status, 'completed');
    });

    test('ignores desktop-only status snapshots and heartbeats', () {
      final parser = CodeConversationStreamParser();
      final events = parser.add(
        'data: {"type":"hoxi.status.snapshot","properties":{"scope":"managed","busy":["ses_hoxi_a"]}}\n\n'
        'data: {"type":"server.heartbeat","properties":{}}\n\n'
        'data: {"type":"message.part.delta","properties":{"delta":"still streaming"}}\n\n',
      );
      expect(events, hasLength(1));
      expect(events.single.type, 'delta');
      expect(events.single.content, 'still streaming');
    });

    test(
      'filters out other sessions and ignores non-terminal OpenCode events',
      () {
        final parser = CodeConversationStreamParser(
          targetSessionId: 'ses_target',
        );
        final events = parser.add(
          'data: {"type":"message.part.delta","properties":{"sessionID":"ses_other","delta":"wrong session"}}\n\n'
          'data: {"type":"message.updated","properties":{"info":{"sessionID":"ses_target","role":"assistant"}}}\n\n'
          'data: {"type":"session.updated","properties":{"info":{"id":"ses_target"}}}\n\n'
          'data: {"type":"message.part.updated","properties":{"part":{"sessionID":"ses_target","type":"text","text":"partial"}}}\n\n'
          'data: {"type":"message.part.delta","properties":{"sessionID":"ses_target","delta":"right session"}}\n\n'
          'data: {"type":"session.error","properties":{"sessionID":"ses_target","error":{"data":{"message":"rate limit exceeded"}}}}\n\n',
        );
        expect(events, hasLength(2));
        expect(events[0].type, 'delta');
        expect(events[0].content, 'right session');
        expect(events[1].type, 'error');
        expect(events[1].message, 'rate limit exceeded');
      },
    );
  });

  group('Hoxi auth header probe builder', () {
    test('builds Basic auth when Basic token is provided', () {
      final opts = buildHoxiProbeOptions(
        entrance: '',
        token: 'Basic b3BlbmNvZGU6c2VjcmV0',
      );
      expect(opts.headers?['Authorization'], 'Basic b3BlbmNvZGU6c2VjcmV0');
      expect(opts.extra?['allowRawJson'], isTrue);
    });
  });

  group('Hoxi Code detail pages adapter parity', () {
    test(
      'maps Hoxi session turns into history messages, execution runs, run details, and retries failed instruction',
      () async {
        final postCalls = <({String path, Map<String, dynamic> data})>[];
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
                  'id': 'ses_hoxi_42',
                  'title': 'Detail Page Parity',
                  'directory': '/Users/me/hoxi',
                  'runtime': 'opencode',
                  'modelID': 'claude-sonnet-4-5',
                },
              ];
            }
            if (path == '/api/hoxi/sessions/status') {
              return const <String, dynamic>{};
            }
            if (path == '/api/hoxi/sessions/ses_hoxi_42/message') {
              return const [
                {
                  'info': {
                    'id': 'turn_u_1',
                    'role': 'user',
                    'time': {'created': 1700000000000},
                  },
                  'parts': [
                    {'type': 'text', 'text': 'Check git status'},
                  ],
                },
                {
                  'info': {
                    'id': 'turn_a_1',
                    'role': 'assistant',
                    'modelID': 'claude-sonnet-4-5',
                    'providerID': 'anthropic',
                    'time': {
                      'created': 1700000000200,
                      'completed': 1700000001700,
                    },
                    'tokens': {
                      'input': 120,
                      'output': 45,
                      'reasoning': 15,
                      'cache': {'read': 30},
                    },
                  },
                  'parts': [
                    {
                      'type': 'tool',
                      'tool': 'bash',
                      'state': {'status': 'completed', 'title': 'git status'},
                    },
                    {'type': 'text', 'text': 'Working tree is clean.'},
                  ],
                },
                {
                  'info': {
                    'id': 'turn_u_2',
                    'role': 'user',
                    'time': {'created': 1700000005000},
                  },
                  'parts': [
                    {'type': 'text', 'text': 'Run integration suite'},
                  ],
                },
                {
                  'info': {
                    'id': 'turn_a_2',
                    'role': 'assistant',
                    'time': {
                      'created': 1700000005100,
                      'completed': 1700000006000,
                    },
                    'error': {'message': 'runner timed out'},
                  },
                  'parts': [
                    {'type': 'text', 'text': 'Suite aborted.'},
                  ],
                },
              ];
            }
            return const <String, dynamic>{};
          },
          onPost: (path, data) async {
            postCalls.add((path: path, data: data));
            return {'ok': true, 'instructionId': 99};
          },
        );

        final adapter = HoxiWorkspaceAdapter(client);
        final sessions = await adapter.getSessions();
        final sessionId = sessions.single.id;

        final history = await adapter.getSessionHistory(
          sessionId,
          page: 1,
          limit: 10,
        );
        expect(history.messages, hasLength(4));
        expect(history.messages.first.role, 'user');
        expect(history.messages.first.content, 'Check git status');
        expect(history.total, 2);
        expect(history.runs, hasLength(2));

        // Newest run is first (the failed turn_u_2)
        final newestRun = history.runs.first;
        expect(newestRun.prompt, 'Run integration suite');
        expect(newestRun.status, 'failed');
        expect(newestRun.errorMessage, 'runner timed out');
        expect(newestRun.canRetry, isTrue);

        // Older run has token and duration metrics
        final firstRun = history.runs.last;
        expect(firstRun.prompt, 'Check git status');
        expect(firstRun.output, 'Working tree is clean.');
        expect(firstRun.status, 'completed');
        expect(firstRun.durationMs, 1700);
        expect(firstRun.totalTokens, 210);

        final detail = await adapter.getExecutionRun(firstRun.id);
        expect(detail.id, firstRun.id);
        expect(detail.executorId, 'anthropic');
        expect(detail.model, 'claude-sonnet-4-5');
        expect(detail.inputTokens, 120);
        expect(detail.outputTokens, 45);
        expect(detail.cachedInputTokens, 30);
        expect(detail.reasoningTokens, 15);
        expect(detail.totalTokens, 210);
        expect(detail.rawOutput, contains('[bash:completed] git status'));

        // Retrying the failed instruction re-sends its prompt via prompt_async
        final retried = await adapter.retryInstruction(newestRun.instructionId);
        expect(retried.content, 'Run integration suite');
        expect(postCalls, hasLength(1));
        expect(
          postCalls.single.path,
          '/api/hoxi/sessions/ses_hoxi_42/prompt_async',
        );
        expect(postCalls.single.data['text'], 'Run integration suite');
      },
    );

    test(
      'saves session file via PUT /file/content and persists delivery job state',
      () async {
        var hasWorktreeChanges = false;
        final putCalls = <({String path, Map<String, dynamic> data})>[];
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
                  'id': 'ses_hoxi_42',
                  'title': 'File & Delivery',
                  'directory': '/Users/me/hoxi/.worktree',
                  'projectID': '/Users/me/hoxi',
                  'sandboxMode': 'worktree',
                },
              ];
            }
            if (path == '/api/hoxi/worktree/diff') {
              return hasWorktreeChanges
                  ? const [
                      {'file': 'lib/main.dart', 'additions': 2, 'deletions': 0},
                    ]
                  : const [];
            }
            return const <String, dynamic>{};
          },
          onPost: (path, data) async {
            if (path == '/api/hoxi/worktree/apply') {
              return {'ok': true, 'commitHash': 'deadbeef1234'};
            }
            return {'ok': true};
          },
          onPut: (path, data) async {
            putCalls.add((path: path, data: data));
            return {'path': data['path'], 'version': 'v2'};
          },
        );

        final adapter = HoxiWorkspaceAdapter(client);
        final sessions = await adapter.getSessions();
        final sessionId = sessions.single.id;

        const originalFile = CodeSessionFile(
          path: 'lib/main.dart',
          content: 'void main() {}',
          extension: 'dart',
          size: 14,
          version: 'v1',
        );
        final saved = await adapter.saveSessionFile(
          sessionId: sessionId,
          file: originalFile,
          content: 'void main() => runApp();',
        );
        expect(putCalls, hasLength(1));
        expect(putCalls.single.path, '/file/content');
        expect(saved.content, 'void main() => runApp();');
        expect(saved.version, 'v2');

        final job = await adapter.startDelivery(sessionId);
        expect(job.isCompleted, isTrue);
        expect(job.resultCommit, 'deadbeef1234');

        final reloadedJob = await adapter.getDelivery(sessionId);
        expect(reloadedJob?.isCompleted, isTrue);
        expect(reloadedJob?.hasUncommittedChanges, isFalse);

        hasWorktreeChanges = true;
        final dirtyJob = await adapter.getDelivery(sessionId);
        expect(dirtyJob?.hasUncommittedChanges, isTrue);
        expect(dirtyJob?.hasPendingChanges, isTrue);

        await adapter.saveGitChanges(sessionId, 'feat: follow-up commit');
        final committedJob = await adapter.getDelivery(sessionId);
        expect(committedJob?.hasPendingCommits, isTrue);
        expect(committedJob?.canDeliverPending, isTrue);
      },
    );

    testWidgets(
      'CodeDeliveryCard enables delivery for Hoxi worktree sessions without explicit branch',
      (tester) async {
        tester.binding.platformDispatcher.localeTestValue = const Locale('zh');
        addTearDown(tester.binding.platformDispatcher.clearLocaleTestValue);
        var started = 0;
        final session = AiDevSession.fromJson(const {
          'id': 77,
          'sourceWorkDir': '/Users/me/hoxi',
          'workDir': '/Users/me/hoxi/.worktree',
          'worktreeBranch': '',
          'isolationMode': 'worktree',
          'status': 'active',
        });

        await tester.pumpWidget(
          MaterialApp(
            locale: const Locale('zh'),
            localizationsDelegates: AppLocalizations.localizationsDelegates,
            supportedLocales: AppLocalizations.supportedLocales,
            home: Scaffold(
              body: CodeDeliveryCard(
                session: session,
                delivery: null,
                loading: false,
                errorMessage: null,
                onStart: () => started++,
              ),
            ),
          ),
        );

        expect(find.text('开始统一交付'), findsOneWidget);
        await tester.tap(find.text('开始统一交付'));
        expect(started, 1);
      },
    );

    test(
      'preserves providerID/modelID, resolves worktree parent sourceWorkDir, enforces plan agent for qoder, and lists files via /api/hoxi/file/list',
      () async {
        final getQueries = <String, Map<String, dynamic>?>{};
        final postCalls = <({String path, Map<String, dynamic> data})>[];
        final client = _StubHoxiApiClient(
          onGet: (path, query) async {
            getQueries[path] = query;
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
                  'id': 'ses_oc_1',
                  'title': 'OpenCode Session',
                  'directory': '/Users/me/hoxi/.hoxi/worktrees/ses_oc_1',
                  'projectID': 'ws_uuid_1234',
                  'runtime': 'opencode',
                  'providerID': 'anthropic',
                  'modelID': 'claude-sonnet-4-5',
                  'reasoningEffort': 'high',
                },
                {
                  'id': 'ses_qoder_1',
                  'title': 'Qoder Session',
                  'directory': '/Users/me/hoxi',
                  'runtime': 'qoder',
                  'modelID': 'qoder-max',
                },
              ];
            }
            if (path == '/api/hoxi/file/list') {
              return const [
                {
                  'name': 'README.md',
                  'path': 'README.md',
                  'type': 'file',
                  'size': 512,
                },
              ];
            }
            if (path == '/api/hoxi/provider/models') {
              return const {
                'providers': {
                  'anthropic': {
                    'name': 'Anthropic',
                    'models': {
                      'claude-sonnet-4-5': {'name': 'Claude Sonnet 4.5'},
                    },
                  },
                },
              };
            }
            if (path == '/api/hoxi/runner/gemini-cli/models') {
              return const {
                'available': true,
                'models': [
                  {'id': 'gemini-2.5-pro'},
                ],
              };
            }
            return const <String, dynamic>{};
          },
          onPost: (path, data) async {
            postCalls.add((path: path, data: data));
            return {'ok': true, 'instructionId': 101};
          },
        );

        final adapter = HoxiWorkspaceAdapter(client);
        final sessions = await adapter.getSessions();
        expect(sessions, hasLength(2));

        final ocSession = sessions.firstWhere((s) => s.title == 'OpenCode Session');
        expect(ocSession.providerModel, 'anthropic/claude-sonnet-4-5');
        expect(ocSession.reasoningEffort, 'high');
        expect(ocSession.sourceWorkDir, '/Users/me/hoxi');

        await adapter.sendAiCommand(
          sessionId: ocSession.id,
          command: 'Explain architecture',
        );
        expect(postCalls.last.path, '/api/hoxi/sessions/ses_oc_1/prompt_async');
        expect(postCalls.last.data['model'], {
          'providerID': 'anthropic',
          'modelID': 'claude-sonnet-4-5',
        });

        final qoderSession = sessions.firstWhere(
          (s) => s.title == 'Qoder Session',
        );
        await adapter.sendAiCommand(
          sessionId: qoderSession.id,
          command: 'Inspect repo',
        );
        expect(
          postCalls.last.path,
          '/api/hoxi/sessions/ses_qoder_1/prompt_async',
        );
        expect(postCalls.last.data['agent'], 'plan');

        final structure = await adapter.getSessionStructure(ocSession.id);
        expect(structure.entries, hasLength(1));
        expect(structure.entries.single.path, 'README.md');
        expect(getQueries['/api/hoxi/file/list']?['sessionID'], 'ses_oc_1');

        final executors = await adapter.getExecutors();
        final opencodeExec = executors.firstWhere((e) => e.id == 'opencode');
        expect(
          opencodeExec.modelOptions.map((o) => o.value),
          contains('anthropic/claude-sonnet-4-5'),
        );
        final geminiExec = executors.firstWhere((e) => e.id == 'gemini-cli');
        expect(geminiExec.available, isTrue);
        expect(
          geminiExec.modelOptions.map((o) => o.value),
          contains('gemini-2.5-pro'),
        );
      },
    );

    test(
      'skips archived and subagent child sessions, parses project collapsed state, and deduplicates concurrent getSessions/getTasks',
      () async {
        var sessionRequests = 0;
        final client = _StubHoxiApiClient(
          onGet: (path, query) async {
            if (path == '/api/hoxi/workspaces') {
              return const {
                'workspaces': [
                  {'name': 'main-ws', 'path': '/Users/me/main', 'collapsed': true},
                ],
              };
            }
            if (path == '/api/hoxi/sessions') {
              sessionRequests++;
              return const [
                {
                  'id': 'ses_active',
                  'title': 'Active Task',
                  'directory': '/Users/me/main',
                  'time': {'created': 1700000000000, 'updated': 1700000002000},
                },
                {
                  'id': 'ses_archived',
                  'title': 'Archived Task',
                  'directory': '/Users/me/main',
                  'time': {'created': 1700000000000, 'archived': 1700000005000},
                },
                {
                  'id': 'ses_subagent',
                  'parentID': 'ses_active',
                  'title': 'Subagent Task',
                  'directory': '/Users/me/main',
                },
              ];
            }
            return const <String, dynamic>{};
          },
          onPost: (path, data) async => const {'ok': true},
        );

        final adapter = HoxiWorkspaceAdapter(client);
        final projects = await adapter.getProjects();
        expect(projects.single.collapsed, isTrue);

        final results = await Future.wait([
          adapter.getSessions(),
          adapter.getTasks(),
        ]);
        final sessions = results[0] as List<AiDevSession>;
        expect(sessions, hasLength(1));
        expect(sessions.single.title, 'Active Task');
        expect(sessionRequests, 1);
      },
    );

    test(
      'merges multi-step assistant turns per user cycle, queries session diff endpoint, and scopes local session diffs to current session edits',
      () async {
        final requestedGetPaths = <String>[];
        final client = _StubHoxiApiClient(
          onGet: (path, query) async {
            requestedGetPaths.add(path);
            if (path == '/api/hoxi/workspaces') {
              return const {
                'workspaces': [
                  {'name': 'main-ws', 'path': '/Users/me/main'},
                ],
              };
            }
            if (path == '/api/hoxi/sessions') {
              return const [
                {
                  'id': 'ses_multistep',
                  'title': 'Multi-step Session',
                  'directory': '/Users/me/main',
                },
              ];
            }
            if (path == '/api/hoxi/sessions/ses_multistep/message') {
              return const [
                {
                  'info': {'id': 'u1', 'role': 'user'},
                  'parts': [
                    {'type': 'text', 'text': 'Check and fix config'},
                  ],
                },
                {
                  'info': {'id': 'a1_step1', 'role': 'assistant', 'durationMs': 400},
                  'parts': [
                    {'type': 'reasoning', 'text': 'Inspecting config file'},
                    {
                      'type': 'tool',
                      'tool': 'read',
                      'state': {
                        'status': 'completed',
                        'input': {'filePath': 'lib/config.dart'},
                      },
                    },
                  ],
                },
                {
                  'info': {'id': 'a1_step2', 'role': 'assistant', 'durationMs': 600},
                  'parts': [
                    {'type': 'reasoning', 'text': 'Applying fix'},
                    {
                      'type': 'tool',
                      'tool': 'edit',
                      'state': {
                        'status': 'completed',
                        'title': 'lib/config.dart',
                      },
                    },
                    {'type': 'text', 'text': 'Config fixed and verified.'},
                  ],
                },
                {
                  'info': {'id': 'u2', 'role': 'user'},
                  'parts': [
                    {'type': 'text', 'text': 'Run git status'},
                  ],
                },
                {
                  'info': {'id': 'a2_tool_only', 'role': 'assistant'},
                  'parts': [
                    {
                      'type': 'tool',
                      'tool': 'bash',
                      'state': {
                        'status': 'completed',
                        'input': {'command': 'git status -s'},
                      },
                    },
                    {
                      'type': 'tool',
                      'tool': 'todowrite',
                      'state': {
                        'status': 'completed',
                        'input': {
                          'todos': [
                            {'id': 't1', 'content': 'Update config', 'status': 'completed'},
                            {'id': 't2', 'content': 'Run git status', 'status': 'in_progress'},
                          ],
                        },
                      },
                    },
                  ],
                },
              ];
            }
            if (path == '/api/hoxi/sessions/ses_multistep/diff' ||
                path == '/api/hoxi/worktree/diff') {
              return const [
                {
                  'file': 'lib/config.dart',
                  'additions': 18,
                  'deletions': 4,
                  'patch': '@@ -1,4 +1,18 @@\n+fixed config',
                },
                {'file': 'test/config_test.dart', 'additions': 32, 'deletions': 2},
              ];
            }
            return const <String, dynamic>{};
          },
          onPost: (path, data) async => const {'ok': true},
        );

        final adapter = HoxiWorkspaceAdapter(client);
        final sessions = await adapter.getSessions();
        final sessionId = sessions.single.id;
        final history = await adapter.getSessionHistory(sessionId);

        expect(history.messages, hasLength(4));
        expect(history.messages[0].role, 'user');
        expect(history.messages[0].content, 'Check and fix config');

        final firstReply = history.messages[1];
        expect(firstReply.role, 'assistant');
        expect(firstReply.content, 'Config fixed and verified.');
        expect(firstReply.reasoning, 'Inspecting config file\n\nApplying fix');
        expect(
          firstReply.toolSteps,
          equals(['read · lib/config.dart', 'edit · lib/config.dart']),
        );
        expect(firstReply.durationMs, 1000);

        expect(history.messages[2].role, 'user');
        expect(history.messages[2].content, 'Run git status');

        final secondReply = history.messages[3];
        expect(secondReply.role, 'assistant');
        expect(secondReply.content, isEmpty);
        expect(
          secondReply.toolSteps,
          equals(['bash · git status -s', 'todowrite · 1/2 项任务']),
        );

        final stateInfo = await adapter.getSessionState(sessionId);
        // Queries session-scoped diff endpoint and excludes unrelated workspace dirty files ('test/config_test.dart').
        expect(requestedGetPaths, contains('/api/hoxi/sessions/ses_multistep/diff'));
        expect(stateInfo.changedFiles, equals(['lib/config.dart']));
        expect(stateInfo.turnChangedFiles, isEmpty);
        expect(stateInfo.turnDiffAdditions, 0);
        expect(stateInfo.diffAdditions, 18);
        expect(stateInfo.diffDeletions, 4);
        expect(stateInfo.changedFileStats, hasLength(1));
        expect(stateInfo.changedFileStats.single.path, 'lib/config.dart');
        expect(stateInfo.changedFileStats.single.additions, 18);
        expect(stateInfo.changedFileStats.single.deletions, 4);
        final diff = await adapter.getGitDiff(sessionId: sessionId, path: 'lib/config.dart');
        expect(diff.content, contains('+fixed config'));
        expect(stateInfo.todos, hasLength(2));
        expect(stateInfo.todos.first.content, 'Update config');
        expect(stateInfo.todos.first.isCompleted, isTrue);
        expect(stateInfo.todos.last.isInProgress, isTrue);
      },
    );
  });
}

class _StubHoxiApiClient extends ApiClient {
  _StubHoxiApiClient({
    required this.onGet,
    required this.onPost,
    this.onPut,
  }) : super.forNode(StorageService.activeNodeContext);

  final Future<dynamic> Function(String path, Map<String, dynamic>? query)
  onGet;
  final Future<Map<String, dynamic>> Function(
    String path,
    Map<String, dynamic> data,
  ) onPost;
  final Future<Map<String, dynamic>> Function(
    String path,
    Map<String, dynamic> data,
  )? onPut;

  @override
  Future<ApiResponse<T>> get<T>(
    String path, {
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) async {
    final data = await onGet(path, queryParameters);
    return ApiResponse<T>(code: 200, msg: 'ok', data: data as T?);
  }

  @override
  Future<ApiResponse<T>> post<T>(
    String path, {
    dynamic data,
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) async {
    final map = data is Map
        ? data.cast<String, dynamic>()
        : const <String, dynamic>{};
    final res = await onPost(path, map);
    return ApiResponse<T>(code: 200, msg: 'ok', data: res as T?);
  }

  @override
  Future<ApiResponse<T>> put<T>(
    String path, {
    dynamic data,
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) async {
    final map = data is Map
        ? data.cast<String, dynamic>()
        : const <String, dynamic>{};
    final res = await (onPut != null ? onPut!(path, map) : onPost(path, map));
    return ApiResponse<T>(code: 200, msg: 'ok', data: res as T?);
  }
}
