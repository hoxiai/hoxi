import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/network/api_client.dart';
import 'package:hoxi/core/network/api_response.dart';
import 'package:hoxi/core/storage/storage_service.dart';
import 'package:hoxi/features/ai_workspace/data/hoxi_workspace_adapter.dart';
import 'package:hoxi/features/ai_workspace/models/ai_dev_session.dart';
import 'package:hoxi/features/ai_workspace/models/code_model_catalog.dart';

class _NativeStubApiClient extends ApiClient {
  _NativeStubApiClient({
    required this.onGet,
    required this.onPost,
  }) : super.forNode(StorageService.activeNodeContext);

  final Future<dynamic> Function(String path, Map<String, dynamic>? query) onGet;
  final Future<dynamic> Function(String path, Map<String, dynamic> data) onPost;

  @override
  Future<ApiResponse<T>> get<T>(
    String path, {
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) async {
    final res = await onGet(path, queryParameters);
    return ApiResponse<T>(code: 200, msg: 'ok', data: res as T?);
  }

  @override
  Future<ApiResponse<T>> post<T>(
    String path, {
    dynamic data,
    Map<String, dynamic>? queryParameters,
    Options? options,
  }) async {
    final map = data is Map ? data.cast<String, dynamic>() : <String, dynamic>{};
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
    return ApiResponse<T>(code: 200, msg: 'ok', data: <String, dynamic>{} as T?);
  }
}

void main() {
  group('Hoxi Native runtime preservation and discovery', () {
    test(
      'continuing a desktop native session preserves runtime=native without switching to opencode (before and after getExecutors)',
      () async {
        final postCalls = <({String path, Map<String, dynamic> data})>[];
        final client = _NativeStubApiClient(
          onGet: (path, query) async {
            if (path == '/api/hoxi/workspaces') {
              return const {
                'workspaces': [
                  {'name': 'hoxi', 'path': '/Users/me/hoxi'},
                ],
              };
            }
            if (path == '/api/hoxi/system-info') {
              return const {
                'appVersion': '0.7.5',
                'nativeEngineReady': true,
                'openCodeReady': false,
                'openCodeVersion': '1.19.3',
              };
            }
            if (path == '/api/hoxi/provider/models') {
              return const {
                'providers': {
                  'subapi': {
                    'name': 'SubAPI',
                    'models': {
                      'openai-codex-gpt-6.2-sol': {
                        'name': 'openai-codex-gpt-6.2-sol',
                      },
                    },
                  },
                },
              };
            }
            if (path == '/api/hoxi/sessions') {
              return const [
                {
                  'id': 'ses_nat_desktop_1',
                  'title': 'Desktop Native Task',
                  'directory': '/Users/me/hoxi',
                  'runtime': 'native',
                  'providerID': 'subapi',
                  'modelID': 'openai-codex-gpt-6.2-sol',
                },
              ];
            }
            if (path == '/api/hoxi/sessions/status') {
              return const <String, dynamic>{};
            }
            if (path == '/api/hoxi/sessions/ses_nat_desktop_1/message') {
              return const [
                {
                  'info': {
                    'id': 'msg_u1',
                    'role': 'user',
                    'time': {'created': 1700000001000},
                  },
                  'parts': [
                    {'type': 'text', 'text': 'Check server logs'},
                  ],
                },
                {
                  'info': {
                    'id': 'msg_a1',
                    'role': 'assistant',
                    'providerID': 'subapi',
                    'modelID': 'openai-codex-gpt-6.2-sol',
                    'time': {
                      'created': 1700000002000,
                      'completed': 1700000003000,
                    },
                  },
                  'parts': [
                    {'type': 'text', 'text': 'Inspected logs.'},
                  ],
                },
              ];
            }
            return const <String, dynamic>{};
          },
          onPost: (path, data) async {
            postCalls.add((path: path, data: data));
            return const {'ok': true, 'instructionId': 7};
          },
        );

        final adapter = HoxiWorkspaceAdapter(client);

        // 1. Load session BEFORE getExecutors() has populated cachedExecutors.
        final sessions = await adapter.getSessions();
        expect(sessions, hasLength(1));
        expect(sessions.single.agentName, 'native');
        expect(sessions.single.providerModel, 'subapi/openai-codex-gpt-6.2-sol');

        // 2. Load session state & message history (must keep runtime=native).
        final state = await adapter.getSessionState(sessions.single.id);
        expect(state.session.agentName, 'native');
        expect(state.session.providerModel, 'subapi/openai-codex-gpt-6.2-sol');

        // 3. Continue session via sendAiCommand: must NOT call /runtime and must send runtime=native.
        await adapter.sendAiCommand(
          sessionId: sessions.single.id,
          command: 'Continue with the fix',
        );
        expect(
          postCalls.where((c) => c.path.endsWith('/runtime')),
          isEmpty,
        );
        expect(
          postCalls.last.path,
          '/api/hoxi/sessions/ses_nat_desktop_1/prompt_async',
        );
        expect(postCalls.last.data['runtime'], 'native');
        expect(postCalls.last.data['model'], {
          'providerID': 'subapi',
          'modelID': 'openai-codex-gpt-6.2-sol',
        });

        // 4. After getExecutors(), core engine executor must be native (not opencode).
        final executors = await adapter.getExecutors();
        final nativeExec = executors.firstWhere((e) => e.id == 'native');
        expect(nativeExec.name, 'Hoxi Native');
        expect(
          nativeExec.modelOptions.map((o) => o.value),
          contains('subapi/openai-codex-gpt-6.2-sol'),
        );
        expect(executors.any((e) => e.id == 'opencode'), isFalse);

        // 5. Refreshing and sending another follow-up still keeps runtime=native.
        final refreshed = await adapter.getSessions();
        expect(refreshed.single.agentName, 'native');
        await adapter.sendAiCommand(
          sessionId: refreshed.single.id,
          command: 'Second follow-up',
        );
        expect(
          postCalls.where((c) => c.path.endsWith('/runtime')),
          isEmpty,
        );
        expect(postCalls.last.data['runtime'], 'native');
      },
    );

    test(
      'continuing a corrupted opencode session on a daemonless native node auto-heals back to native',
      () async {
        final postCalls = <({String path, Map<String, dynamic> data})>[];
        final client = _NativeStubApiClient(
          onGet: (path, query) async {
            if (path == '/api/hoxi/workspaces') {
              return const {
                'workspaces': [
                  {'name': 'hoxi', 'path': '/Users/me/hoxi'},
                ],
              };
            }
            if (path == '/api/hoxi/system-info') {
              return const {
                'appVersion': '0.7.5',
                'nativeEngineReady': true,
                'openCodeReady': false,
                'openCodeVersion': '1.19.3',
              };
            }
            if (path == '/api/hoxi/provider/models') {
              return const {
                'providers': {
                  'subapi': {
                    'name': 'SubAPI',
                    'models': {
                      'openai-codex-gpt-6.2-sol': {
                        'name': 'openai-codex-gpt-6.2-sol',
                      },
                    },
                  },
                },
              };
            }
            if (path == '/api/hoxi/sessions') {
              return const [
                {
                  'id': 'ses_nat_corrupted',
                  'title': 'Corrupted Session',
                  'directory': '/Users/me/hoxi',
                  'runtime': 'opencode',
                  'providerID': 'subapi',
                  'modelID': 'openai-codex-gpt-6.2-sol',
                },
              ];
            }
            return const <String, dynamic>{};
          },
          onPost: (path, data) async {
            postCalls.add((path: path, data: data));
            return const {'ok': true, 'instructionId': 9};
          },
        );

        final adapter = HoxiWorkspaceAdapter(client);
        final sessions = await adapter.getSessions();
        await adapter.sendAiCommand(
          sessionId: sessions.single.id,
          command: 'Retry after update',
        );

        expect(
          postCalls.any(
            (c) =>
                c.path == '/api/hoxi/sessions/ses_nat_corrupted/runtime' &&
                c.data['runtime'] == 'native' &&
                c.data['providerID'] == 'subapi' &&
                c.data['modelID'] == 'openai-codex-gpt-6.2-sol',
          ),
          isTrue,
        );
        final promptCall = postCalls.lastWhere(
          (c) => c.path == '/api/hoxi/sessions/ses_nat_corrupted/prompt_async',
        );
        expect(promptCall.data['runtime'], 'native');
      },
    );

    test(
      'hybrid node with both nativeEngineReady and openCodeReady keeps existing opencode session while creating new sessions on native',
      () async {
        final postCalls = <({String path, Map<String, dynamic> data})>[];
        final client = _NativeStubApiClient(
          onGet: (path, query) async {
            if (path == '/api/hoxi/workspaces') {
              return const {
                'workspaces': [
                  {'name': 'hoxi', 'path': '/Users/me/hoxi'},
                ],
              };
            }
            if (path == '/api/hoxi/system-info') {
              return const {
                'appVersion': '0.7.5',
                'nativeEngineReady': true,
                'openCodeReady': true,
                'openCodeVersion': '1.19.3',
              };
            }
            if (path == '/api/hoxi/provider/models') {
              return const {
                'providers': {
                  'anthropic': {
                    'name': 'Anthropic',
                    'models': {
                      'claude-sonnet-4-6': {'name': 'Claude Sonnet 4.6'},
                    },
                  },
                },
              };
            }
            if (path == '/api/hoxi/sessions') {
              return const [
                {
                  'id': 'ses_oc_legacy',
                  'title': 'Legacy Daemon Session',
                  'directory': '/Users/me/hoxi',
                  'runtime': 'opencode',
                  'providerID': 'anthropic',
                  'modelID': 'claude-sonnet-4-6',
                },
              ];
            }
            return const <String, dynamic>{};
          },
          onPost: (path, data) async {
            postCalls.add((path: path, data: data));
            if (path == '/api/hoxi/sessions') {
              return {
                'id': 'ses_nat_new_1',
                'title': data['title'] ?? 'New Session',
                'directory': data['directory'] ?? '/Users/me/hoxi',
                'runtime': data['runtime'] ?? 'native',
                'providerID': data['providerID'] ?? '',
                'modelID': data['modelID'] ?? '',
              };
            }
            return const {'ok': true, 'instructionId': 11};
          },
        );

        final adapter = HoxiWorkspaceAdapter(client);
        await adapter.getExecutors();

        // Continuing an existing opencode daemon session stays on opencode.
        final sessions = await adapter.getSessions();
        expect(sessions.single.agentName, 'opencode');
        await adapter.sendAiCommand(
          sessionId: sessions.single.id,
          command: 'Keep running on daemon',
        );
        expect(postCalls.where((c) => c.path.endsWith('/runtime')), isEmpty);
        expect(postCalls.last.data['runtime'], 'opencode');

        // Creating a new session defaults to native.
        final created = await adapter.createSession(
          projectId: sessions.single.projectId,
          executorId: '',
          approvalPolicy: 'safe_auto',
          title: 'Fresh Native Task',
        );
        expect(created.agentName, 'native');
        final createCall = postCalls.lastWhere((c) => c.path == '/api/hoxi/sessions');
        expect(createCall.data['runtime'], 'native');
      },
    );

    test(
      'registerSessionList preserves native runtime and infers native for ses_nat_ IDs',
      () async {
        final postCalls = <({String path, Map<String, dynamic> data})>[];
        final client = _NativeStubApiClient(
          onGet: (path, query) async => const <String, dynamic>{},
          onPost: (path, data) async {
            postCalls.add((path: path, data: data));
            return const {'ok': true, 'instructionId': 12};
          },
        );

        final adapter = HoxiWorkspaceAdapter(client);
        final registered = adapter.registerSingleSession(
          const {
            'id': 'ses_nat_work_1',
            'title': 'Work App Session',
            'directory': '/Users/me/hoxi',
            'providerID': 'subapi',
            'modelID': 'openai-codex-gpt-6.2-sol',
          },
          fallbackProjectKey: 'work',
        );

        expect(registered, isNotNull);
        expect(registered!.agentName, 'native');

        await adapter.sendAiCommand(
          sessionId: registered.id,
          command: 'Continue work app task',
        );
        expect(postCalls.where((c) => c.path.endsWith('/runtime')), isEmpty);
        expect(postCalls.last.data['runtime'], 'native');
      },
    );

    test(
      'CodeModelCatalog.resolveRuntimeForModel keeps native for provider models and bare models',
      () {
        const nativeExec = CodeExecutor(
          id: 'native',
          name: 'Hoxi Native',
          description: 'Hoxi 原生 Agent 引擎',
          available: true,
          version: '0.7.5',
          reason: '',
          nativeTerminal: false,
          capabilities: ['code', 'automation'],
          approvalPolicies: ['safe_auto', 'manual', 'full_auto'],
          modelOptions: [
            CodeExecutorOption(
              value: 'subapi/openai-codex-gpt-6.2-sol',
              label: 'SubAPI · openai-codex-gpt-6.2-sol',
            ),
          ],
        );

        expect(
          CodeModelCatalog.resolveRuntimeForModel(
            model: 'subapi/openai-codex-gpt-6.2-sol',
            explicitExecutorId: 'native',
            executors: const [],
            fallbackRuntime: 'native',
          ),
          'native',
        );
        expect(
          CodeModelCatalog.resolveRuntimeForModel(
            model: 'anthropic/claude-opus-4-6',
            explicitExecutorId: null,
            executors: const [],
            fallbackRuntime: 'native',
          ),
          'native',
        );
        expect(
          CodeModelCatalog.resolveRuntimeForModel(
            model: 'subapi/unlisted-new-model',
            explicitExecutorId: null,
            executors: const [nativeExec],
            fallbackRuntime: 'antigravity',
          ),
          'native',
        );
        expect(
          CodeModelCatalog.resolveRuntimeForModel(
            model: 'auto',
            explicitExecutorId: 'native',
            executors: const [nativeExec],
            fallbackRuntime: 'native',
          ),
          'native',
        );
        for (final bare in const [
          'gpt-5',
          'gpt-5.4',
          'claude-sonnet-4-6',
          'gemini-2.5-pro',
        ]) {
          expect(
            CodeModelCatalog.resolveRuntimeForModel(
              model: bare,
              explicitExecutorId: 'native',
              executors: const [nativeExec],
              fallbackRuntime: 'native',
            ),
            'native',
          );
        }
      },
    );

    test(
      'updateSessionModel switching from CLI runner to provider/model before getExecutors resolves native',
      () async {
        final postCalls = <({String path, Map<String, dynamic> data})>[];
        final client = _NativeStubApiClient(
          onGet: (path, query) async {
            if (path == '/api/hoxi/workspaces') {
              return const {
                'workspaces': [
                  {'name': 'hoxi', 'path': '/Users/me/hoxi'},
                ],
              };
            }
            if (path == '/api/hoxi/system-info') {
              return const {
                'appVersion': '0.7.5',
                'nativeEngineReady': true,
                'openCodeReady': false,
              };
            }
            if (path == '/api/hoxi/provider/models') {
              return const {
                'providers': {
                  'subapi': {
                    'name': 'SubAPI',
                    'models': {
                      'openai-codex-gpt-6.2-sol': {
                        'name': 'openai-codex-gpt-6.2-sol',
                      },
                    },
                  },
                },
              };
            }
            if (path == '/api/hoxi/sessions') {
              return const [
                {
                  'id': 'ses_cld_1',
                  'title': 'Claude Session',
                  'directory': '/Users/me/hoxi',
                  'runtime': 'claude-code',
                  'providerID': 'claude-code',
                  'modelID': 'claude-sonnet-4-6',
                },
              ];
            }
            return const <String, dynamic>{};
          },
          onPost: (path, data) async {
            postCalls.add((path: path, data: data));
            return const {'ok': true};
          },
        );

        final adapter = HoxiWorkspaceAdapter(client);
        final session = (await adapter.getSessions()).single;
        expect(session.agentName, 'claude-code');

        final updated = await adapter.updateSessionModel(
          session.id,
          model: 'subapi/openai-codex-gpt-6.2-sol',
        );
        expect(updated.agentName, 'native');
        expect(postCalls.last.path, '/api/hoxi/sessions/ses_cld_1/runtime');
        expect(postCalls.last.data['runtime'], 'native');
        expect(postCalls.last.data['providerID'], 'subapi');
        expect(postCalls.last.data['modelID'], 'openai-codex-gpt-6.2-sol');
      },
    );
  });
}
