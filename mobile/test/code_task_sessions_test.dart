import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/network/api_client.dart';
import 'package:hoxi/core/network/api_exception.dart';
import 'package:hoxi/core/network/api_response.dart';
import 'package:hoxi/core/storage/storage_service.dart';
import 'package:hoxi/features/ai_workspace/data/hoxi_workspace_adapter.dart';
import 'package:hoxi/features/ai_workspace/models/ai_dev_session.dart';
import 'package:hoxi/features/ai_workspace/models/code_task.dart';
import 'package:hoxi/features/ai_workspace/presentation/controllers/code_task_sessions.dart';

AiDevSession session({
  required int id,
  required int projectId,
  required int lastTaskId,
  String title = '',
  String currentTaskTitle = '',
}) {
  return AiDevSession(
    id: id,
    userId: 1,
    projectId: projectId,
    title: title.isEmpty ? 'session $id' : title,
    agentName: 'codex',
    workDir: '/work/$id',
    sourceWorkDir: '',
    worktreeBranch: '',
    isolationMode: '',
    status: 'active',
    currentStage: 'idle',
    approvalPolicy: 'safe_auto',
    lastTaskId: lastTaskId,
    currentTaskTitle: currentTaskTitle,
    createdAt: null,
    updatedAt: null,
    lastInstructionAt: null,
  );
}

CodeTask task({
  required int id,
  required int sessionId,
  required int projectId,
  String title = '',
  String agentName = 'codex',
}) {
  return CodeTask(
    id: id,
    sessionId: sessionId,
    projectId: projectId,
    title: title.isEmpty ? 'task $id' : title,
    agentName: agentName,
    workDir: '/work/$sessionId',
    status: 'completed',
    createdAt: null,
    updatedAt: null,
  );
}

void main() {
  test('aligns project history to real code tasks in task order', () {
    final sessions = [
      session(id: 1, projectId: 7, lastTaskId: 11),
      session(id: 2, projectId: 7, lastTaskId: 12),
      session(id: 3, projectId: 7, lastTaskId: 0),
      session(id: 4, projectId: 8, lastTaskId: 14),
    ];
    final tasks = [
      task(id: 12, sessionId: 2, projectId: 7),
      task(id: 11, sessionId: 1, projectId: 7),
      task(id: 14, sessionId: 4, projectId: 8),
    ];

    final result = alignCodeTaskSessions(sessions, tasks, projectId: 7);

    expect(result.map((item) => item.id), [2, 1, 3]);
  });

  test('filters terminal and stale task-to-session relationships', () {
    final sessions = [
      session(id: 1, projectId: 7, lastTaskId: 11),
      session(id: 2, projectId: 7, lastTaskId: 99),
    ];
    final tasks = [
      task(id: 11, sessionId: 1, projectId: 7, agentName: 'terminal'),
      task(id: 12, sessionId: 2, projectId: 7),
    ];

    expect(alignCodeTaskSessions(sessions, tasks, projectId: 7), isEmpty);
  });

  test('detects placeholder session titles and extracts clean prompt titles', () {
    expect(isPlaceholderSessionTitle(''), isTrue);
    expect(isPlaceholderSessionTitle('New Session'), isTrue);
    expect(isPlaceholderSessionTitle('New session - 2026-11-07T12:00:00Z'), isTrue);
    expect(isPlaceholderSessionTitle('Claude Code Session'), isTrue);
    expect(isPlaceholderSessionTitle('新会话'), isTrue);
    expect(isPlaceholderSessionTitle('开发 #42'), isTrue);
    expect(isPlaceholderSessionTitle('修复新建会话标题不自动更新'), isFalse);

    expect(
      extractSessionTitleFromPrompt(
        '@lib/main.dart\n# 修复新建会话以后标题不会自动修改的问题。順便补充单元测试',
      ),
      '修复新建会话以后标题不会自动修改的问',
    );
    expect(
      extractSessionTitleFromPrompt('```dart\nvoid main() {}\n```', hasImage: true),
      '图片指令',
    );
  });

  test('syncWorkspaceTasksWithSession and resolveSessionDisplayTitle prefer real titles over New Session', () {
    final updatedSession = session(
      id: 10,
      projectId: 1,
      lastTaskId: 10,
      title: '修复会话标题自动修改',
      currentTaskTitle: '修复会话标题自动修改',
    );
    final existingTasks = [
      task(id: 10, sessionId: 10, projectId: 1, title: 'New Session'),
    ];
    final synced = syncWorkspaceTasksWithSession(
      existingTasks,
      updatedSession,
      currentTask: const AiTaskSummary(
        id: 10,
        title: 'New Session',
        status: 'running',
        workDir: '/work/10',
        agentName: 'codex',
      ),
    );
    expect(synced.single.title, '修复会话标题自动修改');
    expect(
      resolveSessionDisplayTitle(
        session: updatedSession,
        taskTitle: 'New Session',
      ),
      '修复会话标题自动修改',
    );
  });

  test(
    'HoxiWorkspaceAdapter auto-updates newly created session title on first prompt and preserves it when PATCH returns 403',
    () async {
      final patchCalls = <({String path, Map<String, dynamic> data})>[];
      final client = _StubSessionTitleApiClient(
        onGet: (path, query) async {
          if (path == '/api/hoxi/workspaces') {
            return const {
              'workspaces': [
                {'name': 'hoxi', 'path': '/Users/me/hoxi'},
              ],
            };
          }
          if (path == '/api/hoxi/sessions') {
            // Simulate remote server still returning placeholder because PATCH was 403 over tunnel
            return const [
              {
                'id': 'ses_new_1',
                'title': 'New Session',
                'directory': '/Users/me/hoxi',
                'runtime': 'opencode',
              },
            ];
          }
          if (path == '/api/hoxi/sessions/status') {
            return const <String, dynamic>{'ses_new_1': {'type': 'busy'}};
          }
          if (path == '/api/hoxi/sessions/ses_new_1/message') {
            return const [
              {
                'info': {
                  'id': 'msg_u1',
                  'role': 'user',
                  'time': {'created': 1700000000000},
                },
                'parts': [
                  {'type': 'text', 'text': '修复新建会话后标题不自动修改'},
                ],
              },
            ];
          }
          return const <String, dynamic>{};
        },
        onPost: (path, data) async {
          if (path == '/api/hoxi/sessions') {
            return {
              'id': 'ses_new_1',
              'title': 'New Session',
              'directory': '/Users/me/hoxi',
              'runtime': 'opencode',
            };
          }
          return {'ok': true, 'instructionId': 101};
        },
        onPatch: (path, data) async {
          patchCalls.add((path: path, data: data));
          throw ApiException(code: 403, msg: 'Forbidden on tunnel');
        },
      );

      final adapter = HoxiWorkspaceAdapter(client);
      final created = await adapter.createSession(
        projectId: 1,
        executorId: 'opencode',
        approvalPolicy: 'safe_auto',
      );
      expect(created.title, 'New Session');

      final sent = await adapter.sendAiCommand(
        sessionId: created.id,
        command: '修复新建会话后标题不自动修改',
      );
      expect(sent.session.title, '修复新建会话后标题不自动修改');
      expect(sent.session.currentTaskTitle, '修复新建会话后标题不自动修改');
      expect(sent.task?.title, '修复新建会话后标题不自动修改');
      expect(patchCalls, hasLength(1));
      expect(patchCalls.single.path, '/api/hoxi/sessions/ses_new_1');
      expect(patchCalls.single.data['title'], '修复新建会话后标题不自动修改');

      final sessionsAfterPoll = await adapter.getSessions();
      expect(sessionsAfterPoll.single.title, '修复新建会话后标题不自动修改');

      final stateInfo = await adapter.getSessionState(created.id);
      expect(stateInfo.session.title, '修复新建会话后标题不自动修改');
      expect(stateInfo.currentTask?.title, '修复新建会话后标题不自动修改');
    },
  );
}

class _StubSessionTitleApiClient extends ApiClient {
  _StubSessionTitleApiClient({
    required this.onGet,
    required this.onPost,
    required this.onPatch,
  }) : super.forNode(StorageService.activeNodeContext);

  final Future<dynamic> Function(String path, Map<String, dynamic>? query) onGet;
  final Future<Map<String, dynamic>> Function(
    String path,
    Map<String, dynamic> data,
  ) onPost;
  final Future<Map<String, dynamic>> Function(
    String path,
    Map<String, dynamic> data,
  ) onPatch;

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
  Future<ApiResponse<T>> patch<T>(
    String path, {
    dynamic data,
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) async {
    final map = data is Map
        ? data.cast<String, dynamic>()
        : const <String, dynamic>{};
    final res = await onPatch(path, map);
    return ApiResponse<T>(code: 200, msg: 'ok', data: res as T?);
  }
}
